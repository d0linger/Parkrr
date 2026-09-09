package handlers

import (
	"context"
	"errors"
	"hash/fnv"
	"log/slog"
	"time"
)

// ErrTaskBusy meldet, dass eine andere Instanz dieselbe Einmal-Aufgabe gerade
// ausführt. Kein Fehler im eigentlichen Sinn — der Aufrufer soll ihn nicht als
// Störung protokollieren.
var ErrTaskBusy = errors.New("maintenance task already running elsewhere")

// advisoryKey macht aus einem sprechenden Namen einen stabilen 64-Bit-Schlüssel
// für pg_advisory_lock: die Sperre kennt nur Zahlen, und zwei verschiedene
// Anliegen dürfen sich nicht gegenseitig sperren.
//
// Bewusst hier und nicht als `hashtext($1)::bigint` in der Abfrage: hashtext
// liefert einen VORZEICHENBEHAFTETEN int4 und schnitte den Schlüsselraum von 2^64
// auf 2^32 zusammen. Bei zwei Aufrufern mit verschiedenen Namen wäre eine
// Kollision dann ein grundloses „läuft bereits", das niemand erklären kann.
func advisoryKey(name string) int64 {
	hsh := fnv.New64a()
	_, _ = hsh.Write([]byte(name))
	return int64(hsh.Sum64())
}

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

	key := advisoryKey("parkrr.maintenance." + task)

	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&got); err != nil {
		return err
	}
	if !got {
		return ErrTaskBusy
	}
	defer func() {
		// Eigene Frist wie in reminders.go: context.WithoutCancel allein hat GAR keine,
		// und eine hängende Verbindung hielte sonst Aufrufer und Poolplatz fest, bis TCP
		// aufgibt. Der Fehler wurde dort diagnostiziert — hier stand er unverändert
		// weiter, obwohl dieses Stück die Vorlage war.
		ulCtx, ulCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer ulCancel()
		if _, err := conn.Exec(ulCtx, `SELECT pg_advisory_unlock($1)`, key); err != nil {
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
