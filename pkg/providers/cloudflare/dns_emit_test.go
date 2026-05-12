package cloudflare

import (
	"strings"
	"testing"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/runtime"
)

func baseCfg() *config.Config {
	return &config.Config{
		App:       "hello",
		Env:       "dev",
		Providers: config.Providers{Infra: "hetzner", DNS: "cloudflare"},
		SSHKey:    "/tmp/x.pub",
		Servers: map[string]config.ServerSpec{
			"master": {Type: "cax11", Region: "nbg1", Role: "master"},
		},
		Services: map[string]config.ServiceSpec{
			"web": {Image: "nvoi/web", Port: 8080},
		},
	}
}

func TestEmitDNS_RequiresZoneID(t *testing.T) {
	t.Setenv("CF_ZONE_ID", "")
	t.Setenv("CF_ZONE", "nvoi.to")
	cfg := baseCfg()
	cfg.Domains = map[string][]string{"web": {"www.nvoi.to"}}

	_, err := DNSEmitter{}.EmitDNS(&runtime.Runtime{
		Cfg: cfg,
		Providers: runtime.ProviderInputs{
			Cloudflare: &runtime.CloudflareInputs{Zone: "nvoi.to"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "CF_ZONE_ID") {
		t.Errorf("expected CF_ZONE_ID error, got %v", err)
	}
}

func TestEmitDNS_RequiresZone(t *testing.T) {
	t.Setenv("CF_ZONE_ID", "abc123")
	t.Setenv("CF_ZONE", "")
	cfg := baseCfg()
	cfg.Domains = map[string][]string{"web": {"www.nvoi.to"}}

	_, err := DNSEmitter{}.EmitDNS(&runtime.Runtime{
		Cfg: cfg,
		Providers: runtime.ProviderInputs{
			Cloudflare: &runtime.CloudflareInputs{ZoneID: "abc123"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "CF_ZONE") {
		t.Errorf("expected CF_ZONE error, got %v", err)
	}
}

func TestEmitDNS_RecordsForEachDomain(t *testing.T) {
	t.Setenv("CF_ZONE_ID", "zone123")
	t.Setenv("CF_ZONE", "nvoi.to")
	cfg := baseCfg()
	cfg.Domains = map[string][]string{
		"web": {"www.nvoi.to", "nvoi.to"}, // www + apex
	}

	out, err := DNSEmitter{}.EmitDNS(&runtime.Runtime{
		Cfg: cfg,
		Providers: runtime.ProviderInputs{
			Cloudflare: &runtime.CloudflareInputs{ZoneID: "zone123", Zone: "nvoi.to"},
		},
	})
	if err != nil {
		t.Fatalf("EmitDNS: %v", err)
	}
	hcl := string(out)

	for _, want := range []string{
		// Provider-config block lives here; required_providers +
		// version pin live in the consolidated backend.tf rendered by
		// internal/compile.
		`provider "cloudflare" {}`,
		`local.cloudflare_zone_id`,
		`"zone123"`,
		`resource "cloudflare_record" "web_www_nvoi_to"`,
		`resource "cloudflare_record" "web_nvoi_to"`,
		`name    = "www"`,
		`name    = "@"`,
		`type    = "A"`,
		`content = hcloud_server.master.ipv4_address`, // raw HCL ref, NOT quoted
		`proxied = false`,
	} {
		if !strings.Contains(hcl, want) {
			t.Errorf("HCL missing %q\n--- output ---\n%s", want, hcl)
		}
	}
	// Negative: the meta-block (required_providers / backend) lives
	// in compile-emitted backend.tf, not in per-provider files.
	// Match an actual block opener, not the substring (the template's
	// own comment legitimately mentions the word).
	if strings.Contains(hcl, "required_providers {") {
		t.Errorf("required_providers block must NOT live in cloudflare-dns.tf:\n%s", hcl)
	}
}

func TestEmitDNS_NoDomains_StillRendersProviderBlock(t *testing.T) {
	t.Setenv("CF_ZONE_ID", "zone123")
	t.Setenv("CF_ZONE", "nvoi.to")
	cfg := baseCfg()

	out, err := DNSEmitter{}.EmitDNS(&runtime.Runtime{
		Cfg: cfg,
		Providers: runtime.ProviderInputs{
			Cloudflare: &runtime.CloudflareInputs{ZoneID: "zone123", Zone: "nvoi.to"},
		},
	})
	if err != nil {
		t.Fatalf("EmitDNS: %v", err)
	}
	hcl := string(out)
	if !strings.Contains(hcl, `provider "cloudflare" {}`) {
		t.Errorf("provider block missing: %s", hcl)
	}
	if strings.Contains(hcl, `resource "cloudflare_record"`) {
		t.Errorf("no domains → no records expected: %s", hcl)
	}
}

// ── tunnel mode (providers.ingress: cloudflare) ──────────────────────

func tunnelCfg() *config.Config {
	c := baseCfg()
	c.Providers.Ingress = config.IngressCloudflare
	c.Services = map[string]config.ServiceSpec{
		"app": {Image: "ghcr.io/me/app:latest", Port: 8080},
		"api": {Image: "ghcr.io/me/api:latest", Port: 3000},
	}
	c.Domains = map[string][]string{
		"app": {"app.nvoi.to", "www.nvoi.to"},
		"api": {"api.nvoi.to"},
	}
	return c
}

func TestEmitDNS_TunnelMode_HappyPath(t *testing.T) {
	cfg := tunnelCfg()
	out, err := DNSEmitter{}.EmitDNS(&runtime.Runtime{
		Cfg: cfg,
		Providers: runtime.ProviderInputs{
			Cloudflare: &runtime.CloudflareInputs{
				ZoneID: "zone123", Zone: "nvoi.to", AccountID: "acc456",
				TunnelSecret: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
			},
		},
	})
	if err != nil {
		t.Fatalf("EmitDNS: %v", err)
	}
	hcl := string(out)

	for _, want := range []string{
		// Provider + locals
		`provider "cloudflare" {}`,
		`cloudflare_account_id = "acc456"`,
		`cloudflare_zone_id    = "zone123"`,
		// Tunnel resources (named after app+env from baseCfg = "hello-dev")
		`tunnel_name           = "nvoi-hello-dev"`,
		`resource "cloudflare_zero_trust_tunnel_cloudflared" "main"`,
		`secret     = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="`,
		`resource "cloudflare_zero_trust_tunnel_cloudflared_config" "main"`,
		`tunnel_id  = cloudflare_zero_trust_tunnel_cloudflared.main.id`,
		// Ingress rules — one per (service, domain) pair, alphabetic
		// by service via utils.SortedKeys. ALL rules upstream at the
		// in-cluster Traefik Service; Traefik does host-based routing
		// + L7 load balancing. cloudflared preserves Host header by
		// default.
		`hostname = "api.nvoi.to"`,
		`service  = "http://traefik.kube-system.svc.cluster.local:80"`,
		`hostname = "app.nvoi.to"`,
		`hostname = "www.nvoi.to"`,
		// Catch-all
		`service = "http_status:404"`,
		// CNAME records pointing at tunnel cname
		`type    = "CNAME"`,
		`content = "${cloudflare_zero_trust_tunnel_cloudflared.main.cname}"`,
		`proxied = true`,
		// Sensitive tunnel output
		`output "tunnel"`,
		`sensitive = true`,
	} {
		if !strings.Contains(hcl, want) {
			t.Errorf("tunnel HCL missing %q\n--- output ---\n%s", want, hcl)
		}
	}

	// Traefik-only artifacts must NOT appear in tunnel HCL.
	for _, banned := range []string{
		`type    = "A"`,
		`hcloud_server.master.ipv4_address`,
		`proxied = false`,
	} {
		if strings.Contains(hcl, banned) {
			t.Errorf("tunnel HCL contains Traefik-only token %q:\n%s", banned, hcl)
		}
	}
}

func TestEmitDNS_TunnelMode_MissingAccountID(t *testing.T) {
	cfg := tunnelCfg()
	_, err := DNSEmitter{}.EmitDNS(&runtime.Runtime{
		Cfg: cfg,
		Providers: runtime.ProviderInputs{
			Cloudflare: &runtime.CloudflareInputs{ZoneID: "zone123", Zone: "nvoi.to"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "account_id required") {
		t.Errorf("expected account_id error, got %v", err)
	}
}

func TestEmitDNS_TunnelMode_MissingTunnelSecret(t *testing.T) {
	cfg := tunnelCfg()
	_, err := DNSEmitter{}.EmitDNS(&runtime.Runtime{
		Cfg: cfg,
		Providers: runtime.ProviderInputs{
			Cloudflare: &runtime.CloudflareInputs{
				ZoneID: "zone123", Zone: "nvoi.to", AccountID: "acc456",
				// TunnelSecret deliberately blank — boundary normally
				// catches this, but the emitter's defensive check keeps
				// the failure local rather than letting an empty literal
				// land in HCL.
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "tunnel_secret required") {
		t.Errorf("expected tunnel_secret error, got %v", err)
	}
}

func TestEmitDNS_TunnelMode_UpstreamIsTraefik(t *testing.T) {
	cfg := tunnelCfg()
	// Per-service ports are irrelevant in tunnel mode now — every
	// ingress rule points at Traefik. Override one to confirm it
	// is NOT threaded through.
	cfg.Services["app"] = config.ServiceSpec{Image: "ghcr.io/me/app:latest", Port: 9999}
	out, err := DNSEmitter{}.EmitDNS(&runtime.Runtime{
		Cfg: cfg,
		Providers: runtime.ProviderInputs{
			Cloudflare: &runtime.CloudflareInputs{
				ZoneID: "zone123", Zone: "nvoi.to", AccountID: "acc456",
				TunnelSecret: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
			},
		},
	})
	if err != nil {
		t.Fatalf("EmitDNS: %v", err)
	}
	hcl := string(out)
	if !strings.Contains(hcl, `service  = "http://traefik.kube-system.svc.cluster.local:80"`) {
		t.Errorf("tunnel upstream is not Traefik\n--- output ---\n%s", hcl)
	}
	// Negative — per-service ports must NOT leak into tunnel upstreams.
	if strings.Contains(hcl, `:9999"`) || strings.Contains(hcl, `:8080"`) {
		t.Errorf("per-service port leaked into tunnel HCL (should always be :80 via Traefik)\n--- output ---\n%s", hcl)
	}
}

func TestRecordNameFor(t *testing.T) {
	cases := []struct {
		domain, zone, want string
	}{
		{"nvoi.to", "nvoi.to", "@"},
		{"www.nvoi.to", "nvoi.to", "www"},
		{"api.www.nvoi.to", "nvoi.to", "api.www"},
		{"other.com", "nvoi.to", "other.com"}, // outside zone — pass through
	}
	for _, tc := range cases {
		if got := recordNameFor(tc.domain, tc.zone); got != tc.want {
			t.Errorf("recordNameFor(%q, %q) = %q, want %q", tc.domain, tc.zone, got, tc.want)
		}
	}
}

func TestSanitizeResourceName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"web", "web"},
		{"web_www.nvoi.to", "web_www_nvoi_to"},
		{"with-dash", "with_dash"},
		{"123starts-digit", "_123starts_digit"},
		{"", "_"},
	}
	for _, tc := range cases {
		if got := sanitizeResourceName(tc.in); got != tc.want {
			t.Errorf("sanitizeResourceName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
