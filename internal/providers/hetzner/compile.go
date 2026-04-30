package hetzner

import (
	"bytes"
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/getnvoi/core/internal/cloudinit"
	"github.com/getnvoi/core/internal/compile"
	"github.com/getnvoi/core/internal/naming"
	"github.com/getnvoi/core/internal/runtime"
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

// Provider declares the terraform provider this emitter relies on.
// Aggregated by compile into the consolidated backend.tf so the
// module ends up with exactly one `required_providers` block.
func (emitter) Provider() compile.ProviderRequirement {
	return compile.ProviderRequirement{
		Alias:   "hcloud",
		Source:  "hetznercloud/hcloud",
		Version: "~> 1.48",
	}
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

	// PublicHTTPIngress opens hcloud_firewall.default for 80/443 when
	// the master serves Caddy directly. True iff domains: is declared
	// AND providers.tunnel is unset. Tunnel mode closes the ports —
	// all ingress flows through the agent's outbound connection.
	PublicHTTPIngress bool
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

	data := templateData{
		App:               cfg.App,
		Env:               cfg.Env,
		Prefix:            naming.Prefix(cfg.App, cfg.Env),
		NetworkCIDR:       networkCIDR,
		NetworkSubnet:     networkSubnet,
		NetworkZone:       zone,
		Servers:           servers,
		HA:                len(masters) >= 2,
		PrimaryMaster:     masters[0], // alphabetically first by sort above
		PublicHTTPIngress: len(cfg.Domains) > 0 && cfg.Providers.Tunnel == "",
	}

	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("render hetzner.tf: %w", err)
	}
	return buf.Bytes(), nil
}
