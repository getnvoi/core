// Package config owns the parsed YAML. Pure data — no flags, no
// runtime, no env reads, no disk I/O beyond Load. Read-only after Load
// returns; every internal package downstream trusts a *Config that came
// out of Load.
//
// Path resolution (tilde expansion, file existence, key reads) lives
// at the cmd/ boundary. Validate (in validate.go) checks YAML shape
// + provider registration only.
package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the parsed YAML. One struct, one shape, mirrors the
// upstream nvoi.yaml surface so porting is mechanical.
type Config struct {
	App       string                 `yaml:"app"`
	Env       string                 `yaml:"env"`
	Providers Providers              `yaml:"providers"`
	SSHKey    string                 `yaml:"ssh_key"` // path to public key — resolved + read at the cmd/ boundary
	Servers   map[string]ServerSpec  `yaml:"servers"`
	Registry  map[string]RegistryDef `yaml:"registry,omitempty"`

	// Secrets is the top-level list of secret NAMES. Each name resolves
	// at the cmd/ boundary against os.Getenv (one source today —
	// CredentialSource backends are an upper-layer concern). Resolved
	// values land in `runtime.Runtime.SecretValues` and are rendered into a
	// single Opaque Secret named `nvoi-secrets` in the app namespace
	// during workload apply. Per-service `secrets:` whitelists which
	// names that service consumes via env-via-secretKeyRef injection.
	Secrets []string `yaml:"secrets,omitempty"`

	// Databases is the per-database block — declarative provisioning +
	// connection material for every backend nvoi knows. Each entry's
	// `engine:` resolves through the providers.RegisterDatabase
	// registry. Postgres is the reference (in-cluster on ZFS-LocalPV);
	// every other engine is an HCL emitter + Go API adapter living
	// under pkg/providers/<engine>/. See pkg/providers/database.go and
	// .claude/todos/database/ for the full contract.
	Databases map[string]DatabaseSpec `yaml:"databases,omitempty"`

	Services map[string]ServiceSpec `yaml:"services,omitempty"`

	// Aliases are operator-defined shortcuts. Each entry maps a name
	// to a command-string composed of existing nvoi verbs (exec,
	// kubectl, ssh, …). `nvoi <name>` expands the body into argv
	// before cobra dispatches — pure text substitution, no new logic.
	// Mirrors Kamal's `aliases:` shape verbatim.
	//
	//   aliases:
	//     visits: exec postgres -- psql -U nvoi -d nvoi -tAc "SELECT COUNT(*) FROM visits"
	//     weblogs: kubectl -- logs deploy/web -f
	//
	// Bodies tokenize via utils.ShellSplit (single + double quotes,
	// no escapes, no $VAR interpolation — runtime values come from
	// the verbs the alias expands to, not from the alias layer).
	Aliases map[string]string `yaml:"aliases,omitempty"`

	// Domains maps a service name to its public hostnames. Each key
	// must exist in services:. CF tunnel + per-domain CNAMEs activate
	// implicitly whenever Domains is non-empty (no `providers.dns` or
	// `providers.ingress` toggle — Cloudflare is the only DNS+tunnel
	// path in this substrate).
	Domains Domains `yaml:"domains,omitempty"`

	// HA is the opt-in apiserver high-availability flag.
	//
	//   unset / false (default):
	//     Single master. Validator REQUIRES exactly 1 master. Workers
	//     join via the master's private IP. Hardware failure = sub-90s
	//     rebuild from R2-stored tfstate (Hetzner per-server SLA
	//     ~99.9%). No load balancer. The 90% case.
	//
	//   true:
	//     Validator REQUIRES an odd master count ≥3 (etcd quorum:
	//     3/5/7…). Provisions a private-only hcloud LB on port 6443
	//     fronting the master pool; secondary masters + workers join
	//     via the LB IP. Apiserver failover is cloud-router-mediated
	//     (~5s LB health check); etcd tolerates ⌊(N-1)/2⌋ master
	//     failures. ~5€/month per cluster for the LB. The opt-in
	//     production path.
	HA bool `yaml:"ha,omitempty"`
}

// RegistryDef holds pull credentials for a single private container
// registry. Username and Password may be literal values or `$VAR`
// references resolved at the cmd/ boundary from os.Getenv.
type RegistryDef struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// DatabaseSpec is the per-database YAML shape. Engine-agnostic on
// purpose — each engine reads the fields it cares about and the
// validator rejects fields that don't apply to the declared engine.
//
//	postgres (selfhosted, ZFS-LocalPV — reference impl, v1):
//	  engine: postgres, version, server, size, credentials, backup
//
// Future SaaS engines (managed MySQL/Postgres, edge-replicated SQLite,
// etc.) ride the same shape — `region`, `instance_class`, and `backup`
// fields are reserved for them. Each engine lands as its own provider
// under pkg/providers/<engine>/ when there's demand.
//
// `backup:` is universal — when set, the reconciler provisions the
// per-DB backup bucket on providers.storage and emits the shared
// backup CronJob via cmd/db. providers.storage is mandatory when any
// database carries `backup:`; the validator enforces it.
type DatabaseSpec struct {
	Engine        string              `yaml:"engine"`
	Version       string              `yaml:"version,omitempty"`
	Server        string              `yaml:"server,omitempty"`
	Size          int                 `yaml:"size,omitempty"`
	Region        string              `yaml:"region,omitempty"`
	InstanceClass string              `yaml:"instance_class,omitempty"`
	Credentials   *DatabaseCredsSpec  `yaml:"credentials,omitempty"`
	Backup        *DatabaseBackupSpec `yaml:"backup,omitempty"`
}

// DatabaseCredsSpec carries the operator's chosen credentials for a
// selfhosted database (postgres only). Every field accepts a literal
// or a `$VAR` reference resolved at the cmd/ boundary from os.Getenv.
//
// Rejected on SaaS engines — they own their credentials at the vendor
// API level; nvoi reads them out of tofu output and writes them into
// the credentials Secret without operator-supplied material.
type DatabaseCredsSpec struct {
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Database string `yaml:"database"`
}

// DatabaseBackupSpec configures the scheduled-backup CronJob the
// reconciler emits. The bucket itself (`nvoi-{app}-{env}-db-{name}-
// backups`) is provisioned implicitly on providers.storage. Retention
// is days, applied as a bucket-lifecycle policy by BucketProvider.
//
// Backups are gzipped logical dumps in the bucket (key shape:
// YYYYMMDDTHHMMSSZ.sql.gz). The cmd/db image dumps via pg_dump (or
// the engine's equivalent when more engines land) and restores by
// piping the inverse — uniform across every engine.
type DatabaseBackupSpec struct {
	Schedule  string `yaml:"schedule"`
	Retention int    `yaml:"retention"`
}

// ServiceSpec describes one workload to deploy. Image is required —
// either pre-built (`image: nginx:1.27-alpine`) or paired with `build:`
// for code we compile locally and push.
//
// Storage presence is the implicit StatefulSet discriminator: when set,
// the workload becomes a StatefulSet with a VolumeClaimTemplate (k3s
// `local-path` storage class on hostPath) and the corresponding Service
// becomes headless. Without storage, the workload is a Deployment with
// a ClusterIP Service.
//
// Servers controls node placement:
//   - empty       → defaults to ["master"] inside applyNodePlacement
//   - 1 entry     → nodeSelector on nvoi-role=<key>
//   - 2+ entries  → nodeAffinity (Required, In) + topologySpreadConstraints
//
// Secrets is the per-service whitelist of names from the top-level
// `secrets:` list that get injected into Container.Env via
// secretKeyRef pointing at the shared `nvoi-secrets` Secret.
type ServiceSpec struct {
	Image    string       `yaml:"image"`
	Build    *BuildSpec   `yaml:"build,omitempty"`
	Port     int          `yaml:"port"`
	Replicas *int         `yaml:"replicas,omitempty"` // nil → default 1
	Storage  *StorageSpec `yaml:"storage,omitempty"`
	Servers  []string     `yaml:"servers,omitempty"`
	Secrets  []string     `yaml:"secrets,omitempty"`

	// Databases binds this service's pod env to one or more databases
	// declared at the top level. Each entry is `<PREFIX>=<dbname>`
	// (e.g. `DATABASE=app`, `REPORTS=analytics`); the prefix expands
	// to the canonical 5-var bundle wired through SecretKeyRef:
	//
	//   <PREFIX>_URL      ← credentials Secret key `url`
	//   <PREFIX>_HOST     ← `host`
	//   <PREFIX>_PORT     ← `port`
	//   <PREFIX>_USER     ← `user`
	//   <PREFIX>_PASSWORD ← `password`
	//
	// Default prefix is `DATABASE` when the operator writes just the
	// database name (`databases: [app]`). Validator rejects duplicate
	// prefixes within one service and prefixes referencing undeclared
	// databases. Secrets normalization is non-negotiable: every
	// engine writes the same key shape into the credentials Secret;
	// apps depend on the canonical env-var names.
	Databases []string `yaml:"databases,omitempty"`
}

// StorageSpec is the per-service persistent volume request. Two fields
// only — size + mountPath. StorageClass is intentionally absent: k3s
// ships with `local-path` as the default class (Rancher's
// local-path-provisioner, hostPath-backed), so leaving the class
// unspecified picks it up implicitly. The day a different class lands
// (longhorn, scaleway-csi, …) we add a tiny `class:` field; until
// then the surface stays minimal.
type StorageSpec struct {
	Size      string `yaml:"size"`      // e.g. "10Gi" — passed verbatim to resource.MustParse
	MountPath string `yaml:"mountPath"` // e.g. "/var/lib/postgresql/data"
}

type Providers struct {
	Infra string `yaml:"infra"`

	// Storage is OPTIONAL. When unset, tofu state lives locally
	// in `.tf/<app>-<env>/terraform.tfstate`. When set, the bucket
	// `nvoi-{app}-{env}-tfstate` is auto-provisioned at deploy time
	// and tofu's `s3` backend points at it. Transitions both
	// directions auto-migrate state.
	Storage string `yaml:"storage,omitempty"`
}

// Domains maps service names to public hostnames. Each service must
// already exist in cfg.Services. When non-empty, the CF DNS+tunnel
// emitter activates implicitly — Cloudflare is the only DNS+tunnel
// path in this substrate, so there's no provider toggle.
type Domains map[string][]string

// ServerSpec describes one server. Role is `master` or `worker`.
// Primary marks the master that runs `--cluster-init` on cold-start
// bootstrap (writes the etcd cluster). Required only when ≥2 masters;
// implicit on a single master.
type ServerSpec struct {
	Type    string `yaml:"type"`
	Region  string `yaml:"region"`
	Role    string `yaml:"role"`
	Primary bool   `yaml:"primary,omitempty"`
}

// BuildSpec describes how to build a service's image locally before
// the deploy proceeds. Three YAML shapes accepted:
//
//	build: true                                 # Context=".", Dockerfile="Dockerfile"
//	build: ./services/api                       # Context="./services/api"
//	build: {context: ./api, dockerfile: prod.Dockerfile}
type BuildSpec struct {
	Context    string `yaml:"context"`
	Dockerfile string `yaml:"dockerfile"`
}

// UnmarshalYAML accepts bool | string | mapping for `build:`.
func (b *BuildSpec) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		if node.Tag == "!!bool" {
			if node.Value == "true" {
				b.Context = "."
				b.Dockerfile = "Dockerfile"
			}
			return nil
		}
		// scalar string — context path; dockerfile defaults
		b.Context = node.Value
		b.Dockerfile = "Dockerfile"
		return nil
	case yaml.MappingNode:
		var raw struct {
			Context    string `yaml:"context"`
			Dockerfile string `yaml:"dockerfile"`
		}
		if err := node.Decode(&raw); err != nil {
			return err
		}
		b.Context = raw.Context
		if b.Context == "" {
			b.Context = "."
		}
		b.Dockerfile = raw.Dockerfile
		if b.Dockerfile == "" {
			b.Dockerfile = "Dockerfile"
		}
		return nil
	}
	return fmt.Errorf("build: unexpected yaml kind (%v)", node.Kind)
}

// HasBuild reports whether this service should be built locally.
// True when build: is set with a non-empty context.
func (s ServiceSpec) HasBuild() bool { return s.Build != nil && s.Build.Context != "" }

// IsStateful reports whether the service requires a StatefulSet.
// Implicit discriminator: storage presence flips the kind.
func (s ServiceSpec) IsStateful() bool { return s.Storage != nil }

// ImageHost returns the registry host of the service's image
// (e.g. "ghcr.io" for "ghcr.io/myorg/api:v1"). Returns "" for bare
// shortnames like "nginx" or "alpine" — those resolve to docker.io
// implicitly but we don't classify them as having a registry host.
func (s ServiceSpec) ImageHost() string {
	img := s.Image
	if i := strings.IndexByte(img, '/'); i >= 0 {
		host := img[:i]
		if strings.ContainsAny(host, ".:") {
			return host // hostname or host:port
		}
	}
	return ""
}

// PrimaryMaster returns the YAML key of the master that runs --cluster-init
// on cold-start bootstrap. For multi-master clusters that's the one with
// `primary: true`. For single-master clusters it's the lone master.
// Caller may assume Validate has already succeeded.
func (c *Config) PrimaryMaster() string {
	var lone string
	for name, srv := range c.Servers {
		if srv.Role != "master" {
			continue
		}
		if srv.Primary {
			return name
		}
		lone = name
	}
	return lone // single-master implicit
}

// MasterCount returns the number of servers with role=master. Used by
// HA decisions in the install pipeline read this.
func (c *Config) MasterCount() int {
	n := 0
	for _, srv := range c.Servers {
		if srv.Role == "master" {
			n++
		}
	}
	return n
}

