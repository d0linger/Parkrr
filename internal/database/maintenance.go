package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const restoreLockName = "parkrr.database-restore"

// ErrApplicationActive means at least one Parkrr server still owns the shared
// process lease, so an offline restore must not start.
var ErrApplicationActive = errors.New("one or more Parkrr application instances are still running")

// RestoreLease pins one raw PostgreSQL session. Application servers hold the
// shared form while an application generation can serve or run jobs; offline and
// coordinated browser restores require the exclusive form. This costs one
// connection per replica, not one per request.
type RestoreLease struct {
	conn   *pgx.Conn
	shared bool
}

func maintenanceConn(ctx context.Context, dbURL string) (*pgx.Conn, error) {
	// pgxpool.ParseConfig accepts pool_* URL parameters and gives us the underlying
	// connection config; pgx.ParseConfig would reject those production DSNs.
	cfg, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url for restore lease: %w", err)
	}
	conn, err := pgx.ConnectConfig(ctx, cfg.ConnConfig)
	if err != nil {
		return nil, fmt.Errorf("connect restore lease: %w", err)
	}
	return conn, nil
}

// AcquireApplicationLease blocks startup behind an active restore and then holds
// a shared lease until the server shuts down.
func AcquireApplicationLease(ctx context.Context, dbURL string) (*RestoreLease, error) {
	conn, err := maintenanceConn(ctx, dbURL)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec(ctx,
		`SELECT pg_advisory_lock_shared(hashtextextended($1, 0))`, restoreLockName); err != nil {
		_ = conn.Close(context.Background())
		return nil, fmt.Errorf("acquire application restore lease: %w", err)
	}
	return &RestoreLease{conn: conn, shared: true}, nil
}

// TryAcquireRestoreLease refuses immediately while any application replica is
// running. That makes the destructive CLI an explicitly offline operation.
func TryAcquireRestoreLease(ctx context.Context, dbURL string) (*RestoreLease, error) {
	conn, err := maintenanceConn(ctx, dbURL)
	if err != nil {
		return nil, err
	}
	var acquired bool
	if err := conn.QueryRow(ctx,
		`SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, restoreLockName).Scan(&acquired); err != nil {
		_ = conn.Close(context.Background())
		return nil, fmt.Errorf("acquire exclusive restore lease: %w", err)
	}
	if !acquired {
		_ = conn.Close(context.Background())
		return nil, ErrApplicationActive
	}
	return &RestoreLease{conn: conn}, nil
}

// AcquireRestoreLease waits until every application replica has released its
// shared process lease, then owns the exclusive restore lease. New replicas that
// request the shared form queue behind this waiter and cannot start serving in
// the middle of a restore.
func AcquireRestoreLease(ctx context.Context, dbURL string) (*RestoreLease, error) {
	conn, err := maintenanceConn(ctx, dbURL)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec(ctx,
		`SELECT pg_advisory_lock(hashtextextended($1, 0))`, restoreLockName); err != nil {
		_ = conn.Close(context.Background())
		return nil, fmt.Errorf("acquire exclusive restore lease: %w", err)
	}
	return &RestoreLease{conn: conn}, nil
}

// Release confirms the advisory unlock before closing the dedicated session.
// Closing is still attempted if the unlock check fails because session teardown
// is PostgreSQL's final lock-release backstop.
func (l *RestoreLease) Release() error {
	if l == nil || l.conn == nil {
		return nil
	}
	conn := l.conn
	l.conn = nil
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	fn := "pg_advisory_unlock"
	if l.shared {
		fn = "pg_advisory_unlock_shared"
	}
	var unlocked bool
	unlockErr := conn.QueryRow(ctx,
		`SELECT `+fn+`(hashtextextended($1, 0))`, restoreLockName).Scan(&unlocked)
	if unlockErr == nil && !unlocked {
		unlockErr = errors.New("restore advisory lock was not held")
	}
	closeErr := conn.Close(ctx)
	return errors.Join(unlockErr, closeErr)
}
