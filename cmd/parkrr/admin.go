package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/preining/parkrr/internal/auth"
	"github.com/preining/parkrr/internal/config"
)

// bootstrapAdmin ensures the admin account defined via environment variables
// exists. On first run it is created; on subsequent runs its password, email
// and admin flag are refreshed so the ENV remains the source of truth.
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
		_, err = pool.Exec(ctx,
			`UPDATE users SET email=$1, password_hash=$2, is_admin=TRUE, role='admin', updated_at=now()
			 WHERE id=$3`, cfg.AdminEmail, hash, existingID)
		if err != nil {
			return err
		}
		slog.Info("admin account refreshed from environment", "username", cfg.AdminUsername)
	case errors.Is(err, pgx.ErrNoRows):
		_, err = pool.Exec(ctx,
			`INSERT INTO users (username, email, password_hash, is_admin, role)
			 VALUES ($1, $2, $3, TRUE, 'admin')`,
			cfg.AdminUsername, cfg.AdminEmail, hash)
		if err != nil {
			return err
		}
		slog.Info("admin account created from environment", "username", cfg.AdminUsername)
	default:
		return err
	}
	return nil
}
