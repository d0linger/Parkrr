// Package database manages the Postgres connection pool and schema migrations.
package database

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Connect opens a pgx connection pool and waits until the database is reachable.
func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	// Default to 10, but honor an explicit pool_max_conns in the DSN (ParseConfig
	// already parsed it) — lets the test suite cap each package's pool so parallel
	// `go test ./...` doesn't collectively overwhelm a small test-DB container.
	// Production leaves it unset and keeps 10.
	if !strings.Contains(url, "pool_max_conns") {
		cfg.MaxConns = 10
	}
	cfg.MinConns = 1
	cfg.MaxConnLifetime = time.Hour
	// Server-side backstop: no single statement may run unbounded and pin a
	// pooled connection, even if a handler forgets a context deadline.
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "10000" // ms

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	// Retry until Postgres accepts connections (container startup ordering).
	deadline := time.Now().Add(60 * time.Second)
	for {
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err = pool.Ping(pingCtx)
		cancel()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			pool.Close()
			return nil, fmt.Errorf("database not reachable: %w", err)
		}
		// Cancellation-aware backoff: a shutdown during connect retry returns at once
		// instead of blocking the full second per iteration (finding L-03).
		select {
		case <-ctx.Done():
			pool.Close()
			return nil, ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}

	return pool, nil
}

// migrationLockKey is a fixed advisory-lock key ("prkr") that serializes schema
// migrations across application instances, so two pods starting at once cannot
// apply the same migration concurrently.
const migrationLockKey int64 = 0x70726b72

// Migrate applies all embedded SQL migrations that have not yet run. It holds a
// Postgres advisory lock for the duration so only one instance migrates at a
// time; others block until it finishes, then find every migration applied.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Release()

	// The pool sets a 10s statement_timeout as a per-request backstop, but that
	// must not apply to migrations (a large DDL/backfill can legitimately run
	// longer) or to the advisory-lock wait (which blocks until a peer finishes
	// migrating). Lift it for this connection and restore it before release so the
	// pooled connection returns to the default.
	if _, err := conn.Exec(ctx, `SET statement_timeout = 0`); err != nil {
		return fmt.Errorf("relax statement timeout for migrations: %w", err)
	}
	defer func() {
		resetCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(resetCtx, `SET statement_timeout = 10000`); err != nil {
			// Couldn't restore the cap: close the underlying connection so the pool
			// discards it on Release rather than handing out an unbounded one.
			_ = conn.Conn().Close(resetCtx)
		}
	}()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockKey); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() {
		// Release on a fresh context so a cancelled ctx still unlocks.
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, migrationLockKey)
	}()

	_, err = conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`)
	if err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}

	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		var exists bool
		if err := conn.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)`, name,
		).Scan(&exists); err != nil {
			return fmt.Errorf("check migration %s: %w", name, err)
		}
		if exists {
			continue
		}

		sqlBytes, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}

		err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx,
				`INSERT INTO schema_migrations (version) VALUES ($1)`, name)
			return err
		})
		if err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
	}

	return nil
}

// auditKeepForeverEntities are never pruned at any age: invoice issuance/Storno,
// payment and its reversal, billing-settings changes (UID / USt-rate / Nummernkreis)
// and per-period Pauschale/Nebenkosten settlements. For a person-level charge the
// audit row is the ONLY record that a period was paid, so these explain documents
// and receipts kept for 7 years (BAO §132).
var auditKeepForeverEntities = []string{"invoice", "payment", "billing", "flatrate", "recurring_charge"}

// auditShortLivedActions age out on the SHORT window instead of the long one:
// authentication and operational noise that is never a business transaction.
//
// The categorisation is deliberately an allow-list, so an action added later
// defaults to the LONG window — losing the trail of a change is far worse than
// keeping some noise. Two entries are pointedly NOT here:
//   - "restore": a database restore is the most security-relevant admin action
//     there is, not ops noise;
//   - "security": the passkey-clone warning is technically auth, but it is an
//     indication of compromise and belongs in the long window.
var auditShortLivedActions = []string{"login", "logout", "backup", "remind", "import"}

// auditPruneBatch caps how many rows a single DELETE statement removes.
//
// The size matters because audit_log carries a FOR EACH ROW plpgsql trigger
// (migration 008) and every pooled connection runs under statement_timeout=10s.
// One unbounded DELETE therefore has a hard cliff, and past it the failure mode is
// not "slow" but "never": the statement is cancelled, the whole transaction rolls
// back, and exactly zero rows are removed — so the next run faces the same backlog
// and fails identically, forever.
//
// Measured against the real schema and trigger on this deployment's Postgres:
// 200k rows delete in 515 ms (~390k rows/s), while 4M rows hit the 10s cap and
// removed nothing at all, twice in a row. 20k rows is ~50 ms, two orders of
// magnitude inside the cap, and keeps each transaction (and so each lock hold on
// an append-only table) short.
const auditPruneBatch = 20000

// PruneAuditLog applies the retention policy. Each batch runs in its own
// transaction that opts into the append-only guard's retention exception (see
// migration 008), so this is the single sanctioned path for removing audit rows.
//
// keep is the long window (records of who changed what). shortKeep, when > 0 and
// shorter than keep, additionally ages out auditShortLivedActions early; pass 0 to
// disable the short tier so everything follows keep. Returns the total pruned.
//
// Per-batch commits are deliberate: retention is idempotent, so partial progress is
// durable and a cancelled run resumes where it stopped instead of discarding its
// work. The returned count is therefore meaningful even alongside an error.
func PruneAuditLog(ctx context.Context, pool *pgxpool.Pool, keep, shortKeep time.Duration) (int64, error) {
	var total int64
	// keep <= 0 means "keep the trail forever" — skip the long pass entirely; the
	// short tier below is an independent setting and still applies.
	if keep > 0 {
		n, err := pruneAuditBatched(ctx, pool,
			`DELETE FROM audit_log WHERE id IN (
			     SELECT id FROM audit_log
			      WHERE created_at < $1 AND entity <> ALL($2)
			      LIMIT $3)`,
			time.Now().Add(-keep), auditKeepForeverEntities)
		total += n
		if err != nil {
			return total, err
		}
	}
	// Short tier: auth/ops noise only, and only when it actually shortens the
	// window (a shortKeep >= keep would be a no-op the long pass already covered).
	//
	// Deliberately NOT filtered by auditKeepForeverEntities. That list protects
	// records of account by ENTITY, but a short-lived ACTION is noise whatever it
	// references: a payment reminder is logged under entity 'invoice' because it
	// concerns that invoice, yet the mail is not the record — the invoice is. With
	// the entity filter the highest-volume noise category on a reminder-sending
	// deployment was the one category the tier provably could not touch.
	// auditShortLivedActions is the authority here, and it is an allow-list with a
	// test demanding a documented reason per entry.
	if shortKeep > 0 && (keep <= 0 || shortKeep < keep) {
		n, err := pruneAuditBatched(ctx, pool,
			`DELETE FROM audit_log WHERE id IN (
			     SELECT id FROM audit_log
			      WHERE created_at < $1 AND action = ANY($2)
			      LIMIT $3)`,
			time.Now().Add(-shortKeep), auditShortLivedActions)
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// pruneAuditBatched runs one retention pass to completion in bounded batches. The
// cutoff is computed once by the caller so the set being deleted cannot grow while
// the loop drains it, which is what guarantees termination.
func pruneAuditBatched(ctx context.Context, pool *pgxpool.Pool, sql string, cutoff time.Time, list []string) (int64, error) {
	var total int64
	for {
		// Stop cleanly on a cancelled/expired context rather than letting the next
		// BeginFunc fail: everything committed so far is kept.
		if err := ctx.Err(); err != nil {
			return total, err
		}
		var n int64
		err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			// The guard flag is transaction-scoped, so it must be re-set per batch.
			if _, err := tx.Exec(ctx, `SET LOCAL parkrr.allow_audit_prune = 'on'`); err != nil {
				return err
			}
			ct, err := tx.Exec(ctx, sql, cutoff, list, auditPruneBatch)
			if err != nil {
				return err
			}
			n = ct.RowsAffected()
			return nil
		})
		if err != nil {
			return total, err
		}
		total += n
		// A short batch means the matching set is drained.
		if n < auditPruneBatch {
			return total, nil
		}
	}
}
