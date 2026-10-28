// Package server wires routes, middleware and static assets together.
package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/preining/parkrr/internal/auth"
	"github.com/preining/parkrr/internal/backup"
	"github.com/preining/parkrr/internal/handlers"
	"github.com/preining/parkrr/internal/mail"
	"github.com/preining/parkrr/internal/models"
	"github.com/preining/parkrr/web"
)

// New builds the top-level HTTP handler with all routes registered. Background
// goroutines started here (rate-limiter cleanup, login-throttle cleanup) run
// until stop is closed.
// routePatterns listet die geschützten API-Routen der zuletzt gebauten Instanz (Test).
var routePatterns []string

func New(pool *pgxpool.Pool, authMgr *auth.Manager, wa *auth.WebAuthnService, rateLimitPerMin int, metricsToken string, metricsRequireAuth, checkBreachedPasswords, failClosedOnBreach bool, backupKey, dbURL, backupDir string, s3 backup.S3Config, mailer mail.Sender, publicBaseURL string, stop <-chan struct{}, starters ...func(func())) (http.Handler, *handlers.Handler, error) {
	startWorker := func(fn func()) { go fn() }
	if len(starters) > 0 && starters[0] != nil {
		startWorker = starters[0]
	}
	h := handlers.New(pool)
	h.Auth = authMgr
	h.CheckBreachedPasswords = checkBreachedPasswords
	h.FailClosedOnBreach = failClosedOnBreach
	h.BackupKey = backupKey
	h.DatabaseURL = dbURL
	h.BackupDir = backupDir
	h.S3 = s3
	if mailer != nil {
		h.Mail = mailer
	}
	h.PublicBaseURL = publicBaseURL
	ah := handlers.NewAuthHandler(h, authMgr, wa, stop, startWorker)

	// Archive vehicles of finished-and-settled Pauschalen in the background.
	startWorker(func() { startFlatRateArchival(h, stop) })

	// Record daily occupancy snapshots off the dashboard GET (finding L-02).
	startWorker(func() { startOccupancySnapshot(h, stop) })

	// Idempotent one-shot: book real Zahlungseingänge for Pauschale/Nebenkosten
	// period settlements made before migration 036 (they only flipped an off-book
	// flag). Runs in the background so a large dataset never delays serving — und
	// hinter einem Done-Marker, damit der Vollscan über sämtliche Vereinbarungen
	// nicht bei JEDEM Start erneut anfällt (Hundert 08).
	startWorker(func() {
		switch err := h.RunPeriodPaymentBackfillOnce(context.Background()); {
		case err == nil:
		case errors.Is(err, handlers.ErrTaskBusy):
			slog.Info("period-payment backfill runs on another instance")
		default:
			slog.Error("period-payment backfill failed", "err", err)
		}
	})

	mux := http.NewServeMux()

	// Middleware shortcuts.
	authed := authMgr.RequireAuth
	admin := authMgr.RequireAdmin
	// Editors may do everything except user management and the audit log
	// (admins always satisfy RequireRole as well).
	editor := authMgr.RequireRole(models.RoleEditor)
	// Rollen Buchhaltung und Hallendienst (Ausbau 35). money: alles mit Beträgen —
	// jede Rolle außer dem Hallendienst. hall: die Schreibwege der Halle (Planer,
	// Übergaben, Fotos, Service, Schlüsselbuch) für Bearbeiter und Hallendienst. tax:
	// der Steuerbereich für Admins und die Buchhaltung.
	money := authMgr.RequireRole(models.RoleEditor, models.RoleReader, models.RoleAccounting)
	hall := authMgr.RequireRole(models.RoleEditor, models.RoleHall)
	tax := authMgr.RequireRole(models.RoleAccounting)
	hf := func(f func(http.ResponseWriter, *http.Request)) http.Handler { return http.HandlerFunc(f) }
	// Jede geschützte Route steht zusätzlich in routePatterns: der Rollentest geht sie
	// alle durch, statt eine Liste zu pflegen, die neben dem Code veraltet.
	routePatterns = routePatterns[:0]
	route := func(pattern string, hd http.Handler) {
		mux.Handle(pattern, hd)
		routePatterns = append(routePatterns, pattern)
	}

	// --- Auth (public) ---
	mux.HandleFunc("POST /api/auth/login", ah.Login)
	mux.HandleFunc("GET /api/auth/capabilities", ah.Capabilities)
	mux.HandleFunc("POST /api/auth/passkey/login/begin", ah.PasskeyLoginBegin)
	mux.HandleFunc("POST /api/auth/passkey/login/finish", ah.PasskeyLoginFinish)

	// --- Customer self-service portal (PUBLIC, token-scoped, read-only) ---
	// The token travels in the Authorization header, NOT the URL path, so it stays
	// out of access/reverse-proxy logs, browser history, and Referer (finding SEC-01).
	mux.HandleFunc("GET /api/portal/summary", h.PortalSummary)
	mux.HandleFunc("GET /api/portal/invoices/{id}/pdf", h.PortalInvoicePDF)
	mux.HandleFunc("GET /api/portal/invoices/{id}/pay-qr", h.PortalPayQR)
	// Der einzige öffentliche SCHREIBWEG: ein Briefkasten, kein Stift. Wünsche
	// (Kontaktdaten, Abholtermin) landen als Anfrage beim Betreiber (Hundert 85/87).
	mux.HandleFunc("POST /api/portal/requests", h.PortalCreateRequest)
	mux.HandleFunc("GET /api/portal/pickup-slots", h.PortalPickupSlots)
	// Einstellvertrag (Ausbau 30): lesen und annehmen, nur für die eigenen Gefährte.
	mux.HandleFunc("GET /api/portal/contract", h.PortalContractPDF)
	mux.HandleFunc("POST /api/portal/contract/accept", h.PortalAcceptContract)
	mux.HandleFunc("GET /ical/{token}", h.CalendarFeed)

	// --- Auth (protected) ---
	mux.Handle("POST /api/auth/logout", authMgr.RequireLogout(hf(ah.Logout)))
	route("GET /api/auth/me", authed(hf(ah.Me)))
	route("POST /api/auth/change-password", authed(hf(ah.ChangePassword)))
	route("GET /api/auth/sessions", authed(hf(ah.ListSessions)))
	route("DELETE /api/auth/sessions/{handle}", authed(hf(ah.RevokeSession)))
	route("POST /api/auth/sessions/revoke-others", authed(hf(ah.RevokeOtherSessions)))
	route("POST /api/auth/2fa/setup", authed(hf(ah.TOTPSetup)))
	route("POST /api/auth/2fa/enable", authed(hf(ah.TOTPEnable)))
	route("POST /api/auth/2fa/disable", authed(hf(ah.TOTPDisable)))
	route("GET /api/auth/2fa/backup-codes", authed(hf(ah.TOTPBackupCount)))
	route("POST /api/auth/2fa/backup-codes/regenerate", authed(hf(ah.TOTPRegenerateBackup)))

	// --- Passkeys / WebAuthn (protected: manage your own) ---
	route("GET /api/passkeys", authed(hf(ah.ListPasskeys)))
	route("POST /api/passkeys/register/begin", authed(hf(ah.PasskeyRegisterBegin)))
	route("POST /api/passkeys/register/finish", authed(hf(ah.PasskeyRegisterFinish)))
	route("DELETE /api/passkeys/{id}", authed(hf(ah.DeletePasskey)))

	// Globale Suche für die Befehlspalette. authed wie die Listen, die sie durchsucht:
	// sie zeigt nur, was derselbe Benutzer über GET /api/persons, /vehicles, /garages,
	// /categories, /services und /invoices/{id} ohnehin abrufen kann — kein neuer Zugang
	// zu Daten, nur ein schnellerer Weg dorthin.
	route("GET /api/search", authed(hf(h.Search)))
	// Betreiber-Handbuch für "Hilfe & Handbuch" (eingebettet; nur für Admins).
	route("GET /api/help/handbook", admin(hf(h.Handbook)))

	// --- Persons (read: any; write: manager/admin) ---
	route("GET /api/persons", authed(hf(h.ListPersons)))
	route("GET /api/persons/outstanding", money(hf(h.OutstandingByPerson)))
	route("GET /api/persons/duplicates", authed(hf(h.PersonDuplicates)))
	route("POST /api/persons", editor(hf(h.CreatePerson)))
	route("POST /api/import/persons", editor(hf(h.ImportPersons)))
	route("GET /api/import/persons/template", editor(hf(h.ImportTemplate)))
	route("POST /api/import/vehicles", editor(hf(h.ImportVehicles)))
	route("GET /api/import/vehicles/template", editor(hf(h.VehicleImportTemplate)))
	route("POST /api/persons/{id}/portal-link", editor(hf(h.CreatePortalLink)))
	route("GET /api/persons/{id}/portal-links", editor(hf(h.ListPortalLinks)))
	route("POST /api/persons/{id}/portal-link/revoke", editor(hf(h.RevokePortalLinks)))
	route("POST /api/portal-links/{id}/revoke", editor(hf(h.RevokePortalLink)))
	route("GET /api/portal-requests", editor(hf(h.ListPortalRequests)))
	route("POST /api/portal-requests/{id}/resolve", editor(hf(h.ResolvePortalRequest)))
	route("PUT /api/persons/{id}", editor(hf(h.UpdatePerson)))
	route("DELETE /api/persons/{id}", editor(hf(h.DeletePerson)))
	// Anonymisieren ist der Ausweg, wenn DeletePerson wegen bestehender Rechnungen
	// ablehnt — dieselbe Rolle wie das Löschen, weil es dieselbe Entscheidung ist.
	route("POST /api/persons/{id}/anonymize", editor(hf(h.AnonymizePerson)))
	route("GET /api/persons/{id}/stats", money(hf(h.PersonStats)))
	// Aktivitäts-Verlauf: Zahlungen, Rechnungen, Posten, Statuswechsel, Übergaben,
	// Pauschalen in EINEM Strom (Hundert 56).
	route("GET /api/persons/{id}/timeline", money(hf(h.PersonTimeline)))
	// Datei-Anhänge (PDF/JPEG/PNG) je Person und Gefährt (Hundert 57).
	route("GET /api/persons/{id}/attachments", authed(hf(h.ListAttachments)))
	route("POST /api/persons/{id}/attachments", editor(hf(h.UploadAttachment)))
	route("GET /api/vehicles/{id}/attachments", authed(hf(h.ListAttachments)))
	route("POST /api/vehicles/{id}/attachments", editor(hf(h.UploadAttachment)))
	route("GET /api/attachments/{id}", authed(hf(h.GetAttachment)))
	route("DELETE /api/attachments/{id}", editor(hf(h.DeleteAttachment)))

	// --- Payments (recorded money-in / Kontoauszug) ---
	route("GET /api/persons/{id}/payments", money(hf(h.ListPayments)))
	route("POST /api/persons/{id}/payments", editor(hf(h.CreatePayment)))
	route("DELETE /api/payments/{id}", editor(hf(h.DeletePayment)))
	route("GET /api/persons/{id}/open-items", money(hf(h.OpenItems)))
	route("POST /api/persons/{id}/apply-credit", editor(hf(h.ApplyCredit)))

	// --- Invoicing (Rechnungen) ---
	route("GET /api/billing/settings", admin(hf(h.GetBillingSettings)))
	route("POST /api/billing/settings", admin(hf(h.SaveBillingSettings)))
	route("GET /api/persons/{id}/invoices", money(hf(h.ListInvoices)))
	route("POST /api/persons/{id}/invoices", editor(hf(h.CreateInvoice)))
	route("GET /api/invoices", money(hf(h.AllInvoices)))
	route("GET /api/invoices/{id}", money(hf(h.GetInvoice)))
	route("GET /api/invoices/{id}/pdf", money(hf(h.InvoicePDF)))
	route("GET /api/invoices/{id}/pay-qr", money(hf(h.PayQR)))
	route("POST /api/invoices/{id}/remind", editor(hf(h.RemindInvoice)))
	// Mail-Versand und Mahnautomatik (Ausbau 1, 19, 20, 26, 36).
	route("POST /api/invoices/remind", editor(hf(h.RemindInvoices)))
	route("GET /api/invoices/preview", editor(hf(h.InvoiceRunPreview)))
	route("POST /api/invoices/{id}/send", editor(hf(h.SendInvoiceMail)))
	route("GET /api/invoices/{id}/mailings", money(hf(h.InvoiceMailings)))
	route("GET /api/mail-outbox", editor(hf(h.ListOutbox)))
	route("POST /api/mail-outbox/{id}/retry", editor(hf(h.RetryOutbox)))
	route("GET /api/mail-automation", admin(hf(h.GetMailAutomation)))
	route("PUT /api/mail-automation", admin(hf(h.SaveMailAutomation)))
	route("PUT /api/persons/{id}/dunning", editor(hf(h.SetPersonDunning)))
	// Saison und Kunden (Ausbau 21–23, 31).
	route("GET /api/pickup-windows", editor(hf(h.GetPickupWindows)))
	route("PUT /api/pickup-windows", editor(hf(h.SavePickupWindows)))
	route("GET /api/waitlist", editor(hf(h.ListWaitlist)))
	route("GET /api/waitlist/summary", authed(hf(h.WaitlistSummary)))
	route("POST /api/waitlist", editor(hf(h.CreateWaitlist)))
	route("PUT /api/waitlist/{id}", editor(hf(h.UpdateWaitlist)))
	route("DELETE /api/waitlist/{id}", editor(hf(h.DeleteWaitlist)))
	route("POST /api/waitlist/{id}/convert", editor(hf(h.ConvertWaitlist)))
	route("POST /api/invoices/{id}/cancel", editor(hf(h.CancelInvoice)))
	route("POST /api/persons/{id}/pay-invoices", editor(hf(h.PayInvoices)))
	// Kautionen (Ausbau 32): eigenes Konto je Person, Einbehalt über pay-invoices.
	route("GET /api/persons/{id}/deposits", money(hf(h.ListDeposits)))
	route("POST /api/persons/{id}/deposits", editor(hf(h.CreateDeposit)))
	route("DELETE /api/deposits/{id}", admin(hf(h.DeleteDeposit)))
	// Kontoauszug (Ausbau 27): camt.053 einlesen, per Klick zuordnen.
	route("POST /api/bank-import", editor(hf(h.ImportBankStatement)))
	route("GET /api/bank-transactions", money(hf(h.ListBankTransactions)))
	route("POST /api/bank-transactions/{id}/assign", editor(hf(h.AssignBankTransaction)))
	route("POST /api/bank-transactions/{id}/ignore", editor(hf(h.SetBankTransactionIgnored)))
	route("GET /api/invoices/overdue", money(hf(h.OverdueInvoices)))
	// Offene-Posten-Liste als druckfertiges Dokument mit Stichtag (Hundert 17).
	route("GET /api/reports/outstanding.pdf", money(hf(h.OutstandingReportPDF)))
	route("GET /api/reports/week.pdf", authed(hf(h.WeekSchedulePDF)))
	// Kalender-Abo (Ausbau 10): Verwaltung angemeldet, der Feed selbst öffentlich über
	// einen widerrufbaren Schlüssel im Pfad (siehe handlers/schedule.go, server.logPath).
	route("GET /api/calendar/feed", authed(hf(h.CalendarFeedStatus)))
	route("POST /api/calendar/feed", authed(hf(h.CalendarFeedCreate)))
	route("DELETE /api/calendar/feed", authed(hf(h.CalendarFeedRevoke)))
	// Steuerjahr (Einnahmenaufstellung): admin-only, like the invoice settings.
	route("GET /api/reports/tax-year", tax(hf(h.TaxYear)))
	route("GET /api/reports/tax-year.csv", tax(hf(h.TaxYearCSV)))
	route("GET /api/reports/tax-year.pdf", tax(hf(h.TaxYearPDF)))
	route("GET /api/reports/tax-year.zip", tax(hf(h.TaxYearPackage)))
	// Stateless calculation only: usable in tax read-only mode, still admin/CSRF protected.
	route("POST /api/reports/tax-review/preview", tax(hf(h.TaxReviewPreview)))
	route("POST /api/reports/tax-import/preview", tax(hf(h.TaxImportPreview)))
	route("GET /api/tax/properties", tax(hf(h.ListTaxProperties)))
	route("POST /api/tax/properties", tax(hf(h.CreateTaxProperty)))
	route("PUT /api/tax/properties/{id}", tax(hf(h.UpdateTaxProperty)))
	route("GET /api/tax/categories", tax(hf(h.ListTaxCategories)))
	route("GET /api/tax/settings", tax(hf(h.GetTaxSettings)))
	route("PUT /api/tax/settings", tax(hf(h.SaveTaxSettings)))
	route("GET /api/tax/expenses", tax(hf(h.ListTaxExpenses)))
	route("POST /api/tax/expenses", tax(hf(h.CreateTaxExpense)))
	route("POST /api/tax/expenses/{id}/reverse", tax(hf(h.ReverseTaxExpense)))
	route("POST /api/tax/expenses/{id}/receipts", tax(hf(h.UploadTaxReceipt)))
	route("GET /api/tax/expenses/{id}/receipts", tax(hf(h.ListTaxReceipts)))
	route("GET /api/tax/receipts/{id}", tax(hf(h.GetTaxReceipt)))
	route("GET /api/tax/recurring", tax(hf(h.ListTaxRecurring)))
	route("POST /api/tax/recurring", tax(hf(h.CreateTaxRecurring)))
	route("PUT /api/tax/recurring/{id}", tax(hf(h.UpdateTaxRecurring)))
	route("POST /api/tax/recurring/{id}/book", tax(hf(h.BookTaxRecurring)))
	route("GET /api/tax/assets", tax(hf(h.ListTaxAssets)))
	route("POST /api/tax/assets", tax(hf(h.CreateTaxAsset)))
	route("PUT /api/tax/payments/{id}", tax(hf(h.UpdatePaymentTaxMetadata)))
	route("POST /api/tax/years/{year}/lock", tax(hf(h.LockTaxYear)))
	route("DELETE /api/tax/years/{year}/lock", tax(hf(h.UnlockTaxYear)))

	// --- Flat-rate agreements (Pauschale-Einträge) ---
	route("GET /api/persons/{id}/agreements", money(hf(h.ListAgreements)))
	route("POST /api/persons/{id}/agreements", editor(hf(h.CreateAgreement)))
	route("PUT /api/agreements/{id}", editor(hf(h.UpdateAgreement)))
	route("DELETE /api/agreements/{id}", editor(hf(h.DeleteAgreement)))
	route("POST /api/agreements/{id}/paid", editor(hf(h.SetAgreementPaid)))
	route("POST /api/agreements/{id}/period-paid", editor(hf(h.SetAgreementPeriodPaid)))

	// --- Recurring extra costs (Wiederkehrende Nebenkosten) ---
	route("GET /api/persons/{id}/recurring", money(hf(h.ListRecurringCharges)))
	route("POST /api/persons/{id}/recurring", editor(hf(h.CreateRecurringCharge)))
	route("PUT /api/recurring/{id}", editor(hf(h.UpdateRecurringCharge)))
	route("DELETE /api/recurring/{id}", editor(hf(h.DeleteRecurringCharge)))
	route("POST /api/recurring/{id}/paid", editor(hf(h.SetRecurringChargePaid)))
	route("POST /api/recurring/{id}/period-paid", editor(hf(h.SetRecurringChargePeriodPaid)))

	// --- Vehicles ---
	route("GET /api/vehicles", authed(hf(h.ListVehicles)))
	route("GET /api/vehicles/duplicates", authed(hf(h.VehicleDuplicates)))
	route("POST /api/vehicles", editor(hf(h.CreateVehicle)))
	route("PUT /api/vehicles/{id}", editor(hf(h.UpdateVehicle)))
	route("DELETE /api/vehicles/{id}", editor(hf(h.DeleteVehicle)))
	route("POST /api/vehicles/{id}/status", editor(hf(h.ChangeVehicleStatus)))
	route("POST /api/vehicles/{id}/paid", editor(hf(h.MarkPaid)))
	route("POST /api/vehicles/{id}/reactivate", editor(hf(h.ReactivateVehicle)))
	route("POST /api/vehicles/{id}/duplicate", editor(hf(h.DuplicateVehicle)))
	route("GET /api/vehicles/{id}/history", authed(hf(h.VehicleHistory)))
	route("GET /api/vehicles/{id}/label", authed(hf(h.VehicleLabel)))
	route("GET /api/halls/{id}/labels", authed(hf(h.HallLabels)))
	// Literal segment beats the {id} pattern in Go's ServeMux, so this is safe.
	route("GET /api/vehicles/unassigned", authed(hf(h.ListUnassignedVehicles)))

	// --- Vehicle photos ---
	route("GET /api/vehicles/{id}/photos", authed(hf(h.ListPhotos)))
	route("POST /api/vehicles/{id}/photos", hall(hf(h.UploadPhoto)))
	// Fotoreihenfolge in einem Zug; Position 0 = Titelbild (Hundert 58).
	route("PUT /api/vehicles/{id}/photos/order", hall(hf(h.ReorderPhotos)))
	route("GET /api/photos/{id}", authed(hf(h.GetPhoto)))
	route("DELETE /api/photos/{id}", hall(hf(h.DeletePhoto)))

	// --- Handover protocols (Übergabeprotokoll) ---
	route("GET /api/vehicles/{id}/handovers", authed(hf(h.ListHandovers)))
	route("POST /api/vehicles/{id}/handovers", hall(hf(h.CreateHandover)))
	route("GET /api/handover-templates", authed(hf(h.ListHandoverTemplates)))
	route("POST /api/handover-templates", editor(hf(h.CreateHandoverTemplate)))
	route("PUT /api/handover-templates/{id}", editor(hf(h.UpdateHandoverTemplate)))
	route("DELETE /api/handover-templates/{id}", editor(hf(h.DeleteHandoverTemplate)))
	route("GET /api/handovers/{id}/signature", authed(hf(h.GetHandoverSignature)))
	route("GET /api/handovers/{id}/pdf", authed(hf(h.HandoverPDF)))
	// Zustandsfotos zum Protokoll (Ausbau 24), nur bis 24 h nach dem Protokoll.
	route("POST /api/handovers/{id}/photos", hall(hf(h.UploadHandoverPhoto)))
	// Ein unterschriebenes Übergabeprotokoll ist ein Beleg: nur Admins dürfen es
	// entfernen, nicht jeder Bearbeiter (Hundert SEC-79). Gegen nachträgliche
	// Änderungen schützt zusätzlich der Trigger aus Migration 051.
	route("DELETE /api/handovers/{id}", admin(hf(h.DeleteHandover)))

	// --- Serviceaufgaben (Ausbau 25) ---
	route("GET /api/service-tasks", authed(hf(h.ListServiceTasks)))
	route("GET /api/service-tasks/due", authed(hf(h.ServiceTasksDue)))
	route("POST /api/service-tasks", editor(hf(h.CreateServiceTask)))
	route("PUT /api/service-tasks/{id}", editor(hf(h.UpdateServiceTask)))
	route("DELETE /api/service-tasks/{id}", editor(hf(h.DeleteServiceTask)))
	route("POST /api/service-tasks/{id}/done", hall(hf(h.CompleteServiceTask)))
	route("DELETE /api/service-task-log/{id}", hall(hf(h.UndoServiceTask)))

	// --- Schlüssel- und Handsenderbuch (Ausbau 34) ---
	route("GET /api/access-media", authed(hf(h.ListAccessMedia)))
	route("POST /api/access-media", editor(hf(h.CreateAccessMedium)))
	route("PUT /api/access-media/{id}", editor(hf(h.UpdateAccessMedium)))
	route("DELETE /api/access-media/{id}", editor(hf(h.DeleteAccessMedium)))
	route("POST /api/access-media/{id}/issue", hall(hf(h.IssueAccessMedium)))
	route("POST /api/access-media/{id}/return", hall(hf(h.ReturnAccessMedium)))
	route("GET /api/access-media/{id}/loans", authed(hf(h.AccessMediumLoans)))
	route("GET /api/persons/{id}/access-media", authed(hf(h.PersonAccessMedia)))
	// Datenauskunft nach Art. 15 DSGVO (Ausbau 28): nur Admins, steht im Änderungsprotokoll.
	route("GET /api/persons/{id}/export", admin(hf(h.PersonDataExport)))
	// Einstellvertrag (Ausbau 30): versionierte Vorlage, PDF je Gefährt, Annahmen.
	route("GET /api/contract-template", authed(hf(h.GetContractTemplate)))
	route("POST /api/contract-template", admin(hf(h.SaveContractTemplate)))
	route("GET /api/vehicles/{id}/contract.pdf", money(hf(h.VehicleContractPDF)))
	route("GET /api/persons/{id}/contracts", authed(hf(h.PersonContracts)))
	// Wertsicherung (Ausbau 29): VPI-Werte, Tarif-Basis, Vorschau, Anpassen.
	route("GET /api/cpi", money(hf(h.ListCPI)))
	route("PUT /api/cpi", editor(hf(h.SaveCPI)))
	route("DELETE /api/cpi/{month}", editor(hf(h.DeleteCPI)))
	route("GET /api/indexation", money(hf(h.IndexationSettings)))
	route("PUT /api/categories/{id}/indexation", editor(hf(h.SetCategoryIndexation)))
	route("GET /api/indexation/preview", money(hf(h.IndexationPreview)))
	route("POST /api/indexation/apply", editor(hf(h.ApplyIndexation)))
	route("GET /api/vehicles/{id}/rate-changes", money(hf(h.VehicleRateChanges)))
	// Ruhezeit „Kurz weg“: Miete und gebundene Nebenkosten ruhen, Status bleibt eingelagert.
	route("GET /api/vehicles/{id}/pauses", authed(hf(h.VehiclePauses)))
	route("GET /api/vehicles/{id}/pause-preview", money(hf(h.PausePreview)))
	route("GET /api/pauses/due", authed(hf(h.PausesDue)))
	route("POST /api/vehicles/{id}/pause", editor(hf(h.StartPause)))
	route("PUT /api/vehicle-pauses/{id}", editor(hf(h.ExtendPause)))
	// Ausbauplan O6: „Ist zurück“ sieht, wer in der Halle steht.
	route("POST /api/vehicle-pauses/{id}/return", hall(hf(h.ReturnFromPause)))
	route("POST /api/vehicle-pauses/{id}/collect", editor(hf(h.CollectFromPause)))
	route("DELETE /api/vehicle-pauses/{id}", editor(hf(h.DeletePause)))
	route("DELETE /api/vehicle-rate-changes/{id}", editor(hf(h.DeleteRateChange)))

	// --- Categories (tariffs) ---
	route("GET /api/categories", authed(hf(h.ListCategories)))
	route("POST /api/categories", editor(hf(h.CreateCategory)))
	route("PUT /api/categories/{id}", editor(hf(h.UpdateCategory)))
	route("POST /api/categories/{id}/archived", editor(hf(h.SetCategoryArchived)))
	route("DELETE /api/categories/{id}", editor(hf(h.DeleteCategory)))

	// --- Stellplatz / Garagenplaner (read: any; write: editor) ---
	route("GET /api/garages", authed(hf(h.ListGarages)))
	route("POST /api/garages", editor(hf(h.CreateGarage)))
	route("PUT /api/garages/{id}", editor(hf(h.UpdateGarage)))
	route("DELETE /api/garages/{id}", editor(hf(h.DeleteGarage)))
	route("GET /api/garages/{id}/halls", authed(hf(h.ListHalls)))
	route("POST /api/garages/{id}/halls", editor(hf(h.CreateHall)))
	route("PUT /api/halls/{id}", editor(hf(h.UpdateHall)))
	route("PUT /api/halls/{id}/park-area", editor(hf(h.SetHallParkArea)))
	route("DELETE /api/halls/{id}", editor(hf(h.DeleteHall)))
	route("GET /api/halls/{id}/plan", authed(hf(h.GetHallPlan)))
	route("POST /api/halls/{id}/spots", hall(hf(h.CreateSpot)))
	route("PUT /api/spots/{id}", hall(hf(h.UpdateSpot)))
	// Batch: die Geometrie vieler Plätze EINER Halle in einer Transaktion — der
	// Weg fürs Auto-Anordnen, damit ein Abbruch keine halbe Anordnung hinterlässt.
	route("PUT /api/halls/{id}/spots/geometry", hall(hf(h.BatchUpdateSpotGeometry)))
	route("DELETE /api/spots/{id}", hall(hf(h.DeleteSpot)))
	route("PUT /api/spots/{id}/vehicle", hall(hf(h.AssignSpotVehicle)))
	route("DELETE /api/spots/{id}/vehicle", hall(hf(h.UnassignSpotVehicle)))
	// Belegungshistorie: wer stand wann auf dem Platz / wo stand das Gefährt (Hundert 79).
	route("GET /api/spots/{id}/history", authed(hf(h.SpotHistory)))
	route("GET /api/vehicles/{id}/spot-history", authed(hf(h.VehicleSpotHistory)))
	route("PUT /api/vehicles/{id}/dimensions", hall(hf(h.SetVehicleDimensions)))
	route("PUT /api/vehicles/{id}/planner", hall(hf(h.UpdateVehiclePlanner)))

	// Custom Garagenplaner icons ("tags").
	route("GET /api/planner-icons", authed(hf(h.ListPlannerIcons)))
	route("POST /api/planner-icons", editor(hf(h.UploadPlannerIcon)))
	route("GET /api/planner-icons/{id}", authed(hf(h.GetPlannerIcon)))
	route("PUT /api/planner-icons/{id}", editor(hf(h.UpdatePlannerIcon)))
	route("DELETE /api/planner-icons/{id}", editor(hf(h.DeletePlannerIcon)))
	route("GET /api/wall-templates", authed(hf(h.ListWallTemplates)))
	route("POST /api/wall-templates", editor(hf(h.CreateWallTemplate)))
	route("DELETE /api/wall-templates/{id}", editor(hf(h.DeleteWallTemplate)))

	// --- Service catalog ---
	route("GET /api/services", authed(hf(h.ListServiceTypes)))
	route("POST /api/services", editor(hf(h.CreateServiceType)))
	route("PUT /api/services/{id}", editor(hf(h.UpdateServiceType)))
	route("POST /api/services/{id}/archived", editor(hf(h.SetServiceArchived)))
	route("DELETE /api/services/{id}", editor(hf(h.DeleteServiceType)))

	// --- Charges ---
	route("GET /api/charges", money(hf(h.ListCharges)))
	route("POST /api/charges", editor(hf(h.CreateCharge)))
	route("PUT /api/charges/{id}", editor(hf(h.UpdateCharge)))
	route("POST /api/charges/{id}/paid", editor(hf(h.SetChargePaid)))
	route("DELETE /api/charges/{id}", editor(hf(h.DeleteCharge)))

	// --- Stats ---
	route("GET /api/overview", money(hf(h.Overview)))
	route("GET /api/overview/forecast", money(hf(h.OverviewForecast)))
	route("GET /api/occupancy", authed(hf(h.Occupancy)))
	route("GET /api/vehicles/ending-soon", authed(hf(h.EndingSoon)))
	route("GET /api/vehicles/incomplete", authed(hf(h.IncompleteVehicles)))

	// --- CSV export (outstanding | payments | persons | vehicles) ---
	route("GET /api/export/{entity}", money(hf(h.ExportCSV)))

	// --- Users & audit (admin only) ---
	route("GET /api/users", admin(hf(h.ListUsers)))
	route("POST /api/users", admin(hf(h.CreateUser)))
	route("PUT /api/users/{id}", admin(hf(h.UpdateUser)))
	route("DELETE /api/users/{id}", admin(hf(h.DeleteUser)))
	route("POST /api/users/{id}/reset-2fa", admin(hf(h.ResetUserTOTP)))
	route("GET /api/audit", admin(hf(h.ListAudit)))
	// Revisionssicherer Export: JSONL mit SHA-256-Hashkette, prüfbar ohne Parkrr.
	route("GET /api/audit/export", admin(hf(h.ExportAudit)))
	// E-Mail-Versandprotokoll: jeder Versuch mit Empfänger, Betreff, Ausgang (Hundert 86).
	route("GET /api/mail-log", admin(hf(h.ListMailLog)))
	route("POST /api/backup", admin(hf(h.CreateBackup)))
	// Nur die Ampel, bewusst editor+ statt admin: Bearbeiter arbeiten den ganzen Tag
	// in der App und sollen ein totes Backup sehen, ohne Admin zu sein. Liefert weder
	// Verzeichnis noch Bucket noch Dateiliste — dafür bleibt /status admin-only.
	route("GET /api/backup/health", editor(hf(h.BackupHealth)))
	route("GET /api/backup/status", admin(hf(h.BackupStatus)))
	route("GET /api/admin/readiness", admin(hf(h.OperationalReadiness)))
	route("POST /api/backup/schedule", admin(hf(h.SaveBackupSchedule)))
	route("POST /api/backup/run", admin(hf(h.RunScheduledBackup)))
	route("GET /api/backup/file/{name}", admin(hf(h.BackupDownloadFile)))
	route("POST /api/backup/validate", admin(hf(h.BackupValidate)))
	route("POST /api/backup/restore", admin(hf(h.BackupRestore)))
	route("POST /api/backup/s3/test", admin(hf(h.BackupS3Test)))
	route("POST /api/backup/s3", admin(hf(h.CreateBackupS3)))
	route("GET /api/backup/s3/file/{name}", admin(hf(h.BackupS3Download)))
	route("POST /api/backup/validate-s3", admin(hf(h.BackupValidateS3)))
	route("POST /api/backup/restore-s3", admin(hf(h.BackupRestoreS3)))

	// Client-side error telemetry (SPA window.onerror → server log).
	route("POST /api/client-error", authed(hf(h.ClientError)))

	// --- Health, readiness and metrics ---
	registerObservability(mux, pool, metricsToken, metricsRequireAuth)

	// --- Static assets and SPA shell ---
	staticFS, err := fs.Sub(web.StaticFS, "static")
	if err != nil {
		return nil, nil, err
	}
	fileServer := http.FileServer(http.FS(staticFS))

	indexHTML, err := web.StaticFS.ReadFile("static/index.html")
	if err != nil {
		return nil, nil, err
	}
	swJS, err := web.StaticFS.ReadFile("static/sw.js")
	if err != nil {
		return nil, nil, err
	}
	// Asset fingerprinting: append a content hash to the JS/CSS references so a
	// changed asset gets a new URL (cache-bust) while unchanged assets stay
	// cacheable forever. Hashes are computed once from the embedded files.
	indexHTML = fingerprintAsset(indexHTML, staticFS, "/js/geometry.js", "js/geometry.js")
	indexHTML = fingerprintAsset(indexHTML, staticFS, "/js/app.js", "js/app.js")
	indexHTML = fingerprintAsset(indexHTML, staticFS, "/css/style.css", "css/style.css")
	// Expose the build version to the SPA (client-error telemetry reads this meta).
	indexHTML = bytes.ReplaceAll(indexHTML, []byte("__APP_VERSION__"), []byte(Version))
	// Same substitution in the service worker, so its cache name changes with every
	// build. Previously the name was hand-maintained ("parkrr-v288") and a forgotten
	// bump left the OFFLINE shell pinned to stale precached assets (finding PWA-65).
	swJS = bytes.ReplaceAll(swJS, []byte("__APP_VERSION__"), []byte(Version))

	mux.Handle("GET /", gzipStatic(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		// Service worker must be served from the root scope.
		if path == "sw.js" {
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
			w.Header().Set("Service-Worker-Allowed", "/")
			_, _ = w.Write(swJS)
			return
		}
		// Unknown API paths must not fall through to the SPA shell.
		if strings.HasPrefix(path, "api/") {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return
		}
		// Serve real static files if they exist.
		if path != "" {
			if f, err := staticFS.Open(path); err == nil {
				_ = f.Close()
				// Fingerprinted assets (carrying ?v=) are immutable: safe to cache
				// aggressively because the URL changes whenever the content does.
				if r.URL.Query().Get("v") != "" {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		// Otherwise serve the SPA shell.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(indexHTML)
	})))

	return buildChain(authMgr, mux, rateLimitPerMin, stop), h, nil
}

// buildChain assembles the middleware stack around the router (outermost first:
// access log -> metrics -> rate limit -> security headers -> body limit ->
// routes). Extracted from New so the wiring — including the request-body limit —
// is testable without a live database pool.
func buildChain(authMgr *auth.Manager, mux *http.ServeMux, rateLimitPerMin int, stop <-chan struct{}) http.Handler {
	// recoverPanics wraps the router directly (innermost) so a handler panic is
	// turned into a normal 500 return — which the outer metrics/log middleware
	// then record — instead of an unlogged, unmeasured dropped connection.
	chain := securityHeaders(authMgr, limitRequestBody(recoverPanics(mux)))
	chain = rateLimit(authMgr, rateLimitPerMin, stop, chain)
	chain = metricsMiddleware(mux, chain)
	chain = requestLogger(authMgr, chain)
	return chain
}

// recoverPanics converts a handler panic into a logged 500 (with stack + request
// context) instead of a silently aborted connection that emits no log or metric.
// It only emits the 500 body when the handler hasn't already started the response
// (a panic mid-stream, e.g. in streamBackup, must not append a JSON error or
// re-send headers). Unwrap() exposes the underlying writer so http.Response
// Controller can still reach optional interfaces (Flusher/Hijacker).
func recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &panicResponseWriter{ResponseWriter: w}
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic recovered in handler", "err", rec,
					"method", r.Method, "path", r.URL.Path, "stack", string(debug.Stack()))
				if !rw.started {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"error":"internal server error"}`))
				}
			}
		}()
		next.ServeHTTP(rw, r)
	})
}

// panicResponseWriter tracks whether the response has started so recoverPanics
// can tell an unstarted request (safe to send a 500) from one already streaming.
type panicResponseWriter struct {
	http.ResponseWriter
	started bool
}

func (w *panicResponseWriter) WriteHeader(code int) {
	w.started = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *panicResponseWriter) Write(b []byte) (int, error) {
	w.started = true
	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController find optional interfaces on the wrapped writer.
func (w *panicResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// maxRequestBody caps ordinary request bodies as a DoS backstop. It sits ABOVE the
// 8 MiB photo-upload cap (handlers.maxPhotoBytes) so legitimate uploads still
// pass, while JSON bodies stay further limited to 1 MiB in decodeJSON. A request
// that declares more is rejected with 413 before any read; MaxBytesReader caps
// the actual read for chunked/undeclared bodies.
const maxRequestBody = 9 << 20 // 9 MiB

// tooLargeMessage benennt die Grenze, die DIESE Schranke tatsächlich zieht.
//
// Hart hingeschriebene "höchstens 8 MB je Upload" waren zweimal falsch: 8 MB ist die
// Grenze des ANHANG-Handlers, nicht die des Rumpfs (9 MiB), und diese Middleware sitzt
// vor JEDEM Endpunkt — auch vor dem CSV-Import (2 MiB) und der Rücksicherung (9 MiB,
// deren eigene Meldung als einzige den Ausweg "parkrr restore" nennt) und vor jedem
// gewöhnlichen JSON-Rumpf, der gar keine Datei ist. Wer eine 9,5-MiB-Sicherung hochlud,
// las eine Zahl, die auf seinem Weg nichts bedeutete, und verkleinerte auf 8 MB, obwohl
// 9 gereicht hätten. Die Zahl kommt daher aus der Konstante, und der Text spricht vom
// Rumpf; die feineren, freundlicheren Grenzen nennt weiterhin der jeweilige Handler.
var tooLargeMessage = fmt.Sprintf("Die Anfrage ist zu groß (höchstens %d MB). "+
	"Große Sicherungen bitte über die Kommandozeile einspielen.", maxRequestBody>>20)

// limitRequestBody rejects over-large request bodies with 413 and caps the read.
// Restore upload endpoints stream directly to a private file and therefore get a
// separate 1 GiB cap; every other endpoint retains the small global boundary.
func limitRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := int64(maxRequestBody)
		message := tooLargeMessage
		if r.URL.Path == "/api/backup/validate" || r.URL.Path == "/api/backup/restore" {
			limit = handlers.MaxBrowserRestoreRequestBody
			message = "Die Sicherungsdatei ist zu groß (Browser-Limit 1 GiB). Bitte die CLI verwenden."
		}
		if r.ContentLength > limit {
			// Als JSON und auf Deutsch, wie jede andere Absage der Anwendung: die
			// Oberfläche liest `error` aus dem Rumpf und zeigt sonst die nackte
			// Zeichenfolge "HTTP 413". Diese Schranke greift VOR jedem Handler, also
			// auch vor dessen eigener, freundlicher Meldung — sie ist für einen
			// zu großen Upload die EINZIGE Antwort, die der Anwender je zu sehen
			// bekommt, und muss ihm daher selbst sagen, was los ist.
			//
			// Über writeJSONStatus (observability.go), nicht von Hand: das war die
			// vierte handgeschriebene JSON-Absage in diesem Paket und die einzige mit
			// nosniff — der Header gehört an die eine Stelle, nicht an eine von vieren.
			w.Header().Set("X-Content-Type-Options", "nosniff")
			writeJSONStatus(w, http.StatusRequestEntityTooLarge,
				map[string]string{"error": message})
			return
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next.ServeHTTP(w, r)
	})
}

// gzipCompressible reports whether a Content-Type is worth gzip-encoding.
func gzipCompressible(ct string) bool {
	ct = strings.ToLower(ct)
	for _, p := range []string{"text/", "application/javascript", "application/json", "image/svg+xml", "application/manifest+json", "application/wasm"} {
		if strings.HasPrefix(ct, p) {
			return true
		}
	}
	return false
}

// gzipResponseWriter lazily starts gzip on the first write, deciding from the
// Content-Type the inner handler set. Non-compressible, already-encoded, 204/304
// and partial responses pass through untouched.
type gzipResponseWriter struct {
	http.ResponseWriter
	gz          *gzip.Writer
	wroteHeader bool
	passthrough bool
}

func (g *gzipResponseWriter) WriteHeader(code int) {
	if g.wroteHeader {
		return
	}
	g.wroteHeader = true
	h := g.Header()
	// Vary is set before deciding compression so the compressed and uncompressed
	// representations of the same URL share cache variance (a shared cache must not
	// serve one to a client that expects the other).
	h.Add("Vary", "Accept-Encoding")
	if code == http.StatusNoContent || code == http.StatusNotModified ||
		h.Get("Content-Encoding") != "" || !gzipCompressible(h.Get("Content-Type")) {
		g.passthrough = true
		g.ResponseWriter.WriteHeader(code)
		return
	}
	h.Del("Content-Length") // length changes once compressed
	h.Set("Content-Encoding", "gzip")
	g.gz = gzip.NewWriter(g.ResponseWriter)
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	if !g.wroteHeader {
		g.WriteHeader(http.StatusOK)
	}
	if g.passthrough {
		return g.ResponseWriter.Write(b)
	}
	return g.gz.Write(b)
}

func (g *gzipResponseWriter) Close() {
	if g.gz != nil {
		_ = g.gz.Close()
	}
}

// acceptsGzip reports whether the client accepts gzip per Accept-Encoding,
// honoring quality values — "gzip;q=0" (and a "*;q=0" with no gzip token) means
// NOT acceptable, so plain strings.Contains would be wrong.
func acceptsGzip(header string) bool {
	gzipQ, starQ := -1.0, -1.0
	for _, part := range strings.Split(header, ",") {
		name, q := strings.TrimSpace(strings.ToLower(part)), 1.0
		if i := strings.IndexByte(name, ';'); i >= 0 {
			params := name[i+1:]
			name = strings.TrimSpace(name[:i])
			for _, p := range strings.Split(params, ";") {
				if p = strings.TrimSpace(p); strings.HasPrefix(p, "q=") {
					if v, err := strconv.ParseFloat(strings.TrimSpace(p[2:]), 64); err == nil {
						q = v
					}
				}
			}
		}
		switch name {
		case "gzip":
			gzipQ = q
		case "*":
			starQ = q
		}
	}
	if gzipQ >= 0 {
		return gzipQ > 0
	}
	return starQ > 0
}

// gzipStatic transparently gzip-encodes compressible static/SPA responses when
// the client advertises gzip support. Range requests bypass it (gzip and byte
// ranges don't mix); the FileServer's fingerprinted immutable caching is kept.
func gzipStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !acceptsGzip(r.Header.Get("Accept-Encoding")) || r.Header.Get("Range") != "" {
			w.Header().Add("Vary", "Accept-Encoding") // the resource is gzippable; mark cache variance even uncompressed
			next.ServeHTTP(w, r)
			return
		}
		gw := &gzipResponseWriter{ResponseWriter: w}
		defer gw.Close()
		next.ServeHTTP(gw, r)
	})
}

// securityHeaders wraps a handler with sensible default security headers.
// HSTS is only emitted when the original request is HTTPS.
func securityHeaders(authMgr *auth.Manager, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Permissions-Policy",
			"geolocation=(), camera=(), microphone=(), payment=(), usb=(), interest-cohort=()")
		// blob: for object URLs the SPA builds itself (e.g. the portal pay-QR fetched
		// with an auth header and shown via URL.createObjectURL) — same-origin, local.
		csp := "default-src 'self'; img-src 'self' data: blob:; style-src 'self'; " +
			"script-src 'self'; connect-src 'self'; manifest-src 'self'; " +
			"base-uri 'self'; form-action 'self'; frame-ancestors 'none'; " +
			"object-src 'none'; frame-src 'none'; child-src 'none'; worker-src 'self'"
		if authMgr.RequestIsHTTPS(r) {
			// Only over HTTPS: forcing upgrades on plain-HTTP local dev would break
			// same-origin API calls (upgraded to an https port that isn't served).
			csp += "; upgrade-insecure-requests"
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		h.Set("Content-Security-Policy", csp)
		next.ServeHTTP(w, r)
	})
}

// fingerprintAsset appends "?v=<hash>" to every occurrence of ref in html,
// where hash is a short content hash of the embedded file at fsPath. If the
// file cannot be read, html is returned unchanged.
func fingerprintAsset(html []byte, fsys fs.FS, ref, fsPath string) []byte {
	b, err := fs.ReadFile(fsys, fsPath)
	if err != nil {
		return html
	}
	sum := sha256.Sum256(b)
	v := hex.EncodeToString(sum[:])[:10]
	return bytes.ReplaceAll(html, []byte(ref+`"`), []byte(ref+`?v=`+v+`"`))
}

// expirySweeps sind die Nebentabellen, deren Zeilen von sich aus ablaufen. Alle drei
// wurden bisher NUR beiläufig aufgeräumt — beim Anlegen des nächsten Portal-Links,
// beim Start der nächsten Passkey-Zeremonie. Passiert das nicht mehr (der letzte
// Portal-Link wurde vor einem Jahr verschickt, niemand nutzt Passkeys), bleiben die
// abgelaufenen Zeilen für immer liegen: aufräumen tut nur, wer die Tabelle ohnehin
// benutzt (Hundert 32).
//
// Die Bedingungen sind absichtlich dieselben wie an den beiläufigen Stellen, damit
// der Sweep nichts entfernt, was jene stehen lassen würden.
var expirySweeps = []struct {
	table string
	sql   string
}{
	// Abgelaufene Sitzungen; identisch zu auth.CleanupExpired, hier nur der Vollständigkeit
	// halber NICHT aufgeführt — die läuft weiter über den Manager (eigener Pool-Zugriff).
	{"webauthn_ceremonies", `DELETE FROM webauthn_ceremonies WHERE expires_at < now()`},
	// Warteliste (Ausbau 31): die Einwilligung gilt für ein Jahr, danach geht der
	// Eintrag, gleich ob offen, übernommen oder abgesagt.
	{"waitlist", `DELETE FROM waitlist WHERE created_at < now() - interval '12 months'`},
	// 30 Tage Nachlauf: ein gerade abgelaufener ODER widerrufener Link soll in der
	// Verwaltung noch als "abgelaufen"/"widerrufen" sichtbar sein, statt spurlos zu
	// verschwinden.
	//
	// Der `revoked`-Zweig stand ohne jede Frist daneben (`WHERE revoked OR expires_at
	// < …`): er traf eine widerrufene Zeile SOFORT, unabhängig davon, wie jung sie war.
	// Seit dieser Lauf stündlich statt nur gelegentlich läuft, war ein widerrufener Link
	// binnen einer Stunde weg, und der Betreiber konnte nicht mehr sehen, dass je
	// einer bestand — obwohl der Kommentar darüber genau das zusagt. Auch das
	// Anonymisieren setzt revoked, dessen Spuren also mit.
	//
	// Die Frist hängt jetzt allein am Ablaufdatum, das auch ein widerrufener Link
	// trägt. Ein Widerruf wirkt sofort (die Prüfung im Anmeldeweg liest `revoked`),
	// er muss die Zeile also nicht vorzeitig entfernen — er soll sie nur nicht
	// länger als nötig aufheben. Eine eigene `revoked_at`-Spalte wäre die genauere,
	// aber teurere Antwort; das Ablaufdatum ist die vorhandene und ausreichende.
	{"self_service_tokens", `DELETE FROM self_service_tokens WHERE expires_at < now() - interval '30 days'`},
}

// StartExpiryCleanup runs a background loop pruning expired sessions and the
// other self-expiring side tables.
func StartExpiryCleanup(pool *pgxpool.Pool, authMgr *auth.Manager, stop <-chan struct{}) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	sweep := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		// Nicht mehr `_ =`: ein dauerhaft scheiternder Lauf sah bisher exakt aus wie ein
		// funktionierender, und genau so wächst eine Tabelle unbemerkt (Hundert 32).
		if err := authMgr.CleanupExpired(ctx); err != nil {
			slog.Warn("expiry cleanup: sessions failed", "err", err)
		}
		for _, s := range expirySweeps {
			tag, err := pool.Exec(ctx, s.sql)
			if err != nil {
				slog.Warn("expiry cleanup failed", "table", s.table, "err", err)
				continue
			}
			if n := tag.RowsAffected(); n > 0 {
				slog.Info("expiry cleanup: removed expired rows", "table", s.table, "rows", n)
			}
		}
	}
	sweep() // einmal beim Start, damit ein lange nicht gelaufener Bestand nicht erst in einer Stunde schrumpft
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			sweep()
		}
	}
}
