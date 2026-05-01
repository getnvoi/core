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
	providerInputs := ResolveProviderInputs(os.Getenv)

	var backend *state.Backend
	if cfg.Providers.Storage != "" {
		bucketCreds, err := ResolveBucketCreds(cfg.Providers.Storage, os.Getenv)
		if err != nil {
			return nil, err
		}
		bp, err := providers.ResolveBucket(cfg.Providers.Storage, bucketCreds)
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
		Secrets:       secrets,
		RegistryCreds: registryCreds,
		Providers:     providerInputs,
	})
}
