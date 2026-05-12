package hetzner

import (
	"bytes"
	"embed"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/getnvoi/core/pkg/internal/cloudinit"
	"github.com/getnvoi/core/pkg/internal/compile"
	"github.com/getnvoi/core/pkg/naming"
	"github.com/getnvoi/core/pkg/runtime"
)

const (
	defaultImage  = "ubuntu-24.04"
	networkCIDR   = "10.0.0.0/16"
	networkSubnet = "10.0.1.0/24"
)

// locationToZone mirrors nvoi's pkg/provider/hetzner/network.go map.
// Hetzner Cloud requires a network_zone for the subnet + LB, derived
// from any server's location.
var locationToZone = map[string]string{
	"fsn1": "eu-central",
	"nbg1": "eu-central",
	"hel1": "eu-central",
	"ash":  "us-east",
	"hil":  "us-west",
	"sin":  "ap-southeast",
}

func zoneForLocation(loc string) string {
	if z, ok := locationToZone[loc]; ok {
		return z
	}
	return "eu-central"
}

//go:embed templates/hetzner.tf.tmpl
var templateFS embed.FS

// hclQuote renders a Go string as a double-quoted HCL string literal.
// Reused by every `{{ X | hcl }}` invocation in the template — keeps
// escape semantics identical to JSON, which HCL accepts.
func hclQuote(s string) string { return strconv.Quote(s) }

// Template name must match the file basename — ParseFS registers each
// parsed file under its basename, and Execute on the root template
// uses the root's name.
var tpl = template.Must(template.New("hetzner.tf.tmpl").
	Funcs(template.FuncMap{"hcl": hclQuote}).
	ParseFS(templateFS, "templates/hetzner.tf.tmpl"))

type emitter struct{}

// ServerResourceType is the terraform resource type the hetzner
// template uses for k3s nodes. Used by detach to filter plans.
func (emitter) ServerResourceType() string { return "hcloud_server" }

// hetznerReservedServerNames are YAML keys an operator must NOT pick
// for a server, because the hetzner template uses these names for its
// own non-server resources (network, LB, subnet). Registered into the
// providers package via init() so the generic validator can query it
// without importing this package.
var hetznerReservedServerNames = map[string]bool{
	"default": true, // hcloud_network.default, hcloud_firewall.default, hcloud_network_subnet.default
	"cp":      true, // hcloud_load_balancer.cp + lb_network/target/service
}

// Providers declares the terraform providers this emitter's HCL
// references. Aggregated by compile into the consolidated backend.tf
// so the module ends up with exactly one `required_providers` block.
func (emitter) Providers() []compile.ProviderRequirement {
	return []compile.ProviderRequirement{{
		Alias:   "hcloud",
		Source:  "hetznercloud/hcloud",
		Version: "~> 1.48",
	}}
}

// templateData is the shape the template consumes. Built from rt.Cfg
// + rt.SSHPubKey; pure transformation.
type templateData struct {
	App           string
	Env           string
	Prefix        string
	NetworkCIDR   string
	NetworkSubnet string
	NetworkZone   string
	Servers       []serverData
	HA            bool
	PrimaryMaster string // used by outputs in non-HA mode (any master Key works; we pick the first)

	// PublicHTTPIngress opens hcloud_firewall.default for 80/443 from
	// 0.0.0.0/0 — the single-master path where the master IS the public
	// face. True iff domains is declared AND HA is false AND ingress
	// mode is traefik. In tunnel mode there is NO public HTTP path
	// regardless of domains.
	PublicHTTPIngress bool

	// LBHTTPIngress opens hcloud_firewall.default for 80/443 from the
	// private subnet only AND emits 80/443 services on the public LB.
	// True iff domains is declared AND HA AND ingress mode is traefik.
	// In tunnel mode there is NO public HTTP path; in KubeVIP mode
	// (tunnel + HA) the LB is dropped entirely (see EmitLB) so this
	// flag stays false there too.
	LBHTTPIngress bool

	// VIP is the private-subnet IP kube-vip ARP-claims. Non-empty iff
	// kube-vip is active (tunnel + HA). Presence is the toggle:
	//   .VIP != ""  →  no LB block; api_endpoint.private = VIP literal
	//   .VIP == "" && .HA   →  LB block; api_endpoint.private = LB IP
	//   .VIP == "" && !.HA  →  no LB; api_endpoint.private = master priv
	// Single field; the template branches on (.VIP, .HA) in that order.
	VIP string
}

type serverData struct {
	Key      string // YAML key — used as terraform resource name
	Hostname string
	Type     string
	Region   string
	Role     string
	Image    string
	UserData string
}

// EmitInfra renders hetzner.tf from templateData. Pure: no disk, no env.
// SSH public key arrives already-resolved on rt.SSHPubKey.
//
// HA is auto-detected: ≥2 servers with role=master triggers the
// hcloud_load_balancer block. Targets enroll via label selector so
// scaling masters up or down is just a YAML edit + redeploy.
func (emitter) EmitInfra(rt *runtime.Runtime) ([]byte, error) {
	cfg := rt.Cfg
	pubKey := strings.TrimSpace(string(rt.SSHPubKey))
	if pubKey == "" {
		return nil, fmt.Errorf("ssh public key is empty (programming error: runtime.Inputs.SSHPubKey unset)")
	}

	// Sorted iteration → deterministic output.
	keys := make([]string, 0, len(cfg.Servers))
	for k := range cfg.Servers {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	servers := make([]serverData, 0, len(keys))
	masters := make([]string, 0)
	zone := ""
	for _, k := range keys {
		s := cfg.Servers[k]
		hostname := naming.Server(cfg.App, cfg.Env, k)
		userData, err := cloudinit.Render(pubKey, hostname)
		if err != nil {
			return nil, fmt.Errorf("cloud-init for %s: %w", k, err)
		}
		servers = append(servers, serverData{
			Key:      k,
			Hostname: hostname,
			Type:     s.Type,
			Region:   s.Region,
			Role:     s.Role,
			Image:    defaultImage,
			UserData: userData,
		})
		if s.Role == "master" {
			masters = append(masters, k)
			if zone == "" {
				zone = zoneForLocation(s.Region)
			}
		}
	}
	if zone == "" {
		// Defensive — Validate enforces ≥1 master, so we'd never hit this.
		zone = zoneForLocation(servers[0].Region)
	}
	if len(masters) == 0 {
		return nil, fmt.Errorf("no masters in servers (validator should have rejected)")
	}

	mode := cfg.DeployMode()
	hasDomains := len(cfg.Domains) > 0
	// Tunnel mode (providers.ingress: cloudflare) suppresses ALL
	// public HTTP/S exposure on Hetzner: firewall drops 80/443,
	// LB drops its public interface (via the existing
	// `{{ if not .LBHTTPIngress }}` template gate) and drops its
	// 80/443 service blocks. The tunnel terminates externally at
	// Cloudflare's edge — no inbound surface on the nodes.
	//
	// KubeVIP mode (tunnel + HA) suppresses the hcloud LB entirely —
	// kube-vip on the private subnet carries 6443 instead. EmitLB
	// gates the whole hcloud_load_balancer* block; api_endpoint.private
	// emits the VIP literal instead of the LB private IP.
	var vip string
	if mode.KubeVIP() {
		vip = vipFor(networkSubnet)
	}
	data := templateData{
		App:               cfg.App,
		Env:               cfg.Env,
		Prefix:            naming.Prefix(cfg.App, cfg.Env),
		NetworkCIDR:       networkCIDR,
		NetworkSubnet:     networkSubnet,
		NetworkZone:       zone,
		Servers:           servers,
		HA:                mode.HA,
		PrimaryMaster:     masters[0], // alphabetically first by sort above
		PublicHTTPIngress: hasDomains && !mode.HA && !mode.Tunnel,
		LBHTTPIngress:     hasDomains && mode.HA && !mode.Tunnel,
		VIP:               vip,
	}

	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("render hetzner.tf: %w", err)
	}
	return buf.Bytes(), nil
}

// vipFor picks an unused IP at the top of the private subnet for
// kube-vip to ARP-claim. Hetzner's gateway sits at .1 and auto-attached
// servers consume IPs ascending from .2, so the high end is safe for
// the cluster sizes nvoi targets. For a /24 the value is .250; the
// formula is `broadcast - 5` so smaller subnets stay safely above
// Hetzner's auto-allocation range as well.
//
// Returns an empty string for malformed CIDRs — caller is responsible
// for not invoking this on bad config (compile already validates the
// subnet shape upstream).
func vipFor(cidr string) string {
	p, err := netip.ParsePrefix(cidr)
	if err != nil {
		return ""
	}
	mask := p.Bits()
	// Subnet must be big enough that broadcast-5 stays inside the
	// usable range. /29 (8 addresses, 6 usable) is the floor.
	if mask > 29 {
		return ""
	}
	// Walk the addr to the broadcast and step back 5.
	addr := p.Masked().Addr()
	size := uint64(1) << (32 - mask)
	target := size - 6 // -1 broadcast, -5 offset
	for i := uint64(0); i < target; i++ {
		addr = addr.Next()
	}
	return addr.String()
}
