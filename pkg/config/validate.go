package config

import (
	"fmt"
	"strings"

	"github.com/getnvoi/core/pkg/internal/utils"
	"github.com/getnvoi/core/pkg/providers"
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
	"database":   true,
	"help":       true,
	"completion": true,
}

// Validate enforces YAML shape invariants and verifies that any
// referenced provider is actually registered. Pure — no disk, no env.
// File existence / tilde expansion / credential resolution happen at
// the cmd/ boundary AFTER Validate has succeeded.
//
// Note on import: this file imports pkg/providers. By the time
// Validate is called from cmd/cli, the blank imports there
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

	// Reserved-name set is per-provider — each infra emitter declares
	// its own collisions with non-server HCL resources via
	// providers.RegisterReservedServerNames in its init(). Provider
	// blank-imports in cmd/cli have already populated the registry
	// by the time Validate runs. nil = no reservations for this
	// provider, treated as a permissive empty set.
	reservedServerNames := providers.ReservedServerNames(c.Providers.Infra)

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
	if err := validateHA(c, masters, primaries); err != nil {
		return err
	}

	if err := validateSecrets(c); err != nil {
		return err
	}
	if err := validateServices(c); err != nil {
		return err
	}
	if err := validateAliases(c); err != nil {
		return err
	}
	if err := validateDomains(c); err != nil {
		return err
	}
	if err := validateDatabases(c); err != nil {
		return err
	}
	return nil
}

// validateHA enforces the master-count rules implied by the top-level
// `ha:` flag.
//
//   ha unset / false:
//     exactly 1 master. Multiple masters without ha: true is rejected
//     — it would suggest the operator wants HA but hasn't asked for
//     the LB primitive, leaving the cluster with no apiserver failover.
//
//   ha: true:
//     odd master count ≥3. Etcd quorum needs majority; odd counts use
//     every node optimally (4 has the same failure tolerance as 3 but
//     wastes a node, hence rejected). Exactly one master must carry
//     `primary: true` for cold-start `--cluster-init`.
//
// The LB primitive (private hcloud LB on 6443) is emitted by the
// infra emitter under the same `cfg.HA` gate. Single-master path
// skips the LB entirely.
func validateHA(c *Config, masters, primaries int) error {
	if !c.HA {
		if masters != 1 {
			return fmt.Errorf("servers: %d masters declared but `ha:` is unset — set `ha: true` and use an odd master count ≥3, or scale down to exactly 1 master", masters)
		}
		// Single-master case: primary field is implicit. Setting it
		// explicitly is allowed (forward-compat for flipping ha: true
		// later) but redundant.
		return nil
	}
	// ha: true
	if masters < 3 {
		return fmt.Errorf("ha: true requires an odd master count ≥3 (got %d) — etcd quorum + LB failover both need at least 3 masters", masters)
	}
	if masters%2 == 0 {
		return fmt.Errorf("ha: true: master count must be odd (got %d) — even counts have the same failure tolerance as the next-lower odd count and waste a node", masters)
	}
	if primaries != 1 {
		return fmt.Errorf("ha: true with %d masters: exactly one must have primary: true (got %d) — the primary runs --cluster-init on cold start", masters, primaries)
	}
	return nil
}

func validateDatabases(c *Config) error {
	registered := providers.RegisteredDatabaseEngines()
	dbNodes := map[string]string{}

	for name, db := range c.Databases {
		if name == "" {
			return fmt.Errorf("databases: empty database name")
		}
		if db.Engine == "" {
			return fmt.Errorf("databases.%s.engine: required (one of %v)", name, registered)
		}
		if !providers.IsRegisteredDatabase(db.Engine) {
			return fmt.Errorf("databases.%s.engine: unknown engine %q (registered: %v)", name, db.Engine, registered)
		}

		switch db.Engine {
		case "postgres":
			if db.Server == "" {
				return fmt.Errorf("databases.%s.server: required for engine=postgres (the YAML key of a role: worker server dedicated to this DB)", name)
			}
			if db.Size <= 0 {
				return fmt.Errorf("databases.%s.size: required for engine=postgres (GiB, hard ZFS quota)", name)
			}
			if db.Region != "" {
				return fmt.Errorf("databases.%s.region: not valid for engine=postgres", name)
			}
			if db.InstanceClass != "" {
				return fmt.Errorf("databases.%s.instance_class: not valid for engine=postgres", name)
			}
			if db.Credentials == nil {
				return fmt.Errorf("databases.%s.credentials: required for engine=postgres", name)
			}
			if db.Credentials.User == "" {
				return fmt.Errorf("databases.%s.credentials.user: required", name)
			}
			if db.Credentials.Password == "" {
				return fmt.Errorf("databases.%s.credentials.password: required", name)
			}
			if db.Credentials.Database == "" {
				return fmt.Errorf("databases.%s.credentials.database: required", name)
			}
			srv, ok := c.Servers[db.Server]
			if !ok {
				return fmt.Errorf("databases.%s.server: %q is not a declared server", name, db.Server)
			}
			if srv.Role != "worker" {
				return fmt.Errorf("databases.%s.server: %q must have role: worker (selfhosted DBs need a dedicated node; the master runs etcd + apiserver)", name, db.Server)
			}
			if prior, dup := dbNodes[db.Server]; dup {
				return fmt.Errorf("databases.%s.server: %q is already pinned by databases.%s (one selfhosted DB per node — pools are node-local)", name, db.Server, prior)
			}
			dbNodes[db.Server] = name

		default:
			if db.Server != "" {
				return fmt.Errorf("databases.%s.server: not valid for engine=%s (SaaS engines have no node)", name, db.Engine)
			}
			if db.Size != 0 {
				return fmt.Errorf("databases.%s.size: not valid for engine=%s", name, db.Engine)
			}
			if db.Credentials != nil {
				return fmt.Errorf("databases.%s.credentials: not valid for engine=%s (vendor API owns credentials)", name, db.Engine)
			}
			if db.Engine == "rds-postgres" {
				if db.InstanceClass == "" {
					return fmt.Errorf("databases.%s.instance_class: required for engine=rds-postgres", name)
				}
			} else if db.InstanceClass != "" {
				return fmt.Errorf("databases.%s.instance_class: not valid for engine=%s", name, db.Engine)
			}
			if db.Region == "" {
				return fmt.Errorf("databases.%s.region: required for engine=%s", name, db.Engine)
			}
		}

		if db.Backup != nil {
			if db.Backup.Schedule == "" {
				return fmt.Errorf("databases.%s.backup.schedule: required when backup: is set", name)
			}
			if db.Backup.Retention <= 0 {
				return fmt.Errorf("databases.%s.backup.retention: must be > 0 days", name)
			}
			if c.Providers.Storage == "" {
				return fmt.Errorf("databases.%s.backup: requires providers.storage (the bucket holds gzipped dumps)", name)
			}
		}
	}

	for svcName, svc := range c.Services {
		for _, srv := range svc.Servers {
			if dbName, hit := dbNodes[srv]; hit {
				return fmt.Errorf("services.%s.servers: %q hosts databases.%s — selfhosted DB nodes are exclusive (no other workloads). Move the service to a different worker", svcName, srv, dbName)
			}
		}
	}

	for svcName, svc := range c.Services {
		seenPrefix := map[string]string{}
		for _, entry := range svc.Databases {
			prefix, dbName, err := parseDatabaseBinding(entry)
			if err != nil {
				return fmt.Errorf("services.%s.databases: %w", svcName, err)
			}
			if _, ok := c.Databases[dbName]; !ok {
				return fmt.Errorf("services.%s.databases: %q references undeclared database %q", svcName, entry, dbName)
			}
			if prior, dup := seenPrefix[prefix]; dup {
				return fmt.Errorf("services.%s.databases: prefix %q used by both %q and %q (each service must use distinct prefixes)", svcName, prefix, prior, entry)
			}
			seenPrefix[prefix] = entry
		}
	}

	return nil
}

// ParseDatabaseBinding is the public re-export of parseDatabaseBinding —
// pkg/workload (env-var injection) uses it so the parser the
// validator runs is the parser the manifest builder runs. One source
// of truth.
func ParseDatabaseBinding(entry string) (prefix, dbName string, err error) {
	return parseDatabaseBinding(entry)
}

func parseDatabaseBinding(entry string) (prefix, dbName string, err error) {
	if entry == "" {
		return "", "", fmt.Errorf("empty entry")
	}
	if i := strings.IndexByte(entry, '='); i >= 0 {
		prefix = entry[:i]
		dbName = entry[i+1:]
	} else {
		prefix = "DATABASE"
		dbName = entry
	}
	if prefix == "" {
		return "", "", fmt.Errorf("%q: empty prefix", entry)
	}
	if dbName == "" {
		return "", "", fmt.Errorf("%q: empty database name (use PREFIX=dbname)", entry)
	}
	for _, r := range prefix {
		if !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '_' {
			return "", "", fmt.Errorf("%q: prefix must be UPPER_CASE_WITH_UNDERSCORES (got %q)", entry, prefix)
		}
	}
	for _, suf := range []string{"_URL", "_HOST", "_PORT", "_USER", "_PASSWORD"} {
		if strings.HasSuffix(prefix, suf) {
			return "", "", fmt.Errorf("%q: prefix ends with %q which collides with the canonical suffix. Write %s=%s instead — the prefix expands to <PREFIX>_URL, _HOST, _PORT, _USER, _PASSWORD automatically", entry, suf, strings.TrimSuffix(prefix, suf), dbName)
		}
	}
	return prefix, dbName, nil
}

// validateDomains enforces:
//   - every key in Domains must be a declared service
//   - every hostname is DNS-1123-shaped (lowercase letters / digits /
//     dashes / dots; labels ≤63 chars; total ≤253 chars)
//
// CF DNS+tunnel is the only path: presence of Domains activates the
// tunnel implicitly. CF env vars (CF_ZONE_ID, CF_ZONE, CF_ACCOUNT_ID,
// CF_API_TOKEN, CF_TUNNEL_SECRET) are validated at the cmd/cli
// boundary (load.go), keeping config.Validate env-free.
func validateDomains(c *Config) error {
	if len(c.Domains) == 0 {
		return nil
	}
	for svcName, hosts := range c.Domains {
		if _, ok := c.Services[svcName]; !ok {
			return fmt.Errorf("domains.%s: %q is not a declared service", svcName, svcName)
		}
		if len(hosts) == 0 {
			return fmt.Errorf("domains.%s: at least one hostname required", svcName)
		}
		for _, h := range hosts {
			if !isValidHostname(h) {
				return fmt.Errorf("domains.%s: %q is not a valid DNS hostname", svcName, h)
			}
		}
	}
	return nil
}

// isValidHostname accepts DNS-1123-shaped hostnames: lowercase letters,
// digits, dashes, separated by dots; each label 1-63 chars, no
// leading/trailing dash; total ≤253 chars. No wildcards in v1.
func isValidHostname(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	labels := strings.Split(h, ".")
	if len(labels) < 2 {
		return false // must be FQDN-ish (at least one dot)
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			switch {
			case r >= 'a' && r <= 'z':
			case r >= '0' && r <= '9':
			case r == '-':
			default:
				return false
			}
		}
	}
	return true
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
