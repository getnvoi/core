package compile

import (
	"fmt"

	nvoiRuntime "github.com/getnvoi/core/pkg/runtime"
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
type DNSEmitter interface {
	EmitDNS(rt *nvoiRuntime.Runtime) ([]byte, error)
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
