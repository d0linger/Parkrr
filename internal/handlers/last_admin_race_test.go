package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/preining/parkrr/internal/auth"
	"github.com/preining/parkrr/internal/models"
)

// Finding SH-04: two concurrent demotions of the last two admins must not both
// succeed and leave zero administrators. UpdateUser now runs the last-admin check
// and the role change together under SELECT ... FOR UPDATE on the admin rows, so
// one demotion wins and the other is refused (409); at least one admin remains.
//
// Runs only when PARKRR_TEST_DATABASE_URL is set (testHandler skips otherwise).
func TestLastAdminDemotionRaceKeepsOneAdmin(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()

	// Precondition: a clean admin table, so the two we create are the last two.
	var nAdmins int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE is_admin`).Scan(&nAdmins); err != nil {
		t.Fatalf("count admins: %v", err)
	}
	if nAdmins != 0 {
		t.Skipf("expected a clean admin table, found %d pre-existing admin(s)", nAdmins)
	}

	mkAdmin := func(uname string) int64 {
		var id int64
		if err := h.Pool.QueryRow(ctx,
			`INSERT INTO users (username, password_hash, role, is_admin)
			 VALUES ($1,'x','admin',true) RETURNING id`, uname).Scan(&id); err != nil {
			t.Fatalf("insert admin %s: %v", uname, err)
		}
		return id
	}
	nameA, nameB := "sh04-admin-a-Integration", "sh04-admin-b-Integration"
	idA, idB := mkAdmin(nameA), mkAdmin(nameB)
	t.Cleanup(func() {
		_, _ = h.Pool.Exec(ctx, `DELETE FROM users WHERE username LIKE 'sh04-%-Integration'`)
	})

	// Synthetic actor: audit_log.user_id has no FK, and a negative id can never
	// collide with a real (serial) user, so the self-guard is never tripped.
	actor := &models.User{ID: -1, Username: "sh04-actor-Integration", Role: models.RoleAdmin, IsAdmin: true}

	demote := func(id int64, uname string) int {
		body, _ := json.Marshal(userRequest{Username: uname, Email: "", Role: models.RoleEditor})
		req := httptest.NewRequest(http.MethodPut, "/api/users/"+strconv.FormatInt(id, 10), bytes.NewReader(body))
		req.SetPathValue("id", strconv.FormatInt(id, 10))
		req = req.WithContext(auth.ContextWithUser(ctx, actor))
		w := httptest.NewRecorder()
		h.UpdateUser(w, req)
		return w.Code
	}

	// Release both demotions together to maximize overlap on the FOR UPDATE lock.
	start := make(chan struct{})
	var wg sync.WaitGroup
	codes := make([]int, 2)
	wg.Add(2)
	go func() { defer wg.Done(); <-start; codes[0] = demote(idA, nameA) }()
	go func() { defer wg.Done(); <-start; codes[1] = demote(idB, nameB) }()
	close(start)
	wg.Wait()

	okN, conflictN := 0, 0
	for _, c := range codes {
		switch c {
		case http.StatusOK:
			okN++
		case http.StatusConflict:
			conflictN++
		default:
			t.Fatalf("unexpected status %d (codes=%v)", c, codes)
		}
	}
	if okN != 1 || conflictN != 1 {
		t.Fatalf("want exactly one 200 and one 409, got codes=%v", codes)
	}

	var remaining int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE is_admin`).Scan(&remaining); err != nil {
		t.Fatalf("count admins after: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("last-admin invariant violated: %d admins remain (want 1)", remaining)
	}
}
