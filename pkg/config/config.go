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

	"github.com/getnvoi/core/pkg/providers"
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

	// Domains maps a service name to its public hostnames. Requires
	// providers.dns when non-empty. Each key must exist in services:.
	// Ingress flows: master 80/443 → in-cluster Caddy → Service.
	Domains Domains `yaml:"domains,omitempty"`

	// ACMEEmail is the contact address Caddy registers with Let's
	// Encrypt. Optional — when empty, BuildCaddyConfig falls back to
	// `acme@<first-domain>`. Real operators want a real address so
	// LE rate-limit and expiration warnings reach a human.
	ACMEEmail string `yaml:"acme_email,omitempty"`

	// Monitor activates the observability stack (Prometheus + Thanos +
	// Loki + Grafana + dashboards + alert rules + contact points)
	// when non-nil. Presence is the toggle — same convention as
	// `build:` / `storage:` / `domains:` everywhere else. Empty
	// `monitor: {}` is the minimum valid form (stack on, tunnel-only
	// access via `nvoi monitor`, no alert routing).
	Monitor *MonitorSpec `yaml:"monitor,omitempty"`
}

// MonitorSpec configures the observability stack. Tunnel-only access
// is the default — operators reach Grafana via `nvoi monitor` (SSH
// port-forward). Set Domain to also expose Grafana over a public
// Ingress with cert-manager-issued TLS.
//
// AdminPassword is required when Domain is set (public exposure must
// have real auth). Tunnel-only mode runs Grafana with anonymous
// viewer access — no admin password needed because the SSH tunnel is
// the access control.
//
// Both Domain and AdminPassword accept literal values or `$VAR`
// references resolved at the cmd/cli boundary.
type MonitorSpec struct {
	Domain        string      `yaml:"domain,omitempty"`
	AdminPassword string      `yaml:"admin_password,omitempty"`
	Alerts        *AlertsSpec `yaml:"alerts,omitempty"`

	// Dashboards lists glob paths (relative to the config file) to
	// Grafana dashboard JSON files. Each match becomes one ConfigMap
	// the Grafana sidecar provisions. Empty → no dashboards (operator
	// can still explore raw metrics via Grafana's Explore tab).
	//
	// nvoi ships reference dashboards under examples/dashboards/ —
	// operators copy + customize, OR pull from grafana.com.
	Dashboards []string `yaml:"dashboards,omitempty"`

	// AlertRules lists glob paths to Grafana alert-rule provisioning
	// YAML files. Each match becomes a file in Grafana's
	// /etc/grafana/provisioning/alerting/ directory. Empty → no alert
	// rules (only datasources + contact points come from nvoi).
	//
	// nvoi ships reference alert rules under examples/alerts/.
	AlertRules []string `yaml:"alert_rules,omitempty"`
}

// AlertsSpec configures notification channels. Each non-nil field is
// provisioned as a Grafana contact point + folded into the default
// notification policy (every firing alert routes through every
// configured channel for v1). When AlertsSpec is nil, alerts fire to
// the Grafana dashboard only — operators see them on the home page,
// no external notification.
//
// Slack is a plain webhook URL (single-vendor field, no provider
// abstraction). Email and SMS dispatch through registered providers
// (postmark, twilio, …) — same registry pattern as BucketProvider.
type AlertsSpec struct {
	Slack string               `yaml:"slack,omitempty"`
	Email *providers.AlertSpec `yaml:"email,omitempty"`
	SMS   *providers.AlertSpec `yaml:"sms,omitempty"`
}

// RegistryDef holds pull credentials for a single private container
// registry. Username and Password may be literal values or `$VAR`
// references resolved at the cmd/ boundary from os.Getenv.
type RegistryDef struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
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

	// DNS is REQUIRED when Domains is non-empty. Today: cloudflare.
	// The named provider's emitter writes tofu resources for the
	// domain → master IP bindings; tofu owns the lifecycle (drift
	// detection, deletion) — there are no runtime API calls from
	// nvoi to the DNS provider.
	DNS string `yaml:"dns,omitempty"`
}

// Domains maps service names to public hostnames. Each service must
// already exist in cfg.Services. When non-empty, providers.dns is
// required (and the chosen DNS backend's emitter writes the records
// during tf-apply).
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
