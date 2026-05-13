// Package compile turns a runtime.Runtime into an HCL Bundle ready to
// write into a tofu working directory. Pure transformation: no
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
	"fmt"

	"github.com/getnvoi/core/pkg/naming"
	"github.com/getnvoi/core/pkg/providers/cloudflare"
	nvoiRuntime "github.com/getnvoi/core/pkg/runtime"
)

// Compile resolves the configured providers, asks each to emit its
// HCL bytes, and packages the result into a Bundle. One file per
// concern:
//
//	backend.tf            terraform { required_providers + backend }
//	                      — aggregated from infra + (when domains:)
//	                      the Cloudflare DNS+tunnel emitter.
//	<infra-provider>.tf   provider "X" {} + servers/network/firewall
//	cloudflare-dns.tf     provider "cloudflare" {} + tunnel resource +
//	                      ingress config + CNAME records. Emitted only
//	                      when cfg.Domains is non-empty.
//
// Each provider's emitter writes ONLY its provider-config block + its
// resources. The terraform meta-block lives in backend.tf alone —
// tofu rejects duplicate `required_providers` blocks at the
// module level, so per-provider declarations must aggregate.
//
// DNS is NOT pluggable: Cloudflare is the only DNS+tunnel provider
// (substrate-level dependency). Compile calls cloudflare.EmitTunnelDNS
// directly — keeps the seam honest about the hard dep.
func Compile(rt *nvoiRuntime.Runtime) (*Bundle, error) {
	b := NewBundle()

	backendHCL, err := emitBackend(rt)
	if err != nil {
		return nil, fmt.Errorf("emit backend.tf: %w", err)
	}
	b.Set("backend.tf", backendHCL)

	infra, err := resolveInfra(rt.Cfg.Providers.Infra)
	if err != nil {
		return nil, err
	}
	infraHCL, err := infra.EmitInfra(rt)
	if err != nil {
		return nil, err
	}
	b.Set(naming.ProviderHCL(rt.Cfg.Providers.Infra), infraHCL)

	// CF tunnel + CNAME records are tf-managed: one CNAME per
	// (service, domain) → tunnel CNAME target; tunnel resource +
	// ingress config carry the route table. Activated by the
	// presence of cfg.Domains alone — no separate toggle.
	if len(rt.Cfg.Domains) > 0 {
		dnsHCL, err := cloudflare.EmitTunnelDNS(rt)
		if err != nil {
			return nil, err
		}
		b.Set("cloudflare-dns.tf", dnsHCL)
	}

	return b, nil
}
