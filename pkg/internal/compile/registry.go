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
// meta-block. compile aggregates Providers() requirements from every
// active emitter into a single backend.tf so the module always has
// exactly one terraform meta-block.
//
// DNS is NOT pluggable: Cloudflare is the only supported DNS+tunnel
// provider (substrate-level dependency). compile.Compile calls
// cloudflare.EmitTunnelDNS directly — no DNSEmitter interface.
type InfraEmitter interface {
	EmitInfra(rt *nvoiRuntime.Runtime) ([]byte, error)

	// ServerResourceType returns the terraform resource type this
	// emitter uses for servers (k3s nodes). Used by the deploy
	// pipeline's detach step to filter "which resources in the plan
	// are nodes being removed".
	ServerResourceType() string

	// Providers returns every terraform provider this emitter's HCL
	// references — primary plus any utility providers (random,
	// time, etc.) the emitted resources depend on. Aggregated by
	// compile into backend.tf's required_providers block.
	Providers() []ProviderRequirement
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
