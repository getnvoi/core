package compile_test

import (
	"strings"
	"testing"

	"github.com/getnvoi/core/pkg/compile"
	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/log"
	"github.com/getnvoi/core/pkg/runtime"
	"github.com/getnvoi/core/pkg/state"
	"github.com/getnvoi/core/pkg/testutil/hcltest"

	_ "github.com/getnvoi/core/pkg/providers/hetzner"
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
	return bundleFile(t, r, "hetzner.tf")
}

// bundleFile runs Compile and returns the named file from the bundle.
// Used to pull either hetzner.tf or backend.tf depending on what
// the test is asserting against.
func bundleFile(t *testing.T, r *runtime.Runtime, name string) []byte {
	t.Helper()
	b, err := compile.Compile(r)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	files, err := b.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	src, ok := files[name]
	if !ok {
		t.Fatalf("%s not in bundle: %v", name, keys(files))
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
	src := bundleFile(t, rt(t, map[string]config.ServerSpec{
		"master": {Type: "cax11", Region: "nbg1", Role: "master"},
	}, nil), "backend.tf")
	body := hcltest.ParseValid(t, src, "backend.tf")

	tf := hcltest.FindBlock(body, "terraform")
	if tf == nil {
		t.Fatal("missing terraform { } block in backend.tf")
	}
	if hcltest.FindBlock(tf.Body, "backend", "s3") != nil {
		t.Errorf("backend should NOT be present when rt.Backend is nil")
	}
}

func TestCompile_BackendBlockEmitsResolvedCreds(t *testing.T) {
	src := bundleFile(t, rt(t, map[string]config.ServerSpec{
		"master": {Type: "cax11", Region: "nbg1", Role: "master"},
	}, &state.Backend{
		Bucket:    "nvoi-hello-dev-tfstate",
		Endpoint:  "https://acct.r2.example.com",
		Region:    "auto",
		AccessKey: "AKIATEST",
		SecretKey: "secrettest",
	}), "backend.tf")
	body := hcltest.ParseValid(t, src, "backend.tf")

	tf := hcltest.FindBlock(body, "terraform")
	if tf == nil {
		t.Fatal("missing terraform { } block in backend.tf")
	}
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

// backend.tf aggregates required_providers across every active
// emitter. This locks the rule "exactly one terraform meta-block per
// module" — terraform errors at init if multiple required_providers
// blocks exist.
func TestCompile_BackendTF_AggregatesRequiredProviders(t *testing.T) {
	src := bundleFile(t, rt(t, map[string]config.ServerSpec{
		"master": {Type: "cax11", Region: "nbg1", Role: "master"},
	}, nil), "backend.tf")
	body := hcltest.ParseValid(t, src, "backend.tf")

	tf := hcltest.FindBlock(body, "terraform")
	if tf == nil {
		t.Fatal("backend.tf missing top-level terraform block")
	}
	rp := hcltest.FindBlock(tf.Body, "required_providers")
	if rp == nil {
		t.Fatal("backend.tf missing required_providers block")
	}
	// hcloud is active (infra: hetzner) — must appear with the
	// version pin from hetzner emitter's Provider().
	hcl := string(src)
	for _, want := range []string{
		`hcloud = `,
		`"hetznercloud/hcloud"`,
		`"~> 1.48"`,
	} {
		if !strings.Contains(hcl, want) {
			t.Errorf("backend.tf missing %q\n--- output ---\n%s", want, hcl)
		}
	}
}
