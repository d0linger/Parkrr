package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/preining/parkrr/internal/auth"
	"github.com/preining/parkrr/internal/config"
)

// auditSystem writes an audit row for an action the SYSTEM performed (no request,
// no logged-in user), such as the boot-time admin bootstrap. Best-effort: a failure
// is logged but must never stop the server from starting.
func auditSystem(ctx context.Context, pool *pgxpool.Pool, action, entity string, id int64, summary string) {
	var entID *int64
	if id > 0 {
		entID = &id
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO audit_log (user_id, username, action, entity, entity_id, summary)
		 VALUES (NULL, 'system', $1, $2, $3, $4)`,
		action, entity, entID, summary); err != nil {
		slog.Warn("audit: system entry failed", "action", action, "entity", entity, "err", err)
	}
}

// bootstrapAdmin ensures the admin account defined via environment variables
// exists. On first run it is created (password from ENV). On subsequent runs the
// email and admin flag are refreshed, but the password is left as-is so a change
// made in the UI is not silently reverted on restart — unless
// PARKRR_ADMIN_PASSWORD_FORCE=true, which re-applies the ENV password.
func bootstrapAdmin(ctx context.Context, pool *pgxpool.Pool, cfg *config.Config) error {
	hash, err := auth.HashPassword(cfg.AdminPassword)
	if err != nil {
		return err
	}

	var existingID int64
	err = pool.QueryRow(ctx,
		`SELECT id FROM users WHERE lower(username) = lower($1)`,
		cfg.AdminUsername,
	).Scan(&existingID)

	switch {
	case err == nil: //nolint:gocritic // clearest form for this three-way branch
		// Restrict the UPDATE to rows that actually differ, so a plain restart is a
		// no-op: RowsAffected then tells us whether anything really changed.
		var ct pgconn.CommandTag
		if cfg.AdminPasswordForce {
			ct, err = pool.Exec(ctx,
				`UPDATE users SET email=$1, password_hash=$2, is_admin=TRUE, role='admin', updated_at=now()
				 WHERE id=$3`, cfg.AdminEmail, hash, existingID)
		} else {
			ct, err = pool.Exec(ctx,
				`UPDATE users SET email=$1, is_admin=TRUE, role='admin', updated_at=now()
				 WHERE id=$2
				   AND (email IS DISTINCT FROM $1 OR NOT is_admin OR role <> 'admin')`,
				cfg.AdminEmail, existingID)
		}
		if err != nil {
			return err
		}
		// Emergency password rotation must not leave stolen admin cookies valid:
		// when the ENV password is force-re-applied, revoke every existing session for
		// this account so a compromised cookie dies with the old password (finding H-04).
		if cfg.AdminPasswordForce {
			if _, derr := pool.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, existingID); derr != nil {
				return derr
			}
		}
		changed := ct.RowsAffected() > 0
		// Log the mode as a control-flow-selected literal rather than logging the
		// AdminPasswordForce field directly: the field name matches a "password"
		// heuristic and trips clear-text-logging scanners even though it's a bool.
		mode := "env refresh (password left as-is)"
		if cfg.AdminPasswordForce {
			mode = "env refresh + password re-applied"
		}
		slog.Info("admin account refreshed from environment", "username", cfg.AdminUsername, "mode", mode)
		// Only audit when something actually changed. This branch runs on EVERY start
		// after the first, and audit_log is append-only with a 7-year window: a
		// crash-looping container would otherwise generate unbounded immutable rows
		// that the retention job cannot reach, drowning the trail. Without
		// AdminPasswordForce the statement only re-applies the same email/role, so
		// RowsAffected tells us whether it was a real change.
		if cfg.AdminPasswordForce || changed {
			auditSystem(ctx, pool, "update", "user", existingID,
				"Admin-Konto aus der Umgebung aktualisiert ("+cfg.AdminUsername+"): "+mode)
		}
	case errors.Is(err, pgx.ErrNoRows):
		var newID int64
		err = pool.QueryRow(ctx,
			`INSERT INTO users (username, email, password_hash, is_admin, role)
			 VALUES ($1, $2, $3, TRUE, 'admin') RETURNING id`,
			cfg.AdminUsername, cfg.AdminEmail, hash).Scan(&newID)
		if err != nil {
			return err
		}
		slog.Info("admin account created from environment", "username", cfg.AdminUsername)
		auditSystem(ctx, pool, "create", "user", newID,
			"Admin-Konto aus der Umgebung angelegt: "+cfg.AdminUsername)
	default:
		return err
	}
	return nil
}
