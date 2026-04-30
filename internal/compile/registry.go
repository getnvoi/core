package compile

import (
	"fmt"

	"github.com/getnvoi/tf/internal/runtime"
)

// InfraEmitter renders the provider block + infra resources for one
// backend (hetzner / aws / scaleway) as rendered HCL bytes. Compile
// collects every emitter's output into the bundle — emitters never
// touch the bundle directly. One input (rt), one output (bytes).
//
// Emitters are pure: no disk, no network. They read whatever they need
// from rt.Cfg + rt.SSHPubKey + rt.* and return rendered HCL.
//
// Future kinds (DNSEmitter, BucketEmitter, KubeEmitter) follow the same
// shape — one interface per plug point, one registry, one Resolve.
type InfraEmitter interface {
	EmitInfra(rt *runtime.Runtime) ([]byte, error)

	// ServerResourceType returns the terraform resource type this
	// emitter uses for servers (k3s nodes). Used by the deploy
	// pipeline's detach step to filter "which resources in the plan
	// are nodes being removed". One source of truth per provider —
	// the day AWS lands, its emitter declares "aws_instance" and
	// nothing else changes.
	ServerResourceType() string
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
