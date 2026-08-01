package backup

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Settings is the GUI-editable per-target backup schedule (one row, id=1). The
// crons are 5-field standard cron; an empty cron means "off". Keep is the number
// of newest archives to retain (0 = keep all).
type Settings struct {
	VolumeCron string `json:"volume_cron"`
	VolumeKeep int    `json:"volume_keep"`
	S3Cron     string `json:"s3_cron"`
	S3Keep     int    `json:"s3_keep"`
}

// Status is the runtime record of the last scheduled runs (one row, id=1). The
// pointers are nil until the corresponding action has happened at least once.
type Status struct {
	LastVolumeAt    *time.Time `json:"last_volume_at"`
	LastVolumeSize  int64      `json:"last_volume_size"`
	LastVolumeOK    bool       `json:"last_volume_ok"`
	RestoreTestedAt *time.Time `json:"restore_tested_at"`
	LastS3At        *time.Time `json:"last_s3_at"`
	LastS3OK        bool       `json:"last_s3_ok"`
}

// LoadSettings reads the single backup_settings row.
func LoadSettings(ctx context.Context, pool *pgxpool.Pool) (Settings, error) {
	var s Settings
	err := pool.QueryRow(ctx,
		`SELECT volume_cron, volume_keep, s3_cron, s3_keep FROM backup_settings WHERE id = 1`,
	).Scan(&s.VolumeCron, &s.VolumeKeep, &s.S3Cron, &s.S3Keep)
	return s, err
}

// SaveSettings writes the single backup_settings row. Crons must already be
// validated by the caller (ValidCron).
func SaveSettings(ctx context.Context, pool *pgxpool.Pool, s Settings) error {
	if s.VolumeKeep < 0 {
		s.VolumeKeep = 0
	}
	if s.S3Keep < 0 {
		s.S3Keep = 0
	}
	_, err := pool.Exec(ctx,
		`UPDATE backup_settings
		    SET volume_cron = $1, volume_keep = $2, s3_cron = $3, s3_keep = $4
		  WHERE id = 1`,
		s.VolumeCron, s.VolumeKeep, s.S3Cron, s.S3Keep)
	return err
}

// LoadStatus reads the single backup_status row.
func LoadStatus(ctx context.Context, pool *pgxpool.Pool) (Status, error) {
	var s Status
	err := pool.QueryRow(ctx,
		`SELECT last_volume_at, last_volume_size, last_volume_ok,
		        restore_tested_at, last_s3_at, last_s3_ok
		   FROM backup_status WHERE id = 1`,
	).Scan(&s.LastVolumeAt, &s.LastVolumeSize, &s.LastVolumeOK,
		&s.RestoreTestedAt, &s.LastS3At, &s.LastS3OK)
	return s, err
}

// recordVolume stamps the outcome of a volume backup. tested marks that the
// written archive was decrypted and its table-of-contents listed successfully
// (an integrity check), updating restore_tested_at.
func recordVolume(ctx context.Context, pool *pgxpool.Pool, at time.Time, size int64, ok, tested bool) error {
	_, err := pool.Exec(ctx,
		`UPDATE backup_status
		    SET last_volume_at = $1, last_volume_size = $2, last_volume_ok = $3,
		        restore_tested_at = CASE WHEN $4 THEN $1 ELSE restore_tested_at END
		  WHERE id = 1`,
		at, size, ok, tested)
	return err
}

// recordS3 stamps the outcome of an S3 backup.
func recordS3(ctx context.Context, pool *pgxpool.Pool, at time.Time, ok bool) error {
	_, err := pool.Exec(ctx,
		`UPDATE backup_status SET last_s3_at = $1, last_s3_ok = $2 WHERE id = 1`,
		at, ok)
	return err
}

// SchemaVersion returns the newest applied migration filename (e.g.
// "022_backup_settings.sql"), or "" if none is recorded.
func SchemaVersion(ctx context.Context, pool *pgxpool.Pool) string {
	var v string
	_ = pool.QueryRow(ctx,
		`SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1`).Scan(&v)
	return v
}
