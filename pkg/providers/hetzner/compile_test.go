package hetzner

import (
	"strings"
	"testing"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/internal/testutil/hcltest"
	"github.com/getnvoi/core/pkg/runtime"
)

// emitFor renders the hetzner template directly from EmitInfra so the
// tests stay scoped to the hetzner emitter — no DNS / bucket / compile
// dependencies. Cross-emitter integration belongs to
// pkg/internal/compile/compile_test.go.
func emitFor(t *testing.T, rt *runtime.Runtime) []byte {
	t.Helper()
	src, err := (emitter{}).EmitInfra(rt)
	if err != nil {
		t.Fatalf("EmitInfra: %v", err)
	}
	return src
}

// rtFor builds a minimal runtime for the hetzner emitter. `ha` toggles
// the new top-level `ha:` flag; servers must satisfy whatever count
// rules the test asserts. All-tunnel: no ingress mode toggle; per-test
// assertions cover firewall posture and LB emission.
func rtFor(t *testing.T, ha bool, servers map[string]config.ServerSpec) *runtime.Runtime {
	t.Helper()
	cfg := &config.Config{
		App:       "hello",
		Env:       "dev",
		Providers: config.Providers{Infra: "hetzner"},
		Servers:   servers,
		Services:  map[string]config.ServiceSpec{"web": {Image: "nginx", Port: 80}},
		Domains:   config.Domains{"web": {"www.nvoi.to"}},
		HA:        ha,
	}
	return &runtime.Runtime{
		Cfg:       cfg,
		SSHPubKey: []byte("ssh-ed25519 AAAA test"),
		WorkDir:   t.TempDir(),
	}
}

// All-tunnel: SSH (22) is the only public ingress. No firewall rule
// opens 80 or 443 to the internet, regardless of master count or HA.
func TestEmitInfra_NoPublic80443(t *testing.T) {
	src := emitFor(t, rtFor(t, false, map[string]config.ServerSpec{
		"master": {Type: "cax11", Region: "nbg1", Role: "master"},
	}))
	hcl := string(src)
	for _, port := range []string{`port       = "80"`, `port       = "443"`} {
		if strings.Contains(hcl, port) {
			t.Errorf("all-tunnel: firewall contains port rule %q\n--- output ---\n%s", port, hcl)
		}
	}
}

// ha: true → emit a private-only hcloud LB on 6443 fronting the master
// pool. api_endpoint.private resolves to the LB's private IP, NOT a
// master's private IP — workers + secondary masters join via the LB,
// which steers around dead masters in ~10s.
func TestEmitInfra_HA_EmitsPrivateLB(t *testing.T) {
	src := emitFor(t, rtFor(t, true, map[string]config.ServerSpec{
		"m1": {Type: "cax21", Region: "nbg1", Role: "master", Primary: true},
		"m2": {Type: "cax21", Region: "nbg1", Role: "master"},
		"m3": {Type: "cax21", Region: "nbg1", Role: "master"},
	}))
	body := hcltest.ParseValid(t, src, "hetzner.tf")

	// LB resources MUST be emitted.
	for _, name := range []string{
		"hcloud_load_balancer",
		"hcloud_load_balancer_network",
		"hcloud_load_balancer_target",
		"hcloud_load_balancer_service",
	} {
		if hcltest.FindBlock(body, "resource", name, "cp") == nil {
			t.Errorf("HA: missing resource %s.cp", name)
		}
	}

	hcl := string(src)
	// Public interface MUST be disabled — the LB is reachable only
	// from the private subnet. All-tunnel guarantee.
	if !strings.Contains(hcl, "enable_public_interface = false") {
		t.Errorf("HA: LB must disable public interface\n--- output ---\n%s", hcl)
	}
	// Only port 6443. No 80/443 services (cloudflared handles app ingress).
	if strings.Contains(hcl, "listen_port      = 80") || strings.Contains(hcl, "listen_port      = 443") {
		t.Errorf("HA: LB must not expose 80/443\n--- output ---\n%s", hcl)
	}
	// api_endpoint.private references the LB.
	if !strings.Contains(hcl, "private = hcloud_load_balancer_network.cp.ip") {
		t.Errorf("HA: api_endpoint.private must reference the LB private IP\n--- output ---\n%s", hcl)
	}
}

// ha: false (or unset) → no LB emitted; api_endpoint.private is the
// lone master's private IP via hcloud_server_network.
func TestEmitInfra_NoHA_NoLB(t *testing.T) {
	src := emitFor(t, rtFor(t, false, map[string]config.ServerSpec{
		"master": {Type: "cax11", Region: "nbg1", Role: "master"},
	}))
	body := hcltest.ParseValid(t, src, "hetzner.tf")

	for _, name := range []string{
		"hcloud_load_balancer",
		"hcloud_load_balancer_network",
		"hcloud_load_balancer_target",
		"hcloud_load_balancer_service",
	} {
		if hcltest.FindBlock(body, "resource", name, "cp") != nil {
			t.Errorf("non-HA: %s.cp should not be emitted", name)
		}
	}

	hcl := string(src)
	if !strings.Contains(hcl, "private = hcloud_server_network.master.ip") {
		t.Errorf("non-HA: api_endpoint.private must reference the master's hcloud_server_network\n--- output ---\n%s", hcl)
	}
}
