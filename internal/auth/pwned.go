package auth

import (
	"bufio"
	"context"
	"crypto/sha1" // #nosec G505 -- SHA-1 is mandated by the HIBP range protocol, not used as a security primitive.
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// hibpEndpoint is the Have I Been Pwned range API. Only the first five hex
// characters of the password's SHA-1 hash are ever sent (k-anonymity), so the
// password itself never leaves the process. A var (not const) so tests can
// point it at a local stub.
var hibpEndpoint = "https://api.pwnedpasswords.com/range/"

// BreachedPasswordCount reports how many times the password appears in known
// breaches per the HIBP range API. A count > 0 means the password is exposed.
// Network/parse failures return an error so callers can fail open.
func BreachedPasswordCount(ctx context.Context, client *http.Client, password string) (int, error) {
	// The HIBP range protocol mandates SHA-1 of the candidate password (only a
	// 5-char prefix is ever sent); it is not used to store or protect passwords.
	// codeql[go/weak-sensitive-data-hashing]
	sum := sha1.Sum([]byte(password)) // #nosec G401 -- see note on the import above.
	full := strings.ToUpper(hex.EncodeToString(sum[:]))
	prefix, suffix := full[:5], full[5:]

	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, hibpEndpoint+prefix, nil)
	if err != nil {
		return 0, err
	}
	// Ask for the padded response so the returned size does not leak the prefix.
	req.Header.Set("Add-Padding", "true")
	req.Header.Set("User-Agent", "parkrr")

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("hibp: unexpected status %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(io.LimitReader(resp.Body, 1<<20))
	for scanner.Scan() {
		line := scanner.Text()
		sfx, countStr, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(sfx), suffix) {
			n, _ := strconv.Atoi(strings.TrimSpace(countStr))
			return n, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return 0, nil
}
