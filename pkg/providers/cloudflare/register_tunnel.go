package cloudflare

import "github.com/getnvoi/core/pkg/compile"

// init registers the Cloudflare Tunnel emitter. Triggered alongside
// the bucket + DNS registrations by the existing blank import in
// cmd/cli/main.go.
func init() {
	compile.RegisterTunnel("cloudflare", TunnelEmitter{})
}
