package backup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

// runGate serializes backup/restore execution while allowing queued callers to
// leave immediately when their context is canceled.
var runGate = make(chan struct{}, 1)

func acquireRun(ctx context.Context) error {
	select {
	case runGate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func releaseRun() { <-runGate }

func uniqueBackupName(t time.Time) (string, error) {
	id := make([]byte, 8)
	if _, err := rand.Read(id); err != nil {
		return "", err
	}
	return "parkrr-" + t.Format("2006-01-02-150405") + "-" + hex.EncodeToString(id) + ".dump.enc", nil
}

// ErrBackupBusy meldet, dass eine andere Replik denselben Lauf gerade ausführt.
// Das ist kein Fehlschlag: der Lauf findet statt, nur nicht hier. Der Planer
// überspringt ihn deshalb ohne Fehlerprotokoll und ohne Alarm-E-Mail.
var ErrBackupBusy = errors.New("another Parkrr instance is running this backup right now")

// tryAcquireLease serializes a complete backup run across application replicas.
// Advisory locks are session-scoped because the lease spans external I/O; an
// unconfirmed unlock evicts the physical connection rather than returning a
// possibly lock-owning session to the pool.
//
// BEWUSST pg_try_advisory_lock statt pg_advisory_lock (BAK-03): das blockierende
// Warten lief auf einer Pool-Verbindung mit statement_timeout=10s und wurde nach
// zehn Sekunden abgebrochen — jede zweite Replik meldete so bei jedem Lauf einen
// Fehlschlag samt Alarm. Warten wäre ohnehin sinnlos: wer die Sperre hält, macht
// genau diesen Lauf, und ein zweiter direkt danach wäre ein Duplikat. Der
// Versuch kehrt sofort zurück, der statement_timeout spielt keine Rolle mehr.
//
// Der zurückgegebene Kontext endet, sobald die Sperrsitzung wegbricht: eine
// Session-Sperre gilt nur, solange ihre Verbindung lebt. Ohne diese Überwachung
// liefe ein Lauf nach einem Verbindungsabbruch ungeschützt weiter, während eine
// andere Replik die Sperre bekommt — und deren sweepStaleParts räumte die noch
// wachsende .part weg. Die Freigabe beendet erst die Überwachung, dann die Sperre.
func tryAcquireLease(ctx context.Context, pool *pgxpool.Pool, name string) (context.Context, func(), error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, nil, err
	}
	var acquired bool
	if err := conn.QueryRow(ctx,
		`SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, name).Scan(&acquired); err != nil {
		conn.Release()
		return nil, nil, err
	}
	if !acquired {
		conn.Release()
		return nil, nil, ErrBackupBusy
	}
	leaseCtx, cancelLease := context.WithCancelCause(ctx)
	stopWatch := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		t := time.NewTicker(leaseCheckInterval)
		defer t.Stop()
		for {
			select {
			case <-stopWatch:
				return
			case <-leaseCtx.Done():
				return
			case <-t.C:
				pctx, cancel := context.WithTimeout(leaseCtx, 5*time.Second)
				err := conn.Ping(pctx)
				cancel()
				if err != nil && leaseCtx.Err() == nil {
					slog.Error("backup: advisory lease session lost; stopping the run", "lease", name, "err", err)
					cancelLease(fmt.Errorf("%w: %v", errLeaseLost, err))
					return
				}
			}
		}
	}()
	return leaseCtx, func() {
		close(stopWatch)
		<-watchDone // the watcher no longer touches conn
		defer cancelLease(nil)
		uctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var unlocked bool
		if err := conn.QueryRow(uctx,
			`SELECT pg_advisory_unlock(hashtextextended($1, 0))`, name).Scan(&unlocked); err != nil || !unlocked {
			slog.Error("backup: could not release advisory lease; evicting connection", "lease", name, "err", err)
			raw := conn.Hijack()
			_ = raw.Close(uctx)
			return
		}
		conn.Release()
	}, nil
}

// errLeaseLost ends a run whose advisory lease session went away mid-run.
var errLeaseLost = errors.New("backup lease lost")

// leaseCheckInterval is how often a held lease's session is pinged.
var leaseCheckInterval = 15 * time.Second

// s3LeaseName is the advisory-lease name that serializes S3 runs per bucket.
func s3LeaseName(bucket string) string { return "parkrr.backup-s3:" + bucket }

// volumeLeaseName: Repliken teilen sich das Backup-Verzeichnis (BAK-05). Ohne
// gemeinsame Sperre starteten alle in derselben Minute, und sweepStaleParts der
// einen löschte die halb geschriebene .part der anderen.
//
// Der Name folgt der Identität des VOLUMES, nicht dem Pfad: Repliken können
// dasselbe Volume unter verschiedenen Pfaden einhängen. Die Identität liegt als
// Zufalls-ID in einer Datei auf dem Volume selbst; wer sie zuerst veröffentlicht (Hardlink),
// legt sie fest, alle anderen lesen dieselbe. Ist das Verzeichnis nicht
// beschreibbar, bleibt als Rückfall der bereinigte Pfad.
func volumeLeaseName(dir string) string {
	if id, err := volumeIdentity(dir); err == nil {
		return "parkrr.backup-volume:" + id
	} else {
		slog.Warn("backup: volume identity unavailable; locking by path", "dir", dir, "err", err)
	}
	return "parkrr.backup-volume:" + filepath.Clean(dir)
}

const volumeIDFile = ".parkrr-volume-id"

// volumeIdentity returns the volume's identity, creating it on first use. The ID
// is written and synced to a private temp file first and then published with a
// hard link, which fails atomically when a peer published first — readers never
// see a half-written file, and the first creator wins. An invalid file older than
// staleVolumeIDAge (left by an older build or a crash) is removed and recreated.
func volumeIdentity(dir string) (string, error) {
	path := filepath.Join(dir, volumeIDFile)
	for attempt := 0; attempt < 3; attempt++ {
		b, err := os.ReadFile(path) // #nosec G304 -- fixed name inside the configured backup dir
		switch {
		case err == nil:
			if id := strings.TrimSpace(string(b)); len(id) == 32 {
				return id, nil
			}
			if fi, serr := os.Stat(path); serr == nil && time.Since(fi.ModTime()) > staleVolumeIDAge {
				_ = os.Remove(path)
			} else {
				time.Sleep(50 * time.Millisecond)
			}
			continue
		case !errors.Is(err, os.ErrNotExist):
			return "", err
		}
		id, err := publishVolumeIdentity(dir, path)
		if errors.Is(err, os.ErrExist) {
			continue // a peer published first: use theirs
		}
		return id, err
	}
	return "", errors.New("volume identity file is unreadable or incomplete")
}

// staleVolumeIDAge is how old an invalid identity file must be before it counts
// as abandoned rather than as a peer's in-progress write.
const staleVolumeIDAge = 10 * time.Second

// publishVolumeIdentity writes a fresh ID to a temp file and links it into place.
// It returns os.ErrExist when an identity is already published.
func publishVolumeIdentity(dir, path string) (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	id := hex.EncodeToString(raw)
	tmp, err := os.CreateTemp(dir, volumeIDFile+".tmp-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	_, werr := tmp.WriteString(id + "\n")
	if werr == nil {
		werr = tmp.Sync()
	}
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return "", werr
	}
	if err := os.Link(tmpName, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", os.ErrExist
		}
		return "", err
	}
	return id, nil
}

// RunVolume makes an encrypted backup, writes it to dir, verifies the archive
// (decrypt + pg_restore --list), prunes to the newest `keep`, and records the
// outcome in backup_status. Returns the archive size and whether it VERIFIED.
//
// A written-but-unverified archive is deliberately not an error — the older, verified
// archives must not be rotated out behind it — but callers must be able to tell the
// two apart, or they report a success the status table simultaneously calls a failure.
func RunVolume(ctx context.Context, pool *pgxpool.Pool, dbURL, key, dir string, r Retention) (int64, bool, error) {
	if err := acquireRun(ctx); err != nil {
		return 0, false, err
	}
	defer releaseRun()
	_ = os.MkdirAll(dir, 0o700) // the create below surfaces the actionable error
	ctx, releaseLease, err := tryAcquireLease(ctx, pool, volumeLeaseName(dir))
	if err != nil {
		return 0, false, err
	}
	defer releaseLease()
	// Erst prüfen, dann sichtbar machen. Geschrieben wird nach *.part; erst nach
	// bestandener Prüfung wird umbenannt. Vorher landete das Archiv sofort unter
	// seinem endgültigen Namen — ein durchgefallenes blieb liegen, erschien in der
	// Backup-Übersicht als wiederherstellbar und war als NEUESTE Datei sogar
	// bevorzugt. Das Glob-Muster parkrr-*.dump.enc greift bei *.part nicht, die
	// Zwischendatei taucht also weder in der Liste noch beim Aufräumen auf.
	//
	// Der Name trägt ein Zufallssuffix (uniqueBackupName, wie beim S3-Ziel): zwei
	// Läufe in derselben Sekunde öffneten vorher dieselbe Datei mit O_TRUNC und
	// schrieben ineinander. O_EXCL macht eine Kollision zum Fehler statt zum Salat.
	name, err := uniqueBackupName(time.Now())
	if err != nil {
		recordVolumeSafe(ctx, pool, 0, false, false)
		return 0, false, err
	}
	p := filepath.Join(dir, name)
	part := p + ".part"
	// dir is an operator-owned configuration value and the name is generated
	// locally from a fixed format; no request-controlled path component is used.
	f, err := os.OpenFile(part, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304
	if err != nil {
		recordVolumeSafe(ctx, pool, 0, false, false)
		return 0, false, err
	}
	if err := DumpEncrypted(ctx, dbURL, key, f); err != nil {
		_ = f.Close()
		_ = os.Remove(part)
		recordVolumeSafe(ctx, pool, 0, false, false)
		return 0, false, err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		recordVolumeSafe(ctx, pool, 0, false, false)
		return 0, false, err
	}
	if err := f.Close(); err != nil {
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
	// Re-open and hash the durable file. DumpEncrypted propagates short writes and
	// Sync catches delayed filesystem errors; this confirms the complete archive is
	// readable without allocating a second full-size copy.
	_, size, rerr := ChecksumFile(part)
	if rerr != nil {
		slog.Warn("backup: read-back failed – discarding the new archive", "path", part, "err", rerr)
		recordVolumeSafe(ctx, pool, 0, false, false)
		audit(ctx, actionBackupFailed, "Volume-Backup: Rücklesen der Datei fehlgeschlagen",
			map[string]any{"target": "volume", "stage": "readback", "error": rerr.Error()})
		return 0, false, nil
	}
	// Stufen 3+4: Archivkopf und Kerntabellen.
	if _, verr := VerifyArchiveFile(ctx, part, key); verr != nil {
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
	pruneDir(ctx, dir, r)
	recordVolumeSafe(ctx, pool, size, true, true)
	return size, true, nil
}

// RunS3 makes an encrypted backup, uploads it to the bucket (pruning to `keep`),
// and records the outcome. Returns the object name.
func RunS3(ctx context.Context, pool *pgxpool.Pool, dbURL, key string, s3 S3Config, r Retention) (string, error) {
	if err := acquireRun(ctx); err != nil {
		return "", err
	}
	defer releaseRun()
	ctx, releaseLease, err := tryAcquireLease(ctx, pool, s3LeaseName(s3.Bucket))
	if err != nil {
		return "", err
	}
	defer releaseLease()

	tmp, err := createWorkFile("parkrr-s3-upload-", ".dump.enc")
	if err != nil {
		recordS3Safe(ctx, pool, false)
		return "", err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := DumpEncrypted(ctx, dbURL, key, tmp); err != nil {
		_ = tmp.Close()
		recordS3Safe(ctx, pool, false)
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		recordS3Safe(ctx, pool, false)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		recordS3Safe(ctx, pool, false)
		return "", err
	}
	sha, size, err := ChecksumFile(tmpPath)
	if err != nil {
		recordS3Safe(ctx, pool, false)
		return "", err
	}
	name, err := uniqueBackupName(time.Now())
	if err != nil {
		recordS3Safe(ctx, pool, false)
		return "", err
	}
	// keep=0: beim Hochladen NICHT aufräumen. UploadS3 rief pruneS3 direkt nach
	// PutObject auf — ein abgebrochener Upload verdrängte damit einen guten alten
	// Stand, bevor überhaupt jemand das neue Objekt geprüft hatte. Aufgeräumt wird
	// unten, erst nach bestandener Prüfung; dieselbe Reihenfolge wie beim
	// Volume-Ziel.
	if err := UploadS3File(ctx, s3, name, tmpPath, size, Retention{}); err != nil {
		recordS3Safe(ctx, pool, false)
		return "", err
	}
	// Bis hierher hieß "erfolgreich" nur, dass PutObject zurückkam. Ein
	// Verbindungsabbruch mitten im Upload hinterlässt ein kürzeres Objekt, das
	// genauso aussieht. Deshalb Stufe 2: Größe, dann zurücklesen und Prüfsumme
	// vergleichen — gegen die Summe des ERZEUGTEN Archivs, nicht gegen die des
	// Objekts, sonst prüft man das Ergebnis mit sich selbst.
	if verr := VerifyS3ObjectFile(ctx, s3, name, key, sha, size); verr != nil {
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
	if perr := PruneS3(ctx, s3, r); perr != nil {
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

// Alerter meldet einen Backup-Fehlschlag nach außen — an einen Menschen, nicht in
// eine Datei. Log und Änderungsprotokoll halten den Fehlschlag zwar fest, aber
// beide muss jemand ANSEHEN; genau das passiert bei einem Backup, das seit Wochen
// nicht mehr läuft, erfahrungsgemäß nicht (Hundert 04). nil = kein Versand.
type Alerter func(ctx context.Context, subject, body string)

// alertBackupFailure baut die Nachricht und schickt sie, wenn ein Alerter gesetzt
// ist. Als eigene Funktion, damit der Text an EINER Stelle steht und die drei
// Fehlerzweige unten nicht je eine eigene Formulierung erfinden.
func alertBackupFailure(ctx context.Context, alert Alerter, target, headline, detail string) {
	if alert == nil {
		return
	}
	// Vom Lauf-Context loesen, genau wie audit() zwoelf Zeilen weiter oben — und aus
	// demselben Grund, nur mit mehr Gewicht: der haeufigste Grund, WARUM ein Lauf
	// scheitert, ist das Ablaufen eben dieses Contexts (30-Minuten-Budget des Planers,
	// oder das Herunterfahren). Ein Versand darueber liefe in einen bereits
	// abgebrochenen Context, und ein context-treuer SMTP-Versand kaeme sofort
	// ergebnislos zurueck: Der Betreiber bekaeme ausgerechnet fuer den Fehlschlag
	// keine Nachricht, fuer den diese Funktion ueberhaupt existiert. Das Protokoll
	// haelt ihn zwar fest, aber dorthin schaut niemand von selbst — das ist die
	// gesamte Begruendung von Hundert 04.
	actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	var b strings.Builder
	fmt.Fprintf(&b, "Das geplante %s-Backup ist fehlgeschlagen.\n\n%s\n", target, headline)
	if detail != "" {
		fmt.Fprintf(&b, "\nDetails: %s\n", detail)
	}
	b.WriteString("\nSolange das so bleibt, gibt es keinen frischen Wiederherstellungspunkt.\n")
	b.WriteString("Die Backup-Kachel im Dashboard und das Änderungsprotokoll zeigen den Verlauf.\n")
	alert(actx, "Parkrr: "+target+"-Backup fehlgeschlagen", b.String())
}

// StartScheduler runs scheduled backups driven by the DB-stored cron schedule
// (backup_settings). Each minute it reloads the schedule and fires any target
// whose cron is due since its last recorded run. Blocks until stop is closed —
// run it in a goroutine. A no-op unless a key and at least one target (a backup
// directory or S3) are configured.
func StartScheduler(stop <-chan struct{}, pool *pgxpool.Pool, dbURL, key, dir string, s3 S3Config, alert Alerter) {
	if key == "" || (dir == "" && !s3.Enabled()) {
		return
	}
	// Der Lauf hängt am stop-Kanal (BAK-04). Vorher lief schedulerTick unter einem
	// eigenen 30-Minuten-Context, der vom Anhalten nichts wusste: ein Drain für eine
	// Browser-Wiederherstellung wartete dann bis zu einer halben Stunde auf einen
	// pg_dump von Daten, die gleich ersetzt werden — mit geschlossenem Zugang für
	// alle. Jetzt beendet das Schließen von stop pg_dump, Upload und Prüfung sofort.
	runCtx, cancelRuns := contextUntil(stop)
	defer cancelRuns()
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
			schedulerTick(runCtx, pool, dbURL, key, dir, s3, &lastVol, &lastS3, alert)
		}
	}
}

// contextUntil returns a context that is cancelled as soon as stop closes (or
// cancel is called, which also ends the watcher goroutine).
func contextUntil(stop <-chan struct{}) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

// EffectiveLast returns the later of the persisted last-run and the in-memory
// guard, or nil if neither is set (target never run).
//
// Exportiert, weil der automatische Rechnungslauf (server.StartAutoInvoice) genau
// dieselbe Frage stellt: ein gespeicherter Merker, ein Wächter im Speicher, und ein
// drittes, ECHTES "noch nie gelaufen". Ihn dort nachzubauen hiesse, die Regel zweimal
// zu haben — und beim naechsten Mal wuerde nur eine der beiden verbessert.
func EffectiveLast(dbLast *time.Time, mem time.Time) *time.Time {
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

// schedulerTick runs the volume and S3 backups that are due, audits each outcome
// and alerts on real failures; a busy lease or a shutdown is not a failure.
func schedulerTick(parent context.Context, pool *pgxpool.Pool, dbURL, key, dir string, s3 S3Config, lastVol, lastS3 *time.Time, alert Alerter) {
	if parent.Err() != nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Minute)
	defer cancel()
	// stopped: der Lauf wurde abgebrochen, weil die Anwendung angehalten wird
	// (Herunterfahren oder Wiederherstellung). Protokolliert wird das weiterhin,
	// aber niemand bekommt dafür eine Alarm-E-Mail — es ist kein Defekt.
	stopped := func() bool { return parent.Err() != nil }

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
	if dir != "" && fireDue(settings.VolumeCron, EffectiveLast(status.LastVolumeAt, *lastVol), now) {
		*lastVol = now // advance the guard before running so a status-write failure can't re-fire
		switch size, verified, err := RunVolume(ctx, pool, dbURL, key, dir, settings.VolumeRetention()); {
		case errors.Is(err, ErrBackupBusy):
			slog.Info("scheduled volume backup skipped: another instance is running it", "dir", dir)
		case err != nil && stopped():
			slog.Warn("scheduled volume backup cancelled: the application is stopping", "err", err)
			audit(ctx, actionBackupFailed, "Geplantes Volume-Backup abgebrochen (Anwendung wird angehalten)",
				map[string]any{"target": "volume", "ok": false, "cron": settings.VolumeCron, "error": err.Error()})
		case err != nil:
			slog.Error("scheduled volume backup failed", "err", err)
			audit(ctx, actionBackupFailed, "Geplantes Volume-Backup FEHLGESCHLAGEN",
				map[string]any{"target": "volume", "ok": false, "cron": settings.VolumeCron, "error": err.Error()})
			alertBackupFailure(ctx, alert, "Volume", "Der Lauf brach ab.", err.Error())
		case !verified && stopped():
			// The stop signal cancelled the verify step: a shutdown, not a bad archive.
			slog.Warn("scheduled volume backup cancelled during verification: the application is stopping", "dir", dir)
			audit(ctx, actionBackupFailed, "Geplantes Volume-Backup abgebrochen (Anwendung wird angehalten)",
				map[string]any{"target": "volume", "ok": false, "verified": false, "cron": settings.VolumeCron})
		case !verified:
			// Written, but it did not decrypt/restore-list cleanly. recordVolume already
			// flagged it not-OK; reporting ok:true here would leave the append-only trail
			// asserting a good backup that the status table simultaneously calls failed.
			slog.Warn("scheduled volume backup written but NOT verified", "dir", dir, "bytes", size)
			audit(ctx, actionBackupFailed, "Geplantes Volume-Backup geschrieben, aber NICHT verifiziert",
				map[string]any{"target": "volume", "ok": false, "verified": false, "bytes": size, "cron": settings.VolumeCron})
			alertBackupFailure(ctx, alert, "Volume",
				"Das Archiv wurde geschrieben, hat die Wiederherstellungsprüfung aber NICHT bestanden und wurde deshalb nicht übernommen.", "")
		default:
			slog.Info("scheduled volume backup written", "dir", dir, "bytes", size)
			audit(ctx, "backup", "Geplantes Volume-Backup erstellt und verifiziert",
				map[string]any{"target": "volume", "ok": true, "verified": true, "bytes": size, "cron": settings.VolumeCron, "keep": settings.VolumeKeep})
		}
	}
	if stopped() {
		return
	}
	if s3.Enabled() && dir != "" {
		// Der Volume-Lauf kann eine halbe Stunde gedauert haben. In der Zeit hat eine
		// andere Replik das fällige S3-Backup womöglich schon erledigt; mit dem Stand
		// vom Anfang des Ticks liefe es hier ein zweites Mal.
		if fresh, err := LoadStatus(ctx, pool); err == nil {
			status = fresh
		}
		now = time.Now()
	}
	if s3.Enabled() && fireDue(settings.S3Cron, EffectiveLast(status.LastS3At, *lastS3), now) {
		*lastS3 = now
		name, err := RunS3(ctx, pool, dbURL, key, s3, settings.S3Retention())
		switch {
		case errors.Is(err, ErrBackupBusy):
			slog.Info("scheduled S3 backup skipped: another instance is running it", "bucket", s3.Bucket)
		case err != nil && stopped():
			slog.Warn("scheduled S3 backup cancelled: the application is stopping", "err", err)
			audit(ctx, actionBackupFailed, "Geplantes S3-Backup abgebrochen (Anwendung wird angehalten)",
				map[string]any{"target": "s3", "ok": false, "bucket": s3.Bucket, "cron": settings.S3Cron, "error": err.Error()})
		case err != nil:
			slog.Error("scheduled S3 backup failed", "err", err)
			audit(ctx, actionBackupFailed, "Geplantes S3-Backup FEHLGESCHLAGEN",
				map[string]any{"target": "s3", "ok": false, "bucket": s3.Bucket, "cron": settings.S3Cron, "error": err.Error()})
			alertBackupFailure(ctx, alert, "S3", "Der Lauf in den Bucket "+s3.Bucket+" brach ab.", err.Error())
		default:
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
//
// Mehrere Repliken auf einem Verzeichnis (BAK-05): RunVolume hält während des
// ganzen Laufs die Sperre volumeLeaseName(dir), und jede .part trägt einen
// eindeutigen Namen. Solange der Sweep läuft, schreibt deshalb niemand sonst eine
// .part in dieses Verzeichnis — jede andere ist wirklich ein Rest.
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
// prunableFiles ist das Dateipendant zu prunableS3: alles jenseits der neuesten
// `keep` UND älter als `keepDays` Tage. files muss chronologisch aufsteigend
// sortiert sein (die Zeitstempel im Namen leisten das). keepDays=0 = keine
// Altersgrenze, also das bisherige Verhalten.
//
// Das Alter kommt aus der Änderungszeit der Datei, nicht aus dem Namen: ein
// wiederhergestelltes oder umbenanntes Archiv soll nach seinem tatsächlichen Alter
// beurteilt werden. Ist sie nicht lesbar, gilt die Datei als NICHT alt genug —
// im Zweifel aufbewahren statt löschen.
func prunableFiles(files []string, r Retention, now time.Time) []string {
	if r.Keep < 1 || len(files) <= r.Keep {
		return nil
	}
	cand := files[:len(files)-r.Keep]
	if r.KeepDays <= 0 {
		return cand
	}
	cutoff := now.AddDate(0, 0, -r.KeepDays)
	var out []string
	for _, f := range cand {
		fi, err := os.Stat(f)
		if err != nil {
			slog.Warn("backup: prune stat failed – keeping the archive", "path", f, "err", err)
			continue
		}
		if fi.ModTime().Before(cutoff) {
			out = append(out, f)
		}
	}
	return out
}

func pruneDir(ctx context.Context, dir string, r Retention) {
	if r.Keep < 1 {
		return
	}
	files, err := filepath.Glob(filepath.Join(dir, "parkrr-*.dump.enc"))
	if err != nil || len(files) <= r.Keep {
		return
	}
	sort.Strings(files) // timestamped names sort chronologically
	var removed []string
	for _, old := range prunableFiles(files, r, time.Now()) {
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
			fmt.Sprintf("%d alte Backup-Archive gelöscht (Aufbewahrung: %d)", len(removed), r.Keep),
			map[string]any{"deleted_files": removed, "deleted_count": len(removed), "keep": r.Keep, "keep_days": r.KeepDays})
	}
}
