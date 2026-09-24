package handlers

import (
	"bufio"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fakeAuditRows liefert n Zeilen und zählt, wie viele davon gelesen wurden.
type fakeAuditRows struct {
	n, read int
}

func (f *fakeAuditRows) Next() bool {
	if f.read >= f.n {
		return false
	}
	f.read++
	return true
}

func (f *fakeAuditRows) Scan(dest ...any) error {
	*(dest[0].(*int64)) = int64(f.read)
	*(dest[3].(*string)) = "update"
	*(dest[7].(*json.RawMessage)) = json.RawMessage(`{"x":{"old":1,"new":2}}`)
	*(dest[8].(*time.Time)) = time.Unix(int64(f.read), 0)
	return nil
}

func (f *fakeAuditRows) Err() error { return nil }

type brokenWriter struct{ writes int }

func (b *brokenWriter) Write([]byte) (int, error) {
	b.writes++
	return 0, errors.New("broken pipe")
}

// PRT-02: ist der Abrufer weg, endet die Schleife beim ERSTEN Schreibfehler —
// statt das ganze Protokoll für niemanden weiterzulesen.
func TestAuditExportStopsOnFirstWriteError(t *testing.T) {
	rows := &fakeAuditRows{n: 10000}
	bw := bufio.NewWriterSize(&brokenWriter{}, 256)
	_, _, err := writeAuditExportRows(rows, bw)
	if !errors.Is(err, errAuditExportWrite) {
		t.Fatalf("want errAuditExportWrite, got %v", err)
	}
	if rows.read > 10 {
		t.Errorf("the loop kept scanning after the client was gone: %d of %d rows read", rows.read, rows.n)
	}
}

func TestAuditExportRowsCompleteWithoutError(t *testing.T) {
	rows := &fakeAuditRows{n: 3}
	rec := httptest.NewRecorder()
	bw := bufio.NewWriter(rec)
	count, chain, err := writeAuditExportRows(rows, bw)
	if err != nil || count != 3 || chain == "" {
		t.Fatalf("count=%d chain=%q err=%v", count, chain, err)
	}
}

// Höchstens ein Export gleichzeitig: ein zweiter Klick bekommt 429, statt eine
// weitere der zehn Poolverbindungen zu belegen.
func TestAuditExportConcurrencySlot(t *testing.T) {
	auditExportSlots <- struct{}{}
	defer func() { <-auditExportSlots }()
	h := &Handler{}
	rec := httptest.NewRecorder()
	h.ExportAudit(rec, httptest.NewRequest(http.MethodGet, "/api/audit/export", nil))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second concurrent export: %d, want 429", rec.Code)
	}
}
