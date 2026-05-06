package compile

import (
	"bytes"
	"embed"
	"sort"
	"strconv"
	"text/template"

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
	reqs := []ProviderRequirement{infra.Provider()}

	if rt.Cfg.Providers.DNS != "" {
		dns, err := ResolveDNS(rt.Cfg.Providers.DNS)
		if err != nil {
			return nil, err
		}
		reqs = append(reqs, dns.Provider())
	}

	// Dedupe by alias — emitters may declare the same provider
	// (e.g. CF DNS + a future CF resource both want cloudflare). Last
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
