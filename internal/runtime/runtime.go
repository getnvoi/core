// Package runtime is the per-invocation context. One struct — Runtime —
// holds everything resolved at the cmd/ boundary that internal packages
// might need beyond the YAML itself: log sink, work directory, cache
// directory, resolved SSH public key bytes, future credential source,
// future deploy hash, …
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

	"github.com/getnvoi/core/internal/config"
	"github.com/getnvoi/core/internal/log"
	"github.com/getnvoi/core/internal/naming"
	"github.com/getnvoi/core/internal/state"
)

// Flags is the typed view of cobra's persistent flags. Constructed in
// cmd/cli, copied onto Runtime so internal packages that need a flag
// access via rt.Flags.X.
type Flags struct {
	ConfigPath string
	JSON       bool
}

// Inputs are the boundary-resolved values cmd/cli passes to Build.
// Every disk read, env lookup, home expansion happens before this
// struct is constructed.
type Inputs struct {
	Cfg        *config.Config
	Flags      Flags
	Log        log.Log
	SSHPubKey  []byte // pre-read, trimmed, non-empty — for cloud-init injection
	SSHPrivKey []byte // pre-read PEM bytes — for SSH dial during install
	CacheDir   string // absolute, terraform binary cache

	// DeployHash is the per-deploy tag fragment (YYYYMMDD-HHMMSS UTC).
	// Stamped on built image tags AND every nvoi-managed workload's
	// metadata so PodSpec image strings change per deploy → rolling
	// updates auto-trigger. Computed once at deploy start; same value
	// flows through build phase + workload phase.
	DeployHash string

	// Backend is set when providers.storage is configured. nil means
	// terraform stores state locally in .tf/<app>-<env>/terraform.tfstate.
	// Set means terraform's s3 backend points at the bucket.
	Backend *state.Backend

	// Secrets is the resolved name→value map for every entry in
	// `cfg.Secrets`. Populated at the cmd/ boundary against
	// os.Getenv (today's only source — CredentialSource backends are
	// upper-layer). Missing or empty values cause the boundary to
	// hard-error before runtime.Build runs; downstream code can
	// trust every requested name has a non-empty literal here.
	//
	// Workload apply renders this into a single Opaque Secret named
	// `nvoi-secrets`; per-service `secrets:` lists drive
	// secretKeyRef-based env injection into each PodSpec.
	Secrets map[string]string
}

// Runtime is the bag every internal package accepts when it needs more
// than just the YAML. Fields grow as we port more nvoi pieces (Creds,
// GitRemote, DeployHash, …).
type Runtime struct {
	Cfg        *config.Config
	Flags      Flags
	Log        log.Log
	SSHPubKey  []byte
	SSHPrivKey []byte
	CacheDir   string
	WorkDir    string
	DeployHash string
	Backend    *state.Backend    // nil = local state; non-nil = remote on configured bucket
	Secrets    map[string]string // resolved top-level secrets — see Inputs.Secrets
}

// Build is pure assembly. ctx is accepted for future callers
// (credential backends, network probes); none of today's resolution
// needs it, but the signature is the contract going forward.
func Build(ctx context.Context, in Inputs) (*Runtime, error) {
	_ = ctx
	return &Runtime{
		Cfg:        in.Cfg,
		Flags:      in.Flags,
		Log:        in.Log,
		SSHPubKey:  in.SSHPubKey,
		SSHPrivKey: in.SSHPrivKey,
		CacheDir:   in.CacheDir,
		WorkDir:    naming.WorkDir(in.Cfg.App, in.Cfg.Env),
		DeployHash: in.DeployHash,
		Backend:    in.Backend,
		Secrets:    in.Secrets,
	}, nil
}
