package server

import (
	"context"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/preining/parkrr/internal/handlers"
)

// StartMailQueue treibt die Mail-Warteschlange (Ausbau 36): alle 30 Sekunden oder
// sofort nach einem KickOutbox werden fällige Mails verschickt. Einmal pro Stunde
// reiht der Lauf fällige automatische Mahnungen ein, wenn die Mahnautomatik an ist
// (Ausbau 20, standardmäßig aus). Ohne SMTP verschickt der Lauf nichts; wartende
// Mails bleiben liegen, bis SMTP eingerichtet ist.
func StartMailQueue(h *handlers.Handler, stop <-chan struct{}) {
	if h.OutboxKick == nil {
		h.OutboxKick = make(chan struct{}, 1)
	}
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	var lastDunning time.Time
	run := func() {
		defer func() {
			if p := recover(); p != nil {
				slog.Error("mail-queue: Panic im Lauf", "panic", p, "stack", string(debug.Stack()))
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if time.Since(lastDunning) >= time.Hour {
			lastDunning = time.Now()
			if n, err := h.QueueDueReminders(ctx); err != nil {
				slog.Warn("mail-queue: fällige Mahnungen nicht ermittelbar", "err", err)
			} else if n > 0 {
				slog.Info("mail-queue: automatische Mahnungen eingereiht", "count", n)
			}
		}
		// Mehrere Runden, solange volle Zehnerpakete kommen: ein großer Rechnungslauf
		// soll nicht 30 Sekunden pro zehn Mails warten.
		for i := 0; i < 20 && ctx.Err() == nil; i++ {
			if h.ProcessOutbox(ctx) < 10 {
				break
			}
		}
	}
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			run()
		case <-h.OutboxKick:
			run()
		}
	}
}
