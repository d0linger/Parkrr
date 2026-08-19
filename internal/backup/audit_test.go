package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Deleting a backup is destructive and irreversible, and it used to happen with
// nothing but a debug line. These pin that the retention sweep reports WHICH
// archives it removed, so a missing restore point can be explained afterwards.

type recorded struct {
	action, entity, summary string
	changes                 any
}

// val unwraps the {old:null,new:v} snapshot wrapper the sink now applies.
func val(changes any, field string) any {
	m, ok := changes.(map[string]any)
	if !ok {
		return nil
	}
	w, ok := m[field].(map[string]any)
	if !ok {
		return nil
	}
	return w["new"]
}

// withRecorder installs a capturing audit sink and restores the previous one.
func withRecorder(t *testing.T) *[]recorded {
	t.Helper()
	prev := auditSink.Load()
	var got []recorded
	SetAuditor(func(_ context.Context, action, entity string, _ int64, summary string, changes any) {
		got = append(got, recorded{action, entity, summary, changes})
	})
	t.Cleanup(func() {
		if prev != nil {
			auditSink.Store(prev)
		} else {
			auditSink.Store(nil)
		}
	})
	return &got
}

func seedArchives(t *testing.T, n int) string {
	t.Helper()
	dir := t.TempDir()
	// Timestamped names sort chronologically, which is how pruneDir picks the oldest.
	for _, stamp := range []string{
		"2026-01-01-000000", "2026-01-02-000000", "2026-01-03-000000",
		"2026-01-04-000000", "2026-01-05-000000",
	}[:n] {
		p := filepath.Join(dir, "parkrr-"+stamp+".dump.enc")
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatalf("seed %s: %v", p, err)
		}
	}
	return dir
}

func TestPruneDirAuditsTheDeletedArchives(t *testing.T) {
	got := withRecorder(t)
	dir := seedArchives(t, 5)

	pruneDir(context.Background(), dir, 2)

	left, _ := filepath.Glob(filepath.Join(dir, "parkrr-*.dump.enc"))
	if len(left) != 2 {
		t.Fatalf("expected 2 archives to survive, got %d", len(left))
	}
	if len(*got) != 1 {
		t.Fatalf("expected exactly one audit entry for the sweep, got %d", len(*got))
	}
	e := (*got)[0]
	// entity "system" matches what the manual backup endpoints already use, so one
	// filter shows scheduled and manual runs together.
	if e.action != "delete" || e.entity != "system" {
		t.Errorf("entry should be delete/system, got %s/%s", e.action, e.entity)
	}
	if n, _ := val(e.changes, "deleted_count").(int); n != 3 {
		t.Errorf("deleted_count should be 3, got %v", val(e.changes, "deleted_count"))
	}
	// The names matter: "3 files deleted" cannot tell you which restore point is gone.
	names, ok := val(e.changes, "deleted_files").([]string)
	if !ok || len(names) != 3 {
		t.Fatalf("deleted_files should name all 3 removed archives, got %v", val(e.changes, "deleted_files"))
	}
	for _, n := range names {
		if filepath.Ext(n) != ".enc" || filepath.Base(n) != n {
			t.Errorf("expected a bare archive filename, got %q", n)
		}
	}
}

func TestPruneDirIsSilentWhenNothingIsDeleted(t *testing.T) {
	got := withRecorder(t)
	dir := seedArchives(t, 2)

	pruneDir(context.Background(), dir, 5) // keep more than exist
	pruneDir(context.Background(), dir, 0) // 0 = keep all, must not even scan

	if len(*got) != 0 {
		t.Fatalf("a sweep that deletes nothing must write no entry, got %d", len(*got))
	}
}

// The package must stay usable with no sink installed — tests and any embedding
// binary that never calls SetAuditor must not panic on a nil sink.
func TestAuditIsANoOpWithoutASink(t *testing.T) {
	prev := auditSink.Load()
	auditSink.Store(nil)
	t.Cleanup(func() { auditSink.Store(prev) })

	dir := seedArchives(t, 3)
	pruneDir(context.Background(), dir, 1) // must not panic
	if left, _ := filepath.Glob(filepath.Join(dir, "parkrr-*.dump.enc")); len(left) != 1 {
		t.Fatalf("pruning must still work without an auditor, %d archives left", len(left))
	}
}
