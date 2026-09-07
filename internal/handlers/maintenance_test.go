package handlers

import (
	"context"
	"errors"
	"testing"
)

// runOnce muss die Aufgabe genau EINMAL ausführen — das ist der ganze Zweck: der
// Zahlungs-Backfill scannt sämtliche Vereinbarungen und lief bisher bei jedem
// Serverstart erneut (Hundert 08).
func TestRunOnceExecutesOnlyOnce(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	const task = "test_run_once"
	if _, err := h.Pool.Exec(ctx, `DELETE FROM maintenance_tasks WHERE task=$1`, task); err != nil {
		t.Fatalf("clear marker: %v", err)
	}
	t.Cleanup(func() { _, _ = h.Pool.Exec(context.Background(), `DELETE FROM maintenance_tasks WHERE task=$1`, task) })

	runs := 0
	fn := func(context.Context) error { runs++; return nil }
	for i := 0; i < 3; i++ {
		if err := h.runOnce(ctx, task, fn); err != nil {
			t.Fatalf("runOnce #%d: %v", i, err)
		}
	}
	if runs != 1 {
		t.Errorf("die Aufgabe lief %dmal, erwartet genau 1", runs)
	}
}

// Ein FEHLGESCHLAGENER Lauf darf keinen Marker hinterlassen: sonst bliebe die
// Nacharbeit für immer liegen, und zwar unsichtbar.
func TestRunOnceDoesNotMarkAFailedRun(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	const task = "test_run_once_failing"
	if _, err := h.Pool.Exec(ctx, `DELETE FROM maintenance_tasks WHERE task=$1`, task); err != nil {
		t.Fatalf("clear marker: %v", err)
	}
	t.Cleanup(func() { _, _ = h.Pool.Exec(context.Background(), `DELETE FROM maintenance_tasks WHERE task=$1`, task) })

	boom := errors.New("boom")
	runs := 0
	fn := func(context.Context) error { runs++; return boom }
	for i := 0; i < 2; i++ {
		if err := h.runOnce(ctx, task, fn); !errors.Is(err, boom) {
			t.Fatalf("runOnce #%d gab %v zurück, erwartet den Fehler der Aufgabe", i, err)
		}
	}
	if runs != 2 {
		t.Errorf("ein Fehlschlag muss wiederholt werden: %d Läufe, erwartet 2", runs)
	}
	var marked bool
	if err := h.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM maintenance_tasks WHERE task=$1)`, task).Scan(&marked); err != nil {
		t.Fatalf("query marker: %v", err)
	}
	if marked {
		t.Error("ein fehlgeschlagener Lauf hat sich als erledigt eingetragen")
	}
}

// Der Backfill selbst muss hinter dem Marker liegen, nicht daneben: der Aufruf aus
// dem Serverstart geht über RunPeriodPaymentBackfillOnce.
func TestPeriodPaymentBackfillIsMarkedDone(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	const task = "period_payment_backfill"
	if _, err := h.Pool.Exec(ctx, `DELETE FROM maintenance_tasks WHERE task=$1`, task); err != nil {
		t.Fatalf("clear marker: %v", err)
	}
	if err := h.RunPeriodPaymentBackfillOnce(ctx); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	var marked bool
	if err := h.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM maintenance_tasks WHERE task=$1)`, task).Scan(&marked); err != nil {
		t.Fatalf("query marker: %v", err)
	}
	if !marked {
		t.Error("nach einem erfolgreichen Backfill fehlt der Marker — er liefe bei jedem Start erneut")
	}
}
