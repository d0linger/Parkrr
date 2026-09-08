package server

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"runtime/debug"
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

	var created, skipped, failed, incomplete int
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
		// Der Handler läuft hier OHNE die recoverPanics-Middleware, die ihn bei jedem
		// echten HTTP-Aufruf umgibt — der synthetische Weg umgeht die ganze Kette. Ohne
		// eigenes recover risse ein Panic aus den Daten EINER Person die Goroutine und
		// damit den Prozess mit: der Container liefe jede Nacht in eine Neustartschleife,
		// während derselbe Datensatz über den Knopf nur ein 500 erzeugt hätte.
		func() {
			defer func() {
				if p := recover(); p != nil {
					failed++
					slog.Error("auto-invoice: Panic bei einer Person — übersprungen",
						"person_id", pid, "panic", p, "stack", string(debug.Stack()))
				}
			}()
			h.CreateInvoice(rec, req)
		}()
		// Die Einordnung liest den maschinenlesbaren Ausgang des Handlers, nicht den
		// deutschen Meldungstext: eine Umformulierung in billing.go hätte sonst
		// stillschweigend die Bedeutung dieses Laufs verändert.
		outcome := rec.Header().Get(handlers.InvoiceOutcomeHeader)
		switch {
		case rec.Code == 201:
			created++
		case rec.Code == 400 && outcome == handlers.OutcomeNoOpenItems:
			skipped++
		case rec.Code == 409 && outcome == handlers.OutcomeRaced:
			// Ein gleichzeitiger Lauf (oder der Knopf) war schneller. Der Handler nennt
			// das ausdrücklich unkritisch: keine Doppelrechnung, keine verbrannte Nummer.
			// Als Fehlschlag gezählt schickte es den Betreiber auf die Suche nach einem
			// Vorfall, den es nicht gab.
			skipped++
		case rec.Code == 422 && outcome == handlers.OutcomeComplianceSeller:
			// Verkäuferdaten fehlen: das betrifft JEDE Rechnung dieses Laufs gleich —
			// einmal laut, dann abbrechen, statt dieselbe Meldung hundertmal zu erzeugen.
			slog.Error("auto-invoice: Pflichtangaben des Ausstellers unvollständig — Lauf abgebrochen",
				"person_id", pid, "detail", strings.TrimSpace(rec.Body.String()))
			complianceStop = true
		case rec.Code == 422:
			// Empfängerdaten fehlen (z. B. die ab 400 € brutto nötige Anschrift): das
			// betrifft GENAU DIESE Person. Früher brach der Lauf auch hier ab — ein
			// einzelner unvollständiger Datensatz hielt damit die Fakturierung aller
			// nachfolgenden Personen an, Nacht für Nacht, ohne dass die Zusammenfassung
			// es erwähnte.
			incomplete++
			slog.Warn("auto-invoice: Empfängerdaten unvollständig — Person übersprungen",
				"person_id", pid, "detail", strings.TrimSpace(rec.Body.String()))
		default:
			failed++
			slog.Error("auto-invoice: Rechnung fehlgeschlagen",
				"person_id", pid, "status", rec.Code, "body", strings.TrimSpace(rec.Body.String()))
		}
		if complianceStop {
			break
		}
	}

	slog.Info("auto-invoice: Lauf beendet",
		"created", created, "skipped", skipped, "incomplete", incomplete, "failed", failed,
		"aborted", complianceStop)
	// Der Lauf selbst ist eine Handlung und gehört ins Protokoll — die einzelnen
	// Rechnungen auditiert der Handler bereits selbst.
	//
	// Die Zusammenfassung nennt AUSDRÜCKLICH, wenn der Lauf vorzeitig endete oder
	// Personen wegen unvollständiger Empfängerdaten ausgelassen wurden. Ein Eintrag,
	// der nur "N erstellt" meldet, während Hunderte nicht fakturiert wurden, ist
	// schlimmer als keiner: er sieht nach einem geglückten Lauf aus.
	summary := "Automatischer Rechnungslauf: " + strconv.Itoa(created) + " erstellt, " +
		strconv.Itoa(skipped) + " ohne offene Positionen, " + strconv.Itoa(failed) + " fehlgeschlagen"
	if incomplete > 0 {
		summary += ", " + strconv.Itoa(incomplete) + " wegen fehlender Empfängerdaten übersprungen"
	}
	if complianceStop {
		summary += " — ABGEBROCHEN: Pflichtangaben des Ausstellers fehlen, verbleibende Personen wurden nicht fakturiert"
	}
	if created > 0 || failed > 0 || incomplete > 0 || complianceStop {
		// WithoutCancel: genau der Lauf, der an der 15-Minuten-Grenze abgeschnitten
		// wurde, ist der, dessen Protokolleintrag am wichtigsten wäre — und mit dem
		// abgelaufenen ctx wäre er der einzige, der nicht geschrieben werden kann.
		// Dasselbe Muster nutzen backup/schedule.go und server/observability.go.
		wctx, wcancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer wcancel()
		h.AuditSystem(wctx, "create", "system", 0, summary, nil)
	}
}
