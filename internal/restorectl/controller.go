// Package restorectl coordinates browser-triggered database restores across
// Parkrr replicas. It stores only non-secret job metadata in a schema excluded
// from backups; archive keys remain in the owning process's memory.
package restorectl

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/preining/parkrr/internal/backup"
)

type Phase string

const (
	PhaseQueued          Phase = "queued"
	PhaseDraining        Phase = "draining"
	PhaseRestoring       Phase = "restoring"
	PhaseMigrating       Phase = "migrating"
	PhasePurgingSessions Phase = "purging_sessions"
	PhaseVerifying       Phase = "verifying"
	PhaseRecovering      Phase = "recovering"
	PhaseComplete        Phase = "complete"
	PhaseFailed          Phase = "failed"
	PhaseCancelled       Phase = "cancelled"
)

const coordinatorTimeout = 15 * time.Second

var (
	ErrDisabled        = errors.New("browser restore is disabled")
	ErrBusy            = errors.New("another restore is already active")
	ErrNotOwner        = errors.New("restore job belongs to another instance")
	ErrCoordinatorLost = errors.New("restore coordinator heartbeat expired")
)

type Job struct {
	ID               string    `json:"id"`
	OwnerInstance    string    `json:"-"`
	Phase            Phase     `json:"phase"`
	SourceName       string    `json:"source_name"`
	SourcePath       string    `json:"-"`
	BackupCreated    string    `json:"backup_created,omitempty"`
	ArchiveEntries   int       `json:"archive_entries"`
	ChecksumSHA256   string    `json:"checksum_sha256"`
	RequestedBy      string    `json:"-"`
	ErrorMessage     string    `json:"error,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	CompletedAt      time.Time `json:"completed_at,omitempty"`
	CoordinatorStale bool      `json:"-"`
}

func (j Job) Terminal() bool {
	return j.Phase == PhaseComplete || j.Phase == PhaseFailed || j.Phase == PhaseCancelled
}

func (j Job) CoordinatorLost() bool {
	return !j.Terminal() && j.CoordinatorStale
}

type SubmitRequest struct {
	SourcePath     string
	SourceName     string
	BackupCreated  string
	ArchiveEntries int
	ChecksumSHA256 string
	RequestedBy    string
	Key            string
}

type localSecret struct {
	path            string
	key             string
	heartbeatCancel context.CancelFunc
}

type Controller struct {
	pool       *pgxpool.Pool
	enabled    bool
	instanceID string
	ctx        context.Context
	cancel     context.CancelFunc
	closeOnce  sync.Once

	mu      sync.Mutex
	secrets map[string]localSecret

	statusMu     sync.Mutex
	statusTokens float64
	statusLast   time.Time
	statusSlots  chan struct{}
}

func New(ctx context.Context, pool *pgxpool.Pool, enabled bool) (*Controller, error) {
	if ctx == nil {
		return nil, errors.New("restore controller requires a context")
	}
	if pool == nil {
		return nil, errors.New("restore controller requires a database pool")
	}
	instanceID, err := randomID()
	if err != nil {
		return nil, fmt.Errorf("create restore instance id: %w", err)
	}
	controllerCtx, cancel := context.WithCancel(ctx)
	return &Controller{
		pool:         pool,
		enabled:      enabled,
		instanceID:   instanceID,
		ctx:          controllerCtx,
		cancel:       cancel,
		secrets:      make(map[string]localSecret),
		statusTokens: 40,
		statusLast:   time.Now(),
		statusSlots:  make(chan struct{}, 4),
	}, nil
}

func (c *Controller) acquireStatusSlot(now time.Time) bool {
	c.statusMu.Lock()
	if c.statusLast.IsZero() {
		c.statusLast = now
		c.statusTokens = 40
	}
	c.statusTokens += now.Sub(c.statusLast).Seconds() * 20
	if c.statusTokens > 40 {
		c.statusTokens = 40
	}
	c.statusLast = now
	if c.statusTokens < 1 {
		c.statusMu.Unlock()
		return false
	}
	c.statusTokens--
	c.statusMu.Unlock()

	select {
	case c.statusSlots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (c *Controller) releaseStatusSlot() {
	<-c.statusSlots
}

// Close stops coordinator heartbeats and removes locally staged encrypted
// archives that were queued but not yet consumed. It is safe to call repeatedly.
func (c *Controller) Close() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		if c.cancel != nil {
			c.cancel()
		}
		c.mu.Lock()
		secrets := c.secrets
		c.secrets = make(map[string]localSecret)
		c.mu.Unlock()
		for _, secret := range secrets {
			if secret.heartbeatCancel != nil {
				secret.heartbeatCancel()
			}
			if secret.path != "" {
				if err := os.Remove(secret.path); err != nil && !errors.Is(err, os.ErrNotExist) {
					slog.Warn("remove staged restore archive", "err", err)
				}
			}
		}
	})
}

func randomID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func (c *Controller) Enabled() bool { return c != nil && c.enabled }

func (c *Controller) InstanceID() string {
	if c == nil {
		return ""
	}
	return c.instanceID
}

func (c *Controller) Submit(ctx context.Context, in SubmitRequest) (Job, error) {
	if !c.Enabled() {
		return Job{}, ErrDisabled
	}
	if in.SourcePath == "" || in.SourceName == "" || in.Key == "" || in.ChecksumSHA256 == "" {
		return Job{}, errors.New("restore submission is incomplete")
	}
	id, err := randomID()
	if err != nil {
		return Job{}, fmt.Errorf("create restore job id: %w", err)
	}
	job := Job{
		ID:             id,
		OwnerInstance:  c.instanceID,
		Phase:          PhaseQueued,
		SourceName:     in.SourceName,
		SourcePath:     in.SourcePath,
		BackupCreated:  in.BackupCreated,
		ArchiveEntries: in.ArchiveEntries,
		ChecksumSHA256: in.ChecksumSHA256,
		RequestedBy:    in.RequestedBy,
	}
	err = c.pool.QueryRow(ctx, `
		INSERT INTO parkrr_control.restore_jobs
			(id, owner_instance, phase, source_name, source_path, backup_created,
			 archive_entries, checksum_sha256, requested_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING created_at, updated_at`,
		job.ID, job.OwnerInstance, job.Phase, job.SourceName, job.SourcePath,
		job.BackupCreated, job.ArchiveEntries, job.ChecksumSHA256, job.RequestedBy,
	).Scan(&job.CreatedAt, &job.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" &&
			pgErr.ConstraintName == "restore_jobs_one_active" {
			return Job{}, ErrBusy
		}
		return Job{}, fmt.Errorf("create restore job: %w", err)
	}
	heartbeatCancel := c.StartHeartbeat(c.ctx, job.ID)
	c.mu.Lock()
	c.secrets[job.ID] = localSecret{
		path: in.SourcePath, key: in.Key, heartbeatCancel: heartbeatCancel,
	}
	c.mu.Unlock()
	return job, nil
}

func (c *Controller) Active(ctx context.Context) (*Job, error) {
	row := c.pool.QueryRow(ctx, `
		SELECT id, owner_instance, phase, source_name, source_path, backup_created,
		       archive_entries, checksum_sha256, requested_by, error_message,
		       created_at, updated_at, completed_at,
		       updated_at < now() - ($1 * interval '1 second')
		FROM parkrr_control.restore_jobs
		WHERE phase NOT IN ('complete','failed','cancelled')
		ORDER BY created_at
		LIMIT 1`, int(coordinatorTimeout/time.Second))
	job, err := scanJob(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load active restore job: %w", err)
	}
	return &job, nil
}

func (c *Controller) Get(ctx context.Context, id string) (Job, error) {
	row := c.pool.QueryRow(ctx, `
		SELECT id, owner_instance, phase, source_name, source_path, backup_created,
		       archive_entries, checksum_sha256, requested_by, error_message,
		       created_at, updated_at, completed_at,
		       updated_at < now() - ($2 * interval '1 second')
		FROM parkrr_control.restore_jobs WHERE id=$1`, id, int(coordinatorTimeout/time.Second))
	job, err := scanJob(row)
	if err != nil {
		return Job{}, fmt.Errorf("load restore job: %w", err)
	}
	return job, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanJob(row rowScanner) (Job, error) {
	var job Job
	var completedAt *time.Time
	err := row.Scan(&job.ID, &job.OwnerInstance, &job.Phase, &job.SourceName,
		&job.SourcePath, &job.BackupCreated, &job.ArchiveEntries,
		&job.ChecksumSHA256, &job.RequestedBy, &job.ErrorMessage,
		&job.CreatedAt, &job.UpdatedAt, &completedAt, &job.CoordinatorStale)
	if completedAt != nil {
		job.CompletedAt = *completedAt
	}
	return job, err
}

func (c *Controller) UpdatePhase(ctx context.Context, id string, phase Phase) error {
	tag, err := c.pool.Exec(ctx, `
		UPDATE parkrr_control.restore_jobs
		SET phase=$3, error_message='', updated_at=now()
		WHERE id=$1 AND owner_instance=$2`, id, c.instanceID, phase)
	if err != nil {
		return fmt.Errorf("update restore phase: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrNotOwner
	}
	return nil
}

func (c *Controller) Finish(ctx context.Context, id string, phase Phase, publicError string) error {
	if phase != PhaseComplete && phase != PhaseFailed && phase != PhaseCancelled {
		return errors.New("restore terminal phase is invalid")
	}
	tag, err := c.pool.Exec(ctx, `
		UPDATE parkrr_control.restore_jobs
		SET phase=$3, error_message=$4, updated_at=now(), completed_at=now()
		WHERE id=$1 AND owner_instance=$2`, id, c.instanceID, phase, publicError)
	if err != nil {
		return fmt.Errorf("finish restore job: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrNotOwner
	}
	return nil
}

func (c *Controller) LocalSecret(id string) (path, key string, ok bool) {
	if c == nil {
		return "", "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	secret, ok := c.secrets[id]
	return secret.path, secret.key, ok
}

func (c *Controller) ForgetLocal(id string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	secret, ok := c.secrets[id]
	delete(c.secrets, id)
	c.mu.Unlock()
	if ok && secret.heartbeatCancel != nil {
		secret.heartbeatCancel()
	}
	if ok && secret.path != "" {
		if err := os.Remove(secret.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("remove staged restore archive", "err", err)
		}
	}
}

// RemoveRecoveredArchive cleans up a file left by a crashed owner. Only the two
// application-generated staging name/location shapes are accepted; a corrupted
// control row must not turn recovery into an arbitrary-file deletion primitive.
func (c *Controller) RemoveRecoveredArchive(job Job) {
	path := filepath.Clean(job.SourcePath)
	base := filepath.Base(path)
	dir := filepath.Dir(path)
	uploaded := filepath.Base(dir) == ".restore-staging" &&
		strings.HasPrefix(base, "restore-") && strings.HasSuffix(base, ".dump.enc")
	// S3 downloads live in the backup work directory (<PARKRR_BACKUP_DIR>/.tmp);
	// os.TempDir() stays accepted for the fallback and for jobs of older versions.
	s3Dir := dir == filepath.Clean(backup.WorkDir()) || dir == filepath.Clean(os.TempDir())
	s3 := s3Dir && strings.HasPrefix(base, "parkrr-s3-") && strings.HasSuffix(base, ".dump.enc")
	if !uploaded && !s3 {
		slog.Warn("refusing to remove unrecognized recovered restore path", "job", job.ID)
		return
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("remove recovered restore archive", "job", job.ID, "err", err)
	}
}

func (c *Controller) StartHeartbeat(ctx context.Context, id string) func() {
	heartbeatCtx, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		failed := false
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				_, err := c.pool.Exec(heartbeatCtx, `
					UPDATE parkrr_control.restore_jobs SET updated_at=now()
					WHERE id=$1 AND owner_instance=$2
					  AND phase NOT IN ('complete','failed','cancelled')`, id, c.instanceID)
				if err != nil && heartbeatCtx.Err() == nil && !failed {
					slog.Warn("restore coordinator heartbeat failed", "job", id, "err", err)
					failed = true
				} else if err == nil {
					failed = false
				}
			}
		}
	}()
	return cancel
}

func (c *Controller) Watch(ctx context.Context) <-chan Job {
	out := make(chan Job, 1)
	go func() {
		defer close(out)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		var last string
		failed := false
		for {
			job, err := c.Active(ctx)
			if err != nil && ctx.Err() == nil && !failed {
				slog.Warn("restore job watcher failed", "err", err)
				failed = true
			} else if err == nil {
				failed = false
			}
			if err == nil && job != nil {
				key := job.ID + ":" + string(job.Phase)
				if key != last {
					select {
					case out <- *job:
						last = key
					default:
					}
				}
			} else if err == nil {
				last = ""
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return out
}

func (c *Controller) WaitTerminal(ctx context.Context, id string) (Job, error) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		job, err := c.Get(ctx, id)
		if err != nil {
			return Job{}, err
		}
		if job.Terminal() {
			return job, nil
		}
		if job.CoordinatorLost() {
			return job, ErrCoordinatorLost
		}
		select {
		case <-ctx.Done():
			return Job{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *Controller) ClaimRecovery(ctx context.Context, job Job) (bool, error) {
	tag, err := c.pool.Exec(ctx, `
		UPDATE parkrr_control.restore_jobs
		SET owner_instance=$2, phase=$3, error_message='', updated_at=now()
		WHERE id=$1 AND owner_instance=$4 AND updated_at=$5
		  AND phase NOT IN ('complete','failed','cancelled')`,
		job.ID, c.instanceID, PhaseRecovering, job.OwnerInstance, job.UpdatedAt)
	if err != nil {
		return false, fmt.Errorf("claim restore recovery: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
