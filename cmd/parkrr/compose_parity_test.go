package main

import (
	"os"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

// composeApp is the subset of a Compose file we assert parity on: the app service's
// environment (keys AND values) and its volume mounts.
type composeApp struct {
	Services struct {
		App struct {
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

func decodeCompose(t *testing.T, path string) composeApp {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var f composeApp
	if err := yaml.Unmarshal(b, &f); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return f
}
