package cloudflare

import (
	"strings"
	"testing"

	"github.com/getnvoi/core/internal/config"
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

	_, err := DNSEmitter{}.EmitDNS(cfg)
	if err == nil || !strings.Contains(err.Error(), "CF_ZONE_ID") {
		t.Errorf("expected CF_ZONE_ID error, got %v", err)
	}
}

func TestEmitDNS_RequiresZone(t *testing.T) {
	t.Setenv("CF_ZONE_ID", "abc123")
	t.Setenv("CF_ZONE", "")
	cfg := baseCfg()
	cfg.Domains = map[string][]string{"web": {"www.nvoi.to"}}

	_, err := DNSEmitter{}.EmitDNS(cfg)
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

	out, err := DNSEmitter{}.EmitDNS(cfg)
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

	out, err := DNSEmitter{}.EmitDNS(cfg)
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
