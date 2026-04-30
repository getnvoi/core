// Command tf is the entrypoint. Cobra wires the verbs; one file per
// verb under cmd/cli/. The blank imports below are how every provider
// registers itself with the compile registry.
//
// Layering — three structs, one direction:
//
//	internal/config   *Config   parsed YAML, read-only after Load
//	internal/runtime  *Runtime  per-invocation state, read-only after Build
//	per-stage         Bundle, Runner, soon SSH/Kube — locals in the lifecycle, never
//	                  stashed on Config or Runtime
//
// ctx is a function parameter end-to-end. Never a struct field.
// signal.NotifyContext binds SIGINT/SIGTERM in main(); cobra carries
// it via ExecuteContext; each verb threads it into runWith and the
// runner methods.
//
// OS reads (env, disk, home, file existence) live ONLY in this file
// (and sshkey.go). runtime.Build is pure assembly — internal packages
// are pure consumers.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/getnvoi/core/internal/config"
	"github.com/getnvoi/core/internal/log"
	"github.com/getnvoi/core/internal/naming"
	"github.com/getnvoi/core/internal/providers"
	"github.com/getnvoi/core/internal/runtime"
	"github.com/getnvoi/core/internal/state"
	"github.com/getnvoi/core/internal/workload"

	_ "github.com/getnvoi/core/internal/providers/cloudflare"
	_ "github.com/getnvoi/core/internal/providers/hetzner"
)

// rt is the cmd-local bag populated by PersistentPreRunE and closed
// over by every verb's RunE. The verbs read rt.cfg / rt.runtime, never
// rebuild them.
type rt struct {
	flags   runtime.Flags
	cfg     *config.Config
	runtime *runtime.Runtime
	log     log.Log
}

func newRoot(r *rt) *cobra.Command {
	root := &cobra.Command{
		Use:           "nvoi",
		Short:         "nvoi — YAML → Terraform → k3s",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVarP(&r.flags.ConfigPath, "config", "c", "nvoi.yaml", "path to YAML config")
	root.PersistentFlags().BoolVar(&r.flags.JSON, "json", false, "stream machine-readable JSONL output")

	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		// Build log FIRST so any subsequent error in this hook (and in
		// every verb's RunE) routes through it.
		r.log = log.New(r.flags.JSON)

		// Load .env BEFORE config so env vars (HCLOUD_TOKEN etc.) are in
		// process env when terraform-exec subprocess inherits.
		if envPath := resolveDotEnv(r.flags.ConfigPath); envPath != "" {
			if err := loadDotEnv(envPath); err != nil {
				return err
			}
		}

		// Boundary I/O — all OS interaction happens here.
		cfg, err := config.Load(r.flags.ConfigPath)
		if err != nil {
			return err
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("user home: %w", err)
		}
		pubKey, privKey, err := readSSHKeys(cfg.SSHKey, home)
		if err != nil {
			return err
		}

		// Resolve $VAR references in registry: creds against the
		// operator's environment. From here on, every internal package
		// sees literal usernames + passwords.
		cfg.Registry = workload.ResolveRegistryCreds(cfg.Registry, os.Getenv)

		// Resolve top-level secrets: against the operator's environment.
		// Empty values = hard error; the deploy can't continue without
		// them and we'd rather fail at the boundary than mid-apply.
		secrets, err := resolveSecrets(cfg.Secrets, os.Getenv)
		if err != nil {
			return err
		}

		// Optional remote-state backend. Validator already verified
		// the provider name is registered; here we resolve creds and
		// upsert the bucket.
		var backend *state.Backend
		if cfg.Providers.Storage != "" {
			bp, err := providers.ResolveBucket(cfg.Providers.Storage)
			if err != nil {
				return err
			}
			b, err := state.Configure(cmd.Context(), cfg.App, cfg.Env, bp, r.log)
			if err != nil {
				return err
			}
			backend = &b
		}

		// Pure assembly downstream.
		built, err := runtime.Build(cmd.Context(), runtime.Inputs{
			Cfg:        cfg,
			Flags:      r.flags,
			Log:        r.log,
			SSHPubKey:  pubKey,
			SSHPrivKey: privKey,
			CacheDir:   filepath.Join(home, ".cache", naming.CacheDirSegment),
			DeployHash: time.Now().UTC().Format("20060102-150405"),
			Backend:    backend,
			Secrets:    secrets,
		})
		if err != nil {
			return err
		}
		r.cfg, r.runtime = cfg, built
		return nil
	}

	root.AddCommand(deployCmd(r), planCmd(r), destroyCmd(r), sshCmd(r), kubectlCmd(r), execCmd(r))
	return root
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	var r rt
	if err := newRoot(&r).ExecuteContext(ctx); err != nil {
		// Once PersistentPreRunE has run at least far enough to build
		// the log, route through it. Otherwise (cobra-level flag parse
		// errors, --help shouldn't reach here) fall back to stderr.
		if r.log != nil {
			r.log.Error(err)
		} else {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
		os.Exit(1)
	}
}
