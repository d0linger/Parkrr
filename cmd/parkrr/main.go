// Command parkrr starts the Parkrr web application server.
package main

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/preining/parkrr/internal/restorectl"
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
	// "parkrr seed-demo" befüllt eine FRISCHE Datenbank mit Demo-Daten (Hundert 95).
	if len(os.Args) > 1 && os.Args[1] == "seed-demo" {
		os.Exit(runSeedDemo(os.Args[2:]))
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
	// Die Geschäftszeitzone einmal als Prozesszone setzen, VOR jedem time.Now() und
	// vor dem Verbindungsaufbau (database.Connect bindet die Sitzungszeitzone daran).
	// Danach meinen Anwendung und Datenbank denselben Kalendertag (Hundert 13).
	time.Local = cfg.Location
	slog.Info("business time zone", "zone", time.Local.String())

	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Große Zwischendateien (S3, Downloads, entschlüsselte Dumps) unter
	// <PARKRR_BACKUP_DIR>/.tmp statt im oft winzigen /tmp (BAK-02).
	backup.ConfigureWorkDir(cfg.BackupDir)

	pool, err := database.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// One shared lease prevents the offline restore CLI from running while this
	// replica can serve requests or background jobs. A coordinated browser restore
	// releases it only after the application generation has fully drained.
	appLease, err := database.AcquireApplicationLease(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer func() {
		if appLease != nil {
			if err := appLease.Release(); err != nil {
				slog.Error("release application restore lease", "err", err)
			}
		}
	}()

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
	if err := authMgr.SetTrustedProxyCIDRs(cfg.TrustedProxyCIDRs); err != nil {
		return err
	}
	if cfg.Require2FA {
		authMgr.SetRequire2FA(true)
		slog.Info("required MFA enabled: enrollment and verified session factor required")
	}
	if cfg.TrustedProxies && len(cfg.TrustedProxyCIDRs) == 0 {
		// Fail closed at startup: trusting forwarded headers from ANY direct peer lets a
		// client with direct backend access spoof audit/rate-limit IPs. Refuse to start
		// rather than run in that state (finding H-06).
		return errors.New("PARKRR_TRUSTED_PROXY=true requires PARKRR_TRUSTED_PROXY_CIDRS " +
			"(the reverse-proxy IP range); without it forwarded client IPs would be trusted " +
			"from any direct peer. Set the CIDR allowlist, or set PARKRR_TRUSTED_PROXY=false " +
			"when the backend is reached directly")
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
	webAuthn.SetSuspendOnClone(cfg.WebAuthnSuspendOnClone)
	if webAuthn.Enabled() {
		slog.Info("passkeys enabled", "rp_id", cfg.WebAuthnRPID, "origins", cfg.WebAuthnOrigins)
	}

	s3 := backup.S3Config{
		Endpoint: cfg.S3Endpoint, Bucket: cfg.S3Bucket,
		AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey,
		Region: cfg.S3Region, Prefix: cfg.S3Prefix, UseSSL: cfg.S3UseSSL,
	}
	mailer := newMailer(pool, cfg)
	if mailer.Enabled() {
		slog.Info("SMTP e-mail enabled", "host", cfg.SMTPHost, "port", cfg.SMTPPort, "tls", cfg.SMTPTLS)
	}

	// This pool remains available while the application pool and handlers are
	// quiesced. The parkrr_control schema is deliberately excluded from backups.
	controlPool, err := database.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer controlPool.Close()
	restores, err := restorectl.New(ctx, controlPool, cfg.BrowserRestore)
	if err != nil {
		return err
	}
	defer restores.Close()
	if cfg.BrowserRestore {
		slog.Warn("browser restore enabled; recent administrator authentication and checksum confirmation are required")
	}

	lifecycle := newLifecycleHandler(restores)
	pending, err := restores.Active(ctx)
	if err != nil {
		return err
	}
	// Reste abgebrochener Läufe wegräumen (BAK-06): ein SIGKILL/OOM-Kill überspringt
	// jedes defer os.Remove und ließ sonst einen entschlüsselten Dump liegen. Erst
	// hier, mit gehaltener Anwendungs-Lease (kein CLI-Restore läuft) und bekanntem
	// Restore-Job, dessen Archiv nicht angefasst werden darf.
	var keepStaged string
	if pending != nil {
		keepStaged = pending.SourcePath
	}
	backup.SweepWorkFiles(cfg.BackupDir, keepStaged)
	var generation *appGeneration
	if pending == nil {
		generation, err = startAppGeneration(pool, authMgr, webAuthn, cfg, s3, mailer, restores)
		if err != nil {
			return err
		}
		lifecycle.Activate(generation.handler)
	} else if err := lifecycle.EnterMaintenance(ctx, pending.ID); err != nil {
		return err
	}
	defer func() {
		drainCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := generation.Drain(drainCtx); err != nil {
			slog.Warn("background worker drain failed", "err", err)
		}
	}()

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           lifecycle,
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

	jobs := restores.Watch(ctx)
	for {
		var job restorectl.Job
		if pending != nil {
			job = *pending
			pending = nil
		} else {
			select {
			case err := <-serverErr:
				return err
			case <-ctx.Done():
				slog.Info("shutdown signal received")
				return shutdownServer(srv, lifecycle, generation)
			case watched, ok := <-jobs:
				if !ok {
					if ctx.Err() != nil {
						return shutdownServer(srv, lifecycle, generation)
					}
					return errors.New("restore job watcher stopped unexpectedly")
				}
				job = watched
			}
		}

		if job.Terminal() {
			continue
		}
		current, err := restores.Get(ctx, job.ID)
		if err != nil {
			return err
		}
		if current.Terminal() {
			continue
		}
		job = current
		if err := lifecycle.EnterMaintenance(ctx, job.ID); err != nil {
			return err
		}
		if err := generation.Drain(ctx); err != nil {
			return err
		}
		pool.Reset()
		if err := appLease.Release(); err != nil {
			return fmt.Errorf("release application restore lease: %w", err)
		}
		appLease = nil

		safe, restoreErr := coordinateRestore(ctx, cfg.DatabaseURL, pool, restores, job)
		if !safe {
			return fmt.Errorf("restore %s did not reach a safe database state: %w", job.ID, restoreErr)
		}
		if restoreErr != nil {
			slog.Error("browser restore ended without replacing the database", "job", job.ID, "err", restoreErr)
		}

		pool.Reset()
		appLease, err = database.AcquireApplicationLease(ctx, cfg.DatabaseURL)
		if err != nil {
			return err
		}
		if err := database.Migrate(ctx, pool); err != nil {
			return err
		}
		// Restoring an older database must not roll back the environment-managed
		// administrator identity. Startup applies this same invariant.
		if err := bootstrapAdmin(ctx, pool, cfg); err != nil {
			return err
		}
		generation, err = startAppGeneration(pool, authMgr, webAuthn, cfg, s3, mailer, restores)
		if err != nil {
			return err
		}
		lifecycle.Activate(generation.handler)
		slog.Info("application resumed after restore coordination", "job", job.ID)
	}
}

// ensureAdmin validates admin configuration is present.
func ensureAdmin(_ context.Context, cfg *config.Config) error {
	if cfg.AdminUsername == "" || cfg.AdminPassword == "" {
		return errors.New("admin username and password must be configured")
	}
	return nil
}
