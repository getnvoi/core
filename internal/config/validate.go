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

	if err := validateServices(c); err != nil {
		return err
	}
	return nil
}

// validateServices enforces the YAML shape for services + registry:
//
//   - every service requires `image`
//   - every service requires `port`
//   - if `build:` is set: image must be fully qualified (host/...)
//     AND that host must appear under registry: (so cluster can pull
//     the image we're about to push)
//   - replicas, when set, must be > 0
//
// Push-side auth (operator's ~/.docker/config.json) is checked at the
// cmd/ boundary, not here — that's I/O.
func validateServices(c *Config) error {
	for name, svc := range c.Services {
		if svc.Image == "" {
			return fmt.Errorf("services.%s.image: required", name)
		}
		if svc.Port == 0 {
			return fmt.Errorf("services.%s.port: required", name)
		}
		if svc.Replicas != nil && *svc.Replicas < 1 {
			return fmt.Errorf("services.%s.replicas: must be >= 1 (got %d)", name, *svc.Replicas)
		}
		if svc.HasBuild() {
			host := svc.ImageHost()
			if host == "" {
				return fmt.Errorf("services.%s: build set but image %q is a bare shortname; use a fully qualified tag (e.g. ghcr.io/org/%s)", name, svc.Image, name)
			}
			if _, ok := c.Registry[host]; !ok {
				return fmt.Errorf("services.%s: build pushes to %s, but no registry: entry for that host", name, host)
			}
		}
	}
	for host, reg := range c.Registry {
		if reg.Username == "" {
			return fmt.Errorf("registry.%s.username: required", host)
		}
		if reg.Password == "" {
			return fmt.Errorf("registry.%s.password: required", host)
		}
	}
	return nil
}
