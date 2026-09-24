package restorectl

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestJobTerminalPhases(t *testing.T) {
	for _, phase := range []Phase{PhaseComplete, PhaseFailed, PhaseCancelled} {
		if !(Job{Phase: phase}).Terminal() {
			t.Errorf("phase %q should be terminal", phase)
		}
	}
	for _, phase := range []Phase{PhaseQueued, PhaseDraining, PhaseRestoring,
		PhaseMigrating, PhasePurgingSessions, PhaseVerifying, PhaseRecovering} {
		if (Job{Phase: phase}).Terminal() {
			t.Errorf("phase %q should not be terminal", phase)
		}
	}
}

func TestCoordinatorLost(t *testing.T) {
	job := Job{Phase: PhaseRestoring, CoordinatorStale: false}
	if job.CoordinatorLost() {
		t.Fatal("fresh heartbeat should still be accepted")
	}
	job.CoordinatorStale = true
	if !job.CoordinatorLost() {
		t.Fatal("stale heartbeat should be considered lost")
	}
	job.Phase = PhaseComplete
	if job.CoordinatorLost() {
		t.Fatal("terminal jobs never lose a coordinator")
	}
}

func TestRandomIDIsOpaque128BitValue(t *testing.T) {
	first, err := randomID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := randomID()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 32 || first == second {
		t.Fatalf("unexpected restore IDs %q and %q", first, second)
	}
	if _, err := hex.DecodeString(first); err != nil {
		t.Fatalf("restore ID is not hexadecimal: %v", err)
	}
}

func TestNewRejectsMissingDependencies(t *testing.T) {
	if _, err := New(nil, nil, true); err == nil {
		t.Fatal("nil context should be rejected")
	}
	if _, err := New(context.Background(), nil, true); err == nil {
		t.Fatal("nil pool should be rejected")
	}
}

func TestStatusRejectsMalformedJobIDBeforeDatabaseAccess(t *testing.T) {
	c := &Controller{}
	rec := httptest.NewRecorder()
	c.ServeStatus(rec, httptest.NewRequest(http.MethodGet, "/api/restore/status/not-an-id", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestRemoveRecoveredArchiveOnlyAcceptsStagingShape(t *testing.T) {
	root := t.TempDir()
	staging := filepath.Join(root, ".restore-staging")
	if err := os.Mkdir(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	allowed := filepath.Join(staging, "restore-123.dump.enc")
	if err := os.WriteFile(allowed, []byte("encrypted"), 0o600); err != nil {
		t.Fatal(err)
	}
	(&Controller{}).RemoveRecoveredArchive(Job{ID: "test", SourcePath: allowed})
	if _, err := os.Stat(allowed); !os.IsNotExist(err) {
		t.Fatalf("allowed staging archive was not removed: %v", err)
	}

	refused := filepath.Join(root, "restore-123.dump.enc")
	if err := os.WriteFile(refused, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	(&Controller{}).RemoveRecoveredArchive(Job{ID: "test", SourcePath: refused})
	if _, err := os.Stat(refused); err != nil {
		t.Fatalf("unrecognized path should remain: %v", err)
	}
}

func TestStatusLimiterBoundsBurstAndConcurrency(t *testing.T) {
	now := time.Now()
	c := &Controller{
		statusTokens: 1,
		statusLast:   now,
		statusSlots:  make(chan struct{}, 1),
	}
	if !c.acquireStatusSlot(now) {
		t.Fatal("first status request should be admitted")
	}
	if c.acquireStatusSlot(now) {
		t.Fatal("second simultaneous request should be rejected")
	}
	c.releaseStatusSlot()
	if !c.acquireStatusSlot(now.Add(time.Second)) {
		t.Fatal("tokens should refill over time")
	}
	c.releaseStatusSlot()
}
