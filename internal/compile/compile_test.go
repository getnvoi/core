package compile_test

import (
	"testing"

	"github.com/getnvoi/core/internal/compile"
	"github.com/getnvoi/core/internal/config"
	"github.com/getnvoi/core/internal/log"
	"github.com/getnvoi/core/internal/runtime"
	"github.com/getnvoi/core/internal/state"
	"github.com/getnvoi/core/internal/testutil/hcltest"

	_ "github.com/getnvoi/core/internal/providers/hetzner"
)

// rt builds a minimal runtime suitable for compile tests. Pure
// in-memory — no disk, no env, no provider calls.
func rt(t *testing.T, servers map[string]config.ServerSpec, backend *state.Backend) *runtime.Runtime {
	t.Helper()
	return &runtime.Runtime{
		Cfg: &config.Config{
			App:       "hello",
			Env:       "dev",
			Providers: config.Providers{Infra: "hetzner"},
			Servers:   servers,
		},
		SSHPubKey: []byte("ssh-ed25519 AAAA test"),
		Log:       log.New(false),
		WorkDir:   t.TempDir(),
		Backend:   backend,
	}
}

// fileBytes runs Compile and returns the rendered hetzner.tf bytes.
func fileBytes(t *testing.T, r *runtime.Runtime) []byte {
	t.Helper()
	b, err := compile.Compile(r)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	files, err := b.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	src, ok := files["hetzner.tf"]
	if !ok {
		t.Fatalf("hetzner.tf not in bundle: %v", keys(files))
	}
	return src
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// ── tests ──────────────────────────────────────────────────────────

func TestCompile_MinimalIsValidHCL(t *testing.T) {
	src := fileBytes(t, rt(t, map[string]config.ServerSpec{
		"master": {Type: "cax11", Region: "nbg1", Role: "master"},
	}, nil))
	hcltest.ParseValid(t, src, "hetzner.tf")
}

func TestCompile_HAEmitsLoadBalancer(t *testing.T) {
	src := fileBytes(t, rt(t, map[string]config.ServerSpec{
		"m1": {Type: "cax21", Region: "nbg1", Role: "master", Primary: true},
		"m2": {Type: "cax21", Region: "nbg1", Role: "master"},
		"m3": {Type: "cax21", Region: "nbg1", Role: "master"},
	}, nil))
	body := hcltest.ParseValid(t, src, "hetzner.tf")

	if hcltest.FindBlock(body, "resource", "hcloud_load_balancer", "cp") == nil {
		t.Errorf("HA: missing hcloud_load_balancer.cp")
	}
	if hcltest.FindBlock(body, "resource", "hcloud_load_balancer_target", "cp") == nil {
		t.Errorf("HA: missing hcloud_load_balancer_target.cp")
	}
	// every master gets its own server resource
	for _, k := range []string{"m1", "m2", "m3"} {
		if hcltest.FindBlock(body, "resource", "hcloud_server", k) == nil {
			t.Errorf("missing hcloud_server.%s", k)
		}
	}
}

func TestCompile_NonHASkipsLoadBalancer(t *testing.T) {
	src := fileBytes(t, rt(t, map[string]config.ServerSpec{
		"master": {Type: "cax11", Region: "nbg1", Role: "master"},
	}, nil))
	body := hcltest.ParseValid(t, src, "hetzner.tf")

	if hcltest.FindBlock(body, "resource", "hcloud_load_balancer", "cp") != nil {
		t.Errorf("single master: should NOT emit hcloud_load_balancer.cp")
	}
}

func TestCompile_BackendBlockOmittedWhenNil(t *testing.T) {
	src := fileBytes(t, rt(t, map[string]config.ServerSpec{
		"master": {Type: "cax11", Region: "nbg1", Role: "master"},
	}, nil))
	body := hcltest.ParseValid(t, src, "hetzner.tf")

	tf := hcltest.FindBlock(body, "terraform")
	if tf == nil {
		t.Fatal("missing terraform { } block")
	}
	if hcltest.FindBlock(tf.Body, "backend", "s3") != nil {
		t.Errorf("backend should NOT be present when rt.Backend is nil")
	}
}

func TestCompile_BackendBlockEmitsResolvedCreds(t *testing.T) {
	src := fileBytes(t, rt(t, map[string]config.ServerSpec{
		"master": {Type: "cax11", Region: "nbg1", Role: "master"},
	}, &state.Backend{
		Bucket:    "nvoi-hello-dev-tfstate",
		Endpoint:  "https://acct.r2.example.com",
		Region:    "auto",
		AccessKey: "AKIATEST",
		SecretKey: "secrettest",
	}))
	body := hcltest.ParseValid(t, src, "hetzner.tf")

	tf := hcltest.FindBlock(body, "terraform")
	bk := hcltest.FindBlock(tf.Body, "backend", "s3")
	if bk == nil {
		t.Fatal("backend \"s3\" block not present")
	}

	want := map[string]string{
		"bucket":     "nvoi-hello-dev-tfstate",
		"region":     "auto",
		"access_key": "AKIATEST",
		"secret_key": "secrettest",
	}
	for k, v := range want {
		got, ok := hcltest.StringAttr(bk, k)
		if !ok {
			t.Errorf("backend.%s: missing or non-literal", k)
			continue
		}
		if got != v {
			t.Errorf("backend.%s: got %q want %q", k, got, v)
		}
	}
}
