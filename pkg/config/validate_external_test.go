package config_test

import (
	"strings"
	"testing"

	"github.com/getnvoi/core/pkg/config"

	// Triggers cloudflare.init() so providers.IsRegisteredBucket
	// ("cloudflare") returns true. Lives here (external test
	// package) and not in validate_test.go because cloudflare's
	// dns.go imports internal/config — same-package blank-import
	// would cycle.
	_ "github.com/getnvoi/core/pkg/providers/cloudflare"

	// Triggers hetzner.init() so
	// providers.ReservedServerNames("hetzner") returns the registered
	// set. Lives here for the same reason — hetzner's compile.go
	// imports internal/runtime which imports internal/config.
	_ "github.com/getnvoi/core/pkg/providers/hetzner"
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

// TestValidate_ReservedServerName_Hetzner locks the contract: a YAML
// server keyed "default" (or any other key the active infra emitter
// registered as reserved) is rejected with a clear error. Per-provider
// reservation sets are registered via
// providers.RegisterReservedServerNames in each provider's init().
func TestValidate_ReservedServerName_Hetzner(t *testing.T) {
	c := &config.Config{
		App:       "hello",
		Env:       "dev",
		Providers: config.Providers{Infra: "hetzner"},
		SSHKey:    "/tmp/x.pub",
		Servers: map[string]config.ServerSpec{
			"master":  {Type: "cax11", Region: "nbg1", Role: "master"},
			"default": {Type: "cax11", Region: "nbg1", Role: "worker"},
		},
	}
	err := c.Validate()
	if err == nil {
		t.Fatal("expected error for reserved server name 'default', got nil")
	}
	if !strings.Contains(err.Error(), "name reserved") {
		t.Fatalf("expected error containing 'name reserved', got: %v", err)
	}
}
