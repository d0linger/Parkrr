package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Regressionen der Daybreak-Prüfung (2026-11): Mails, Portal-Schreibwege und Restore
// gegen Anonymisierung, geänderte E-Mail-Adresse und Widerruf. Die Rennen werden nicht mit
// Schlafpausen geraten, sondern über pg_stat_activity festgenagelt: der Test wartet,
// bis PostgreSQL den Gegenspieler nachweislich an der Sperre parkt.

// waitLocked blocks until a statement matching like waits on a lock. finished
// reports whether the competitor already returned, which means it never waited.
func waitLocked(t *testing.T, ctx context.Context, h *Handler, like string, finished func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err := h.Pool.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity WHERE datname=current_database()
			   AND wait_event_type='Lock' AND query LIKE $1)`, like).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		if finished() {
			t.Fatalf("finished without waiting on the lock (%s)", like)
		}
		if time.Now().After(deadline) {
			t.Fatalf("never waited on the lock (%s)", like)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// purgePersonByID räumt eine Person weg, die cleanupPersons nicht mehr findet
// (nach dem Anonymisieren heißt sie nicht mehr 'Integration').
func purgePersonByID(t *testing.T, h *Handler, id int64) {
	t.Cleanup(func() {
		c := context.Background()
		if err := purgeExec(c, h.Pool, `DELETE FROM invoices WHERE person_id=$1`, id); err != nil {
			t.Logf("cleanup invoices: %v", err)
		}
		if err := purgeExec(c, h.Pool, `DELETE FROM persons WHERE id=$1`, id); err != nil {
			t.Logf("cleanup person: %v", err)
		}
	})
}

// Eine eingereihte Rechnung geht an die Adresse, die beim Versand gilt — nicht an die
// beim Einreihen festgehaltene, die inzwischen korrigiert wurde.
func TestOutboxSendsToCurrentAddress(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	pid, iv := mailInvoiceFixture(t, h, "old.mailbox@example.com")
	fake := &attachSender{}
	h.Mail = fake
	_, id, err := h.queueInvoiceMail(ctx, iv.ID)
	if err != nil {
		t.Fatal(err)
	}
	cleanupMailRows(t, h, id)
	if _, err := h.Pool.Exec(ctx, `UPDATE persons SET email=' new.mailbox@example.com ' WHERE id=$1`, pid); err != nil {
		t.Fatal(err)
	}
	if n := h.ProcessOutbox(ctx); n != 1 {
		t.Fatalf("processed %d rows, want 1", n)
	}
	if len(fake.sent) != 1 || len(fake.sent[0].to) != 1 || fake.sent[0].to[0] != "new.mailbox@example.com" {
		t.Fatalf("delivered to %+v, want the current address only", fake.sent)
	}
	var recipient, status string
	if err := h.Pool.QueryRow(ctx, `SELECT recipient, status FROM mail_outbox WHERE id=$1`, id).Scan(&recipient, &status); err != nil {
		t.Fatal(err)
	}
	if status != "sent" || recipient != "new.mailbox@example.com" {
		t.Fatalf("outbox row: status=%s recipient=%s", status, recipient)
	}

	// Ohne Adresse geht nichts hinaus, auch nicht an die alte.
	_, id2, err := h.queueInvoiceMail(ctx, iv.ID)
	if err != nil {
		t.Fatal(err)
	}
	cleanupMailRows(t, h, id2)
	if _, err := h.Pool.Exec(ctx, `UPDATE persons SET email='' WHERE id=$1`, pid); err != nil {
		t.Fatal(err)
	}
	h.ProcessOutbox(ctx)
	if st, _, att, msg := outboxStatus(t, h, id2); st != "failed" || att || msg == "" || len(fake.sent) != 1 {
		t.Fatalf("no address: status=%s attachment=%v error=%q sent=%d", st, att, msg, len(fake.sent))
	}
}

// Ein Versand, der schon beim Mailserver hängt, wenn die Person anonymisiert wird:
// die Anonymisierung wartet, bis er durch ist, und leert danach auch diese Zeile.
// Vorher meldete sie Erfolg, während die Rechnung noch hinausging.
func TestInvoiceDeliverySerializesAnonymization(t *testing.T) {
	h := testHandler(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pid, iv := mailInvoiceFixture(t, h, "inflight.anon@example.com")
	purgePersonByID(t, h, pid)
	fake := &blockingInvoiceSender{started: make(chan struct{}), release: make(chan struct{})}
	h.Mail = fake
	_, id, err := h.queueInvoiceMail(ctx, iv.ID)
	if err != nil {
		t.Fatal(err)
	}
	cleanupMailRows(t, h, id)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(fake.release) }) }
	var workers sync.WaitGroup
	defer func() {
		release()
		cancel()
		workers.Wait()
	}()
	delivered := make(chan int, 1)
	workers.Add(1)
	go func() {
		defer workers.Done()
		delivered <- h.ProcessOutbox(ctx)
	}()
	select {
	case <-fake.started:
	case <-time.After(5 * time.Second):
		t.Fatal("delivery did not reach sender")
	}
	anonymized := make(chan *httptest.ResponseRecorder, 1)
	workers.Add(1)
	go func() {
		defer workers.Done()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/anonymize", nil).WithContext(ctx)
		req.SetPathValue("id", strconv.FormatInt(pid, 10))
		h.AnonymizePerson(w, req)
		anonymized <- w
	}()
	waitLocked(t, ctx, h, "%first_name = 'Anonymisiert'%", func() bool { return len(anonymized) > 0 })
	release()
	select {
	case n := <-delivered:
		if n != 1 || len(fake.sent) != 1 {
			t.Fatalf("delivery before anonymization: processed=%d sent=%d", n, len(fake.sent))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("delivery did not finish")
	}
	select {
	case w := <-anonymized:
		if w.Code != http.StatusOK {
			t.Fatalf("anonymize after delivery: %d %s", w.Code, w.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("anonymization did not finish after delivery")
	}
	var recipient, status string
	if err := h.Pool.QueryRow(ctx, `SELECT recipient, status FROM mail_outbox WHERE id=$1`, id).Scan(&recipient, &status); err != nil {
		t.Fatal(err)
	}
	if status != "sent" || recipient != "anonymisiert" {
		t.Fatalf("after anonymization: status=%s recipient=%s", status, recipient)
	}
}

// Ein Portal-Wunsch, dessen Link am Anfang noch galt, darf nach einer Anonymisierung
// keinen neuen Datensatz mit Personendaten anlegen. Der Test hält die
// Anonymisierung offen, bis der Wunsch an der Personenzeile wartet.
func TestPortalRequestStopsAtAnonymizationCommittedMidRequest(t *testing.T) {
	h := testHandler(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pid := createIntegrationPerson(t, h)
	token := createPortalLink(t, h, pid)

	anon, err := h.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = anon.Rollback(context.Background()) }()
	if _, err := anon.Exec(ctx, `UPDATE persons SET anonymized=TRUE, email='' WHERE id=$1`, pid); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- portalRequestReq(t, h, token, map[string]any{"kind": "contact_update", "email": "nach.loeschung@example.com"})
	}()
	waitLocked(t, ctx, h, "%SELECT anonymized FROM persons WHERE id=$1 FOR SHARE%", func() bool { return len(done) > 0 })
	if _, err := anon.Exec(ctx, `UPDATE self_service_tokens SET revoked=TRUE WHERE person_id=$1`, pid); err != nil {
		t.Fatal(err)
	}
	if err := anon.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case w := <-done:
		if w.Code != http.StatusNotFound {
			t.Fatalf("portal request after anonymization: %d %s", w.Code, w.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("portal request did not finish")
	}
	var n int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM portal_requests WHERE person_id=$1`, pid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d portal request(s) stored after anonymization", n)
	}
}

// Die Versionsprüfung der Annahme lief vor dem PDF und außerhalb der Transaktion.
// Speichert der Betreiber dazwischen eine neue Fassung, wird jetzt nichts
// angenommen (409) statt der veralteten.
func TestContractAcceptanceRechecksTemplateInTransaction(t *testing.T) {
	h := testHandler(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	t.Cleanup(func() {
		_, _ = h.Pool.Exec(context.Background(), `DELETE FROM contract_acceptances`)
		_, _ = h.Pool.Exec(context.Background(), `DELETE FROM contract_templates`)
	})
	pid, vid, _ := seasonFixture(t, h, "2027-03-31")
	w := callJSONRaw(h, h.SaveContractTemplate, http.MethodPost, "/api/contract-template", "",
		`{"title":"Einstellvertrag","body":"Vertrag für {{name}}."}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	var v1 contractTemplate
	_ = json.Unmarshal(w.Body.Bytes(), &v1)
	token := createPortalLink(t, h, pid)

	publish, err := h.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = publish.Rollback(context.Background()) }()
	if _, err := publish.Exec(ctx, `LOCK TABLE contract_templates IN EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	if _, err := publish.Exec(ctx,
		`INSERT INTO contract_templates (version, title, body) SELECT max(version) + 1, 'Einstellvertrag', 'Neue Fassung.' FROM contract_templates`); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		b, _ := json.Marshal(map[string]any{"vehicle_id": vid, "version": v1.Version})
		req := httptest.NewRequest(http.MethodPost, "/api/portal/contract/accept", bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.PortalAcceptContract(rec, req)
		done <- rec
	}()
	waitLocked(t, ctx, h, "%LOCK TABLE contract_templates IN SHARE MODE%", func() bool { return len(done) > 0 })
	if err := publish.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case rec := <-done:
		if rec.Code != http.StatusConflict {
			t.Fatalf("accepting the superseded version: %d %s", rec.Code, rec.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("acceptance did not finish")
	}
	var n int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM contract_acceptances WHERE person_id=$1`, pid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d acceptance(s) of a superseded template stored", n)
	}
}

// Ein Link, der abläuft, während die Annahme auf die Sperre der Vertragsvorlagen
// wartet, gilt danach nicht mehr. Die Prüfung davor lag vor dem Warten.
func TestContractAcceptanceRejectsLinkExpiredDuringTemplateLockWait(t *testing.T) {
	h := testHandler(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	t.Cleanup(func() {
		_, _ = h.Pool.Exec(context.Background(), `DELETE FROM contract_acceptances`)
		_, _ = h.Pool.Exec(context.Background(), `DELETE FROM contract_templates`)
	})
	pid, vid, _ := seasonFixture(t, h, "2027-03-31")
	w := callJSONRaw(h, h.SaveContractTemplate, http.MethodPost, "/api/contract-template", "",
		`{"title":"Einstellvertrag","body":"Vertrag für {{name}}."}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	var v1 contractTemplate
	_ = json.Unmarshal(w.Body.Bytes(), &v1)
	token := createPortalLink(t, h, pid)
	if _, err := h.Pool.Exec(ctx, `UPDATE self_service_tokens SET expires_at = clock_timestamp() + interval '2 seconds'
		WHERE token_hash = $1`, hashPortalToken(token)); err != nil {
		t.Fatal(err)
	}
	// Ein Speichern, das die Tabelle sperrt und dann verworfen wird: die Fassung bleibt
	// aktuell, nur die Zeit vergeht.
	publish, err := h.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = publish.Rollback(context.Background()) }()
	if _, err := publish.Exec(ctx, `LOCK TABLE contract_templates IN EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		b, _ := json.Marshal(map[string]any{"vehicle_id": vid, "version": v1.Version})
		req := httptest.NewRequest(http.MethodPost, "/api/portal/contract/accept", bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.PortalAcceptContract(rec, req)
		done <- rec
	}()
	waitLocked(t, ctx, h, "%LOCK TABLE contract_templates IN SHARE MODE%", func() bool { return len(done) > 0 })
	var valid bool
	if err := h.Pool.QueryRow(ctx, `SELECT expires_at > clock_timestamp() FROM self_service_tokens WHERE token_hash = $1`,
		hashPortalToken(token)).Scan(&valid); err != nil || !valid {
		t.Fatalf("the link expired before the acceptance reached the template lock (valid=%v err=%v)", valid, err)
	}
	time.Sleep(2300 * time.Millisecond)
	if err := publish.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case rec := <-done:
		if rec.Code != http.StatusNotFound {
			t.Fatalf("acceptance with a link that expired at the template lock: %d %s", rec.Code, rec.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("acceptance did not finish")
	}
	var n int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM contract_acceptances WHERE person_id=$1`, pid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d acceptance(s) stored with an expired link", n)
	}
}

// Ein Restore spielt den Widerrufsstand von Portal-Links und Kalender-Abos zurück.
// Was vor dem Restore nicht mehr galt, darf danach nicht wieder gelten; was galt,
// bleibt. Unbekannt (Absturz, leere Datenbank) heißt: alles widerrufen.
func TestRestoreReappliesLinkRevocations(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	pid := createIntegrationPerson(t, h)
	revokedTok, liveTok := createPortalLink(t, h, pid), createPortalLink(t, h, pid)
	// Ältere Daten: anonymisiert, der Link aber nie widerrufen. Er löst nicht auf
	// und darf durch den Restore eines Stands VOR der Anonymisierung nicht wieder.
	anonPid := createIntegrationPerson(t, h)
	anonTok := createPortalLink(t, h, anonPid)
	var users [2]int64
	for i := range users {
		if err := h.Pool.QueryRow(ctx,
			`INSERT INTO users (username, email, password_hash, role) VALUES ($1, '', 'x', 'reader') RETURNING id`,
			"restore-links-"+strconv.Itoa(i)+"-"+strconv.FormatInt(time.Now().UnixNano(), 10)).Scan(&users[i]); err != nil {
			t.Fatal(err)
		}
		if _, err := h.Pool.Exec(ctx, `INSERT INTO calendar_feeds (user_id, token_hash) VALUES ($1, $2)`,
			users[i], feedHash("restore-feed-"+strconv.FormatInt(users[i], 10))); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = h.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1)`, users[:])
	})
	isRevoked := func(tok string) bool {
		t.Helper()
		var r bool
		if err := h.Pool.QueryRow(ctx, `SELECT revoked FROM self_service_tokens WHERE token_hash=$1`, hashPortalToken(tok)).Scan(&r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	feeds := func() int {
		t.Helper()
		var n int
		if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM calendar_feeds WHERE user_id = ANY($1)`, users[:]).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// Vor dem Restore: ein Link widerrufen, ein Abo widerrufen (gelöscht).
	if _, err := h.Pool.Exec(ctx, `UPDATE self_service_tokens SET revoked=TRUE WHERE token_hash=$1`, hashPortalToken(revokedTok)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(ctx, `DELETE FROM calendar_feeds WHERE user_id=$1`, users[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(ctx, `UPDATE persons SET anonymized=TRUE WHERE id=$1`, anonPid); err != nil {
		t.Fatal(err)
	}
	live := CaptureLiveLinks(ctx, h.Pool)
	// Der Restore bringt den älteren Stand zurück.
	if _, err := h.Pool.Exec(ctx, `UPDATE self_service_tokens SET revoked=FALSE WHERE token_hash=$1`, hashPortalToken(revokedTok)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(ctx, `UPDATE persons SET anonymized=FALSE WHERE id=$1`, anonPid); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(ctx, `INSERT INTO calendar_feeds (user_id, token_hash) VALUES ($1, $2)`,
		users[0], feedHash("restore-feed-"+strconv.FormatInt(users[0], 10))); err != nil {
		t.Fatal(err)
	}
	// Die Bereinigung läuft im Restore selbst, unter dem leeren search_path, den
	// pg_restore hinterlässt.
	runEpilogue := func(l LiveLinks) {
		t.Helper()
		if err := pgx.BeginFunc(ctx, h.Pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SET LOCAL search_path = ''`); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, l.RestoreEpilogue())
			return err
		}); err != nil {
			t.Fatalf("restore epilogue: %v", err)
		}
	}
	runEpilogue(live)
	if !isRevoked(revokedTok) || isRevoked(liveTok) {
		t.Fatalf("after the restore: revoked link revoked=%v, live link revoked=%v", isRevoked(revokedTok), isRevoked(liveTok))
	}
	if !isRevoked(anonTok) {
		t.Fatal("after the restore: the anonymized person's never-revoked link became usable again")
	}
	if n := feeds(); n != 1 {
		t.Fatalf("after the restore: %d calendar feeds, want only the live one", n)
	}
	// The post-restore pass finds nothing left to do.
	if err := h.ReapplyLiveLinks(ctx, live); err != nil {
		t.Fatal(err)
	}
	if isRevoked(liveTok) || feeds() != 1 {
		t.Fatal("the post-restore pass revoked a live link")
	}

	runEpilogue(LiveLinks{})
	if !isRevoked(liveTok) || feeds() != 0 {
		t.Fatal("unknown pre-restore state must revoke every link")
	}
	// The recovery path (no epilogue ran) prunes the same way.
	if _, err := h.Pool.Exec(ctx, `UPDATE self_service_tokens SET revoked=FALSE WHERE token_hash=$1`, hashPortalToken(liveTok)); err != nil {
		t.Fatal(err)
	}
	if err := h.ReapplyLiveLinks(ctx, LiveLinks{}); err != nil {
		t.Fatal(err)
	}
	if !isRevoked(liveTok) {
		t.Fatal("recovery with an unknown pre-restore state must revoke every link")
	}
}

// Ein Link, der abläuft, während der Wunsch auf die Sperre der Person wartet, gilt
// danach nicht mehr: now() wäre der Beginn der Transaktion, vor dem Warten.
func TestPortalRequestRejectsLinkExpiredDuringLockWait(t *testing.T) {
	h := testHandler(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pid := createIntegrationPerson(t, h)
	token := createPortalLink(t, h, pid)
	holder, err := h.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback(context.Background()) }()
	if _, err := holder.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`,
		advisoryKey("parkrr.portal_request."+strconv.FormatInt(pid, 10))); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(ctx, `UPDATE self_service_tokens SET expires_at = clock_timestamp() + interval '1 second'
		WHERE token_hash = $1`, hashPortalToken(token)); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- portalRequestReq(t, h, token, map[string]any{"kind": "contact_update", "email": "zu.spaet@example.com"})
	}()
	waitLocked(t, ctx, h, "%pg_advisory_xact_lock%", func() bool { return len(done) > 0 })
	var valid bool
	if err := h.Pool.QueryRow(ctx, `SELECT expires_at > clock_timestamp() FROM self_service_tokens WHERE token_hash = $1`,
		hashPortalToken(token)).Scan(&valid); err != nil || !valid {
		t.Fatalf("the link expired before the request started waiting (valid=%v err=%v)", valid, err)
	}
	time.Sleep(1300 * time.Millisecond)
	if err := holder.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case w := <-done:
		if w.Code != http.StatusNotFound {
			t.Fatalf("request with a link that expired while waiting: %d %s", w.Code, w.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("portal request did not finish")
	}
	var n int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM portal_requests WHERE person_id=$1`, pid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d portal request(s) stored with an expired link", n)
	}
}

// Der Vorher-Wert im Protokoll stammt aus derselben Sperre wie die Speicherung:
// wartet ein Speichern auf ein anderes, nennt es dessen Ergebnis als alten Wert.
func TestSaveCPIAuditsTheValueItReplaced(t *testing.T) {
	h := testHandler(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	const month = "2031-07"
	t.Cleanup(func() {
		_, _ = h.Pool.Exec(context.Background(), `DELETE FROM cpi_values WHERE month = '2031-07-01'`)
	})
	save := func(v string) *httptest.ResponseRecorder {
		return callJSONRaw(h, h.SaveCPI, http.MethodPut, "/api/cpi", "", `{"month":"`+month+`","value":`+v+`}`)
	}
	if w := save("100"); w.Code != http.StatusOK {
		t.Fatalf("first save: %d %s", w.Code, w.Body.String())
	}
	other, err := h.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Rollback(context.Background()) }()
	if _, err := other.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, advisoryKey("parkrr.cpi."+month)); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Exec(ctx, `UPDATE cpi_values SET value = 110 WHERE month = '2031-07-01'`); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- save("120") }()
	waitLocked(t, ctx, h, "%pg_advisory_xact_lock%", func() bool { return len(done) > 0 })
	if err := other.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case w := <-done:
		if w.Code != http.StatusOK {
			t.Fatalf("second save: %d %s", w.Code, w.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("save did not finish")
	}
	var oldVal, newVal string
	if err := h.Pool.QueryRow(ctx, `SELECT changes->'value'->>'old', changes->'value'->>'new' FROM audit_log
		WHERE entity = 'cpi' AND summary = 'VPI 07/2031 gespeichert' ORDER BY id DESC LIMIT 1`).Scan(&oldVal, &newVal); err != nil {
		t.Fatal(err)
	}
	if oldVal != "110" || newVal != "120" {
		t.Fatalf("audit diff %s → %s, want 110 → 120", oldVal, newVal)
	}
}

// Löschen wartet auf ein laufendes Speichern desselben VPI-Monats und protokolliert
// den Wert, den es tatsächlich löscht.
func TestDeleteCPIWaitsForSaveOfTheSameMonth(t *testing.T) {
	h := testHandler(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	const month = "2031-08"
	t.Cleanup(func() {
		_, _ = h.Pool.Exec(context.Background(), `DELETE FROM cpi_values WHERE month = '2031-08-01'`)
	})
	if w := callJSONRaw(h, h.SaveCPI, http.MethodPut, "/api/cpi", "", `{"month":"`+month+`","value":100}`); w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	saving, err := h.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = saving.Rollback(context.Background()) }()
	if _, err := saving.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, advisoryKey("parkrr.cpi."+month)); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodDelete, "/api/cpi/"+month, nil)
		req.SetPathValue("month", month)
		w := httptest.NewRecorder()
		h.DeleteCPI(w, req)
		done <- w
	}()
	waitLocked(t, ctx, h, "%pg_advisory_xact_lock%", func() bool { return len(done) > 0 })
	if _, err := saving.Exec(ctx, `UPDATE cpi_values SET value = 110 WHERE month = '2031-08-01'`); err != nil {
		t.Fatal(err)
	}
	if err := saving.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case w := <-done:
		if w.Code != http.StatusOK {
			t.Fatalf("delete: %d %s", w.Code, w.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("delete did not finish")
	}
	var deleted string
	if err := h.Pool.QueryRow(ctx, `SELECT changes->'value'->>'old' FROM audit_log
		WHERE entity = 'cpi' AND summary = 'VPI 08/2031 gelöscht' ORDER BY id DESC LIMIT 1`).Scan(&deleted); err != nil {
		t.Fatal(err)
	}
	if deleted != "110" {
		t.Fatalf("audit records %s as deleted, want the saved 110", deleted)
	}
}

// Ein Link, der abläuft, während die Schreibtransaktion auf die Sperre der
// Link-Zeile wartet (hier gehalten von einer Berührung wie last_used_at), gilt
// danach nicht mehr — der Zeitstempel der sperrenden Anweisung liegt vor dem Warten.
func TestPortalRequestRejectsLinkExpiredDuringLinkLockWait(t *testing.T) {
	h := testHandler(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pid := createIntegrationPerson(t, h)
	token := createPortalLink(t, h, pid)
	if _, err := h.Pool.Exec(ctx, `UPDATE self_service_tokens SET expires_at = clock_timestamp() + interval '2 seconds'
		WHERE token_hash = $1`, hashPortalToken(token)); err != nil {
		t.Fatal(err)
	}
	// Barrier 1 holds the request inside its transaction, past resolvePortalPerson.
	gate, err := h.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gate.Rollback(context.Background()) }()
	if _, err := gate.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`,
		advisoryKey("parkrr.portal_request."+strconv.FormatInt(pid, 10))); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- portalRequestReq(t, h, token, map[string]any{"kind": "contact_update", "email": "warten@example.com"})
	}()
	waitLocked(t, ctx, h, "%pg_advisory_xact_lock%", func() bool { return len(done) > 0 })
	// Barrier 2 holds the link row; then barrier 1 lets the request run into it.
	touch, err := h.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = touch.Rollback(context.Background()) }()
	if _, err := touch.Exec(ctx, `UPDATE self_service_tokens SET last_used_at = now() WHERE token_hash = $1`, hashPortalToken(token)); err != nil {
		t.Fatal(err)
	}
	if err := gate.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	waitLocked(t, ctx, h, "%FROM self_service_tokens%FOR SHARE%", func() bool { return len(done) > 0 })
	var valid bool
	if err := h.Pool.QueryRow(ctx, `SELECT expires_at > clock_timestamp() FROM self_service_tokens WHERE token_hash = $1`,
		hashPortalToken(token)).Scan(&valid); err != nil || !valid {
		t.Fatalf("the link expired before the request reached the link lock (valid=%v err=%v)", valid, err)
	}
	time.Sleep(2300 * time.Millisecond)
	if err := touch.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case w := <-done:
		if w.Code != http.StatusNotFound {
			t.Fatalf("request with a link that expired at the link lock: %d %s", w.Code, w.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("portal request did not finish")
	}
	var n int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM portal_requests WHERE person_id=$1`, pid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d portal request(s) stored with an expired link", n)
	}
}

// Auch der endgültige Fehlschlag hält fest, an welche Adresse zuletzt gesendet
// wurde — wie Wiederholung und Erfolg.
func TestOutboxFinalFailureRecordsTheAddressTried(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	pid, iv := mailInvoiceFixture(t, h, "old.final@example.com")
	fake := &attachSender{}
	h.Mail = fake
	_, id, err := h.queueInvoiceMail(ctx, iv.ID)
	if err != nil {
		t.Fatal(err)
	}
	cleanupMailRows(t, h, id)
	if _, err := h.Pool.Exec(ctx, `UPDATE persons SET email='new.final@example.com' WHERE id=$1`, pid); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(ctx, `UPDATE mail_outbox SET attempts=$2 WHERE id=$1`, id, len(outboxBackoff)); err != nil {
		t.Fatal(err)
	}
	fake.err = errors.New("relay unavailable")
	if n := h.ProcessOutbox(ctx); n != 1 {
		t.Fatalf("processed %d rows, want 1", n)
	}
	var status, recipient string
	if err := h.Pool.QueryRow(ctx, `SELECT status, recipient FROM mail_outbox WHERE id=$1`, id).Scan(&status, &recipient); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || recipient != "new.final@example.com" || fake.calls != 1 {
		t.Fatalf("final failure: status=%s recipient=%s calls=%d", status, recipient, fake.calls)
	}
}

// Geldbewegung und Protokolleintrag committen gemeinsam: scheitert der Eintrag, gibt
// es auch die Nebenkosten nicht (vorher blieben sie ohne Spur stehen).
func TestRecurringChargeRollsBackWithItsAuditRow(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	pid := createIntegrationPerson(t, h)
	tag := "audit-reject-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if _, err := h.Pool.Exec(ctx, `CREATE OR REPLACE FUNCTION parkrr_test_reject_audit() RETURNS trigger
		LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'audit rejected by test'; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(ctx, `CREATE TRIGGER parkrr_test_reject_audit BEFORE INSERT ON audit_log
		FOR EACH ROW WHEN (NEW.summary LIKE '%`+tag+`%') EXECUTE FUNCTION parkrr_test_reject_audit()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = h.Pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS parkrr_test_reject_audit ON audit_log`)
		_, _ = h.Pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS parkrr_test_reject_audit()`)
	})
	amount := 12.5
	body, _ := json.Marshal(recurringRequest{Description: tag, Amount: &amount, Period: "monthly", StartDate: "2026-01-01"})
	req := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/recurring", bytes.NewReader(body))
	req.SetPathValue("id", strconv.FormatInt(pid, 10))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateRecurringCharge(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("create with a failing audit write: %d %s", rec.Code, rec.Body.String())
	}
	var n int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM recurring_charges WHERE description=$1`, tag).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d recurring charge(s) persisted without their audit row", n)
	}
}
