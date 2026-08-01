// Command parkrr starts the Parkrr web application server.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/preining/parkrr/internal/auth"
	"github.com/preining/parkrr/internal/backup"
	"github.com/preining/parkrr/internal/config"
	"github.com/preining/parkrr/internal/database"
	"github.com/preining/parkrr/internal/server"
)

func main() {
	// "parkrr healthcheck" is used by the container HEALTHCHECK. The distroless
	// image has no shell/curl, so the binary probes itself over HTTP.
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
	}
	// "parkrr restore <file>" decrypts and pg_restores a backup (destructive).
	if len(os.Args) > 1 && os.Args[1] == "restore" {
		os.Exit(runRestore(os.Args[2:]))
	}
	setupLogging()
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// healthcheck performs a local GET /healthz and returns a process exit code.
func healthcheck() int {
	addr := os.Getenv("PARKRR_LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		port = strings.TrimPrefix(addr, ":")
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	// #nosec G704 -- the URL targets this process's own /healthz on the operator-
	// configured listen address (PARKRR_LISTEN_ADDR), never remote/user input.
	resp, err := client.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

// setupLogging configures the default slog logger. Format and level are
// controlled by PARKRR_LOG_FORMAT (json|text) and PARKRR_LOG_LEVEL.
func setupLogging() {
	level := slog.LevelInfo
	switch strings.ToLower(os.Getenv("PARKRR_LOG_LEVEL")) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler = slog.NewJSONHandler(os.Stdout, opts)
	if strings.EqualFold(os.Getenv("PARKRR_LOG_FORMAT"), "text") {
		h = slog.NewTextHandler(os.Stdout, opts)
	}
	slog.SetDefault(slog.New(h))
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := database.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := database.Migrate(ctx, pool); err != nil {
		return err
	}
	slog.Info("database migrations applied")

	if err := ensureAdmin(ctx, cfg); err != nil {
		return err
	}

	authMgr, err := auth.NewManager(pool, auth.SessionConfig{
		MaxAge:         cfg.SessionMaxAge,
		Sliding:        cfg.SessionSliding,
		AbsoluteMaxAge: cfg.SessionAbsoluteMaxAge,
	}, cfg.SecureCookies, cfg.TrustedProxies, cfg.SessionSecret)
	if err != nil {
		return err
	}

	// Bootstrap/refresh the admin account from environment variables.
	if err := bootstrapAdmin(ctx, pool, cfg); err != nil {
		return err
	}

	webAuthn, err := auth.NewWebAuthnService(pool, cfg.WebAuthnRPID,
		cfg.WebAuthnRPDisplayName, cfg.WebAuthnOrigins)
	if err != nil {
		return err
	}
	if webAuthn.Enabled() {
		slog.Info("passkeys enabled", "rp_id", cfg.WebAuthnRPID, "origins", cfg.WebAuthnOrigins)
	}

	cleanupStop := make(chan struct{})
	defer close(cleanupStop)

	handler, err := server.New(pool, authMgr, webAuthn, cfg.RateLimitPerMin, cfg.MetricsToken,
		cfg.CheckBreachedPasswords, cfg.FailClosedOnBreach, cfg.BackupKey, cfg.DatabaseURL, cfg.BackupDir, cleanupStop)
	if err != nil {
		return err
	}

	// Scheduled encrypted backups to a mounted directory (opt-in via env).
	if cfg.BackupKey != "" && cfg.BackupDir != "" {
		go backup.StartScheduled(cleanupStop, cfg.DatabaseURL, cfg.BackupKey, cfg.BackupDir,
			cfg.BackupIntervalHours, cfg.BackupKeep)
		slog.Info("scheduled backups enabled", "dir", cfg.BackupDir, "interval_h", cfg.BackupIntervalHours, "keep", cfg.BackupKeep)
	}

	go server.StartSessionCleanup(authMgr, cleanupStop)
	go server.StartAuditRetention(pool,
		time.Duration(cfg.AuditRetentionDays)*24*time.Hour, cleanupStop)

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		slog.Info("parkrr listening", "addr", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		return err
	case <-ctx.Done():
		slog.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// ensureAdmin validates admin configuration is present.
func ensureAdmin(_ context.Context, cfg *config.Config) error {
	if cfg.AdminUsername == "" || cfg.AdminPassword == "" {
		return errors.New("admin username and password must be configured")
	}
	return nil
}
