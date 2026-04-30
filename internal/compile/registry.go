package compile

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/getnvoi/core/internal/config"
	nvoiRuntime "github.com/getnvoi/core/internal/runtime"
)

// ProviderRequirement is the (alias, source, version) triple every
// emitter declares so compile can aggregate them into a single
// versions.tf. Terraform requires `required_providers` to appear
// exactly once per module — having each provider's .tf file declare
// its own would duplicate-error at init.
type ProviderRequirement struct {
	Alias   string // local terraform alias (e.g. "hcloud", "cloudflare")
	Source  string // registry source (e.g. "hetznercloud/hcloud")
	Version string // version constraint (e.g. "~> 1.48")
}

// InfraEmitter renders the provider's infra resources (servers,
// network, firewall, …) as rendered HCL bytes. Pure transform: no
// disk, no network.
//
// Each emitter writes ONLY its `provider "X" {}` config block and
// resources — never the `terraform { required_providers / backend }`
// meta-block. compile aggregates Provider() requirements from every
// active emitter into a single versions.tf so the module always has
// exactly one terraform meta-block.
type InfraEmitter interface {
	EmitInfra(rt *nvoiRuntime.Runtime) ([]byte, error)

	// ServerResourceType returns the terraform resource type this
	// emitter uses for servers (k3s nodes). Used by the deploy
	// pipeline's detach step to filter "which resources in the plan
	// are nodes being removed".
	ServerResourceType() string

	// Provider returns this emitter's terraform provider requirement.
	// Aggregated by compile into versions.tf.
	Provider() ProviderRequirement
}

var infraEmitters = map[string]InfraEmitter{}

// RegisterInfra is called from a provider package's init().
func RegisterInfra(name string, e InfraEmitter) {
	if _, dup := infraEmitters[name]; dup {
		panic(fmt.Sprintf("compile: duplicate infra emitter %q (programming error)", name))
	}
	infraEmitters[name] = e
}

func resolveInfra(name string) (InfraEmitter, error) {
	e, ok := infraEmitters[name]
	if !ok {
		return nil, fmt.Errorf("unknown infra provider %q", name)
	}
	return e, nil
}

// ServerResourceType is the public lookup the deploy pipeline uses
// to filter terraform plans for "node destroys."
func ServerResourceType(provider string) (string, error) {
	e, err := resolveInfra(provider)
	if err != nil {
		return "", err
	}
	return e.ServerResourceType(), nil
}

// DNSEmitter renders the DNS provider's per-domain
// `<provider>_record` resources as HCL bytes.
//
// Takes *config.Config (NOT *runtime.Runtime) so DNS provider
// packages can avoid importing internal/runtime — the bucket-provider
// blank-import in tests would otherwise pull
// providers/cloudflare → runtime → config → cycle.
//
// Same upper-block discipline as InfraEmitter: emit `provider "X" {}`
// + resources, never the `terraform {}` meta-block.
type DNSEmitter interface {
	EmitDNS(cfg *config.Config) ([]byte, error)
	Provider() ProviderRequirement
}

var dnsEmitters = map[string]DNSEmitter{}

// RegisterDNS is called from a provider package's init().
func RegisterDNS(name string, e DNSEmitter) {
	if _, dup := dnsEmitters[name]; dup {
		panic(fmt.Sprintf("compile: duplicate dns emitter %q (programming error)", name))
	}
	dnsEmitters[name] = e
}

// ResolveDNS returns the registered DNSEmitter for `name`, or an
// error if none exists.
func ResolveDNS(name string) (DNSEmitter, error) {
	e, ok := dnsEmitters[name]
	if !ok {
		return nil, fmt.Errorf("unknown dns provider %q", name)
	}
	return e, nil
}

// TunnelEmitter renders the tunnel provider's terraform resources
// (the tunnel itself + ingress config) AND the post-tf agent
// workloads (Deployment + token Secret applied via kc.ApplyOwned in
// the workload phase).
//
// HCL contract: every TunnelEmitter writes a `locals {}` block
// exposing two well-known names:
//
//	local.tunnel_cname     CNAME target hostname for DNS records
//	local.tunnel_proxied   bool (Cloudflare-orange-cloud only; false elsewhere)
//
// The DNS emitter reads these locals when cfg.Providers.Tunnel is
// set to flip its records from A → CNAME without knowing which
// tunnel backend is active.
//
// Plus a `terraform { output "tunnel_token" }` block carrying the
// agent's auth token; the workload phase reads it via
// `terraform output` and injects it into the agent Secret.
type TunnelEmitter interface {
	EmitTunnel(cfg *config.Config) ([]byte, error)

	// Providers returns ALL terraform provider requirements this
	// tunnel emitter relies on. Plural (vs Infra/DNS's singular
	// Provider()) because tunnel impls often need auxiliary
	// providers — e.g. Cloudflare Tunnel uses hashicorp/random for
	// the tunnel secret. Aggregated into backend.tf.
	Providers() []ProviderRequirement

	// TunnelResourceType returns the terraform resource type the
	// emitter uses for the tunnel object itself (e.g.
	// "cloudflare_zero_trust_tunnel_cloudflared"). Used by the
	// deploy/destroy pipeline's drain step to filter "which
	// resources in the plan are tunnels going away" — Cloudflare's
	// API rejects DELETE on a tunnel with active connections, so
	// nvoi pre-emptively kills the in-cluster agent before tf-apply
	// runs the destroy. Mirrors InfraEmitter.ServerResourceType.
	TunnelResourceType() string

	// AgentWorkloads renders the in-cluster Deployment + Secret(s)
	// the tunnel agent (cloudflared, ngrok) runs as. Called from
	// the workload phase with the resolved token from `terraform
	// output`.
	AgentWorkloads(cfg *config.Config, token string) ([]TunnelWorkload, error)
}

// TunnelWorkload wraps a typed k8s object the workload phase will
// hand to kc.ApplyOwned with owner=tunnel-agent.
type TunnelWorkload struct {
	Kind string // "Deployment" | "Secret" | "ConfigMap"
	Name string
	Obj  runtime.Object
}

var tunnelEmitters = map[string]TunnelEmitter{}

// RegisterTunnel is called from a provider package's init().
func RegisterTunnel(name string, e TunnelEmitter) {
	if _, dup := tunnelEmitters[name]; dup {
		panic(fmt.Sprintf("compile: duplicate tunnel emitter %q (programming error)", name))
	}
	tunnelEmitters[name] = e
}

// ResolveTunnel returns the registered TunnelEmitter for `name`.
func ResolveTunnel(name string) (TunnelEmitter, error) {
	e, ok := tunnelEmitters[name]
	if !ok {
		return nil, fmt.Errorf("unknown tunnel provider %q", name)
	}
	return e, nil
}

// TunnelResourceType is the public lookup the deploy/destroy
// pipelines use to filter terraform plans for "tunnel destroys" —
// the trigger for the pre-apply agent-drain step.
func TunnelResourceType(provider string) (string, error) {
	e, err := ResolveTunnel(provider)
	if err != nil {
		return "", err
	}
	return e.TunnelResourceType(), nil
}
