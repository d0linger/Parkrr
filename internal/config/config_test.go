package config

import "testing"

func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("PARKRR_ADMIN_PASSWORD", "admin-password")
	t.Setenv("PARKRR_SESSION_SECRET", "a-very-long-session-secret-with-enough-entropy")
}

func TestLoadDefaults(t *testing.T) {
	setRequired(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.SecureCookies {
		t.Error("SecureCookies should default to true (secure-by-default)")
	}
	if cfg.AuditRetentionDays != 365 {
		t.Errorf("AuditRetentionDays default: got %d want 365", cfg.AuditRetentionDays)
	}
	if !cfg.CheckBreachedPasswords {
		t.Error("CheckBreachedPasswords should default to true")
	}
	if cfg.RateLimitPerMin != 600 {
		t.Errorf("RateLimitPerMin default: got %d want 600", cfg.RateLimitPerMin)
	}
	if cfg.MetricsToken != "" {
		t.Errorf("MetricsToken default should be empty, got %q", cfg.MetricsToken)
	}
}

func TestLoadRejectsShortSecret(t *testing.T) {
	t.Setenv("PARKRR_ADMIN_PASSWORD", "admin-password")
	t.Setenv("PARKRR_SESSION_SECRET", "short")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for session secret under 32 bytes")
	}
}

func TestLoadRequiresAdminPassword(t *testing.T) {
	t.Setenv("PARKRR_ADMIN_PASSWORD", "")
	t.Setenv("PARKRR_SESSION_SECRET", "a-very-long-session-secret-with-enough-entropy")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when admin password is missing")
	}
}

func TestGetenvBoolOverride(t *testing.T) {
	setRequired(t)
	t.Setenv("PARKRR_SECURE_COOKIES", "false")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SecureCookies {
		t.Error("explicit PARKRR_SECURE_COOKIES=false should override the default")
	}
}
