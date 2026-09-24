package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/preining/parkrr/internal/auth"
	"github.com/preining/parkrr/internal/backup"
	"github.com/preining/parkrr/internal/config"
	"github.com/preining/parkrr/internal/handlers"
	"github.com/preining/parkrr/internal/mail"
	"github.com/preining/parkrr/internal/restorectl"
	"github.com/preining/parkrr/internal/server"
)

// appGeneration contains every component that may access the application schema.
// It can be drained and discarded while the process-lifetime listener and restore
// controller remain available.
type appGeneration struct {
	handler http.Handler
	api     *handlers.Handler
	stop    chan struct{}
	once    sync.Once
	workers sync.WaitGroup
}

func startAppGeneration(
	pool *pgxpool.Pool,
	authMgr *auth.Manager,
	webAuthn *auth.WebAuthnService,
	cfg *config.Config,
	s3 backup.S3Config,
	mailer mail.Sender,
	restores *restorectl.Controller,
) (*appGeneration, error) {
	g := &appGeneration{stop: make(chan struct{})}
	startWorker := func(fn func()) {
		g.workers.Add(1)
		go func() {
			defer g.workers.Done()
			fn()
		}()
	}

	handler, apiHandler, err := server.New(pool, authMgr, webAuthn,
		cfg.RateLimitPerMin, cfg.MetricsToken, cfg.MetricsRequireAuth,
		cfg.CheckBreachedPasswords, cfg.FailClosedOnBreach, cfg.BackupKey,
		cfg.DatabaseURL, cfg.BackupDir, s3, mailer, cfg.PublicBaseURL,
		g.stop, startWorker)
	if err != nil {
		drainCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return nil, errors.Join(err, g.Drain(drainCtx))
	}
	apiHandler.Restore = restores
	if cfg.PasskeyOnly {
		apiHandler.PasskeyOnly = true
	}

	sysAudit := apiHandler.AuditSystem
	backup.SetAuditor(sysAudit)
	var backupAlert backup.Alerter
	if mailer.Enabled() && len(cfg.AlertEmail) > 0 {
		to := cfg.AlertEmail
		backupAlert = func(ctx context.Context, subject, body string) {
			sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			if err := mailer.Send(sendCtx, to, subject, body); err != nil {
				slog.Error("backup alert e-mail failed", "err", err)
			}
		}
	}

	if cfg.BackupKey != "" && (cfg.BackupDir != "" || s3.Enabled()) {
		startWorker(func() {
			backup.StartScheduler(g.stop, pool, cfg.DatabaseURL, cfg.BackupKey,
				cfg.BackupDir, s3, backupAlert)
		})
	}
	startWorker(func() { server.StartExpiryCleanup(pool, authMgr, g.stop) })
	startWorker(func() { server.StartAutoInvoice(pool, apiHandler, cfg.AutoInvoiceCron, g.stop) })
	startWorker(func() {
		server.StartAuditRetention(pool,
			time.Duration(cfg.AuditRetentionDays)*24*time.Hour,
			time.Duration(cfg.AuditRetentionShortDays)*24*time.Hour,
			g.stop, sysAudit)
	})

	g.handler = handler
	g.api = apiHandler
	return g, nil
}

func (g *appGeneration) stopWorkers() {
	if g != nil {
		g.once.Do(func() { close(g.stop) })
	}
}

func (g *appGeneration) Drain(ctx context.Context) error {
	if g == nil {
		return nil
	}
	g.stopWorkers()
	done := make(chan struct{})
	go func() {
		g.workers.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func newMailer(pool *pgxpool.Pool, cfg *config.Config) mail.Sender {
	sender := mail.New(mail.Config{
		Host: cfg.SMTPHost, Port: cfg.SMTPPort,
		Username: cfg.SMTPUsername, Password: cfg.SMTPPassword,
		From: cfg.SMTPFrom, FromName: cfg.SMTPFromName, TLS: cfg.SMTPTLS,
	})
	return mail.WithLog(sender, func(to []string, subject string, ok bool, sendErr error) {
		logCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		errText := ""
		if sendErr != nil {
			errText = sendErr.Error()
		}
		if _, err := pool.Exec(logCtx,
			`INSERT INTO mail_log (recipients, subject, ok, error) VALUES ($1,$2,$3,$4)`,
			strings.Join(to, ", "), subject, ok, errText); err != nil {
			slog.Warn("mail_log write failed", "err", err)
		}
	})
}
