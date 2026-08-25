package main

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestGHCRComposeParity guards against docker-compose.ghcr.yml drifting from the
// base docker-compose.yml. The GHCR file is a full standalone (needed for the
// single-file Portainer paste), so it must carry the SAME app environment and the
// /backups volume — otherwise a GHCR deployment silently runs without the backups,
// S3 or SMTP that its .env configured (finding H-11: it had fallen behind exactly
// this way).
func TestGHCRComposeParity(t *testing.T) {
	base := readComposeFile(t, "../../docker-compose.yml")
	ghcr := readComposeFile(t, "../../docker-compose.ghcr.yml")

	// PARKRR_* keys only appear as app-environment map keys (line-start after
	// indentation); on the db side they appear only inside ${...} substitutions.
	keyRe := regexp.MustCompile(`(?m)^\s+(PARKRR_\w+):`)
	envKeys := func(s string) []string {
		set := map[string]bool{}
		for _, m := range keyRe.FindAllStringSubmatch(s, -1) {
			set[m[1]] = true
		}
		out := make([]string, 0, len(set))
		for k := range set {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}

	baseKeys, ghcrKeys := envKeys(base), envKeys(ghcr)
	if strings.Join(baseKeys, ",") != strings.Join(ghcrKeys, ",") {
		t.Errorf("app environment drifted between the compose files.\n"+
			"missing from ghcr: %v\nextra in ghcr:    %v\n"+
			"keep docker-compose.ghcr.yml in sync with docker-compose.yml (finding H-11).",
			missing(baseKeys, ghcrKeys), missing(ghcrKeys, baseKeys))
	}

	// The scheduled-backup volume must be mounted in BOTH, or the GHCR deployment
	// loses its backups.
	const backupMount = "parkrr-backups:/backups"
	if !strings.Contains(base, backupMount) || !strings.Contains(ghcr, backupMount) {
		t.Errorf("both compose files must mount %q (base=%v ghcr=%v)",
			backupMount, strings.Contains(base, backupMount), strings.Contains(ghcr, backupMount))
	}
}

func readComposeFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// missing returns the elements of a not present in b.
func missing(a, b []string) []string {
	inB := map[string]bool{}
	for _, x := range b {
		inB[x] = true
	}
	var out []string
	for _, x := range a {
		if !inB[x] {
			out = append(out, x)
		}
	}
	return out
}
