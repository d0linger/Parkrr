package config

import (
	"net/url"
	"strings"
	"testing"
)

func TestLoadDatabaseURLRoundTripsReservedCharacters(t *testing.T) {
	setRequired(t)
	t.Setenv("PARKRR_DATABASE_URL", "")
	t.Setenv("PARKRR_DB_USER", "user@tenant")
	t.Setenv("PARKRR_DB_PASSWORD", "#/?@:%Überprüfung")
	t.Setenv("PARKRR_DB_HOST", "::1")
	t.Setenv("PARKRR_DB_NAME", "test database")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(cfg.DatabaseURL)
	if err != nil {
		t.Fatal("database URL could not be parsed")
	}
	password, ok := u.User.Password()
	if !ok || password != "#/?@:%Überprüfung" || u.User.Username() != "user@tenant" {
		t.Fatal("database credentials did not round-trip")
	}
	if u.Hostname() != "::1" || u.Path != "/test database" || u.Query().Get("sslmode") != "disable" {
		t.Fatal("database address did not round-trip")
	}
}

func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("PARKRR_ADMIN_PASSWORD", "admin-password")
	t.Setenv("PARKRR_SESSION_SECRET", "a-very-long-session-secret-with-enough-entropy")
}

func TestLoadRejectsPlaceholderSecrets(t *testing.T) {
	for _, tc := range []struct{ name, key, value string }{
		{"admin example", "PARKRR_ADMIN_PASSWORD", "change-me-admin-password"},
		{"session example", "PARKRR_SESSION_SECRET", "change-me-to-a-long-random-secret-string"},
		{"uppercase variant", "PARKRR_SESSION_SECRET", "CHANGEME-this-is-long-enough-to-pass-length"},
		{"backup variant", "PARKRR_BACKUP_KEY", "change_me-backup-key-that-is-long-enough!!"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setRequired(t)
			t.Setenv("PARKRR_BACKUP_KEY", "")
			t.Setenv(tc.key, tc.value)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("expected placeholder rejection for %s, got %v", tc.key, err)
			}
			if strings.Contains(err.Error(), tc.value) {
				t.Fatal("configuration error disclosed the credential value")
			}
		})
	}
}

func TestBootstrapBreachLookupOptIn(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		want        bool
	}{
		{name: "unset"},
		{name: "explicit off", value: "false"},
		{name: "explicit on", value: "true", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setRequired(t)
			t.Setenv("PARKRR_CHECK_BREACHED_BOOTSTRAP_PASSWORDS", tc.value)
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.CheckBreachedBootstrapPasswords != tc.want {
				t.Fatalf("startup lookup enabled=%v, want %v", cfg.CheckBreachedBootstrapPasswords, tc.want)
			}
		})
	}
}

func TestLoadDefaults(t *testing.T) {
	setRequired(t)
	t.Setenv("PARKRR_TAX_WRITES_ENABLED", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.SecureCookies {
		t.Error("SecureCookies should default to true (secure-by-default)")
	}
	if cfg.AuditRetentionDays != 2555 {
		t.Errorf("AuditRetentionDays default: got %d want 2555 (7-year BAO §132 window)", cfg.AuditRetentionDays)
	}
	if !cfg.CheckBreachedPasswords {
		t.Error("CheckBreachedPasswords should default to true")
	}
	if cfg.CheckBreachedBootstrapPasswords {
		t.Error("startup breach lookup must require explicit opt-in")
	}
	if cfg.RateLimitPerMin != 600 {
		t.Errorf("RateLimitPerMin default: got %d want 600", cfg.RateLimitPerMin)
	}
	if cfg.MetricsToken != "" {
		t.Errorf("MetricsToken default should be empty, got %q", cfg.MetricsToken)
	}
	if cfg.BrowserRestore {
		t.Error("BrowserRestore must default to false")
	}
	if cfg.TaxWritesEnabled {
		t.Error("TaxWritesEnabled must default to false")
	}
}

func TestLoadTaxWritesEnabled(t *testing.T) {
	for _, tc := range []struct {
		name     string
		value    string
		expected bool
	}{
		{name: "unset defaults to read only"},
		{name: "explicit true enables writes", value: "true", expected: true},
		{name: "explicit false keeps read only", value: "false"},
		{name: "invalid value fails closed", value: "enabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setRequired(t)
			t.Setenv("PARKRR_TAX_WRITES_ENABLED", tc.value)
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.TaxWritesEnabled != tc.expected {
				t.Errorf("TaxWritesEnabled = %v, want %v", cfg.TaxWritesEnabled, tc.expected)
			}
		})
	}
}

func TestLoadRejectsShortSecret(t *testing.T) {
	t.Setenv("PARKRR_ADMIN_PASSWORD", "admin-password")
	t.Setenv("PARKRR_SESSION_SECRET", "short")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for session secret under 32 bytes")
	}
}

func TestLoadTaxExportReceiptLimit(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  int64
	}{
		{name: "default", want: DefaultTaxExportMaxReceiptBytes},
		{name: "blank", value: " \t", want: DefaultTaxExportMaxReceiptBytes},
		{name: "minimum", value: "1", want: 1 << 20},
		{name: "trimmed", value: " 25 ", want: 25 << 20},
		{name: "maximum", value: "1024", want: 1024 << 20},
		{name: "zero", value: "0"},
		{name: "negative", value: "-1"},
		{name: "above maximum", value: "1025"},
		{name: "units", value: "25MB"},
		{name: "fraction", value: "1.5"},
		{name: "overflow", value: "9223372036854775808"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setRequired(t)
			t.Setenv("PARKRR_TAX_EXPORT_MAX_RECEIPT_MB", tc.value)
			cfg, err := Load()
			if tc.want == 0 {
				if err == nil || cfg != nil || !strings.Contains(err.Error(), "PARKRR_TAX_EXPORT_MAX_RECEIPT_MB") {
					t.Fatalf("invalid export limit must fail startup: config=%v, err=%v", cfg != nil, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.TaxExportMaxReceiptBytes != tc.want {
				t.Fatalf("limit = %d, want %d", cfg.TaxExportMaxReceiptBytes, tc.want)
			}
			// Serving requests must not reinterpret a changed process environment.
			t.Setenv("PARKRR_TAX_EXPORT_MAX_RECEIPT_MB", "invalid-after-startup")
			if cfg.TaxExportMaxReceiptBytes != tc.want {
				t.Fatal("validated configuration changed after startup")
			}
		})
	}
}

func TestLoadSessionSecretLengthBoundary(t *testing.T) {
	t.Setenv("PARKRR_ADMIN_PASSWORD", "admin-password")
	// One byte under the floor is rejected.
	t.Setenv("PARKRR_SESSION_SECRET", strings.Repeat("x", 31))
	if _, err := Load(); err == nil {
		t.Error("31-byte session secret should be rejected")
	}
	// Exactly at the floor is accepted.
	t.Setenv("PARKRR_SESSION_SECRET", strings.Repeat("x", 32))
	if _, err := Load(); err != nil {
		t.Errorf("32-byte session secret should be accepted, got: %v", err)
	}
}

func TestFailClosedOnBreach(t *testing.T) {
	setRequired(t)
	// Unset -> defaults to false.
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.FailClosedOnBreach {
		t.Error("FailClosedOnBreach should default to false when unset")
	}
	// Explicitly enabled.
	t.Setenv("PARKRR_BREACH_CHECK_FAIL_CLOSED", "true")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.FailClosedOnBreach {
		t.Error("PARKRR_BREACH_CHECK_FAIL_CLOSED=true should set FailClosedOnBreach")
	}
	// Explicitly disabled.
	t.Setenv("PARKRR_BREACH_CHECK_FAIL_CLOSED", "false")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.FailClosedOnBreach {
		t.Error("PARKRR_BREACH_CHECK_FAIL_CLOSED=false should keep FailClosedOnBreach false")
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

func TestBrowserRestoreOptIn(t *testing.T) {
	setRequired(t)
	t.Setenv("PARKRR_BROWSER_RESTORE", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.BrowserRestore {
		t.Error("PARKRR_BROWSER_RESTORE=true should enable browser restore")
	}
}
