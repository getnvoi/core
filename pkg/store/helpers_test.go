package store

import (
	"context"
	"testing"

	"github.com/getnvoi/core/pkg/config"
)

// minimalConfig returns a freshly-constructed *config.Config matching
// the shape in examples/minimal.yaml — hetzner infra + one master.
// Passes cfg.Validate() (no storage/dns providers referenced, so no
// provider registry blank-imports needed in tests).
func minimalConfig(t *testing.T, app string) *config.Config {
	t.Helper()
	return &config.Config{
		App:    app,
		Env:    "test",
		SSHKey: "~/.ssh/id_rsa.pub",
		Providers: config.Providers{
			Infra: "hetzner",
		},
		Servers: map[string]config.ServerSpec{
			"master": {Type: "cax11", Region: "nbg1", Role: "master"},
		},
	}
}

// mustCreateMinimalProject inserts a project with minimalConfig(t, name)
// and t.Fatals on failure. Returns the persisted *Project.
func mustCreateMinimalProject(t *testing.T, st *Store, name string) *Project {
	t.Helper()
	cfg := minimalConfig(t, name)
	proj, err := st.CreateProject(context.Background(), name, cfg)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	return proj
}
