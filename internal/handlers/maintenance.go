package handlers

import (
	"context"
	"errors"
	"hash/fnv"
	"log/slog"
)

// ErrTaskBusy meldet, dass eine andere Instanz dieselbe Einmal-Aufgabe gerade
// ausführt. Kein Fehler im eigentlichen Sinn — der Aufrufer soll ihn nicht als
// Störung protokollieren.
var ErrTaskBusy = errors.New("maintenance task already running elsewhere")

// runOnce führt fn höchstens einmal über die Lebensdauer der Datenbank aus und
// hält das Ergebnis in maintenance_tasks fest (Hundert 08).
//
// Der Marker wird ERST NACH Erfolg gesetzt: ein fehlgeschlagener Lauf muss beim
// nächsten Start wiederholt werden, sonst bliebe die Nacharbeit für immer liegen.
//
// Die Advisory-Lock ist nicht Schmuck: bei mehreren Replikaten starten sonst alle
// gleichzeitig denselben Vollscan über sämtliche Vereinbarungen. Wer die Lock nicht
// bekommt, macht nichts — die Aufgabe läuft bereits nebenan.
func (h *Handler) runOnce(ctx context.Context, task string, fn func(context.Context) error) error {
	var done bool
	if err := h.Pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM maintenance_tasks WHERE task=$1)`, task).Scan(&done); err != nil {
		return err
	}
	if done {
		return nil
	}
	// Eigene Verbindung: pg_advisory_lock hängt an der SITZUNG, und aus dem Pool
	// käme für das Unlock sonst womöglich eine andere.
	conn, err := h.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	// Ein stabiler 64-Bit-Schlüssel aus dem Aufgabennamen — pg_advisory_lock kennt
	// nur Zahlen, und zwei verschiedene Aufgaben dürfen sich nicht gegenseitig sperren.
	hsh := fnv.New64a()
	_, _ = hsh.Write([]byte("parkrr.maintenance." + task))
	key := int64(hsh.Sum64())

	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&got); err != nil {
		return err
	}
	if !got {
		return ErrTaskBusy
	}
	defer func() {
		if _, err := conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, key); err != nil {
			slog.Warn("maintenance advisory unlock failed", "task", task, "err", err)
		}
	}()

	// Nach der Lock ein zweites Mal prüfen: zwischen der ersten Prüfung und dem
	// Erwerb kann eine andere Instanz fertig geworden sein.
	if err := conn.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM maintenance_tasks WHERE task=$1)`, task).Scan(&done); err != nil {
		return err
	}
	if done {
		return nil
	}
	if err := fn(ctx); err != nil {
		return err
	}
	if _, err := conn.Exec(ctx,
		`INSERT INTO maintenance_tasks (task) VALUES ($1) ON CONFLICT (task) DO NOTHING`, task); err != nil {
		// Die Arbeit ist getan, nur der Marker fehlt: der nächste Start wiederholt
		// den (idempotenten) Lauf. Laut melden, aber nicht als Fehlschlag zurückgeben.
		slog.Warn("maintenance marker not written; task will repeat on next start", "task", task, "err", err)
	}
	return nil
}

// RunPeriodPaymentBackfillOnce ist der Einstiegspunkt aus dem Serverstart.
func (h *Handler) RunPeriodPaymentBackfillOnce(ctx context.Context) error {
	return h.runOnce(ctx, "period_payment_backfill", h.BackfillPeriodPayments)
}
