package config_test

import (
	"testing"

	"github.com/getnvoi/core/internal/config"

	// Triggers cloudflare.init() so providers.IsRegisteredBucket
	// ("cloudflare") returns true. Lives here (external test
	// package) and not in validate_test.go because cloudflare's
	// dns.go now imports internal/config — same-package
	// blank-import would cycle.
	_ "github.com/getnvoi/core/internal/providers/cloudflare"
)

// TestValidate_StorageCloudflare_Registered locks the contract:
// when providers.storage: cloudflare is set, the validator
// resolves the registered bucket provider and accepts the value.
// Companion to the "unknown storage provider" case in
// validate_test.go which doesn't need the registration.
func TestValidate_StorageCloudflare_Registered(t *testing.T) {
	c := &config.Config{
		App:       "hello",
		Env:       "dev",
		Providers: config.Providers{Infra: "hetzner", Storage: "cloudflare"},
		SSHKey:    "/tmp/x.pub",
		Servers: map[string]config.ServerSpec{
			"master": {Type: "cax11", Region: "nbg1", Role: "master"},
		},
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("expected validate ok with registered bucket provider, got %v", err)
	}
}
