package compile

import (
	"bytes"
	"embed"
	"sort"
	"strconv"
	"text/template"

	"github.com/getnvoi/core/pkg/providers/cloudflare"
	nvoiRuntime "github.com/getnvoi/core/pkg/runtime"
)

//go:embed templates/backend.tf.tmpl
var backendTemplateFS embed.FS

var backendTpl = template.Must(template.New("backend.tf.tmpl").
	Funcs(template.FuncMap{"hcl": strconv.Quote}).
	ParseFS(backendTemplateFS, "templates/backend.tf.tmpl"))

// emitBackend renders the single `backend.tf` file every module
// gets: the consolidated `terraform { required_providers {...}
// backend "s3" {...} }` block. Aggregates every active emitter's
// Provider() requirement so providers each emit ONLY their
// `provider "X" {}` + resources — never the meta-block.
//
// Empty when no providers register (defensive — should never happen
// because infra is required).
func emitBackend(rt *nvoiRuntime.Runtime) ([]byte, error) {
	infra, err := resolveInfra(rt.Cfg.Providers.Infra)
	if err != nil {
		return nil, err
	}
	reqs := append([]ProviderRequirement(nil), infra.Providers()...)

	// Cloudflare DNS+tunnel provider is added unconditionally when
	// domains: is set — the only DNS path (no provider abstraction).
	// Without domains, no cloudflare provider block is needed (state
	// backend uses the s3-compatible R2 endpoint via raw HTTP creds,
	// not the cloudflare provider).
	if len(rt.Cfg.Domains) > 0 {
		reqs = append(reqs, ProviderRequirement{
			Alias:   cloudflare.TerraformProviderAlias,
			Source:  cloudflare.TerraformProviderSource,
			Version: cloudflare.TerraformProviderVersion,
		})
	}

	// Dedupe by alias — emitters may declare the same provider
	// (e.g. infra + a future CF resource both want cloudflare). Last
	// declaration wins; the slice is small so a linear scan is fine.
	deduped := reqs[:0]
	seen := make(map[string]bool, len(reqs))
	for _, r := range reqs {
		if seen[r.Alias] {
			continue
		}
		seen[r.Alias] = true
		deduped = append(deduped, r)
	}
	reqs = deduped

	// Sort by Alias for deterministic HCL output. terraform doesn't
	// care about the order, but tests + diffs do.
	sort.Slice(reqs, func(i, j int) bool { return reqs[i].Alias < reqs[j].Alias })

	var buf bytes.Buffer
	if err := backendTpl.Execute(&buf, struct {
		Reqs    []ProviderRequirement
		Backend any
	}{Reqs: reqs, Backend: rt.Backend}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
