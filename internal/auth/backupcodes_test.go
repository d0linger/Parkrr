package auth

import (
	"context"
	"regexp"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

var codeRe = regexp.MustCompile(`^[A-Z0-9]{4}-[A-Z0-9]{4}-[A-Z0-9]{4}$`)

func TestRandomCodeFormat(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		c, err := randomCode()
		if err != nil {
			t.Fatalf("randomCode failed: %v", err)
		}
		if !codeRe.MatchString(c) {
			t.Fatalf("bad code format: %q", c)
		}
		seen[c] = true
	}
	if len(seen) < 190 {
		t.Fatalf("codes not sufficiently random: %d unique of 200", len(seen))
	}
}

func TestHashCode(t *testing.T) {
	h, err := hashCode("ABCD-EFGH-IJKL")
	if err != nil {
		t.Fatalf("hashCode failed: %v", err)
	}
	// Matching codes compare true, regardless of case and surrounding space.
	if bcrypt.CompareHashAndPassword([]byte(h), []byte("ABCD-EFGH-IJKL")) != nil {
		t.Fatal("hash should compare true for the original code")
	}
	if bcrypt.CompareHashAndPassword([]byte(h), []byte(normalizeCode("  abcd-efgh-ijkl  "))) != nil {
		t.Fatal("hash should compare true for a case/space-normalized code")
	}
	// A wrong code compares false.
	if bcrypt.CompareHashAndPassword([]byte(h), []byte("ABCD-EFGH-IJKX")) == nil {
		t.Fatal("hash should compare false for a different code")
	}
}

// Die Codes muessen an der Transaktion des AUFRUFERS haengen. Frueher committete
// GenerateBackupCodes selbst, waehrend TOTPEnable totp_enabled in einer ZWEITEN
// Transaktion setzte — ein zweiter, gleichzeitiger Versuch loeschte damit die
// Codes, die dem Benutzer gerade angezeigt worden waren (das DELETE ersetzt den
// ganzen Satz). Ein Rollback darf folglich NICHTS zurueklassen.
func TestGenerateBackupCodesTxHonoursCallerRollback(t *testing.T) {
	m, pool := testAuthManager(t)
	ctx := context.Background()
	var uid int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1,$2,$3) RETURNING id`,
		"mwtest-bc-tx", "x", "reader").Scan(&uid); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	// Ein Lauf, der zurueckgerollt wird, darf keine Codes hinterlassen.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := m.GenerateBackupCodesTx(ctx, tx, uid); err != nil {
		t.Fatalf("GenerateBackupCodesTx: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	n, err := m.RemainingBackupCodes(ctx, uid)
	if err != nil {
		t.Fatalf("RemainingBackupCodes: %v", err)
	}
	if n != 0 {
		t.Errorf("nach Rollback sind %d Codes gespeichert, erwartet 0", n)
	}

	// Und ein Lauf, der committet, liefert genau die Codes, die auch gelten.
	tx2, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	codes, err := m.GenerateBackupCodesTx(ctx, tx2, uid)
	if err != nil {
		t.Fatalf("GenerateBackupCodesTx: %v", err)
	}
	if err := tx2.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if !m.ConsumeBackupCode(ctx, uid, codes[0]) {
		t.Error("ein zurueckgegebener Code galt nach dem Commit nicht")
	}
}
