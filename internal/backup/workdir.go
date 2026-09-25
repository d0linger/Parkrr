package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// Arbeitsverzeichnis für große Zwischendateien (BAK-02/BAK-06).
//
// Die Streaming-Pfade schreiben Dateien in voller Archivgröße: das S3-Upload-
// Archiv, jeden S3-Download und Rücklesevorgang, den Browser-Download und beim
// Einspielen den entschlüsselten Dump. Unter os.TempDir() landete das im
// gehärteten Compose-Overlay auf einem 32-MiB-tmpfs (ENOSPC bei jeder echten
// Datenbank) und sonst in der beschreibbaren Container-Schicht, wo ein
// SIGKILL eine Klartextkopie über jeden Neustart hinweg liegen ließ.
//
// Deshalb liegen sie jetzt unter <PARKRR_BACKUP_DIR>/.tmp — einem Volume, das
// ohnehin für Archive dieser Größe bemessen ist. Nur ohne konfiguriertes
// Backup-Verzeichnis bleibt es bei os.TempDir().
const workDirName = ".tmp"

// staleWorkFileAge: Dateien einer FREMDEN Replik (gemeinsames Backup-Volume)
// werden erst jenseits dieser Frist als verwaist behandelt. Sie liegt über jedem
// Laufbudget, das eine solche Datei offen halten kann (Wiederherstellung 2 h,
// Planer 30 min, Browser-Upload 30 min).
const staleWorkFileAge = 3 * time.Hour

var workDirBase atomic.Pointer[string]

// ConfigureWorkDir legt fest, wo große Zwischendateien entstehen:
// <backupDir>/.tmp, oder os.TempDir(), wenn backupDir leer ist. Einmal beim
// Start aufrufen (Server und CLI), bevor irgendein Backup-Pfad läuft.
func ConfigureWorkDir(backupDir string) {
	workDirBase.Store(&backupDir)
}

// WorkDir liefert das Verzeichnis für große Zwischendateien, ohne es anzulegen.
func WorkDir() string {
	if p := workDirBase.Load(); p != nil && *p != "" {
		return filepath.Join(*p, workDirName)
	}
	return os.TempDir()
}

// hostTag kennzeichnet die Zwischendateien DIESES Containers. Mehrere Repliken
// können sich ein Backup-Volume teilen; beim Start darf eine Replik nur ihre
// eigenen Reste sofort wegräumen, die der anderen erst nach staleWorkFileAge.
// Der Hostname eines Containers überlebt einen Neustart, deshalb findet ein
// neu gestarteter Prozess die Klartext-Reste seines abgestürzten Vorgängers.
var hostTag = func() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		h = "unknown"
	}
	sum := sha256.Sum256([]byte(h))
	return hex.EncodeToString(sum[:4])
}()

// createWorkFile legt eine private (0600) Zwischendatei im Arbeitsverzeichnis an.
// prefix muss mit "-" enden, z. B. "parkrr-restore-".
func createWorkFile(prefix, suffix string) (*os.File, error) {
	dir := WorkDir()
	if dir != os.TempDir() {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		// MkdirAll setzt die Rechte nur bei NEU angelegten Verzeichnissen.
		_ = os.Chmod(dir, 0o700) // #nosec G302 -- a directory needs the search bit; 0700 is owner-only
	}
	return os.CreateTemp(dir, prefix+hostTag+"-*"+suffix)
}

// CreateWorkFile ist createWorkFile für Aufrufer außerhalb des Pakets (der
// Browser-Download in internal/handlers).
func CreateWorkFile(prefix, suffix string) (*os.File, error) {
	return createWorkFile(prefix, suffix)
}

// StagingFilePattern ist das os.CreateTemp-Muster für hochgeladene Archive in
// <dir>/.restore-staging. Das Host-Kennzeichen erlaubt SweepWorkFiles, die
// eigenen Reste sofort zu erkennen.
func StagingFilePattern() string { return "restore-" + hostTag + "-*.dump.enc" }

// workFilePatterns sind die Namen, die die Backup-Pfade selbst erzeugen. Nur sie
// werden je angefasst — os.TempDir() gehört nicht Parkrr allein.
var workFilePatterns = []string{
	"parkrr-restore-*.dump",
	"parkrr-s3-*.dump.enc", // DownloadS3File und RunS3 (parkrr-s3-upload-*)
	"parkrr-download-*.dump.enc",
}

// SweepWorkFiles räumt beim Start Zwischendateien auf, die ein abgebrochener
// Prozess (SIGKILL, OOM) hinterlassen hat: entschlüsselte Dumps, S3-Kopien,
// Browser-Downloads und verwaiste Uploads in .restore-staging. Das defer
// os.Remove der Pfade selbst läuft in genau diesen Fällen nicht.
//
// Eigene Dateien (Host-Kennzeichen) gehen sofort, fremde erst nach
// staleWorkFileAge — sie können einer laufenden Replik gehören. keep sind Pfade,
// auf die ein aktiver Restore-Job verweist; sie bleiben immer liegen.
func SweepWorkFiles(backupDir string, keep ...string) {
	keepSet := make(map[string]bool, len(keep))
	for _, k := range keep {
		if k != "" {
			keepSet[filepath.Clean(k)] = true
		}
	}
	dirs := []string{os.TempDir()}
	if backupDir != "" {
		dirs = append(dirs, filepath.Join(backupDir, workDirName))
	}
	now := time.Now()
	for _, dir := range dirs {
		for _, pattern := range workFilePatterns {
			sweepWorkPattern(dir, pattern, keepSet, now)
		}
	}
	stagingBase := backupDir
	if stagingBase == "" {
		stagingBase = os.TempDir()
	}
	sweepWorkPattern(filepath.Join(stagingBase, ".restore-staging"), "restore-*.dump.enc", keepSet, now)
}

// sweepWorkPattern removes leftover work files matching pattern in dir: this
// host's at once, other hosts' only once stale; paths in keep are skipped.
func sweepWorkPattern(dir, pattern string, keep map[string]bool, now time.Time) {
	matches, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil {
		return
	}
	own := "-" + hostTag + "-"
	for _, f := range matches {
		if keep[filepath.Clean(f)] {
			continue
		}
		fi, err := os.Lstat(f)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		if !strings.Contains(filepath.Base(f), own) && now.Sub(fi.ModTime()) < staleWorkFileAge {
			continue // gehört womöglich einer anderen, noch laufenden Replik
		}
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("backup: could not remove a leftover temporary file", "path", f, "err", err)
			continue
		}
		slog.Info("backup: removed a leftover temporary file from an interrupted run", "path", f)
	}
}
