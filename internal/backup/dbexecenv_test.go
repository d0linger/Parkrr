package backup

import (
	"os"
	"strings"
	"testing"
)

func pgpassword(env []string) (string, bool) {
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "PGPASSWORD="); ok {
			return v, true
		}
	}
	return "", false
}

func TestDBExecEnv(t *testing.T) {
	cases := []struct {
		name        string
		in          string
		wantPW      string // "" means: no PGPASSWORD expected
		mustNotHave string // substring that must be absent from the returned DSN
	}{
		{"userinfo password", "postgres://u:secret@host:5432/db?sslmode=disable", "secret", "secret"},
		{"query password", "postgres://u@host:5432/db?sslmode=disable&password=q-secret", "q-secret", "q-secret"},
		{"userinfo wins over query", "postgres://u:ui@host/db?password=qq", "ui", "qq"},
		{"no password (url)", "postgres://u@host:5432/db?sslmode=disable", "", "password"},
		{"kv password unquoted", "host=db port=5432 user=u password=kv-secret dbname=db", "kv-secret", "kv-secret"},
		{"kv password quoted", `host=db user=u password='kv secret' dbname=db`, "kv secret", "password="},
		{"kv no password", "host=db user=u dbname=db", "", "password"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dsn, env := dbExecEnv(c.in)
			if strings.Contains(dsn, c.mustNotHave) && c.mustNotHave != "" {
				t.Errorf("returned DSN still contains %q: %s", c.mustNotHave, dsn)
			}
			pw, ok := pgpassword(env)
			if c.wantPW == "" {
				if ok {
					t.Errorf("did not expect PGPASSWORD, got %q", pw)
				}
				return
			}
			if !ok {
				t.Fatalf("expected PGPASSWORD=%q, none set", c.wantPW)
			}
			if pw != c.wantPW {
				t.Errorf("PGPASSWORD = %q, want %q", pw, c.wantPW)
			}
			// The env must extend the parent environment, not replace it.
			if len(env) <= len(os.Environ()) {
				t.Errorf("env should extend os.Environ(), got %d entries", len(env))
			}
		})
	}
}
