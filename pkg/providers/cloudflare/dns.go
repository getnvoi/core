package cloudflare

import (
	"bytes"
	"embed"
	"fmt"
	"strconv"
	"strings"
	"text/template"

	"github.com/getnvoi/core/pkg/internal/utils"
	"github.com/getnvoi/core/pkg/naming"
	"github.com/getnvoi/core/pkg/runtime"
)

//go:embed templates/tunnel.tf.tmpl
var dnsTemplateFS embed.FS

var tunnelTpl = template.Must(template.New("tunnel.tf.tmpl").
	Funcs(template.FuncMap{"hcl": strconv.Quote}).
	ParseFS(dnsTemplateFS, "templates/tunnel.tf.tmpl"))

// TerraformProviderSource is the registry coordinates compile aggregates
// into backend.tf's required_providers block. Direct call surface — no
// interface, since cloudflare is the sole DNS+tunnel emitter.
const (
	TerraformProviderAlias   = "cloudflare"
	TerraformProviderSource  = "cloudflare/cloudflare"
	TerraformProviderVersion = "~> 4"
)

// recordData drives the per-record CNAME block in tunnel.tf.tmpl.
type recordData struct {
	ResourceName string // sanitized terraform resource name, unique per (service, host)
	Name         string // record name relative to zone (e.g. "www", "@")
}

// tunnelRouteData is one cloudflared ingress rule. cloudflared matches
// Host headers against .Hostname and forwards to .Service, preserving
// the original Host header by default. .Service is uniformly
// naming.TraefikInClusterURL — Traefik then routes by Host header to
// the per-workload Service via the matching Ingress, giving per-request
// L7 load balancing across pod EndpointSlices. We deliberately do NOT
// upstream cloudflared directly at the workload's ClusterIP: that
// would be L4 kube-proxy connection-balancing, and cloudflared's
// long-lived HTTP/2 connections would stick to a single pod.
type tunnelRouteData struct {
	Hostname string
	Service  string
}

type tunnelTemplateData struct {
	AccountID    string
	ZoneID       string
	TunnelName   string
	TunnelSecret string // operator-supplied (CF_TUNNEL_SECRET); baked as a literal
	Routes       []tunnelRouteData
	Records      []recordData
}

// EmitTunnelDNS renders the cloudflared tunnel resource, its ingress
// configuration, the per-domain CNAMEs, and the `tunnel` output
// (id / cname / token). One route per (service, domain) pair, pointing
// at the in-cluster Traefik Service so per-request L7 routing applies.
//
// All-tunnel mode is the only mode: this function fires whenever
// cfg.Domains is non-empty. compile.Compile calls it directly — no
// interface, no registry indirection.
//
// The namespace and port references duplicate constants from
// pkg/workload (default namespace, service.Port). The duplication
// here is intentional — pkg/providers/cloudflare cannot import
// pkg/workload without creating a cycle (pkg/workload would import
// pkg/internal/compile which imports pkg/providers/*).
func EmitTunnelDNS(rt *runtime.Runtime) ([]byte, error) {
	cfg := rt.Cfg
	if rt.Providers.Cloudflare == nil {
		return nil, fmt.Errorf("cloudflare tunnel: provider inputs required")
	}
	zoneID := rt.Providers.Cloudflare.ZoneID
	if zoneID == "" {
		return nil, fmt.Errorf("cloudflare tunnel: CF_ZONE_ID required")
	}
	zone := rt.Providers.Cloudflare.Zone
	if zone == "" {
		return nil, fmt.Errorf("cloudflare tunnel: CF_ZONE required (e.g. nvoi.to)")
	}
	accountID := rt.Providers.Cloudflare.AccountID
	if accountID == "" {
		return nil, fmt.Errorf("cloudflare tunnel: account_id required (CF_ACCOUNT_ID)")
	}
	tunnelSecret := rt.Providers.Cloudflare.TunnelSecret
	if tunnelSecret == "" {
		// Boundary validation should have caught this; defensive.
		return nil, fmt.Errorf("cloudflare tunnel: tunnel_secret required (CF_TUNNEL_SECRET)")
	}

	tunnelName := fmt.Sprintf("nvoi-%s-%s", cfg.App, cfg.Env)

	// Every hostname routes to Traefik via the same upstream URL; the
	// Host header (preserved by cloudflared by default) carries the
	// hostname through and Traefik's Ingress routing picks the backend.
	routes := make([]tunnelRouteData, 0)
	records := make([]recordData, 0)
	for _, svcName := range utils.SortedKeys(cfg.Domains) {
		if _, ok := cfg.Services[svcName]; !ok {
			// Validator guarantees this — defensive only.
			return nil, fmt.Errorf("cloudflare tunnel: domain key %q is not a declared service", svcName)
		}
		for _, host := range cfg.Domains[svcName] {
			routes = append(routes, tunnelRouteData{
				Hostname: host,
				Service:  naming.TraefikInClusterURL,
			})
			records = append(records, recordData{
				ResourceName: sanitizeResourceName(svcName + "_" + host),
				Name:         recordNameFor(host, zone),
			})
		}
	}

	var buf bytes.Buffer
	if err := tunnelTpl.Execute(&buf, tunnelTemplateData{
		AccountID:    accountID,
		ZoneID:       zoneID,
		TunnelName:   tunnelName,
		TunnelSecret: tunnelSecret,
		Routes:       routes,
		Records:      records,
	}); err != nil {
		return nil, fmt.Errorf("render tunnel.tf: %w", err)
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
