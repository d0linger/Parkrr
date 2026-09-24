package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/preining/parkrr/internal/backup"
	"github.com/preining/parkrr/internal/database"
	"github.com/preining/parkrr/internal/handlers"
	"github.com/preining/parkrr/internal/restorectl"
)

const browserRestoreExecutionTimeout = 2 * time.Hour

// coordinateRestore either performs the locally-owned restore or waits for the
// owner. If an owner disappears, exactly one surviving replica claims recovery
// and reapplies the idempotent database postconditions without replaying restore.
func coordinateRestore(
	ctx context.Context,
	dbURL string,
	pool *pgxpool.Pool,
	restores *restorectl.Controller,
	job restorectl.Job,
) (bool, error) {
	if job.OwnerInstance == restores.InstanceID() {
		return executeBrowserRestore(ctx, dbURL, pool, restores, job)
	}
	for {
		terminal, err := restores.WaitTerminal(ctx, job.ID)
		if err == nil {
			if terminal.Phase == restorectl.PhaseFailed {
				slog.Warn("restore coordinated by another replica failed",
					"job", terminal.ID, "error", terminal.ErrorMessage)
			}
			return true, nil
		}
		if !errors.Is(err, restorectl.ErrCoordinatorLost) {
			return false, err
		}
		claimed, claimErr := restores.ClaimRecovery(ctx, terminal)
		if claimErr != nil {
			return false, claimErr
		}
		if !claimed {
			continue
		}
		if err := recoverInterruptedRestore(ctx, dbURL, pool, restores, terminal); err != nil {
			return false, err
		}
		return true, nil
	}
}

func shutdownServer(
	srv *http.Server,
	lifecycle *lifecycleHandler,
	generation *appGeneration,
) error {
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancelDrain()
	gateErr := lifecycle.EnterMaintenance(drainCtx, "")
	workerErr := generation.Drain(drainCtx)

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelShutdown()
	return errors.Join(gateErr, workerErr, srv.Shutdown(shutdownCtx))
}

// executeBrowserRestore runs only after the HTTP admission gate is closed, all
// generation workers are stopped, and this replica has released its shared lease.
// The returned safe flag means post-restore invariants were re-established and it
// is permissible to rebuild the application generation even when restoreErr is not nil.
func executeBrowserRestore(
	ctx context.Context,
	dbURL string,
	pool *pgxpool.Pool,
	restores *restorectl.Controller,
	job restorectl.Job,
) (safe bool, restoreErr error) {
	path, key, ok := restores.LocalSecret(job.ID)
	if !ok {
		return false, errors.New("restore archive secret is unavailable on owning instance")
	}
	defer restores.ForgetLocal(job.ID)
	if err := restores.UpdatePhase(ctx, job.ID, restorectl.PhaseDraining); err != nil {
		return false, err
	}
	lease, err := database.AcquireRestoreLease(ctx, dbURL)
	if err != nil {
		return false, fmt.Errorf("wait for exclusive restore lease: %w", err)
	}
	release := func() error {
		if lease == nil {
			return nil
		}
		err := lease.Release()
		lease = nil
		return err
	}

	pool.Reset()
	if err := restores.UpdatePhase(ctx, job.ID, restorectl.PhaseRestoring); err != nil {
		return false, errors.Join(err, release())
	}
	restoreCtx, cancelRestore := context.WithTimeout(ctx, browserRestoreExecutionTimeout)
	restoreErr = backup.RestoreFile(restoreCtx, dbURL, path, key)
	cancelRestore()
	pool.Reset()
	if restoreErr != nil {
		slog.Error("browser restore failed; verifying the unchanged/rolled-back database", "job", job.ID, "err", restoreErr)
		if err := restores.UpdatePhase(ctx, job.ID, restorectl.PhaseRecovering); err != nil {
			return false, errors.Join(restoreErr, err, release())
		}
	} else if err := restores.UpdatePhase(ctx, job.ID, restorectl.PhaseMigrating); err != nil {
		return false, errors.Join(err, release())
	}

	if err := repairRestoredDatabase(ctx, pool, restores, job); err != nil {
		// Keep the job active. A restarted or surviving replica will claim recovery
		// after the heartbeat expires; readiness must never return before repair.
		return false, errors.Join(restoreErr, err, release())
	}
	if restoreErr != nil {
		auditRestoreOutcome(ctx, pool, job,
			"browser database restore failed; database invariants verified")
		finishErr := restores.Finish(ctx, job.ID, restorectl.PhaseFailed,
			"Wiederherstellung fehlgeschlagen; die Datenbank wurde geprüft und bleibt betriebsbereit.")
		return true, errors.Join(restoreErr, finishErr, release())
	}
	auditRestoreOutcome(ctx, pool, job,
		"browser database restore completed by "+job.RequestedBy)
	if err := restores.Finish(ctx, job.ID, restorectl.PhaseComplete, ""); err != nil {
		return false, errors.Join(err, release())
	}
	return true, release()
}

// recoverInterruptedRestore handles the deliberately ambiguous crash window around
// pg_restore commit. It never repeats the destructive restore. Instead it obtains
// the exclusive lease, reapplies every idempotent postcondition and only then lets
// replicas serve again.
func recoverInterruptedRestore(
	ctx context.Context,
	dbURL string,
	pool *pgxpool.Pool,
	restores *restorectl.Controller,
	job restorectl.Job,
) error {
	stopHeartbeat := restores.StartHeartbeat(ctx, job.ID)
	defer stopHeartbeat()
	lease, err := database.AcquireRestoreLease(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("acquire restore recovery lease: %w", err)
	}
	pool.Reset()
	if job.Phase == restorectl.PhaseQueued || job.Phase == restorectl.PhaseDraining {
		// The owner never crossed the explicit PhaseRestoring boundary, so no
		// pg_restore process was started. Preserve sessions and application data;
		// only close the abandoned job after proving the current schema is readable.
		var migrationCount int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&migrationCount); err != nil {
			return errors.Join(fmt.Errorf("verify database after pre-restore interruption: %w", err), lease.Release())
		}
		if migrationCount == 0 {
			return errors.Join(errors.New("verify database after pre-restore interruption: no migrations"), lease.Release())
		}
		restores.RemoveRecoveredArchive(job)
		auditRestoreOutcome(ctx, pool, job,
			"browser database restore interrupted before database replacement")
		finishErr := restores.Finish(ctx, job.ID, restorectl.PhaseFailed,
			"Der Restore-Prozess wurde vor der Datenbankänderung unterbrochen; der bisherige Datenstand blieb erhalten.")
		return errors.Join(finishErr, lease.Release())
	}
	repairErr := repairRestoredDatabase(ctx, pool, restores, job)
	if repairErr != nil {
		return errors.Join(repairErr, lease.Release())
	}
	restores.RemoveRecoveredArchive(job)
	auditRestoreOutcome(ctx, pool, job,
		"interrupted browser database restore recovered; verify restored data")
	finishErr := restores.Finish(ctx, job.ID, restorectl.PhaseFailed,
		"Der Restore-Prozess wurde unterbrochen. Die Datenbank wurde repariert und geprüft; bitte den Datenstand kontrollieren.")
	return errors.Join(finishErr, lease.Release())
}

func repairRestoredDatabase(
	ctx context.Context,
	pool *pgxpool.Pool,
	restores *restorectl.Controller,
	job restorectl.Job,
) error {
	if err := restores.UpdatePhase(ctx, job.ID, restorectl.PhaseMigrating); err != nil {
		return err
	}
	if err := database.Migrate(ctx, pool); err != nil {
		return fmt.Errorf("migrate restored database: %w", err)
	}
	if err := restores.UpdatePhase(ctx, job.ID, restorectl.PhasePurgingSessions); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `DELETE FROM sessions`); err != nil {
		return fmt.Errorf("purge restored sessions: %w", err)
	}
	post := handlers.New(pool)
	if err := post.BackfillPeriodPayments(ctx); err != nil {
		return fmt.Errorf("backfill restored period payments: %w", err)
	}
	if err := restores.UpdatePhase(ctx, job.ID, restorectl.PhaseVerifying); err != nil {
		return err
	}
	var sessions int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sessions`).Scan(&sessions); err != nil {
		return fmt.Errorf("verify restored sessions: %w", err)
	}
	if sessions != 0 {
		return fmt.Errorf("verify restored sessions: found %d rows", sessions)
	}
	var migrationCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&migrationCount); err != nil {
		return fmt.Errorf("verify restored migrations: %w", err)
	}
	if migrationCount == 0 {
		return errors.New("verify restored migrations: no applied migrations")
	}
	return nil
}

func auditRestoreOutcome(
	ctx context.Context,
	pool *pgxpool.Pool,
	job restorectl.Job,
	summary string,
) {
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	handlers.New(pool).AuditSystem(auditCtx, "restore", "system", 0, summary,
		map[string]any{"source": job.SourceName, "sha256": job.ChecksumSHA256,
			"backup_created": job.BackupCreated, "entries": job.ArchiveEntries})
}
