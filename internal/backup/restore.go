package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
)

// Einspielen eines Archivs (BAK-01).
//
// `pg_restore --clean --if-exists` räumt nur die Objekte weg, die im Archiv
// stehen. Ein Archiv von einem ÄLTEREN Schemastand kennt die Objekte späterer
// Migrationen nicht (z. B. overview_revision_seq aus 072): sie überlebten den
// Restore, schema_migrations kam aber aus dem Archiv. Migrate spielte 072 danach
// erneut ein, scheiterte an "already exists" — und weil main.go vor jedem Start
// migriert, lief jede Replik von da an in eine Absturzschleife.
//
// Deshalb wird das Schema public jetzt VOLLSTÄNDIG ersetzt: DROP SCHEMA public
// CASCADE und neu anlegen, in DERSELBEN Transaktion wie der Inhalt des Archivs.
// Scheitert irgendetwas, bleibt die Datenbank unverändert. parkrr_control (der
// Zustand des laufenden Restore-Jobs) liegt in einem eigenen Schema, fehlt in
// jedem Archiv und bleibt deshalb erhalten.
//
// pg_restore allein kann keine eigene Anweisung in seine Transaktion legen. Es
// erzeugt darum hier nur das SQL-Skript (--file=-), und psql führt Vorspann,
// Skript und das abschließende COMMIT in einer Sitzung aus. Das COMMIT schreibt
// ausschließlich dieser Code, und nur nachdem pg_restore erfolgreich beendet ist:
// bricht pg_restore mittendrin ab, endet die psql-Sitzung ohne COMMIT, und der
// Server verwirft die offene Transaktion.

// restorePrelude öffnet die Transaktion und ersetzt das Schema public. Fehlt der
// Rolle das Recht dazu (public gehört ihr nicht, etwa bei einer verwalteten
// Datenbank vor PostgreSQL 15), fällt es auf das bisherige Verhalten zurück —
// ein Restore, der bisher gelang, soll nicht an dieser Härtung scheitern.
//
// Vor dem DROP ... CASCADE bricht der Vorspann ab, wenn Sichten, Fremdschlüssel
// oder Spalten mit einem Typ aus public AUSSERHALB von public auf public
// verweisen: CASCADE würde sie
// sonst stillschweigend mitlöschen, obwohl sie nicht im Archiv stehen (etwa eine
// Auswertungssicht des Betreibers). parkrr_control verweist nicht auf public und
// ist davon nicht betroffen.
const restorePrelude = `SET client_min_messages = warning;
BEGIN;
DO $parkrr_restore$
DECLARE
    deps text;
BEGIN
    SELECT string_agg(DISTINCT dep, ', ') INTO deps FROM (
        SELECT vn.nspname || '.' || v.relname AS dep
          FROM pg_depend d
          JOIN pg_rewrite rw ON d.classid = 'pg_rewrite'::regclass AND d.objid = rw.oid
          JOIN pg_class v ON v.oid = rw.ev_class
          JOIN pg_namespace vn ON vn.oid = v.relnamespace AND vn.nspname <> 'public'
          JOIN pg_class t ON d.refclassid = 'pg_class'::regclass AND d.refobjid = t.oid
          JOIN pg_namespace tn ON tn.oid = t.relnamespace AND tn.nspname = 'public'
        UNION ALL
        SELECT sn.nspname || '.' || src.relname || ' (' || c.conname || ')'
          FROM pg_constraint c
          JOIN pg_class src ON src.oid = c.conrelid
          JOIN pg_namespace sn ON sn.oid = src.relnamespace AND sn.nspname <> 'public'
          JOIN pg_class ref ON ref.oid = c.confrelid
          JOIN pg_namespace rn ON rn.oid = ref.relnamespace AND rn.nspname = 'public'
         WHERE c.contype = 'f'
        UNION ALL
        -- A column whose type lives in public (enum, domain, composite) would be
        -- dropped with its data by the CASCADE.
        SELECT an.nspname || '.' || ac.relname || '.' || a.attname
          FROM pg_attribute a
          JOIN pg_class ac ON ac.oid = a.attrelid AND ac.relkind IN ('r', 'p', 'f', 'm', 'v', 'c')
          JOIN pg_namespace an ON an.oid = ac.relnamespace AND an.nspname <> 'public'
               AND an.nspname NOT IN ('pg_catalog', 'information_schema') AND an.nspname NOT LIKE 'pg_toast%'
          JOIN pg_type ty ON ty.oid = a.atttypid
          JOIN pg_namespace tyn ON tyn.oid = ty.typnamespace AND tyn.nspname = 'public'
         WHERE a.attnum > 0 AND NOT a.attisdropped
    ) s;
    IF deps IS NOT NULL THEN
        RAISE EXCEPTION 'parkrr: objects outside schema public depend on it and would be dropped by the restore: %', deps
            USING ERRCODE = 'dependent_objects_still_exist';
    END IF;
    DROP SCHEMA IF EXISTS public CASCADE;
    CREATE SCHEMA public;
    COMMENT ON SCHEMA public IS 'standard public schema';
    GRANT USAGE ON SCHEMA public TO PUBLIC;
EXCEPTION WHEN insufficient_privilege THEN
    RAISE WARNING '` + restoreFallbackMarker + ` (%)', SQLERRM;
END
$parkrr_restore$;
`

const restoreFallbackMarker = "parkrr: schema public could not be replaced; restoring over the existing objects"

// checkRestorableTOC lehnt Archive mit Large Objects ab. pg_restore schreibt für
// sie eigene BEGIN/COMMIT-Paare ins Skript, die die umschließende Transaktion
// vorzeitig abschließen würden — ein Abbruch danach ließe eine halb eingespielte
// Datenbank zurück. Parkrr legt nie Large Objects an (Fotos liegen als bytea vor).
func checkRestorableTOC(toc string) error {
	for _, line := range strings.Split(toc, "\n") {
		if strings.HasPrefix(line, ";") {
			continue
		}
		// "<id>; <catalog oid> <oid> <DESC> <schema> <name> <owner>"
		fields := strings.Fields(line)
		if len(fields) > 3 && (strings.HasPrefix(fields[3], "BLOB") || fields[3] == "LARGE") {
			return errors.New("archive contains large objects, which Parkrr never creates; restore it manually with pg_restore")
		}
	}
	return nil
}

// restoreArchive spielt ein entschlüsseltes pg_dump-Archiv atomar ein. Genau eine
// Quelle ist gesetzt: archivePath (eine Datei) oder archive (stdin von pg_restore).
func restoreArchive(ctx context.Context, dbURL, archivePath string, archive io.Reader) error {
	dsn, env := dbExecEnv(dbURL)
	args := []string{"--clean", "--if-exists", "--no-owner", "--no-privileges", "--file=-"}
	if archivePath != "" {
		args = append(args, archivePath)
	}
	// #nosec G204 -- fixed executable; the path is an application-created temp file.
	script := exec.CommandContext(ctx, "pg_restore", args...)
	if archivePath == "" {
		script.Stdin = archive
	}
	var scriptErr bytes.Buffer
	script.Stderr = &scriptErr
	scriptOut, err := script.StdoutPipe()
	if err != nil {
		return err
	}

	// Keep the database password out of argv (PGPASSWORD via env).
	// #nosec G204 -- fixed executable; DSN is operator configuration.
	psql := exec.CommandContext(ctx, "psql", "--no-psqlrc", "--quiet",
		"--set=ON_ERROR_STOP=1", "--dbname="+dsn)
	psql.Env = env
	psql.Stdout = io.Discard
	var psqlErr bytes.Buffer
	psql.Stderr = &psqlErr
	psqlIn, err := psql.StdinPipe()
	if err != nil {
		return err
	}
	if err := psql.Start(); err != nil {
		return fmt.Errorf("start psql: %w", err)
	}
	if err := script.Start(); err != nil {
		_ = psqlIn.Close() // no COMMIT was written: the session ends and rolls back
		_ = psql.Wait()
		return fmt.Errorf("start pg_restore: %w", err)
	}

	_, feedErr := io.WriteString(psqlIn, restorePrelude)
	if feedErr == nil {
		_, feedErr = io.Copy(psqlIn, scriptOut)
	}
	if feedErr != nil && script.Process != nil {
		// psql stopped reading (its error is reported below); unblock pg_restore.
		_ = script.Process.Kill()
	}
	scriptWaitErr := script.Wait()
	committed := false
	if feedErr == nil && scriptWaitErr == nil {
		if _, feedErr = io.WriteString(psqlIn, "\nCOMMIT;\n"); feedErr == nil {
			committed = true
		}
	}
	_ = psqlIn.Close()
	psqlWaitErr := psql.Wait()

	switch {
	case psqlWaitErr != nil:
		return fmt.Errorf("restore failed, database unchanged: %w: %s", psqlWaitErr, tail(psqlErr.String()))
	case scriptWaitErr != nil:
		return fmt.Errorf("pg_restore failed, database unchanged: %w: %s", scriptWaitErr, tail(scriptErr.String()))
	case feedErr != nil:
		return fmt.Errorf("restore failed, database unchanged: %w", feedErr)
	case !committed:
		return errors.New("restore failed, database unchanged")
	}
	if strings.Contains(psqlErr.String(), restoreFallbackMarker) {
		slog.Warn("backup: restore could not replace schema public; objects outside the archive were kept",
			"detail", tail(psqlErr.String()))
	}
	return nil
}

// tail kürzt Fehlerausgaben auf ihr Ende — dort steht bei psql/pg_restore der Grund.
func tail(s string) string {
	s = strings.TrimSpace(s)
	const limit = 4096
	if len(s) > limit {
		return "…" + s[len(s)-limit:]
	}
	return s
}
