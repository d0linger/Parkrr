package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/preining/parkrr/internal/restorectl"
)

func TestBrowserRestoreEndpointsFailClosedByDefault(t *testing.T) {
	h := &Handler{}
	for _, tc := range []struct {
		name    string
		path    string
		handler http.HandlerFunc
	}{
		{name: "validate upload", path: "/api/backup/validate", handler: h.BackupValidate},
		{name: "restore upload", path: "/api/backup/restore", handler: h.BackupRestore},
		{name: "validate s3", path: "/api/backup/validate-s3", handler: h.BackupValidateS3},
		{name: "restore s3", path: "/api/backup/restore-s3", handler: h.BackupRestoreS3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader("must not be processed"))
			tc.handler(rec, req)
			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestBrowserRestoreValidationEndpointsRequireStepUp(t *testing.T) {
	h := testHandler(t)
	controller, err := restorectl.New(t.Context(), h.Pool, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(controller.Close)
	h.Restore = controller
	for _, tc := range []struct {
		name    string
		path    string
		handler http.HandlerFunc
	}{
		{name: "validate upload", path: "/api/backup/validate", handler: h.BackupValidate},
		{name: "validate s3", path: "/api/backup/validate-s3", handler: h.BackupValidateS3},
		{name: "restore upload", path: "/api/backup/restore", handler: h.BackupRestore},
		{name: "restore s3", path: "/api/backup/restore-s3", handler: h.BackupRestoreS3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(`{"name":"parkrr-test.dump.enc","key":"secret"}`))
			tc.handler(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "reauth_required") {
				t.Fatalf("expected reauth_required in response, got %s", rec.Body.String())
			}
		})
	}
}

func TestRestoreConfirmationBindsChecksumSuffix(t *testing.T) {
	checksum := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if got, want := restoreConfirmation(checksum), "RESTORE 456789ABCDEF"; got != want {
		t.Fatalf("confirmation = %q, want %q", got, want)
	}
}
