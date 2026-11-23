package main

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v4"
)

// composeSpec contains the Compose settings that must agree in both deployments.
type composeSpec struct {
	Services struct {
		DB struct {
			Image   string   `yaml:"image"`
			Volumes []string `yaml:"volumes"`
		} `yaml:"db"`
		// Pointer so an entirely MISSING services.app is nil (not a zero-value that
		// would falsely compare equal to another missing one) — the test then fails.
		App *struct {
			Environment map[string]string `yaml:"environment"`
			Volumes     []string          `yaml:"volumes"`
		} `yaml:"app"`
	} `yaml:"services"`
}

// TestGHCRComposeParity guards against docker-compose.ghcr.yml drifting from the base
// docker-compose.yml. The GHCR file is a full standalone (needed for the single-file
// Portainer paste), so its app service must carry the SAME environment (keys AND
// values) and the same volumes — otherwise a GHCR deployment silently runs without the
// backups, S3 or SMTP that its .env configured (finding H-11: it had fallen behind
// exactly this way). Comparing the decoded maps catches a drifted VALUE too, not just a
// missing key.
func TestGHCRComposeParity(t *testing.T) {
	base := decodeCompose(t, "../../docker-compose.yml").Services.App
	ghcr := decodeCompose(t, "../../docker-compose.ghcr.yml").Services.App
	if base == nil {
		t.Fatal("docker-compose.yml has no services.app")
	}
	if ghcr == nil {
		t.Fatal("docker-compose.ghcr.yml has no services.app")
	}
	if len(base.Environment) == 0 || len(base.Volumes) == 0 {
		t.Fatal("base Compose app must decode nonempty environment and volumes")
	}

	if !reflect.DeepEqual(base.Environment, ghcr.Environment) {
		t.Errorf("app environment drifted between the compose files.\nbase: %v\nghcr: %v\n"+
			"keep docker-compose.ghcr.yml in sync with docker-compose.yml (finding H-11).",
			base.Environment, ghcr.Environment)
	}
	if !reflect.DeepEqual(base.Volumes, ghcr.Volumes) {
		t.Errorf("app volumes drifted: base=%v ghcr=%v (the /backups mount must match).",
			base.Volumes, ghcr.Volumes)
	}
}

func TestPostgreSQLComposeParity(t *testing.T) {
	base := decodeCompose(t, "../../docker-compose.yml").Services.DB
	ghcr := decodeCompose(t, "../../docker-compose.ghcr.yml").Services.DB
	if !strings.HasPrefix(base.Image, "postgres:18-alpine@sha256:") || base.Image != ghcr.Image {
		t.Errorf("both Compose files must use the same pinned PostgreSQL 18 Alpine image: base=%q ghcr=%q", base.Image, ghcr.Image)
	}
	wantMounts := []string{"parkrr-db-data:/var/lib/postgresql"}
	if !reflect.DeepEqual(base.Volumes, wantMounts) || !reflect.DeepEqual(ghcr.Volumes, wantMounts) {
		t.Errorf("both Compose files must mount %v: base=%v ghcr=%v", wantMounts, base.Volumes, ghcr.Volumes)
	}
}

func decodeCompose(t *testing.T, path string) composeSpec {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var f composeSpec
	if err := yaml.Unmarshal(b, &f); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return f
}
