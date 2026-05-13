package workload

import (
	"testing"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/runtime"
)

// TestDatabaseEnvVars_FiveCanonicalPerBinding locks the
// non-negotiable contract: every entry in services.X.databases
// expands to <PREFIX>_URL/HOST/PORT/USER/PASSWORD, each binding
// SecretKeyRef'd at the per-DB credentials Secret with the
// canonical lowercase Secret keys. Drift here breaks the
// substrate's promise that apps stay identical across engines.
func TestDatabaseEnvVars_FiveCanonicalPerBinding(t *testing.T) {
	rt := &runtime.Runtime{Cfg: &config.Config{App: "myapp", Env: "prod"}}
	svc := config.ServiceSpec{
		Databases: []string{"DATABASE=app", "REPORTS=analytics"},
	}
	envs := databaseEnvVars(rt, svc)
	if len(envs) != 10 {
		t.Fatalf("envs = %d, want 10 (2 bindings × 5)", len(envs))
	}

	// Index by env-var name for assertions.
	got := map[string]string{} // env name → Secret key it binds to
	gotSecret := map[string]string{}
	for _, e := range envs {
		if e.ValueFrom == nil || e.ValueFrom.SecretKeyRef == nil {
			t.Errorf("env %s has no SecretKeyRef", e.Name)
			continue
		}
		got[e.Name] = e.ValueFrom.SecretKeyRef.Key
		gotSecret[e.Name] = e.ValueFrom.SecretKeyRef.Name
	}

	// DATABASE prefix → app's credentials Secret with lowercase keys.
	for envName, wantKey := range map[string]string{
		"DATABASE_URL":      "url",
		"DATABASE_HOST":     "host",
		"DATABASE_PORT":     "port",
		"DATABASE_USER":     "user",
		"DATABASE_PASSWORD": "password",
	} {
		if got[envName] != wantKey {
			t.Errorf("%s → key %q, want %q", envName, got[envName], wantKey)
		}
		if gotSecret[envName] != "nvoi-myapp-prod-db-app-credentials" {
			t.Errorf("%s → Secret %q", envName, gotSecret[envName])
		}
	}

	// REPORTS prefix → analytics' credentials Secret.
	if gotSecret["REPORTS_URL"] != "nvoi-myapp-prod-db-analytics-credentials" {
		t.Errorf("REPORTS_URL Secret = %q", gotSecret["REPORTS_URL"])
	}
}

// TestDatabaseEnvVars_DefaultPrefixForBareName locks that
// `databases: [app]` (no prefix) gets the canonical DATABASE_ prefix.
// Single-DB services use this form — zero-ceremony default.
func TestDatabaseEnvVars_DefaultPrefixForBareName(t *testing.T) {
	rt := &runtime.Runtime{Cfg: &config.Config{App: "myapp", Env: "prod"}}
	svc := config.ServiceSpec{Databases: []string{"app"}}
	envs := databaseEnvVars(rt, svc)
	if len(envs) != 5 {
		t.Fatalf("envs = %d, want 5", len(envs))
	}
	names := map[string]bool{}
	for _, e := range envs {
		names[e.Name] = true
	}
	for _, want := range []string{"DATABASE_URL", "DATABASE_HOST", "DATABASE_PORT", "DATABASE_USER", "DATABASE_PASSWORD"} {
		if !names[want] {
			t.Errorf("missing canonical env var %q", want)
		}
	}
}

func TestDatabaseEnvVars_EmptyDatabasesYieldsNil(t *testing.T) {
	rt := &runtime.Runtime{Cfg: &config.Config{App: "myapp", Env: "prod"}}
	if got := databaseEnvVars(rt, config.ServiceSpec{}); got != nil {
		t.Errorf("empty Databases should yield nil, got %v", got)
	}
}
