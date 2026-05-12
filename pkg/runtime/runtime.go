// Package runtime is the per-invocation context. One struct — Runtime —
// holds everything resolved at the cmd/ boundary that internal packages
// might need beyond the YAML itself: log sink, work directory, cache
// directory, resolved SSH public key bytes, resolved registry creds,
// explicit provider inputs, future deploy hash, …
//
// Built once by Build() in PersistentPreRunE; read-only thereafter.
// NEVER stores a context.Context — ctx is always a function parameter.
//
// Per-stage artifacts (compile.Bundle, runner.Runner, future kube
// clients) are NOT fields here. They are produced by stage constructors
// and live as locals in the cmd lifecycle.
//
// Build is pure assembly. ALL OS interaction (env, disk reads, home
// resolution) happens at the cmd/ boundary and arrives via Inputs.
package runtime

import (
	"context"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/log"
	"github.com/getnvoi/core/pkg/providers"
	"github.com/getnvoi/core/pkg/state"
)

// Flags is the typed view of cobra's persistent flags. Constructed in
// cmd/cli, copied onto Runtime so internal packages that need a flag
// access via rt.Flags.X.
type Flags struct {
	ConfigPath string
	JSON       bool
}

type Paths struct {
	WorkDir  string
	CacheDir string
}

type SSHMaterial struct {
	PublicKey  []byte // pre-read, trimmed, non-empty — for cloud-init injection
	PrivateKey []byte // pre-read PEM bytes — for SSH dial during install
}

type ProviderInputs struct {
	Cloudflare *CloudflareInputs
	Hetzner    *HetznerInputs
}

type CloudflareInputs struct {
	APIToken  string
	APIKey    string
	AccountID string
	ZoneID    string
	Zone      string

	// TunnelSecret is the operator-supplied 32-byte base64 string that
	// seeds the cloudflare_zero_trust_tunnel_cloudflared resource.
	// Required when providers.ingress=cloudflare (validated at cmd/cli
	// boundary). Operator-owned: nvoi does NOT generate this — keeps
	// the trust surface explicit and the random_id resource out of
	// tofu state. Source: CF_TUNNEL_SECRET env var (typically piped
	// from a secret manager into .env).
	TunnelSecret string
}

type HetznerInputs struct {
	Token string
}

// Inputs are the boundary-resolved values cmd/cli or library callers
// pass to Build. Every disk read, env lookup, home expansion happens
// before this struct is constructed.
type Inputs struct {
	Config *config.Config
	Log    log.Log
	Flags  Flags

	Paths Paths
	SSH   SSHMaterial

	// DeployHash is the per-deploy tag fragment (YYYYMMDD-HHMMSS UTC).
	// Stamped on built image tags AND every nvoi-managed workload's
	// metadata so PodSpec image strings change per deploy → rolling
	// updates auto-trigger. Computed once at deploy start; same value
	// flows through build phase + workload phase.
	DeployHash string

	// StateBackend is set when providers.storage is configured. nil
	// means tofu stores state locally in .tf/<app>-<env>/terraform.tfstate.
	// Set means tofu's s3 backend points at the bucket.
	StateBackend *state.Backend

	// Secrets is the resolved name→value map for every entry in
	// `cfg.Secrets`. Missing or empty values should already have failed
	// at the boundary before Build runs.
	Secrets map[string]string

	// RegistryCreds holds resolved registry auth keyed by host. The
	// declarative config model stays untouched; build/workload consume
	// the literal values from here.
	RegistryCreds map[string]config.RegistryDef

	// Providers carries any provider-specific values that would
	// otherwise come from ambient process state.
	Providers ProviderInputs

	// Monitor holds the env-resolved monitor block, or nil when YAML
	// didn't set `monitor:`. $VAR references in AdminPassword, Slack
	// URL, and per-AlertSpec Fields are resolved against os.Getenv at
	// the cmd/cli boundary BEFORE landing here. Internal packages
	// consume the resolved view; they never see $VAR strings.
	Monitor *ResolvedMonitor
}

// Runtime is the bag every internal package accepts when it needs more
// than just the YAML.
type Runtime struct {
	Cfg           *config.Config
	Flags         Flags
	Log           log.Log
	SSHPubKey     []byte
	SSHPrivKey    []byte
	CacheDir      string
	WorkDir       string
	DeployHash    string
	Backend       *state.Backend
	SecretValues  map[string]string
	RegistryCreds map[string]config.RegistryDef
	Providers     ProviderInputs

	// Monitor is the env-resolved observability config, mirroring
	// cfg.Monitor with $VAR refs substituted. nil when YAML did not
	// set `monitor:`. Consumers in pkg/internal/observability and
	// pkg/deploy read this; nobody mutates it.
	Monitor *ResolvedMonitor
}

// ResolvedMonitor mirrors config.MonitorSpec with $VAR refs in
// AdminPassword + Alerts.Slack + Alerts.Email.Fields + Alerts.SMS.Fields
// substituted to concrete values. Domain is copied as-is (never a
// $VAR reference — hostnames are literal).
type ResolvedMonitor struct {
	Domain        string
	AdminPassword string
	Alerts        *ResolvedAlerts

	// Dashboards holds the operator's resolved Grafana dashboard
	// files — one entry per file expanded from cfg.Monitor.Dashboards
	// globs at the cmd/cli boundary. Content is the raw JSON; Name
	// is the filename basename (used as the ConfigMap data key,
	// e.g. "nvoi.json").
	Dashboards []NamedFile

	// AlertRules holds the operator's resolved Grafana alert-rule
	// provisioning YAML files. Same shape as Dashboards.
	AlertRules []NamedFile
}

// NamedFile pairs a basename with its content. Used for operator-
// supplied dashboard JSON + alert YAML files threaded from the
// cmd/cli boundary through to the observability stack.
type NamedFile struct {
	Name    string
	Content []byte
}

// ResolvedAlerts mirrors config.AlertsSpec with all $VAR refs
// resolved. Slack is the resolved webhook URL. Email/SMS reuse the
// providers.AlertSpec shape (provider name + Fields map) — the
// "resolved" property is a runtime invariant: Fields values have
// been walked and substituted before reaching here. Providers'
// BuildReceiver assume resolved values; never see $VAR strings.
type ResolvedAlerts struct {
	Slack string
	Email *providers.AlertSpec
	SMS   *providers.AlertSpec
}

// Build is pure assembly. ctx is accepted for future callers
// (credential backends, network probes); none of today's resolution
// needs it, but the signature is the contract going forward.
func Build(ctx context.Context, in Inputs) (*Runtime, error) {
	_ = ctx
	return &Runtime{
		Cfg:           in.Config,
		Flags:         in.Flags,
		Log:           in.Log,
		SSHPubKey:     in.SSH.PublicKey,
		SSHPrivKey:    in.SSH.PrivateKey,
		CacheDir:      in.Paths.CacheDir,
		WorkDir:       in.Paths.WorkDir,
		DeployHash:    in.DeployHash,
		Backend:       in.StateBackend,
		SecretValues:  in.Secrets,
		RegistryCreds: in.RegistryCreds,
		Providers:     in.Providers,
		Monitor:       in.Monitor,
	}, nil
}
