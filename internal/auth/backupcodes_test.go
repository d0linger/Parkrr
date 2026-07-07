package auth

import (
	"regexp"
	"testing"
)

var codeRe = regexp.MustCompile(`^[A-Z0-9]{4}-[A-Z0-9]{4}$`)

func TestRandomCodeFormat(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		c := randomCode()
		if !codeRe.MatchString(c) {
			t.Fatalf("bad code format: %q", c)
		}
		seen[c] = true
	}
	if len(seen) < 190 {
		t.Fatalf("codes not sufficiently random: %d unique of 200", len(seen))
	}
}

func TestHashCodeNormalizes(t *testing.T) {
	// Case and surrounding whitespace must not change the hash.
	if hashCode("abcd-efgh") != hashCode("  ABCD-EFGH  ") {
		t.Fatal("hashCode should be case- and space-insensitive")
	}
	if hashCode("ABCD-EFGH") == hashCode("ABCD-EFGX") {
		t.Fatal("different codes must hash differently")
	}
	if len(hashCode("x")) != 64 {
		t.Fatal("expected hex sha-256 (64 chars)")
	}
}
