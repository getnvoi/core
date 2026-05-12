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

// rtFor builds a minimal runtime for the hetzner emitter under a
// given ingress mode + server set. Domains: + Services: are populated
// when ingress=cloudflare (validator prerequisites), but the emitter
// only consumes them indirectly via the templateData flags.
func rtFor(t *testing.T, ingressMode string, servers map[string]config.ServerSpec) *runtime.Runtime {
	t.Helper()
	cfg := &config.Config{
		App:       "hello",
		Env:       "dev",
		Providers: config.Providers{Infra: "hetzner"},
		Servers:   servers,
	}
	switch ingressMode {
	case config.IngressCloudflare:
		cfg.Providers.DNS = "cloudflare"
		cfg.Providers.Ingress = config.IngressCloudflare
		cfg.Services = map[string]config.ServiceSpec{"web": {Image: "nginx", Port: 80}}
		cfg.Domains = config.Domains{"web": {"www.nvoi.to"}}
	case config.IngressTraefik:
		// Traefik mode WITH domains — the regression baseline for
		// public 80/443 emission.
		cfg.Providers.DNS = "cloudflare"
		cfg.Services = map[string]config.ServiceSpec{"web": {Image: "nginx", Port: 80}}
		cfg.Domains = config.Domains{"web": {"www.nvoi.to"}}
	}
	return &runtime.Runtime{
		Cfg:       cfg,
		SSHPubKey: []byte("ssh-ed25519 AAAA test"),
		WorkDir:   t.TempDir(),
	}
}

// TunnelMode_NoPublic80443 — the InfraEmitter contract requires
// that no rule in `hcloud_firewall.default` opens 80 or 443 to the
// public internet when ingress mode is cloudflare. Other implementers
// adopting the contract on new providers must produce the equivalent
// posture for their own firewall HCL.
func TestEmitInfra_TunnelMode_SingleMaster_NoPublic80443(t *testing.T) {
	src := emitFor(t, rtFor(t, config.IngressCloudflare, map[string]config.ServerSpec{
		"master": {Type: "cax11", Region: "nbg1", Role: "master"},
	}))
	hcl := string(src)
	// The Traefik-mode public-HTTP rule pattern would render as
	// `port = "80"` / `"443"`. Absence is the strongest assertion.
	for _, port := range []string{`port       = "80"`, `port       = "443"`} {
		if strings.Contains(hcl, port) {
			t.Errorf("tunnel mode: firewall contains port rule %q\n--- output ---\n%s", port, hcl)
		}
	}
}

// TunnelMode_HA_KubeVIP — HA + tunnel drops the hcloud LB entirely.
// kube-vip on the private subnet (ARP-claimed VIP literal) carries
// the k3s API on 6443. Every hcloud_load_balancer* resource must be
// absent; api_endpoint.private emits the VIP literal so workers and
// secondary masters dial it directly during install.
func TestEmitInfra_TunnelMode_HA_NoLB_VIPOnly(t *testing.T) {
	src := emitFor(t, rtFor(t, config.IngressCloudflare, map[string]config.ServerSpec{
		"m1": {Type: "cax21", Region: "nbg1", Role: "master", Primary: true},
		"m2": {Type: "cax21", Region: "nbg1", Role: "master"},
	}))
	body := hcltest.ParseValid(t, src, "hetzner.tf")

	for _, name := range []string{
		"hcloud_load_balancer",
		"hcloud_load_balancer_network",
		"hcloud_load_balancer_target",
		"hcloud_load_balancer_service",
	} {
		if hcltest.FindBlock(body, "resource", name, "cp") != nil ||
			hcltest.FindBlock(body, "resource", name, "http") != nil ||
			hcltest.FindBlock(body, "resource", name, "https") != nil {
			t.Errorf("HA + tunnel: %s should not be emitted (kube-vip replaces the LB)", name)
		}
	}

	hcl := string(src)
	// VIP literal lands as the private API endpoint. /24 default subnet
	// → broadcast(.255) - 5 = .250.
	if !strings.Contains(hcl, `private = "10.0.1.250"`) {
		t.Errorf("HA + tunnel: api_endpoint.private must be the VIP literal\n--- output ---\n%s", hcl)
	}
	for _, port := range []string{`port       = "80"`, `port       = "443"`} {
		if strings.Contains(hcl, port) {
			t.Errorf("HA + tunnel: firewall contains %q (expected suppressed)\n--- output ---\n%s", port, hcl)
		}
	}
}

// TraefikMode_StillEmitsPublicHTTP — regression. The tunnel-mode
// suppression must not leak into the Traefik path: with
// ingress unset / explicitly traefik and domains: declared, public
// 80/443 rules still emit.
func TestEmitInfra_TraefikMode_StillEmitsPublicHTTP(t *testing.T) {
	src := emitFor(t, rtFor(t, config.IngressTraefik, map[string]config.ServerSpec{
		"master": {Type: "cax11", Region: "nbg1", Role: "master"},
	}))
	hcl := string(src)
	for _, want := range []string{`port       = "80"`, `port       = "443"`} {
		if !strings.Contains(hcl, want) {
			t.Errorf("Traefik mode with domains: expected %q\n--- output ---\n%s", want, hcl)
		}
	}
}
