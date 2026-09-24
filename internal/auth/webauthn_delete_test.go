package auth

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestDeleteCredentialSafelyPreservesOneUnderConcurrency(t *testing.T) {
	_, pool := testAuthManager(t)
	userID := mkAuthUser(t, pool, "editor", false)
	service := &WebAuthnService{pool: pool}
	ids := make([]int64, 2)
	for i := range ids {
		if err := pool.QueryRow(t.Context(),
			`INSERT INTO webauthn_credentials (user_id,credential_id,public_key,name)
			 VALUES ($1,$2,$3,$4) RETURNING id`, userID,
			[]byte(fmt.Sprintf("delete-safe-%d-%d", time.Now().UnixNano(), i)), []byte{1}, "key").Scan(&ids[i]); err != nil {
			t.Fatal(err)
		}
	}

	type result struct {
		deleted int64
		last    bool
		err     error
	}
	results := make([]result, len(ids))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			deleted, last, err := service.DeleteCredentialSafely(ctx, userID, id, true)
			results[i] = result{deleted, last, err}
		}()
	}
	close(start)
	wg.Wait()
	var succeeded, rejected int
	for i, res := range results {
		switch {
		case res.err != nil:
			t.Fatalf("deletion %d returned unexpected error: %v", i, res.err)
		case res.deleted == 1 && !res.last:
			succeeded++
		case res.deleted == 0 && res.last:
			rejected++
		default:
			t.Fatalf("deletion %d: deleted=%d lastCredential=%v", i, res.deleted, res.last)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("got %d successful and %d lastCredential-rejected deletions, want 1 and 1", succeeded, rejected)
	}
	var remaining int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM webauthn_credentials WHERE user_id=$1`, userID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatalf("concurrent passkey deletion left %d credentials, want 1", remaining)
	}
}
