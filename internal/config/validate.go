package config

import (
	"fmt"

	"github.com/getnvoi/core/internal/providers"
)

// reservedServerNames are YAML keys an operator must NOT pick for a
// server, because the hetzner emitter (or any future infra emitter)
// uses these names for its own non-server resources (network, LB,
// subnet). Keeps the assumption that "name in cfg.Servers" never
// collides with a non-server terraform resource.
var reservedServerNames = map[string]bool{
	"default": true, // hcloud_network.default, hcloud_firewall.default, hcloud_network_subnet.default
	"cp":      true, // hcloud_load_balancer.cp + lb_network/target/service
}

// Validate enforces YAML shape invariants and verifies that any
// referenced provider is actually registered. Pure — no disk, no env.
// File existence / tilde expansion / credential resolution happen at
// the cmd/ boundary AFTER Validate has succeeded.
//
// Note on import: this file imports internal/providers. By the time
// Validate is called from cmd/cli/main.go, the blank imports there
// have triggered each provider package's init() and populated the
// registries. So `providers.IsRegisteredBucket(...)` returns true for
// every provider linked into the binary.
func (c *Config) Validate() error {
	if c.App == "" {
		return fmt.Errorf("app: required")
	}
	if c.Env == "" {
		return fmt.Errorf("env: required")
	}
	if c.Providers.Infra == "" {
		return fmt.Errorf("providers.infra: required")
	}
	if c.Providers.Storage != "" && !providers.IsRegisteredBucket(c.Providers.Storage) {
		return fmt.Errorf("providers.storage: unknown provider %q", c.Providers.Storage)
	}
	if c.SSHKey == "" {
		return fmt.Errorf("ssh_key: required (path to your SSH public key, e.g. ~/.ssh/id_rsa.pub) — no implicit fallback")
	}
	if len(c.Servers) == 0 {
		return fmt.Errorf("servers: at least one required")
	}

	masters, primaries := 0, 0
	for name, srv := range c.Servers {
		if reservedServerNames[name] {
			return fmt.Errorf("servers.%s: name reserved (clashes with internal resource); rename", name)
		}
		if srv.Type == "" {
			return fmt.Errorf("servers.%s.type: required", name)
		}
		if srv.Region == "" {
			return fmt.Errorf("servers.%s.region: required", name)
		}
		if srv.Role == "" {
			return fmt.Errorf("servers.%s.role: required", name)
		}
		if srv.Role != "master" && srv.Role != "worker" {
			return fmt.Errorf("servers.%s.role: must be master or worker (got %q)", name, srv.Role)
		}
		if srv.Primary && srv.Role != "master" {
			return fmt.Errorf("servers.%s: primary: true only valid on role: master", name)
		}
		if srv.Role == "master" {
			masters++
			if srv.Primary {
				primaries++
			}
		}
	}
	if masters < 1 {
		return fmt.Errorf("servers: at least one master required (got %d)", masters)
	}
	if masters > 1 && primaries != 1 {
		return fmt.Errorf("servers: with %d masters, exactly one must have primary: true (got %d)", masters, primaries)
	}
	// Single-master case: primary field is implicit. Setting it
	// explicitly is allowed (forward-compat for adding masters later)
	// but redundant.
	return nil
}
