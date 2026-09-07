package server

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/preining/parkrr/internal/backup"
	"github.com/preining/parkrr/internal/handlers"
)

// StartAutoInvoice ist der automatische Rechnungslauf (Hundert 16) — strikt
// OPT-IN über PARKRR_AUTO_INVOICE_CRON (leer = aus, und aus bleibt es).
//
// Er erzeugt Rechnungen über DENSELBEN Handler wie der "+ Rechnung"-Knopf, per
// synthetischem Request. Das ist Absicht, kein Behelf: die Rechnungserstellung
// ist der sicherheitskritischste Geldpfad der Anwendung, und ein Extrahieren in
// eine "gemeinsame Funktion" hieße, genau diesen Pfad anzufassen. So durchläuft
// der Automat exakt dieselben Prüfungen (Pflichtangaben nach §11, Periodensperre,
// leere Positionen) wie ein Klick — und kann nichts, was der Klick nicht kann.
//
// Doppelt sicher gegen Doppel-Rechnungen: die Periodensperre (invoice_source)
// macht den ganzen Lauf idempotent — schon abgerechnete Perioden liefern keine
// Positionen, "keine offenen Positionen" ist ein stiller Überspringer. Ein
// Neustart direkt nach einem Lauf kann deshalb höchstens einen LEEREN Lauf
// wiederholen.
func StartAutoInvoice(pool *pgxpool.Pool, h *handlers.Handler, cron string, stop <-chan struct{}) {
	cron = strings.TrimSpace(cron)
	if cron == "" {
		return
	}
	if !backup.ValidCron(cron) {
		slog.Error("auto-invoice: PARKRR_AUTO_INVOICE_CRON ist kein gültiger 5-Feld-Cron — Lauf bleibt AUS", "cron", cron)
		return
	}
	slog.Info("automatischer Rechnungslauf aktiv", "cron", cron)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	last := time.Now() // nicht sofort beim Start feuern: der erste Lauf gehört dem Cron
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			now := time.Now()
			if backup.CronDue(cron, last, now) {
				last = now
				runAutoInvoice(pool, h)
			}
		}
	}
}

func runAutoInvoice(pool *pgxpool.Pool, h *handlers.Handler) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	rows, err := pool.Query(ctx, `SELECT id FROM persons WHERE NOT anonymized ORDER BY id`)
	if err != nil {
		slog.Error("auto-invoice: persons query failed", "err", err)
		return
	}
	var pids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			slog.Error("auto-invoice: scan failed", "err", err)
			return
		}
		pids = append(pids, id)
	}
	rows.Close()
	if rows.Err() != nil {
		slog.Error("auto-invoice: persons read failed", "err", rows.Err())
		return
	}

	var created, skipped, failed int
	var complianceStop bool
	for _, pid := range pids {
		if ctx.Err() != nil {
			break
		}
		req := httptest.NewRequest("POST", "/api/persons/"+strconv.FormatInt(pid, 10)+"/invoices",
			strings.NewReader(`{"note":"Automatischer Rechnungslauf"}`))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(ctx)
		req.SetPathValue("id", strconv.FormatInt(pid, 10))
		rec := httptest.NewRecorder()
		h.CreateInvoice(rec, req)
		switch {
		case rec.Code == 201:
			created++
		// NUR die eine erwartete 400 ist ein stiller Überspringer. Jede andere 400
		// (kaputter Request, Validierung) ist ein Fehler des Automaten und muss
		// laut werden — ein pauschales "400 = skip" hat im Test genau den Bug
		// versteckt, dass der synthetische Request keinen JSON-Body trug.
		case rec.Code == 400 && strings.Contains(rec.Body.String(), "keine offenen Positionen"):
			skipped++
		case rec.Code == 422:
			// Pflichtangaben (Verkäuferdaten/Adresse) fehlen: das betrifft JEDE
			// Rechnung dieses Laufs gleich — einmal laut, dann abbrechen, statt
			// dieselbe Meldung hundertmal zu erzeugen.
			slog.Error("auto-invoice: Pflichtangaben unvollständig — Lauf abgebrochen",
				"person_id", pid, "detail", strings.TrimSpace(rec.Body.String()))
			complianceStop = true
		default:
			failed++
			slog.Error("auto-invoice: Rechnung fehlgeschlagen",
				"person_id", pid, "status", rec.Code, "body", strings.TrimSpace(rec.Body.String()))
		}
		if complianceStop {
			break
		}
	}

	slog.Info("auto-invoice: Lauf beendet", "created", created, "skipped", skipped, "failed", failed)
	// Der Lauf selbst ist eine Handlung und gehört ins Protokoll — die einzelnen
	// Rechnungen auditiert der Handler bereits selbst.
	if created > 0 || failed > 0 || complianceStop {
		h.AuditSystem(ctx, "create", "system", 0,
			"Automatischer Rechnungslauf: "+strconv.Itoa(created)+" erstellt, "+
				strconv.Itoa(skipped)+" ohne offene Positionen, "+strconv.Itoa(failed)+" fehlgeschlagen",
			nil)
	}
}
