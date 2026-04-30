// Package compile turns a runtime.Runtime into an HCL Bundle ready to
// write into a terraform working directory. Pure transformation: no
// I/O, no provider API calls, no state, no ctx — runs synchronously
// in-process. Add ctx the day a step inside genuinely waits.
//
// Why Runtime and not just Config: emitters need both YAML AND
// boundary-resolved values (SSH public key bytes, future deploy hash,
// future credential refs). The bag pattern scales as we port more
// nvoi stages — adding a field to Runtime is a one-line change instead
// of widening every emitter signature.
package compile

import (
	"github.com/getnvoi/core/internal/naming"
	"github.com/getnvoi/core/internal/runtime"
)

// Compile resolves the configured infra provider, asks it to emit its
// HCL bytes, and packages the result into a Bundle.
func Compile(rt *runtime.Runtime) (*Bundle, error) {
	infra, err := resolveInfra(rt.Cfg.Providers.Infra)
	if err != nil {
		return nil, err
	}
	content, err := infra.EmitInfra(rt)
	if err != nil {
		return nil, err
	}
	b := NewBundle()
	b.Set(naming.ProviderHCL(rt.Cfg.Providers.Infra), content)
	return b, nil
}
