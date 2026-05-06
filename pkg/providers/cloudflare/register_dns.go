package cloudflare

import "github.com/getnvoi/core/pkg/internal/compile"

// init registers the Cloudflare DNS emitter with the compile registry.
// Triggered by a blank import in cmd/cli/main.go — same pattern as the
// existing bucket-provider registration.
func init() {
	compile.RegisterDNS("cloudflare", DNSEmitter{})
}
