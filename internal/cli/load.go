package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/getnvoi/core/pkg/log"
	"github.com/getnvoi/core/pkg/naming"
	"github.com/getnvoi/core/pkg/providers"
	"github.com/getnvoi/core/pkg/runtime"
	"github.com/getnvoi/core/pkg/state"
	"github.com/getnvoi/core/pkg/workload"
)

func PrepareRuntime(ctx context.Context, flags runtime.Flags, lg log.Log) (*runtime.Runtime, error) {
	if envPath := ResolveDotEnv(flags.ConfigPath); envPath != "" {
		if err := LoadDotEnv(envPath); err != nil {
			return nil, err
		}
	}
	cfg, ok := CachedConfig(flags.ConfigPath)
	if !ok {
		loaded, err := LoadConfig(flags.ConfigPath)
		if err != nil {
			return nil, err
		}
		cfg = loaded
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("user home: %w", err)
	}
	pubKey, privKey, err := ReadSSHKeys(cfg.SSHKey, home)
	if err != nil {
		return nil, err
	}
	registryCreds := workload.ResolveRegistryCreds(cfg.Registry, os.Getenv)
	secrets, err := ResolveSecrets(cfg.Secrets, os.Getenv)
	if err != nil {
		return nil, err
	}
	// Database credentials reference $VARs the operator hasn't
	// necessarily duplicated under top-level `secrets:`. Resolve them
	// here at the boundary and merge into the same map so the deploy
	// reconciler reads ONE secrets source. Hard-error semantics match
	// ResolveSecrets — every missing var surfaces in one error.
	dbSecrets, err := ResolveDatabaseSecrets(cfg, os.Getenv)
	if err != nil {
		return nil, err
	}
	if len(dbSecrets) > 0 {
		if secrets == nil {
			secrets = map[string]string{}
		}
		for k, v := range dbSecrets {
			secrets[k] = v
		}
	}
	providerInputs := ResolveProviderInputs(os.Getenv)

	// Tunnel prerequisite: CF_TUNNEL_SECRET is operator-supplied
	// (32 random bytes, base64). Pinned here at the cmd/cli boundary
	// so the missing-secret failure surfaces ONCE, before any tofu
	// invocation — not as a downstream "Missing required argument" at
	// plan time. CF DNS+tunnel activates implicitly whenever
	// cfg.Domains is non-empty (no provider toggle).
	if len(cfg.Domains) > 0 {
		if providerInputs.Cloudflare == nil || providerInputs.Cloudflare.TunnelSecret == "" {
			return nil, fmt.Errorf("domains: requires CF_TUNNEL_SECRET env var (32-byte base64 string; operator-generated and stored — nvoi does not mint it)")
		}
	}

	var backend *state.Backend
	var bp providers.BucketProvider
	if cfg.Providers.Storage != "" {
		bucketCreds, err := ResolveBucketCreds(cfg.Providers.Storage, os.Getenv)
		if err != nil {
			return nil, err
		}
		bp, err = providers.ResolveBucket(cfg.Providers.Storage, bucketCreds)
		if err != nil {
			return nil, err
		}
		backend, err = ConfigureState(ctx, cfg, bp, lg)
		if err != nil {
			return nil, err
		}
	}

	return runtime.Build(ctx, runtime.Inputs{
		Config: cfg,
		Log:    lg,
		Flags:  flags,
		Paths: runtime.Paths{
			WorkDir:  naming.WorkDir(cfg.App, cfg.Env),
			CacheDir: filepath.Join(home, ".cache", naming.CacheDirSegment),
		},
		SSH: runtime.SSHMaterial{
			PublicKey:  pubKey,
			PrivateKey: privKey,
		},
		DeployHash:    time.Now().UTC().Format("20060102-150405"),
		StateBackend:  backend,
		StorageBucket: bp,
		Secrets:       secrets,
		RegistryCreds: registryCreds,
		Providers:     providerInputs,
	})
}
