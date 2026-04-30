package cloudflare

import (
	"strings"
	"testing"
)

func TestEmitTunnel_RequiresAccountID(t *testing.T) {
	t.Setenv("CF_ACCOUNT_ID", "")
	cfg := baseCfg()
	cfg.Providers.Tunnel = "cloudflare"
	cfg.Domains = map[string][]string{"web": {"www.nvoi.to"}}

	if _, err := (TunnelEmitter{}).EmitTunnel(cfg); err == nil || !strings.Contains(err.Error(), "CF_ACCOUNT_ID") {
		t.Errorf("expected CF_ACCOUNT_ID error, got %v", err)
	}
}

func TestEmitTunnel_RendersTunnelResourcesAndLocals(t *testing.T) {
	t.Setenv("CF_ACCOUNT_ID", "acct-id-xyz")
	cfg := baseCfg()
	cfg.Providers.Tunnel = "cloudflare"
	cfg.Domains = map[string][]string{"web": {"www.nvoi.to"}}

	out, err := TunnelEmitter{}.EmitTunnel(cfg)
	if err != nil {
		t.Fatalf("EmitTunnel: %v", err)
	}
	hcl := string(out)

	for _, want := range []string{
		`resource "cloudflare_zero_trust_tunnel_cloudflared" "main"`,
		`resource "cloudflare_zero_trust_tunnel_cloudflared_config" "main"`,
		`account_id = "acct-id-xyz"`,
		`name       = "nvoi-hello-dev"`, // naming.Prefix(app, env)
		`hostname = "www.nvoi.to"`,
		`service  = "http://web.default.svc.cluster.local:8080"`,
		// Catch-all 404 — anything not in domains: terminates at the edge.
		`http_status:404`,
		// Locals the DNS emitter consumes — cross-emitter contract.
		`locals {`,
		`tunnel_cname   = "${cloudflare_zero_trust_tunnel_cloudflared.main.id}.cfargotunnel.com"`,
		`tunnel_proxied = true`,
		// One output: the agent token. Everything else stays internal
		// to tf via local.* — nvoi only crosses the tf→kube boundary
		// for the token (it's the one value the workload phase needs
		// that doesn't already live in kube).
		`output "tunnel_token"`,
		`sensitive = true`,
	} {
		if !strings.Contains(hcl, want) {
			t.Errorf("HCL missing %q\n--- output ---\n%s", want, hcl)
		}
	}

	// Negative: the cname / proxied values stay LOCAL to tf. nvoi must
	// NOT receive them through `terraform output` — that path was the
	// imperative-DNS-flip artefact, gone with the cleanup. Surfacing
	// them as outputs again would re-introduce the cross-orchestrator
	// drift bug we just retired.
	for _, banned := range []string{
		`output "tunnel_cname"`,
		`output "tunnel_proxied"`,
		`output "tunnel_id"`,
	} {
		if strings.Contains(hcl, banned) {
			t.Errorf("HCL must not expose %q as a tf output (cname/proxied stay tf-internal via local.*):\n%s", banned, hcl)
		}
	}
}

func TestAgentWorkloads_RequiresToken(t *testing.T) {
	if _, err := (TunnelEmitter{}).AgentWorkloads(""); err == nil {
		t.Error("expected error for empty token")
	}
}

func TestAgentWorkloads_BuildsSecretAndDeployment(t *testing.T) {
	wls, err := TunnelEmitter{}.AgentWorkloads("tunnel-token-xyz")
	if err != nil {
		t.Fatalf("AgentWorkloads: %v", err)
	}
	if len(wls) != 2 {
		t.Fatalf("workloads: got %d want 2 (Secret + Deployment)", len(wls))
	}
	kinds := map[string]bool{}
	for _, w := range wls {
		kinds[w.Kind] = true
		if w.Name != "cloudflared" {
			t.Errorf("workload %s name: got %q want cloudflared", w.Kind, w.Name)
		}
	}
	if !kinds["Secret"] || !kinds["Deployment"] {
		t.Errorf("missing kinds: %v", kinds)
	}
}

// DNS emitter must flip to CNAME → local.tunnel_cname when tunnel
// mode is on. Locks the cross-emitter coupling we rely on.
func TestEmitDNS_TunnelModeFlipsToCNAME(t *testing.T) {
	t.Setenv("CF_ZONE_ID", "zone123")
	t.Setenv("CF_ZONE", "nvoi.to")
	cfg := baseCfg()
	cfg.Providers.Tunnel = "cloudflare"
	cfg.Domains = map[string][]string{"web": {"www.nvoi.to"}}

	out, err := DNSEmitter{}.EmitDNS(cfg)
	if err != nil {
		t.Fatalf("EmitDNS: %v", err)
	}
	hcl := string(out)
	for _, want := range []string{
		`type    = "CNAME"`,
		`content = local.tunnel_cname`,
		`proxied = local.tunnel_proxied`,
	} {
		if !strings.Contains(hcl, want) {
			t.Errorf("tunnel-mode HCL missing %q\n--- output ---\n%s", want, hcl)
		}
	}
	// Negative: A record + master IP target must NOT appear in
	// tunnel mode.
	if strings.Contains(hcl, "hcloud_server.master.ipv4_address") {
		t.Error("tunnel mode must not reference master IP for DNS")
	}
}
