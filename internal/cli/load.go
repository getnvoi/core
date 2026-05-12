package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/getnvoi/core/pkg/log"
	"github.com/getnvoi/core/pkg/naming"
	"github.com/getnvoi/core/pkg/providers"
	"github.com/getnvoi/core/pkg/runtime"
	"github.com/getnvoi/core/pkg/state"
	"github.com/getnvoi/core/pkg/store"
	"github.com/getnvoi/core/pkg/store/keyring"
	"github.com/getnvoi/core/pkg/workload"
)

// PrepareRuntime is the cmd/cli boundary that turns Flags into a
// *runtime.Runtime. Branches by mode:
//
//   - YAML mode (default): ConfigPath set, DatabasePath empty.
//     Reads nvoi.yaml + .env, identical to historical behaviour.
//
//   - Store mode: DatabasePath + ProjectName set, ConfigPath empty.
//     Resolves the master key via Keyring, opens the SQLite store,
//     pulls Config + decrypted secrets for the named project.
//
// The two modes are mutually exclusive per invocation; both produce
// a Runtime that downstream verbs (deploy/plan/destroy/ssh/exec/logs)
// consume identically.
func PrepareRuntime(ctx context.Context, flags runtime.Flags, lg log.Log) (*runtime.Runtime, error) {
	if flags.DatabasePath != "" && flags.ConfigPath != "" {
		return nil, errors.New("--database and --config are mutually exclusive")
	}
	// Smart defaults: pick yaml mode if nvoi.yaml exists, store mode
	// if ~/.nvoi/db.sqlite exists, yaml mode otherwise. When BOTH
	// exist, yaml wins and we warn the operator.
	cfgPath, dbPath, bothExisted, err := ApplySmartDefaults(flags.ConfigPath, flags.DatabasePath)
	if err != nil {
		return nil, err
	}
	if bothExisted {
		dbDefault, _ := DefaultDatabasePath()
		lg.Warn(fmt.Sprintf(
			"both nvoi.yaml and %s present; defaulting to YAML mode. "+
				"Pass --database %s --project <name> to use the store instead.",
			dbDefault, dbDefault,
		))
	}
	flags.ConfigPath = cfgPath
	flags.DatabasePath = dbPath

	if flags.DatabasePath != "" {
		expanded, err := ExpandHome(flags.DatabasePath)
		if err != nil {
			return nil, err
		}
		flags.DatabasePath = expanded
		if flags.ProjectName == "" {
			return nil, fmt.Errorf("--database %s requires --project <name> (run `nvoi projects` to list)", flags.DatabasePath)
		}
		return prepareRuntimeFromStore(ctx, flags, lg)
	}
	return prepareRuntimeFromYAML(ctx, flags, lg)
}

// prepareRuntimeFromYAML is the historical PrepareRuntime body. Reads
// .env + nvoi.yaml + os.Environ for every credential.
func prepareRuntimeFromYAML(ctx context.Context, flags runtime.Flags, lg log.Log) (*runtime.Runtime, error) {
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

// prepareRuntimeFromStore loads everything from the SQLite store
// (Config row + decrypted secrets) and assembles a Runtime identical
// in shape to the YAML path. The existing Resolve* helpers are reused
// by feeding them a getter that reads from the decrypted secrets map
// — same semantics as os.Getenv ("" for missing).
func prepareRuntimeFromStore(ctx context.Context, flags runtime.Flags, lg log.Log) (*runtime.Runtime, error) {
	kr, err := keyring.Resolve(ctx, flags.Keyring, flags.DatabasePath)
	if err != nil {
		return nil, err
	}
	key, err := kr.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("keyring (%s): %w", kr.Locator(), err)
	}
	if key == nil {
		return nil, fmt.Errorf("keyring (%s): no master key found", kr.Locator())
	}
	st, err := store.Open(ctx, flags.DatabasePath, key)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	defer st.Close()

	proj, err := st.GetProject(ctx, flags.ProjectName)
	if err != nil {
		return nil, fmt.Errorf("store: load project %q: %w", flags.ProjectName, err)
	}
	cfg, err := proj.ParsedConfig()
	if err != nil {
		return nil, err
	}
	secretsMap, err := st.AllSecretsAsMap(ctx, proj.ID)
	if err != nil {
		return nil, err
	}
	// getFromStore mirrors os.Getenv semantics: returns "" for missing
	// keys, the decrypted value otherwise. Lets us reuse every existing
	// Resolve* helper unchanged.
	getFromStore := func(name string) string { return secretsMap[name] }

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("user home: %w", err)
	}
	pubKey, privKey, err := ReadSSHKeys(cfg.SSHKey, home)
	if err != nil {
		return nil, err
	}
	registryCreds := workload.ResolveRegistryCreds(cfg.Registry, getFromStore)
	declaredSecrets, err := ResolveSecrets(cfg.Secrets, getFromStore)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	providerInputs := ResolveProviderInputs(getFromStore)

	var backend *state.Backend
	if cfg.Providers.Storage != "" {
		bucketCreds, err := ResolveBucketCreds(cfg.Providers.Storage, getFromStore)
		if err != nil {
			return nil, fmt.Errorf("store: %w", err)
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
		Secrets:       declaredSecrets,
		RegistryCreds: registryCreds,
		Providers:     providerInputs,
	})
}
