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

	"github.com/preining/parkrr/internal/auth"
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
	// Der Merker steht in der Datenbank (Migration 065), nicht nur im Speicher. Mit
	// `last := time.Now()` verschob jeder Neustart, der über die geplante Minute fiel,
	// den nächsten Termin um eine volle Periode: der Lauf fiel lautlos aus, ohne
	// Protokolleintrag, weil runAutoInvoice gar nicht erst betreten wurde.
	//
	// Ohne gespeicherten Wert (erste Inbetriebnahme) gilt weiter "jetzt": beim
	// allerersten Start soll der Lauf nicht sofort feuern, sondern dem Cron gehören.
	last := loadAutoInvoiceLast(pool)
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			now := time.Now()
			if backup.CronDue(cron, last, now) {
				last = now
				// NACH dem Lauf festhalten. Der Lauf ist durch die Periodensperre
				// idempotent (siehe Migration 065) — eine bereits abgerechnete Periode
				// liefert keine Positionen —, eine Wiederholung kostet also nichts.
				// Andersherum, VOR dem Lauf, verlor ein Neustart mitten im Lauf (Image-
				// Update, OOM-Kill, Host-Reboot) den GANZEN Rest der Periode: der Merker
				// stand bereits auf "erledigt", der nächste Termin lag eine volle Periode
				// später, und niemand erfuhr davon — weder Protokolleintrag noch Alarm,
				// denn die Zusammenfassung starb mit dem Prozess.
				//
				// Das eigene recover verhindert, dass ein Panic AUSSERHALB der
				// Personenschleife die Goroutine und damit den Prozess mitreisst.
				//
				// Und festgehalten wird NUR ein Lauf, der die Personenliste auch
				// wirklich zu Ende gegangen ist. Unbedingt zu speichern hiess: jeder
				// Abbruch — die 15-Minuten-Grenze, ein Panic, ein Lesefehler auf der
				// Personenabfrage, fehlende Pflichtangaben des Ausstellers — schrieb
				// "erledigt" und verschob den nächsten Termin um eine volle Periode.
				// Genau der lautlose Ausfall, gegen den Migration 065 angetreten ist,
				// nur diesmal dauerhaft statt durch den nächsten Neustart geheilt.
				// Eine Wiederholung kostet nichts: die Periodensperre macht den Lauf
				// idempotent, eine bereits abgerechnete Periode liefert keine Positionen.
				//
				// Kein Dauerfeuer daraus: `last` im Speicher steht bereits auf now, der
				// nächste Versuch kommt also frühestens zum nächsten Cron-Termin oder
				// nach einem Neustart — dann aber mit der Chance, den Rest nachzuholen.
				completed := func() (completed bool) {
					defer func() {
						if p := recover(); p != nil {
							slog.Error("auto-invoice: Panic im Lauf — abgebrochen",
								"panic", p, "stack", string(debug.Stack()))
						}
					}()
					return runAutoInvoice(pool, h)
				}()
				if completed {
					saveAutoInvoiceLast(pool, now)
				} else {
					slog.Warn("auto-invoice: Lauf unvollständig — Merker NICHT fortgeschrieben, " +
						"der nächste Start holt die Periode nach")
				}
			}
		}
	}
}

// loadAutoInvoiceLast liest den gespeicherten Zeitpunkt des letzten Laufs. Fehlt er
// (oder ist die Abfrage nicht möglich), gilt "jetzt" — dieselbe Vorsicht wie bisher.
func loadAutoInvoiceLast(pool *pgxpool.Pool) time.Time {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var t *time.Time
	if err := pool.QueryRow(ctx, `SELECT last_run_at FROM auto_invoice_status WHERE id = 1`).Scan(&t); err != nil {
		slog.Warn("auto-invoice: Merker nicht lesbar — der erste Lauf gehört dem Cron", "err", err)
		return time.Now()
	}
	if t == nil {
		return time.Now()
	}
	return *t
}

// saveAutoInvoiceLast hält den Zeitpunkt fest. Best effort: ein Schreibfehler darf
// den Lauf nicht verhindern, er kostet höchstens eine Wiederholung nach Neustart.
func saveAutoInvoiceLast(pool *pgxpool.Pool, at time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// INSERT ... ON CONFLICT statt UPDATE: ein blosses UPDATE auf die eine Zeile
	// meldet keinen Fehler, wenn es NULL Zeilen trifft. Fehlt die Zeile (Rücksicherung
	// eines Auszugs von vor Migration 065, ein Aufräumen von Hand), wäre der Merker
	// damit für immer eine stille Attrappe — und der Lauf verhielte sich wieder wie
	// vor 065, ohne dass irgendetwas darauf hinweist.
	if _, err := pool.Exec(ctx,
		`INSERT INTO auto_invoice_status (id, last_run_at) VALUES (1, $1)
		 ON CONFLICT (id) DO UPDATE SET last_run_at = EXCLUDED.last_run_at`, at); err != nil {
		slog.Warn("auto-invoice: Merker nicht schreibbar", "err", err)
	}
}

// runAutoInvoice meldet, ob der Lauf die Personenliste vollständig abgearbeitet hat.
// Nur dann darf der Merker fortgeschrieben werden — jeder andere Ausgang (Lesefehler,
// Zeitgrenze, fehlende Ausstellerangaben) lässt eine Periode ganz oder halb offen und
// gehört wiederholt, nicht abgehakt.
func runAutoInvoice(pool *pgxpool.Pool, h *handlers.Handler) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	// Herkunft des Laufs: der synthetische Request trägt keinen angemeldeten
	// Benutzer, also schrieb das Protokoll zu JEDER automatisch erzeugten Rechnung
	// einen leeren Akteur — während die Zusammenfassung desselben Laufs über
	// AuditSystem korrekt "system" nannte. Die Markierung stellt beide Hälften
	// gleich. invoices.created_by bleibt bewusst NULL: der Lauf hat keinen Urheber.
	ctx = auth.ContextWithSystemActor(ctx)

	rows, err := pool.Query(ctx, `SELECT id FROM persons WHERE NOT anonymized ORDER BY id`)
	if err != nil {
		slog.Error("auto-invoice: persons query failed", "err", err)
		return false
	}
	var pids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			slog.Error("auto-invoice: scan failed", "err", err)
			return false
		}
		pids = append(pids, id)
	}
	rows.Close()
	if rows.Err() != nil {
		slog.Error("auto-invoice: persons read failed", "err", rows.Err())
		return false
	}

	var created, skipped, failed, incomplete, processed int
	var complianceStop, truncated bool
	for _, pid := range pids {
		if ctx.Err() != nil {
			// Die 15-Minuten-Grenze hat zugeschlagen. Ohne diesen Merker fiel der Lauf
			// still in dieselbe Zusammenfassung wie ein vollständiger: "300 erstellt,
			// 0 fehlgeschlagen" — nicht zu unterscheiden von einem Lauf, bei dem die
			// übrigen 600 Personen schlicht nichts zu fakturieren hatten. Genau das,
			// wogegen der Absatz weiter unten argumentiert.
			truncated = true
			break
		}
		req := httptest.NewRequest("POST", "/api/persons/"+strconv.FormatInt(pid, 10)+"/invoices",
			strings.NewReader(`{"note":"Automatischer Rechnungslauf"}`))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(ctx)
		req.SetPathValue("id", strconv.FormatInt(pid, 10))
		rec := httptest.NewRecorder()
		// Die Schleifenposition EINMAL zaehlen, statt sie unten aus vier Ausgangs-
		// eimern zurueckzurechnen: der complianceStop-Zweig erhoeht keinen davon, die
		// Summe log also schon jetzt um eine Person, und ein fuenfter Ausgang wuerde
		// die Zahl in einem unveraenderlichen Protokolleintrag still verfaelschen.
		processed++
		// Der Handler läuft hier OHNE die recoverPanics-Middleware, die ihn bei jedem
		// echten HTTP-Aufruf umgibt — der synthetische Weg umgeht die ganze Kette. Ohne
		// eigenes recover risse ein Panic aus den Daten EINER Person die Goroutine und
		// damit den Prozess mit: der Container liefe jede Nacht in eine Neustartschleife,
		// während derselbe Datensatz über den Knopf nur ein 500 erzeugt hätte.
		//
		// Der Ausgang kommt als Rückgabewert heraus, nicht über einen Merker daneben:
		// gezählt wird dann an EINER Stelle. Und gezählt werden MUSS getrennt — ohne
		// das continue liefe die Einordnung darunter weiter, und der unberührte
		// Recorder trägt Code 200: das trifft keinen Fall, landet im default-Zweig und
		// zählte dieselbe Person ein zweites Mal als Fehlschlag.
		ran := func() (ran bool) {
			defer func() {
				if p := recover(); p != nil {
					slog.Error("auto-invoice: Panic bei einer Person — übersprungen",
						"person_id", pid, "panic", p, "stack", string(debug.Stack()))
				}
			}()
			h.CreateInvoice(rec, req)
			return true
		}()
		if !ran {
			failed++
			continue
		}
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
		"aborted", complianceStop, "truncated", truncated, "persons", len(pids))
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
	if truncated {
		summary += " — ABGEBROCHEN an der Zeitgrenze von 15 Minuten: von " + strconv.Itoa(len(pids)) +
			" Personen wurden " + strconv.Itoa(processed) +
			" bearbeitet, der Rest NICHT fakturiert"
	}
	if created > 0 || failed > 0 || incomplete > 0 || complianceStop || truncated {
		// WithoutCancel: genau der Lauf, der an der 15-Minuten-Grenze abgeschnitten
		// wurde, ist der, dessen Protokolleintrag am wichtigsten wäre — und mit dem
		// abgelaufenen ctx wäre er der einzige, der nicht geschrieben werden kann.
		// Dasselbe Muster nutzen backup/schedule.go und server/observability.go.
		wctx, wcancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer wcancel()
		h.AuditSystem(wctx, "create", "system", 0, summary, nil)
	}
	// Vollstaendig heisst: jede Person der Liste wurde angefasst. Beide Abbrueche
	// lassen eine Periode ganz oder halb offen und sollen wiederholt werden.
	return !truncated && !complianceStop
}
