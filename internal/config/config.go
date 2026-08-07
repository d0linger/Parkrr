// Package config loads runtime configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds all runtime configuration for the application.
type Config struct {
	// HTTP
	ListenAddr string

	// Database
	DatabaseURL string

	// Admin bootstrap (created/updated on startup from env)
	AdminUsername      string
	AdminEmail         string
	AdminPassword      string
	AdminPasswordForce bool // re-apply AdminPassword on every boot (default false: don't clobber a UI change)

	// Security
	SessionSecret         string
	SessionMaxAge         int  // seconds (idle window when sliding; else absolute)
	SessionSliding        bool // renew expiry on activity (re-login only after inactivity)
	SessionAbsoluteMaxAge int  // seconds; hard cap on a session's total lifetime
	SecureCookies         bool // force the Secure flag on cookies (default true; set false only for plain-HTTP dev)
	TrustedProxies        bool
	RateLimitPerMin       int // general per-IP request budget (0 = disabled)

	// WebAuthn / passkeys (feature-flagged: enabled only when RPID is set)
	WebAuthnRPID          string   // Relying Party ID = the site's registrable domain
	WebAuthnRPDisplayName string   // human-readable name shown by the authenticator
	WebAuthnOrigins       []string // allowed origins, e.g. https://parkrr.example.com

	// Observability & data lifecycle
	MetricsToken       string // Bearer token required to scrape /metrics ("" = open)
	AuditRetentionDays int    // prune audit entries older than N days (0 = keep forever)

	// Account security
	CheckBreachedPasswords bool // check new passwords against the HIBP range API
	FailClosedOnBreach     bool // if the HIBP check is unavailable, reject the password (default false = allow)

	// Backup (encrypted pg_dump). Enabled only when BackupKey is set. The
	// schedule and retention are stored in the DB (backup_settings) and edited in
	// the Backup tab, not via env.
	BackupKey string // AES-256-GCM passphrase (separate from SessionSecret)
	BackupDir string // if set, scheduled backups are written here

	// S3-compatible off-site backup target (optional).
	S3Endpoint  string
	S3Bucket    string
	S3AccessKey string
	S3SecretKey string
	S3Region    string
	S3Prefix    string
	S3UseSSL    bool
}

// Load reads configuration from the environment, applying sensible defaults.
func Load() (*Config, error) {
	cfg := &Config{
		ListenAddr:            getenv("PARKRR_LISTEN_ADDR", ":8080"),
		DatabaseURL:           os.Getenv("PARKRR_DATABASE_URL"),
		AdminUsername:         getenv("PARKRR_ADMIN_USERNAME", "admin"),
		AdminEmail:            getenv("PARKRR_ADMIN_EMAIL", "admin@example.com"),
		AdminPassword:         os.Getenv("PARKRR_ADMIN_PASSWORD"),
		AdminPasswordForce:    getenvBool("PARKRR_ADMIN_PASSWORD_FORCE", false),
		SessionSecret:         os.Getenv("PARKRR_SESSION_SECRET"),
		SessionMaxAge:         getenvInt("PARKRR_SESSION_MAX_AGE", 60*60*24*7),
		SessionSliding:        getenvBool("PARKRR_SESSION_SLIDING", false),
		SessionAbsoluteMaxAge: getenvInt("PARKRR_SESSION_ABSOLUTE_MAX_AGE", 60*60*24*90),
		SecureCookies:         getenvBool("PARKRR_SECURE_COOKIES", true),
		TrustedProxies:        getenvBool("PARKRR_TRUSTED_PROXY", false),
		RateLimitPerMin:       getenvInt("PARKRR_RATE_LIMIT_PER_MIN", 600),

		WebAuthnRPID:          getenv("PARKRR_WEBAUTHN_RP_ID", ""),
		WebAuthnRPDisplayName: getenv("PARKRR_WEBAUTHN_RP_NAME", "Parkrr"),
		WebAuthnOrigins:       splitList(os.Getenv("PARKRR_WEBAUTHN_ORIGINS")),

		MetricsToken: getenv("PARKRR_METRICS_TOKEN", ""),
		// Default to the 7-year BAO §132 retention window; money-trail rows
		// (invoice/payment/billing) are never pruned regardless (see PruneAuditLog).
		AuditRetentionDays: getenvInt("PARKRR_AUDIT_RETENTION_DAYS", 2555),

		CheckBreachedPasswords: getenvBool("PARKRR_CHECK_BREACHED_PASSWORDS", true),
		FailClosedOnBreach:     getenvBool("PARKRR_BREACH_CHECK_FAIL_CLOSED", false),

		BackupKey: os.Getenv("PARKRR_BACKUP_KEY"),
		BackupDir: os.Getenv("PARKRR_BACKUP_DIR"),

		S3Endpoint:  os.Getenv("PARKRR_S3_ENDPOINT"),
		S3Bucket:    os.Getenv("PARKRR_S3_BUCKET"),
		S3AccessKey: os.Getenv("PARKRR_S3_ACCESS_KEY"),
		S3SecretKey: os.Getenv("PARKRR_S3_SECRET_KEY"),
		S3Region:    os.Getenv("PARKRR_S3_REGION"),
		S3Prefix:    getenv("PARKRR_S3_PREFIX", "parkrr/"),
		S3UseSSL:    getenvBool("PARKRR_S3_USE_SSL", true),
	}

	// Allow assembling the DB URL from discrete parts (docker-compose friendly).
	if cfg.DatabaseURL == "" {
		host := getenv("PARKRR_DB_HOST", "db")
		port := getenv("PARKRR_DB_PORT", "5432")
		user := getenv("PARKRR_DB_USER", "parkrr")
		pass := getenv("PARKRR_DB_PASSWORD", "parkrr")
		name := getenv("PARKRR_DB_NAME", "parkrr")
		ssl := getenv("PARKRR_DB_SSLMODE", "disable")
		cfg.DatabaseURL = fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s",
			user, pass, host, port, name, ssl)
	}

	if cfg.AdminPassword == "" {
		return nil, fmt.Errorf("PARKRR_ADMIN_PASSWORD must be set")
	}
	if cfg.SessionSecret == "" {
		return nil, fmt.Errorf("PARKRR_SESSION_SECRET must be set (use a long random string)")
	}
	if len(cfg.SessionSecret) < 32 {
		return nil, fmt.Errorf("PARKRR_SESSION_SECRET must be at least 32 bytes long; only the length is enforced, so supply a random value (e.g. `openssl rand -base64 48`) — its entropy is your responsibility")
	}
	// The backup key is optional (empty = backups disabled), but when set it must
	// be as strong as the session secret — it protects every database dump.
	if cfg.BackupKey != "" && len(cfg.BackupKey) < 32 {
		return nil, fmt.Errorf("PARKRR_BACKUP_KEY must be at least 32 bytes long when set (supply a random value, e.g. `openssl rand -base64 48`)")
	}

	return cfg, nil
}

func getenv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func getenvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// splitList parses a comma- or space-separated list, dropping empties.
func splitList(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' })
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

func getenvBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}
