// Package hetzner emits Hetzner-Cloud HCL. Single source of truth for
// the hetzner backend; growing this package = porting more of nvoi's
// pkg/provider/hetzner over.
package hetzner

import (
	"github.com/getnvoi/core/pkg/internal/compile"
	"github.com/getnvoi/core/pkg/providers"
)

func init() {
	compile.RegisterInfra("hetzner", &emitter{})
	// Reserved YAML server keys — these names collide with non-server
	// resources the hetzner template emits (network, LB, subnet).
	// Validator consults this registry to reject conflicts at parse
	// time. Lives in the provider package so the validator stays free
	// of hetzner-specific knowledge.
	providers.RegisterReservedServerNames("hetzner", hetznerReservedServerNames)
}
