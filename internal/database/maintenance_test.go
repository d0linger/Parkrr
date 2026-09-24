package database

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestRestoreLeaseRefusesWhileApplicationIsActive(t *testing.T) {
	dbURL := os.Getenv("PARKRR_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("PARKRR_TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	app, err := AcquireApplicationLease(ctx, dbURL)
	if err != nil {
		t.Fatalf("AcquireApplicationLease: %v", err)
	}
	defer func() { _ = app.Release() }()
	if restore, err := TryAcquireRestoreLease(ctx, dbURL); !errors.Is(err, ErrApplicationActive) {
		if restore != nil {
			_ = restore.Release()
		}
		t.Fatalf("TryAcquireRestoreLease error = %v, want ErrApplicationActive", err)
	}
}

func TestRestoreLeaseBlocksApplicationStartup(t *testing.T) {
	dbURL := os.Getenv("PARKRR_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("PARKRR_TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	restore, err := TryAcquireRestoreLease(ctx, dbURL)
	if err != nil {
		t.Fatalf("TryAcquireRestoreLease: %v", err)
	}

	started := make(chan error, 1)
	go func() {
		app, err := AcquireApplicationLease(ctx, dbURL)
		if err == nil {
			_ = app.Release()
		}
		started <- err
	}()
	select {
	case err := <-started:
		t.Fatalf("application lease returned before restore released: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	if err := restore.Release(); err != nil {
		t.Fatalf("release restore lease: %v", err)
	}
	select {
	case err := <-started:
		if err != nil {
			t.Fatalf("AcquireApplicationLease after release: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("application startup did not continue after restore lease release")
	}
}
