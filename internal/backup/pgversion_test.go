package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCheckRestoreClientVersion(t *testing.T) {
	// Boundaries come from the PostgreSQL CVE-2026-18408 advisory, which fixes
	// the bypass of the earlier CVE-2025-8714 protection in the restore pipeline.
	cases := []struct {
		version string
		wantOK  bool
	}{
		{"13.23", false},
		{"14.23", false},
		{"14.24", true},
		{"15.18", false},
		{"15.19", true},
		{"16.9", false},
		{"16.10", false},
		{"16.14", false},
		{"16.15", true},
		{"16.16", true},
		{"17.5", false},
		{"17.6", false},
		{"17.10", false},
		{"17.11", true},
		{"18.0", false},
		{"18.5", false},
		{"18.6", true},
		{"19.0", true},
		{"19beta1", false},
		{"19rc1", false},
		{"18.6devel", false},
		{"18.6 (Debian 18.6-1.pgdg13+1)", true},
		{"16.15 (Ubuntu 16.15-1.pgdg24.04+1)", true},
		{"16.15 (Homebrew)", true},
		{"16", false},
		{"16.x", false},
		{"16.15.1", false},
		{"16.15garbage", false},
		{"16.15\n17.11", false},
		{"16.999999999999999999999999999999999", false},
		{"999999999999999999999999999999999.0", false},
	}
	for _, tool := range []string{"pg_restore", "psql"} {
		for _, tc := range cases {
			t.Run(tool+"/"+tc.version, func(t *testing.T) {
				out := fmt.Sprintf("%s (PostgreSQL) %s\n", tool, tc.version)
				if err := checkRestoreClientVersion(tool, out); (err == nil) != tc.wantOK {
					t.Errorf("%q: error=%v, want accepted=%v", out, err, tc.wantOK)
				}
			})
		}
	}
	for _, out := range []string{"", "garbage", "psql (PostgreSQL) 16.15", "warning: pg_restore (PostgreSQL) 16.15"} {
		if err := checkRestoreClientVersion("pg_restore", out); err == nil {
			t.Errorf("accepted invalid pg_restore output %q", out)
		}
	}
}

func restoreClientStubs(t *testing.T) (dir, commands, sql string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("subprocess fixtures require the POSIX shell used by the production image")
	}
	dir = t.TempDir()
	commands = filepath.Join(dir, "commands")
	sql = filepath.Join(dir, "sql")
	t.Setenv("PATH", dir)
	t.Setenv("PARKRR_TEST_CLIENT_COMMANDS", commands)
	t.Setenv("PARKRR_TEST_CLIENT_SQL", sql)
	t.Setenv("PARKRR_TEST_PG_RESTORE_VERSION", "pg_restore (PostgreSQL) 16.15")
	t.Setenv("PARKRR_TEST_PSQL_VERSION", "psql (PostgreSQL) 16.15")
	t.Setenv("PARKRR_TEST_PG_RESTORE_EXIT", "0")
	t.Setenv("PARKRR_TEST_PSQL_EXIT", "0")
	t.Setenv("PARKRR_TEST_CLIENT_HANG", "")
	for _, tool := range []string{"pg_restore", "psql"} {
		prefix := "PARKRR_TEST_" + strings.ToUpper(tool)
		script := "#!/bin/sh\n" +
			"if [ \"$1\" = --version ]; then\n" +
			"  if [ \"$PARKRR_TEST_CLIENT_HANG\" = \"" + tool + "\" ]; then exec /bin/sleep 30; fi\n" +
			"  printf '%s\\n' \"$" + prefix + "_VERSION\"\n" +
			"  exit \"$" + prefix + "_EXIT\"\nfi\n" +
			"printf '%s\\n' '" + tool + "' >> \"$PARKRR_TEST_CLIENT_COMMANDS\"\n"
		if tool == "pg_restore" {
			script += "printf 'SELECT 42;\\n'\n"
		} else {
			script += "/bin/cat > \"$PARKRR_TEST_CLIENT_SQL\"\n"
		}
		if err := os.WriteFile(filepath.Join(dir, tool), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return dir, commands, sql
}

type unreadRestoreArchive struct {
	reads int
}

func (r *unreadRestoreArchive) Read([]byte) (int, error) {
	r.reads++
	return 0, io.EOF
}

func TestRestoreRejectsUnsafeClientsBeforeSQL(t *testing.T) {
	cases := []struct {
		name, env, value, missing, wantError string
	}{
		{name: "old generator", env: "PARKRR_TEST_PG_RESTORE_VERSION", value: "pg_restore (PostgreSQL) 16.14", wantError: "pg_restore 16.14 is unsafe"},
		{name: "old interpreter", env: "PARKRR_TEST_PSQL_VERSION", value: "psql (PostgreSQL) 16.14", wantError: "psql 16.14 is unsafe"},
		{name: "unsupported interpreter", env: "PARKRR_TEST_PSQL_VERSION", value: "psql (PostgreSQL) 13.23", wantError: "psql 13.23 is unsafe"},
		{name: "malformed generator", env: "PARKRR_TEST_PG_RESTORE_VERSION", value: "pg_restore (PostgreSQL) 17.11garbage", wantError: "cannot determine stable pg_restore version"},
		{name: "empty interpreter output", env: "PARKRR_TEST_PSQL_VERSION", value: "", wantError: "cannot determine stable psql version"},
		{name: "generator command failure", env: "PARKRR_TEST_PG_RESTORE_EXIT", value: "2", wantError: "pg_restore --version:"},
		{name: "interpreter command failure", env: "PARKRR_TEST_PSQL_EXIT", value: "2", wantError: "psql --version:"},
		{name: "missing generator", missing: "pg_restore", wantError: "pg_restore --version:"},
		{name: "missing interpreter", missing: "psql", wantError: "psql --version:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, commands, sql := restoreClientStubs(t)
			if tc.env != "" {
				t.Setenv(tc.env, tc.value)
			}
			if tc.missing != "" {
				if err := os.Remove(filepath.Join(dir, tc.missing)); err != nil {
					t.Fatal(err)
				}
			}
			archive := &unreadRestoreArchive{}
			err := restoreArchive(t.Context(), "postgres://unused.invalid/unused", "", archive)
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("restore error=%v, want %q", err, tc.wantError)
			}
			if archive.reads != 0 {
				t.Errorf("unsafe client consumed archive %d times", archive.reads)
			}
			for _, path := range []string{commands, sql} {
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("unsafe client started restore commands: %s, stat error=%v", filepath.Base(path), err)
				}
			}
		})
	}
}

func TestRestoreVersionCheckHonorsContext(t *testing.T) {
	for _, tool := range []string{"pg_restore", "psql"} {
		t.Run(tool, func(t *testing.T) {
			_, commands, sql := restoreClientStubs(t)
			t.Setenv("PARKRR_TEST_CLIENT_HANG", tool)
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			err := restoreArchive(ctx, "postgres://unused.invalid/unused", "ignored.dump", nil)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("restore error=%v, want deadline exceeded", err)
			}
			for _, path := range []string{commands, sql} {
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("canceled check started restore: %s, stat error=%v", filepath.Base(path), err)
				}
			}
		})
	}
}

func TestRestorePatchedClientsReachAtomicPipeline(t *testing.T) {
	_, commands, sql := restoreClientStubs(t)
	// A patched psql may be newer than pg_restore. Do not impose a same-major
	// restriction; archive/server compatibility remains pg_restore's concern.
	t.Setenv("PARKRR_TEST_PSQL_VERSION", "psql (PostgreSQL) 18.6")
	if err := restoreArchive(t.Context(), "postgres://unused.invalid/unused", "ignored.dump", nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(sql)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), restorePrelude) || !strings.HasSuffix(string(got), "SELECT 42;\n\nCOMMIT;\n") {
		t.Fatalf("patched restore did not preserve transaction boundaries: %q", got)
	}
	gotCommands, err := os.ReadFile(commands)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(gotCommands), "pg_restore\n") != 1 || strings.Count(string(gotCommands), "psql\n") != 1 {
		t.Fatalf("restore command executions=%q", gotCommands)
	}
}
