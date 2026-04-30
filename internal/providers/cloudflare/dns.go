package cloudflare

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/template"

	"github.com/getnvoi/core/internal/compile"
	"github.com/getnvoi/core/internal/config"
	"github.com/getnvoi/core/internal/utils"
)

//go:embed templates/dns.tf.tmpl
var dnsTemplateFS embed.FS

var dnsTpl = template.Must(template.New("dns.tf.tmpl").
	Funcs(template.FuncMap{"hcl": strconv.Quote}).
	ParseFS(dnsTemplateFS, "templates/dns.tf.tmpl"))

// DNSEmitter renders Cloudflare DNS records as terraform HCL. One
// `cloudflare_record` per (service, domain) pair. Without
// providers.tunnel: A records pointing at the master's public IP
// (terraform-interpolated from hcloud_server.<primary>.ipv4_address).
// Tunnel mode (commit #7) flips these to CNAMEs to the tunnel edge.
//
// Stateless: reads cfg.Domains + cfg.Servers + the resolved zone ID
// (from CF_ZONE_ID env var). No I/O at emit time beyond env reads —
// the produced HCL is a pure transform of the input.
type DNSEmitter struct{}

// Provider declares the cloudflare terraform provider requirement.
// Aggregated by compile into the consolidated backend.tf alongside
// other active providers (hcloud, etc).
func (DNSEmitter) Provider() compile.ProviderRequirement {
	return compile.ProviderRequirement{
		Alias:   "cloudflare",
		Source:  "cloudflare/cloudflare",
		Version: "~> 4",
	}
}

// recordData drives the per-record block in dns.tf.tmpl. When
// Tunnel is true, the template emits a CNAME pointing at
// local.tunnel_cname (the tunnel emitter writes that local). When
// false, an A record with Target as the raw HCL expression
// (typically `hcloud_server.master.ipv4_address`, NOT a quoted
// string).
type recordData struct {
	ResourceName string // sanitized terraform resource name, unique
	Name         string // record name relative to zone (e.g. "www", "@")
	Target       string // raw HCL expression for A-record content; ignored when Tunnel
	Tunnel       bool   // emit CNAME → local.tunnel_cname instead of A
}

type dnsTemplateData struct {
	ZoneID  string
	Records []recordData
}

// EmitDNS renders the cloudflare-dns.tf bytes for cfg.Domains.
// Reads CF_ZONE env var to derive record names relative to the zone
// (e.g. www.nvoi.to with zone nvoi.to → name "www"; nvoi.to → "@").
//
// The CF_ZONE_ID env var resolves the zone in Cloudflare's API. It's
// read at emit time (an exception to "internal/ never reads env"
// because the cloudflare provider package is, by design, the
// integration boundary with Cloudflare's environment-driven
// terraform provider — same pattern as `provider "cloudflare" {}`
// auto-reading CLOUDFLARE_API_TOKEN).
func (DNSEmitter) EmitDNS(cfg *config.Config) ([]byte, error) {
	zoneID := os.Getenv("CF_ZONE_ID")
	if zoneID == "" {
		return nil, fmt.Errorf("cloudflare dns: CF_ZONE_ID required")
	}
	zone := os.Getenv("CF_ZONE")
	if zone == "" {
		return nil, fmt.Errorf("cloudflare dns: CF_ZONE required (e.g. nvoi.to)")
	}

	tunnelMode := cfg.Providers.Tunnel != ""

	// In Caddy mode the A target is the master's public IPv4. In
	// tunnel mode the template flips to CNAME → local.tunnel_cname
	// (which the active tunnel emitter declares); the Target string
	// here is unused on that path but we still set it so the
	// template's else-branch is well-defined for assertion.
	var aTarget string
	if !tunnelMode {
		primary := cfg.PrimaryMaster()
		if primary == "" {
			return nil, fmt.Errorf("cloudflare dns: no master in servers (validator should have caught)")
		}
		aTarget = fmt.Sprintf("hcloud_server.%s.ipv4_address", primary)
	}

	// Per-deploy uniqueness: combine service + sanitized hostname so
	// re-running with the same YAML produces a stable resource address.
	records := make([]recordData, 0)
	for _, svcName := range utils.SortedKeys(cfg.Domains) {
		for _, host := range cfg.Domains[svcName] {
			records = append(records, recordData{
				ResourceName: sanitizeResourceName(svcName + "_" + host),
				Name:         recordNameFor(host, zone),
				Target:       aTarget,
				Tunnel:       tunnelMode,
			})
		}
	}

	var buf bytes.Buffer
	if err := dnsTpl.Execute(&buf, dnsTemplateData{
		ZoneID:  zoneID,
		Records: records,
	}); err != nil {
		return nil, fmt.Errorf("render cloudflare-dns.tf: %w", err)
	}
	return buf.Bytes(), nil
}

// recordNameFor returns the relative record name for a domain in a
// zone. Apex (zone == domain) → "@" (Cloudflare's convention).
// Subdomain → strip the trailing ".<zone>".
func recordNameFor(domain, zone string) string {
	if domain == zone {
		return "@"
	}
	if strings.HasSuffix(domain, "."+zone) {
		return strings.TrimSuffix(domain, "."+zone)
	}
	// Domain isn't in the zone — terraform will reject this with a
	// clear "record name not in zone" at apply time. We could fail
	// earlier here but the apply-time error is precise enough that
	// the extra validation isn't worth the surface.
	return domain
}

// sanitizeResourceName turns a free-form string into a valid
// terraform resource address: letters / digits / underscores; first
// char must be a letter or underscore.
func sanitizeResourceName(s string) string {
	var b strings.Builder
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			if i == 0 {
				b.WriteRune('_')
			}
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if out == "" {
		return "_"
	}
	return out
}

