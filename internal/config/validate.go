package config

import (
	"fmt"
	"strings"

	"github.com/getnvoi/core/internal/providers"
	"github.com/getnvoi/core/internal/utils"
)

// reservedAliasNames are command names an alias must not shadow.
// Every built-in cobra verb plus cobra's own help/completion
// subcommands. Any alias matching one of these is a hard error so
// operators can't silently break `nvoi deploy` by writing
// `aliases.deploy: ...`.
var reservedAliasNames = map[string]bool{
	"deploy":     true,
	"plan":       true,
	"destroy":    true,
	"ssh":        true,
	"kubectl":    true,
	"exec":       true,
	"logs":       true,
	"help":       true,
	"completion": true,
}

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

	if err := validateSecrets(c); err != nil {
		return err
	}
	if err := validateServices(c); err != nil {
		return err
	}
	if err := validateAliases(c); err != nil {
		return err
	}
	return nil
}

// validateAliases enforces:
//   - name shape: lowercase letters / digits / dashes / underscores;
//     must start with a letter (matches cobra-friendly verb names)
//   - name must not collide with a built-in verb (deploy, exec, etc.)
//   - body must be non-empty after trim
//   - body must tokenize (balanced quotes)
//
// Tokenization happens here too so misconfigured aliases fail fast at
// load time, not at the moment an operator types `nvoi <alias>`.
func validateAliases(c *Config) error {
	for name, body := range c.Aliases {
		if !isValidAliasName(name) {
			return fmt.Errorf("aliases.%s: invalid name (lowercase letters / digits / dash / underscore; must start with a letter)", name)
		}
		if reservedAliasNames[name] {
			return fmt.Errorf("aliases.%s: name shadows a built-in verb", name)
		}
		if strings.TrimSpace(body) == "" {
			return fmt.Errorf("aliases.%s: empty body", name)
		}
		if _, err := utils.ShellSplit(body); err != nil {
			return fmt.Errorf("aliases.%s: %w", name, err)
		}
	}
	return nil
}

func isValidAliasName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case i > 0 && r >= '0' && r <= '9':
		case i > 0 && (r == '-' || r == '_'):
		default:
			return false
		}
	}
	return true
}

// validateSecrets enforces the top-level `secrets:` shape:
//   - non-empty entries
//   - each entry is a valid POSIX env var name (matches what
//     `os.Getenv` and a k8s Secret key both accept without ceremony)
//   - no duplicates
//
// Resolution against the operator's environment happens at the cmd/
// boundary, NOT here. Validate is pure.
func validateSecrets(c *Config) error {
	seen := map[string]bool{}
	for i, name := range c.Secrets {
		if name == "" {
			return fmt.Errorf("secrets[%d]: empty entry", i)
		}
		if !isValidEnvVarName(name) {
			return fmt.Errorf("secrets[%d]: %q is not a valid env var name (must match [A-Za-z_][A-Za-z0-9_]*)", i, name)
		}
		if seen[name] {
			return fmt.Errorf("secrets: duplicate entry %q", name)
		}
		seen[name] = true
	}
	return nil
}

// isValidEnvVarName mirrors the POSIX env var rule: leading letter or
// underscore, followed by letters/digits/underscores. Tight enough
// that the same string flows safely into both os.Getenv and a k8s
// Secret data key.
func isValidEnvVarName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_':
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

// validateServices enforces the YAML shape for services + registry:
//
//   - every service requires `image`
//   - every service requires `port`
//   - if `build:` is set: image must be fully qualified (host/...) OR
//     the registry: block must declare exactly one host (which we infer)
//   - replicas, when set, must be >= 1
//   - storage, when set, requires size + mountPath
//   - servers, when set, must reference declared servers; len > 1 +
//     storage = error (a hostPath PV can't span nodes)
//   - secrets refs must exist in the top-level secrets: list
//
// Push-side auth (operator's ~/.docker/config.json) is checked at the
// cmd/ boundary, not here — that's I/O.
func validateServices(c *Config) error {
	declaredSecrets := make(map[string]bool, len(c.Secrets))
	for _, name := range c.Secrets {
		declaredSecrets[name] = true
	}

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
			if host != "" {
				if _, ok := c.Registry[host]; !ok {
					return fmt.Errorf("services.%s: build pushes to %s, but no registry: entry for that host", name, host)
				}
			} else {
				// No host in image — try inference from a single
				// registry: entry. With multiple registries this is
				// ambiguous; with none it's broken.
				if !strings.Contains(svc.Image, "/") {
					return fmt.Errorf("services.%s: build set but image %q is a bare shortname; use `<org>/<name>` or a fully qualified tag", name, svc.Image)
				}
				if len(c.Registry) == 0 {
					return fmt.Errorf("services.%s: build set but no registry: block to push to", name)
				}
				if len(c.Registry) > 1 {
					return fmt.Errorf("services.%s: image %q has no host prefix but multiple registries are declared — write a fully qualified tag (e.g. ghcr.io/%s) to disambiguate", name, svc.Image, svc.Image)
				}
			}
		}
		if svc.Storage != nil {
			if svc.Storage.Size == "" {
				return fmt.Errorf("services.%s.storage.size: required", name)
			}
			if svc.Storage.MountPath == "" {
				return fmt.Errorf("services.%s.storage.mountPath: required", name)
			}
		}
		// Server placement
		for _, s := range svc.Servers {
			if _, ok := c.Servers[s]; !ok {
				return fmt.Errorf("services.%s.servers: %q is not a defined server", name, s)
			}
		}
		// Multi-server + storage is impossible — a hostPath PV is
		// pinned to one node, can't span.
		if len(svc.Servers) > 1 && svc.Storage != nil {
			return fmt.Errorf("services.%s: multiple servers with storage — a single PV can't span nodes; pick one server", name)
		}
		// Secret refs
		for _, ref := range svc.Secrets {
			if !declaredSecrets[ref] {
				return fmt.Errorf("services.%s.secrets: %q is not declared in top-level secrets:", name, ref)
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
