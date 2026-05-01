package build_test

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/getnvoi/core/pkg/build"
	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/log"
	"github.com/getnvoi/core/pkg/runtime"
)

// fakeRunner records every Preflight + Login + Build call.
type fakeRunner struct {
	mu sync.Mutex

	preflightCalls int
	logins         []login // host + creds, in order
	builds         []build.Request

	preflightErr error
	loginErr     error
	buildErr     error
}

type login struct{ host, user, pass string }

func (f *fakeRunner) Preflight(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.preflightCalls++
	return f.preflightErr
}

func (f *fakeRunner) Login(_ context.Context, host, user, pass string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logins = append(f.logins, login{host, user, pass})
	return f.loginErr
}

func (f *fakeRunner) Build(_ context.Context, req build.Request, _, _ io.Writer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.builds = append(f.builds, req)
	return f.buildErr
}

func silentLog() log.Log { return log.NewWith(false, io.Discard) }

func rt(services map[string]config.ServiceSpec, registry map[string]config.RegistryDef) *runtime.Runtime {
	return &runtime.Runtime{
		Cfg:           &config.Config{Services: services, Registry: registry},
		RegistryCreds: registry,
		DeployHash:    "20260430-120000",
	}
}

// ── Plan ──────────────────────────────────────────────────────────

func TestPlan_OnlyServicesWithBuild(t *testing.T) {
	reqs := build.Plan(rt(map[string]config.ServiceSpec{
		"api":    {Image: "ghcr.io/x/api", Build: &config.BuildSpec{Context: "./api", Dockerfile: "Dockerfile"}},
		"static": {Image: "nginx:1.27-alpine"}, // no build
		"web":    {Image: "ghcr.io/x/web", Build: &config.BuildSpec{Context: "./web", Dockerfile: "Dockerfile.prod"}},
	}, nil))

	if len(reqs) != 2 {
		t.Fatalf("expected 2 build requests, got %d", len(reqs))
	}
	if reqs[0].ServiceName != "api" || reqs[1].ServiceName != "web" {
		t.Errorf("order: %v", []string{reqs[0].ServiceName, reqs[1].ServiceName})
	}
	if reqs[0].Tag != "ghcr.io/x/api:20260430-120000" {
		t.Errorf("api tag: %q", reqs[0].Tag)
	}
}

func TestPlan_NoServices_Empty(t *testing.T) {
	if got := build.Plan(rt(nil, nil)); len(got) != 0 {
		t.Errorf("expected empty plan, got %v", got)
	}
}

// ── All — full flow ───────────────────────────────────────────────

func TestAll_NoBuilds_NothingInvoked(t *testing.T) {
	r := &fakeRunner{}
	if err := build.All(context.Background(), rt(map[string]config.ServiceSpec{
		"static": {Image: "nginx:alpine"},
	}, nil), r, silentLog()); err != nil {
		t.Fatalf("err: %v", err)
	}
	if r.preflightCalls != 0 || len(r.logins) != 0 || len(r.builds) != 0 {
		t.Errorf("nothing should fire when no service has build:; got pre=%d login=%d build=%d",
			r.preflightCalls, len(r.logins), len(r.builds))
	}
}

func TestAll_PreflightThenLoginPerHostThenBuild(t *testing.T) {
	r := &fakeRunner{}
	err := build.All(context.Background(), rt(
		map[string]config.ServiceSpec{
			"api": {Image: "ghcr.io/x/api", Build: &config.BuildSpec{Context: "./api", Dockerfile: "Dockerfile"}},
			"web": {Image: "ghcr.io/x/web", Build: &config.BuildSpec{Context: "./web", Dockerfile: "Dockerfile"}},
		},
		map[string]config.RegistryDef{
			"ghcr.io": {Username: "alice", Password: "ghp_xxx"},
		},
	), r, silentLog())
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	if r.preflightCalls != 1 {
		t.Errorf("preflight: got %d calls want 1", r.preflightCalls)
	}
	// One login per unique host (both services push to ghcr.io → 1 login)
	if len(r.logins) != 1 || r.logins[0].host != "ghcr.io" || r.logins[0].user != "alice" || r.logins[0].pass != "ghp_xxx" {
		t.Errorf("logins: %+v want [{ghcr.io alice ghp_xxx}]", r.logins)
	}
	if len(r.builds) != 2 {
		t.Errorf("builds: got %d want 2", len(r.builds))
	}
}

func TestAll_MultiHostLoginEach(t *testing.T) {
	r := &fakeRunner{}
	err := build.All(context.Background(), rt(
		map[string]config.ServiceSpec{
			"api": {Image: "ghcr.io/x/api", Build: &config.BuildSpec{Context: ".", Dockerfile: "Dockerfile"}},
			"db":  {Image: "registry.example.com:5000/x/db", Build: &config.BuildSpec{Context: ".", Dockerfile: "Dockerfile"}},
		},
		map[string]config.RegistryDef{
			"ghcr.io":                   {Username: "alice", Password: "p1"},
			"registry.example.com:5000": {Username: "bob", Password: "p2"},
		},
	), r, silentLog())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(r.logins) != 2 {
		t.Fatalf("logins: got %d want 2", len(r.logins))
	}
	// All() sorts registry keys alphabetically → ghcr.io first.
	if r.logins[0].host != "ghcr.io" || r.logins[1].host != "registry.example.com:5000" {
		t.Errorf("login order: %v", []string{r.logins[0].host, r.logins[1].host})
	}
}

// Host-less image with a single declared registry: login fires on
// the declared host. Asserts the user-stated rule — declared creds
// are the contract, no image-name inspection.
func TestAll_HostlessImageStillLogsInToDeclaredRegistry(t *testing.T) {
	r := &fakeRunner{}
	err := build.All(context.Background(), rt(
		map[string]config.ServiceSpec{
			"web": {Image: "nvoi/web", Build: &config.BuildSpec{Context: ".", Dockerfile: "Dockerfile"}},
		},
		map[string]config.RegistryDef{
			"docker.io": {Username: "nvoi", Password: "tok"},
		},
	), r, silentLog())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(r.logins) != 1 || r.logins[0].host != "docker.io" {
		t.Errorf("expected one login to docker.io, got %+v", r.logins)
	}
}

func TestAll_PreflightFailureSkipsLoginAndBuild(t *testing.T) {
	r := &fakeRunner{preflightErr: errors.New("docker daemon down")}
	err := build.All(context.Background(), rt(
		map[string]config.ServiceSpec{
			"api": {Image: "ghcr.io/x/api", Build: &config.BuildSpec{Context: ".", Dockerfile: "Dockerfile"}},
		},
		map[string]config.RegistryDef{
			"ghcr.io": {Username: "u", Password: "p"},
		},
	), r, silentLog())
	if err == nil {
		t.Fatal("expected error")
	}
	if len(r.logins) != 0 || len(r.builds) != 0 {
		t.Errorf("preflight failure must short-circuit; got login=%d build=%d", len(r.logins), len(r.builds))
	}
}

func TestAll_LoginFailureSkipsAllBuilds(t *testing.T) {
	r := &fakeRunner{loginErr: errors.New("invalid credentials")}
	err := build.All(context.Background(), rt(
		map[string]config.ServiceSpec{
			"api": {Image: "ghcr.io/x/api", Build: &config.BuildSpec{Context: ".", Dockerfile: "Dockerfile"}},
		},
		map[string]config.RegistryDef{
			"ghcr.io": {Username: "u", Password: "wrong"},
		},
	), r, silentLog())
	if err == nil {
		t.Fatal("expected error")
	}
	if len(r.builds) != 0 {
		t.Errorf("login failure must abort before any build runs; got %d", len(r.builds))
	}
}

func TestAll_FirstBuildFailureAborts(t *testing.T) {
	r := &fakeRunner{buildErr: errors.New("dockerfile syntax error")}
	err := build.All(context.Background(), rt(
		map[string]config.ServiceSpec{
			"a": {Image: "ghcr.io/x/a", Build: &config.BuildSpec{Context: ".", Dockerfile: "Dockerfile"}},
			"b": {Image: "ghcr.io/x/b", Build: &config.BuildSpec{Context: ".", Dockerfile: "Dockerfile"}},
		},
		map[string]config.RegistryDef{
			"ghcr.io": {Username: "u", Password: "p"},
		},
	), r, silentLog())
	if err == nil {
		t.Fatal("expected error")
	}
	if len(r.builds) != 1 {
		t.Errorf("fail-fast: expected 1 attempted then abort, got %d", len(r.builds))
	}
}

// No defensive "missing registry for image host" check — the
// validator owns that gate (build set + no registry: block →
// validate error). The build phase trusts the validator and just
// logs in to whatever's declared. This test asserts that the
// validator-passed-but-empty-registry path runs build without
// touching login (no creds to use).
func TestAll_NoDeclaredRegistry_BuildStillRuns(t *testing.T) {
	r := &fakeRunner{}
	err := build.All(context.Background(), rt(
		map[string]config.ServiceSpec{
			"api": {Image: "public.io/x/api", Build: &config.BuildSpec{Context: ".", Dockerfile: "Dockerfile"}},
		},
		nil, // no registry: at all — only valid for public/anonymous push targets
	), r, silentLog())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(r.logins) != 0 {
		t.Errorf("no declared registry → no login attempts, got %+v", r.logins)
	}
	if len(r.builds) != 1 {
		t.Errorf("build should still fire, got %d", len(r.builds))
	}
}
