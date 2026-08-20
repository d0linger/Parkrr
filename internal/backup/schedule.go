package backup

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AuditFunc records a system-initiated backup action. It is injected rather than
// imported: internal/handlers already imports this package, so this package cannot
// import it back. main installs handlers.AuditSystem, which routes the entry through
// the same auditExec choke point (and therefore the same redaction) as every other
// audit write in the app.
type AuditFunc func(ctx context.Context, action, entity string, id int64, summary string, changes any)

// auditSink is set once at startup, before the scheduler goroutine starts, but is
// held atomically so the race detector stays quiet if a test installs one later.
var auditSink atomic.Pointer[AuditFunc]

// SetAuditor installs the sink used to record scheduled backups and pruned archives.
// Without it this package stays silent, which keeps the tests free of a DB dependency.
func SetAuditor(fn AuditFunc) { auditSink.Store(&fn) }

// audit records one system-initiated backup action.
//
// entity and entity_id are fixed rather than parameters: "system" is the same bucket
// the manual backup endpoints in internal/handlers use, so scheduled and manual runs
// stay filterable together, and a backup refers to no single database row, so there
// is no id to point at. Both would otherwise be constants dressed up as arguments.
func audit(ctx context.Context, action, summary string, changes map[string]any) {
	p := auditSink.Load()
	if p == nil || *p == nil {
		return
	}
	// Detach from ctx and give the write its own short deadline. The most important
	// entry here is the one describing a run that FAILED, and the most common reason a
	// run fails is that this very ctx expired (the 30-minute scheduler budget, or
	// shutdown) — writing through it would reject the INSERT and lose exactly the
	// record worth keeping. Values on ctx (the actor, if any) are preserved.
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	// These are outcome snapshots, not before/after diffs — wrap them here so the
	// entry has the same {old,new} shape as every other row.
	(*p)(wctx, action, "system", 0, summary, snapshot(changes))
}

// actionBackupFailed is deliberately NOT in database.auditShortLivedActions, so a
// failed or unverified run follows the long retention window while routine successes
// age out with the rest of the ops noise. Kept in sync by a test in that package.
const actionBackupFailed = "backup_failed"

// snapshot renders outcome values as {field:{old:null,new:v}}, mirroring
// handlers.auditSnapshot. Duplicated rather than imported because this package
// cannot depend on internal/handlers.
func snapshot(v map[string]any) any {
	if len(v) == 0 {
		return nil
	}
	out := make(map[string]any, len(v))
	for k, val := range v {
		out[k] = map[string]any{"old": nil, "new": val}
	}
	return out
}

// runMu serializes backup execution. The scheduler and the "run now" endpoints
// share the same heavy dump→encrypt→write path, so at most one runs at a time.
var runMu sync.Mutex

func backupName(t time.Time) string {
	return "parkrr-" + t.Format("2006-01-02-150405") + ".dump.enc"
}

func dumpEncrypt(ctx context.Context, dbURL, key string) ([]byte, error) {
	dump, err := Dump(ctx, dbURL)
	if err != nil {
		return nil, err
	}
	return Encrypt(dump, key)
}

// RunVolume makes an encrypted backup, writes it to dir, verifies the archive
// (decrypt + pg_restore --list), prunes to the newest `keep`, and records the
// outcome in backup_status. Returns the archive size and whether it VERIFIED.
//
// A written-but-unverified archive is deliberately not an error — the older, verified
// archives must not be rotated out behind it — but callers must be able to tell the
// two apart, or they report a success the status table simultaneously calls a failure.
func RunVolume(ctx context.Context, pool *pgxpool.Pool, dbURL, key, dir string, keep int) (int64, bool, error) {
	runMu.Lock()
	defer runMu.Unlock()

	enc, err := dumpEncrypt(ctx, dbURL, key)
	if err != nil {
		recordVolumeSafe(ctx, pool, 0, false, false)
		return 0, false, err
	}
	_ = os.MkdirAll(dir, 0o700) // ensure the target exists; WriteFile surfaces real errors
	// Erst prüfen, dann sichtbar machen. Geschrieben wird nach *.part; erst nach
	// bestandener Prüfung wird umbenannt. Vorher landete das Archiv sofort unter
	// seinem endgültigen Namen — ein durchgefallenes blieb liegen, erschien in der
	// Backup-Übersicht als wiederherstellbar und war als NEUESTE Datei sogar
	// bevorzugt. Das Glob-Muster parkrr-*.dump.enc greift bei *.part nicht, die
	// Zwischendatei taucht also weder in der Liste noch beim Aufräumen auf.
	p := filepath.Join(dir, backupName(time.Now()))
	part := p + ".part"
	if err := os.WriteFile(part, enc, 0o600); err != nil {
		recordVolumeSafe(ctx, pool, 0, false, false)
		return 0, false, err
	}
	// Die Zwischendatei wird bei einer nicht bestandenen Prüfung BEWUSST NICHT
	// gelöscht. Ein Prüffehler heißt nicht zwingend "Archiv kaputt": archiveTOC
	// meldet auch "pg_restore nicht im PATH", "kein Platz für die temporäre Datei"
	// und einen abgelaufenen Context als Fehler. Ein Image ohne pg_restore würde
	// sonst jede Nacht einen einwandfreien Dump erzeugen und sofort wieder löschen —
	// der letzte gute Stand friert ein, ohne dass jemand etwas sieht.
	// Die .part bleibt also liegen: nicht als Backup gelistet (das Glob-Muster
	// greift nicht), aber für den Betreiber vorhanden. Weggeräumt wird sie erst vom
	// nächsten Lauf, siehe sweepStaleParts weiter unten.
	promoted := false
	defer func() {
		if !promoted {
			slog.Warn("backup: keeping the unverified archive for inspection", "path", part)
		}
	}()
	// Reste früherer Läufe wegräumen (OOM-Kill oder Neustart zwischen Schreiben und
	// Prüfen, und die Zwischendatei eines durchgefallenen Laufs). Ohne das sammeln
	// sich Dumps in voller Größe an, die weder in der Übersicht noch beim Aufräumen
	// auftauchen und irgendwann das Volume füllen. Die gerade geschriebene Datei ist
	// ausgenommen — sie wird gleich geprüft.
	sweepStaleParts(dir, part)
	// Wiederherstellungsprüfung. Erst Stufe 1 gegen die DATEI auf der Platte: os.WriteFile
	// kann bei vollem Dateisystem ohne Fehler zurückkommen, und dann läge dort ein
	// abgeschnittenes Archiv, das jede spätere Prüfung im Speicher nicht bemerkt.
	size := int64(len(enc))
	if onDisk, rerr := os.ReadFile(part); rerr != nil { // #nosec G304 -- selbst erzeugter Pfad
		slog.Warn("backup: read-back failed – discarding the new archive", "path", part, "err", rerr)
		recordVolumeSafe(ctx, pool, size, false, false)
		audit(ctx, actionBackupFailed, "Volume-Backup: Rücklesen der Datei fehlgeschlagen",
			map[string]any{"target": "volume", "stage": "readback", "error": rerr.Error()})
		return size, false, nil
	} else if verr := VerifyLocalBytes(onDisk, enc); verr != nil {
		slog.Warn("backup: written file differs – discarding the new archive", "path", part, "err", verr)
		recordVolumeSafe(ctx, pool, size, false, false)
		audit(ctx, actionBackupFailed, "Volume-Backup: Datei weicht vom Erzeugten ab",
			map[string]any{"target": "volume", "stage": "checksum", "error": verr.Error()})
		return size, false, nil
	}
	// Stufen 3+4: Archivkopf und Kerntabellen.
	if _, verr := VerifyArchive(ctx, enc, key); verr != nil {
		// Do NOT rotate the older (verified) archives out behind an UNVERIFIED new
		// one, and record the run as not-OK — otherwise a persistent verify failure
		// would prune away the last good backup while last_volume_ok stayed green.
		slog.Warn("backup: archive verify failed – discarding the new archive", "path", part, "err", verr)
		recordVolumeSafe(ctx, pool, size, false, false)
		audit(ctx, actionBackupFailed, "Volume-Backup: Wiederherstellungsprüfung fehlgeschlagen",
			map[string]any{"target": "volume", "stage": "archive", "error": verr.Error()})
		return size, false, nil
	}
	// Bestanden: jetzt erst sichtbar machen. Rename ist auf einem Dateisystem atomar,
	// es gibt also keinen Moment, in dem eine halbe Datei unter dem echten Namen liegt.
	if err := os.Rename(part, p); err != nil {
		slog.Error("backup: promoting the verified archive failed", "path", p, "err", err)
		recordVolumeSafe(ctx, pool, size, false, false)
		audit(ctx, actionBackupFailed, "Volume-Backup: geprüftes Archiv konnte nicht übernommen werden",
			map[string]any{"target": "volume", "stage": "promote", "error": err.Error()})
		return size, false, nil
	}
	promoted = true
	// Aufräumen erst NACH der Übernahme: sonst würde ein durchgefallener Lauf die
	// alten, geprüften Archive wegräumen, ohne einen gültigen Ersatz zu hinterlassen.
	pruneDir(ctx, dir, keep)
	recordVolumeSafe(ctx, pool, size, true, true)
	return size, true, nil
}

// RunS3 makes an encrypted backup, uploads it to the bucket (pruning to `keep`),
// and records the outcome. Returns the object name.
func RunS3(ctx context.Context, pool *pgxpool.Pool, dbURL, key string, s3 S3Config, keep int) (string, error) {
	runMu.Lock()
	defer runMu.Unlock()

	enc, err := dumpEncrypt(ctx, dbURL, key)
	if err != nil {
		recordS3Safe(ctx, pool, false)
		return "", err
	}
	name := backupName(time.Now())
	// keep=0: beim Hochladen NICHT aufräumen. UploadS3 rief pruneS3 direkt nach
	// PutObject auf — ein abgebrochener Upload verdrängte damit einen guten alten
	// Stand, bevor überhaupt jemand das neue Objekt geprüft hatte. Aufgeräumt wird
	// unten, erst nach bestandener Prüfung; dieselbe Reihenfolge wie beim
	// Volume-Ziel.
	if err := UploadS3(ctx, s3, name, enc, 0); err != nil {
		recordS3Safe(ctx, pool, false)
		return "", err
	}
	// Bis hierher hieß "erfolgreich" nur, dass PutObject zurückkam. Ein
	// Verbindungsabbruch mitten im Upload hinterlässt ein kürzeres Objekt, das
	// genauso aussieht. Deshalb Stufe 2: Größe, dann zurücklesen und Prüfsumme
	// vergleichen — gegen die Summe des ERZEUGTEN Archivs, nicht gegen die des
	// Objekts, sonst prüft man das Ergebnis mit sich selbst.
	if verr := VerifyS3Object(ctx, s3, name, key, Checksum(enc), int64(len(enc))); verr != nil {
		slog.Error("backup: S3 object failed verification", "object", name, "err", verr)
		// Das durchgefallene Objekt wieder entfernen, sonst steht es im Bucket als
		// NEUESTES und damit naheliegendstes Archiv zur Wiederherstellung bereit —
		// genau der Zustand, gegen den das Volume-Ziel abgesichert wurde.
		if derr := DeleteS3(ctx, s3, name); derr != nil {
			slog.Warn("backup: could not remove the unverified S3 object", "object", name, "err", derr)
		}
		recordS3Safe(ctx, pool, false)
		audit(ctx, actionBackupFailed, "S3-Backup: Objekt hat die Prüfung nicht bestanden",
			map[string]any{"target": "s3", "stage": "s3-readback", "object": name,
				"bucket": s3.Bucket, "error": verr.Error()})
		return name, verr
	}
	// Erst jetzt aufräumen: bis hierher ist bewiesen, dass ein gültiger Ersatz da ist.
	if perr := PruneS3(ctx, s3, keep); perr != nil {
		slog.Warn("backup: S3 prune failed", "err", perr)
	}
	recordS3Safe(ctx, pool, true)
	return name, nil
}

// recordVolumeSafe/recordS3Safe schreiben den Status über einen vom Aufrufer
// ABGEKOPPELTEN Context. Scheitert ein Lauf, WEIL der Context ablief (30-Minuten-
// Budget des Planers, Shutdown), dann würde der Status-Schreibvorgang durch denselben
// toten Context ebenfalls abgewiesen — und last_*_ok bliebe auf dem alten `true`
// stehen. Die Anzeige zeigte weiter Grün für einen Lauf, der nichts produziert hat.
// Der audit()-Helfer oben verteidigt sich seit Längerem genauso; hier fehlte es.
func recordVolumeSafe(ctx context.Context, pool *pgxpool.Pool, size int64, ok, tested bool) {
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := recordVolume(wctx, pool, time.Now(), size, ok, tested); err != nil {
		slog.Warn("backup: could not record volume status", "err", err)
	}
}

func recordS3Safe(ctx context.Context, pool *pgxpool.Pool, ok bool) {
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := recordS3(wctx, pool, time.Now(), ok); err != nil {
		slog.Warn("backup: could not record S3 status", "err", err)
	}
}

// StartScheduler runs scheduled backups driven by the DB-stored cron schedule
// (backup_settings). Each minute it reloads the schedule and fires any target
// whose cron is due since its last recorded run. Blocks until stop is closed —
// run it in a goroutine. A no-op unless a key and at least one target (a backup
// directory or S3) are configured.
func StartScheduler(stop <-chan struct{}, pool *pgxpool.Pool, dbURL, key, dir string, s3 S3Config) {
	if key == "" || (dir == "" && !s3.Enabled()) {
		return
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	// In-memory guards for the last fire of each target. They back-stop the DB
	// status row: if a backup runs but its status write fails, the guard still
	// advances, so fireDue can't relaunch the (multi-minute) backup every tick.
	var lastVol, lastS3 time.Time
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			schedulerTick(pool, dbURL, key, dir, s3, &lastVol, &lastS3)
		}
	}
}

// effectiveLast returns the later of the persisted last-run and the in-memory
// guard, or nil if neither is set (target never run).
func effectiveLast(dbLast *time.Time, mem time.Time) *time.Time {
	if dbLast == nil {
		if mem.IsZero() {
			return nil
		}
		return &mem
	}
	if mem.After(*dbLast) {
		return &mem
	}
	return dbLast
}

func schedulerTick(pool *pgxpool.Pool, dbURL, key, dir string, s3 S3Config, lastVol, lastS3 *time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	settings, err := LoadSettings(ctx, pool)
	if err != nil {
		slog.Error("backup scheduler: load settings failed", "err", err)
		return
	}
	status, err := LoadStatus(ctx, pool)
	if err != nil {
		slog.Error("backup scheduler: load status failed", "err", err)
		return
	}
	now := time.Now()

	// Both branches audit the OUTCOME, success and failure alike. A backup that
	// silently stopped running is the failure mode that matters here, and previously
	// the only trace was a log line that nobody keeps for seven years. The manual
	// endpoints audit themselves, so only the scheduled path is recorded here — no
	// double entry. Entity "system" matches what those endpoints already use, so one
	// filter shows scheduled and manual runs together instead of splitting one
	// operation across two buckets.
	//
	// Action: a SUCCESSFUL run is ops noise and uses "backup", which the retention
	// policy ages out on the short window. A FAILURE uses actionBackupFailed, which is
	// deliberately absent from auditShortLivedActions, so "when did the nightly backups
	// stop?" is still answerable once the 365-day short window has passed.
	if dir != "" && fireDue(settings.VolumeCron, effectiveLast(status.LastVolumeAt, *lastVol), now) {
		*lastVol = now // advance the guard before running so a status-write failure can't re-fire
		switch size, verified, err := RunVolume(ctx, pool, dbURL, key, dir, settings.VolumeKeep); {
		case err != nil:
			slog.Error("scheduled volume backup failed", "err", err)
			audit(ctx, actionBackupFailed, "Geplantes Volume-Backup FEHLGESCHLAGEN",
				map[string]any{"target": "volume", "ok": false, "cron": settings.VolumeCron, "error": err.Error()})
		case !verified:
			// Written, but it did not decrypt/restore-list cleanly. recordVolume already
			// flagged it not-OK; reporting ok:true here would leave the append-only trail
			// asserting a good backup that the status table simultaneously calls failed.
			slog.Warn("scheduled volume backup written but NOT verified", "dir", dir, "bytes", size)
			audit(ctx, actionBackupFailed, "Geplantes Volume-Backup geschrieben, aber NICHT verifiziert",
				map[string]any{"target": "volume", "ok": false, "verified": false, "bytes": size, "cron": settings.VolumeCron})
		default:
			slog.Info("scheduled volume backup written", "dir", dir, "bytes", size)
			audit(ctx, "backup", "Geplantes Volume-Backup erstellt und verifiziert",
				map[string]any{"target": "volume", "ok": true, "verified": true, "bytes": size, "cron": settings.VolumeCron, "keep": settings.VolumeKeep})
		}
	}
	if s3.Enabled() && fireDue(settings.S3Cron, effectiveLast(status.LastS3At, *lastS3), now) {
		*lastS3 = now
		if name, err := RunS3(ctx, pool, dbURL, key, s3, settings.S3Keep); err != nil {
			slog.Error("scheduled S3 backup failed", "err", err)
			audit(ctx, actionBackupFailed, "Geplantes S3-Backup FEHLGESCHLAGEN",
				map[string]any{"target": "s3", "ok": false, "bucket": s3.Bucket, "cron": settings.S3Cron, "error": err.Error()})
		} else {
			slog.Info("scheduled S3 backup uploaded", "bucket", s3.Bucket, "name", name)
			audit(ctx, "backup", "Geplantes S3-Backup hochgeladen",
				map[string]any{"target": "s3", "ok": true, "bucket": s3.Bucket, "object": name, "cron": settings.S3Cron, "keep": settings.S3Keep})
		}
	}
}

// fireDue reports whether a target should back up now. An empty cron is off. A
// never-run target (no recorded last run) takes an initial backup immediately,
// then follows the schedule from there.
func fireDue(cron string, last *time.Time, now time.Time) bool {
	if _, ok := parseCron(cron); !ok {
		return false // off (empty) or invalid: never fire
	}
	if last == nil {
		return true // never run: take an initial backup, then follow the schedule
	}
	return CronDue(cron, *last, now)
}

// sweepStaleParts entfernt *.part-Reste und behält GENAU EINEN: den, den der
// laufende Vorgang gerade geschrieben hat (keep). Sie entstehen, wenn der Prozess
// zwischen Schreiben und Prüfen stirbt oder wenn die Prüfung nicht besteht, und
// werden von keinem anderen Pfad erfasst: pruneDir und die Backup-Übersicht filtern
// beide auf parkrr-*.dump.enc, worauf *.part nicht passt.
//
// Vorher lief das über eine Altersfrist von 14 Tagen. Genau im Fall, für den die
// Zwischendatei überhaupt liegen bleibt — eine Prüfung, die JEDE Nacht scheitert,
// etwa weil pg_restore im Image fehlt — sammelten sich damit bis zu vierzehn
// vollständige Dumps an, unsichtbar in der Übersicht und im Aufräumen. Bei einer
// Datenbank von einigen Gigabyte füllt das ein Volume, und das nächste Backup
// scheitert dann am Platz statt am ursprünglichen Fehler.
//
// Die Untersuchbarkeit bleibt: der zuletzt durchgefallene Dump liegt bis zum
// nächsten Lauf bereit. Bei einem wiederkehrenden Fehler ist der neueste ohnehin so
// aussagekräftig wie der von vor zwei Wochen.
//
// keep wird ausdrücklich übergeben und nicht als "der neueste Name" erraten: bei
// einer rückwärts gestellten Uhr sortierte die gerade geschriebene Datei nicht mehr
// zuletzt und würde unter den Händen des eigenen Laufs gelöscht.
func sweepStaleParts(dir, keep string) {
	parts, err := filepath.Glob(filepath.Join(dir, "parkrr-*.dump.enc.part"))
	if err != nil {
		return
	}
	for _, f := range parts {
		if filepath.Base(f) == filepath.Base(keep) {
			continue
		}
		if rerr := os.Remove(f); rerr != nil {
			slog.Warn("backup: could not remove a leftover .part file", "path", f, "err", rerr)
			continue
		}
		slog.Info("backup: removed a leftover .part file from an earlier run", "path", f)
	}
}

// pruneDir keeps only the newest `keep` timestamped backups in dir (0 = keep all).
func pruneDir(ctx context.Context, dir string, keep int) {
	if keep < 1 {
		return
	}
	files, err := filepath.Glob(filepath.Join(dir, "parkrr-*.dump.enc"))
	if err != nil || len(files) <= keep {
		return
	}
	sort.Strings(files) // timestamped names sort chronologically
	var removed []string
	for _, old := range files[:len(files)-keep] {
		if err := os.Remove(old); err != nil {
			slog.Warn("backup: prune failed", "path", old, "err", err)
			continue
		}
		removed = append(removed, filepath.Base(old))
	}
	// Deleting a backup is destructive and irreversible, and until now it happened
	// with nothing but a debug line. Record WHICH archives went and how many remain,
	// so a missing restore point can be explained rather than guessed at.
	if len(removed) > 0 {
		audit(ctx, "delete",
			fmt.Sprintf("%d alte Backup-Archive gelöscht (Aufbewahrung: %d)", len(removed), keep),
			map[string]any{"deleted_files": removed, "deleted_count": len(removed), "keep": keep})
	}
}
