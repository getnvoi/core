package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	internalcli "github.com/getnvoi/core/internal/cli"
	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/providers"
	"github.com/getnvoi/core/pkg/store"
)

func importCmd(r *rt) *cobra.Command {
	return &cobra.Command{
		Use:         "import",
		Short:       "Import a yaml-based project into the store",
		Annotations: skipRuntime(),
		GroupID:     groupStore,
		Long: `Reads an nvoi.yaml (via the persistent -c flag) plus the .env
alongside it, creates a project in the store, and persists every secret
referenced by the config (declared secrets, provider env vars, registry
$VARs) as encrypted rows.

Idempotent: re-running updates the project's config and refreshes any
secrets present in the current .env. Secrets already in the store but
absent from the new .env are LEFT untouched (use ` + "`nvoi secret unset`" + `
to remove them).`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if r.flags.ConfigPath == "" {
				return errors.New("-c <path> required (path to nvoi.yaml to import from)")
			}
			if err := requireProject(r.flags); err != nil {
				return err
			}
			return runImport(cmd.Context(), r, r.flags.ConfigPath)
		},
	}
}

func runImport(ctx context.Context, r *rt, yamlPath string) error {
	// Load yaml + .env (yaml-mode boundary helpers).
	if envPath := internalcli.ResolveDotEnv(yamlPath); envPath != "" {
		if err := internalcli.LoadDotEnv(envPath); err != nil {
			return err
		}
	}
	cfg, err := config.LoadFile(yamlPath)
	if err != nil {
		return err
	}

	st, _, err := openStore(ctx, r.flags)
	if err != nil {
		return err
	}
	defer st.Close()

	// Create or update the project row.
	existing, err := st.GetProject(ctx, r.flags.ProjectName)
	switch {
	case err == nil:
		if err := st.UpdateProjectConfig(ctx, existing.ID, cfg); err != nil {
			return fmt.Errorf("update existing project: %w", err)
		}
		r.log.Info(fmt.Sprintf("project %q updated (existing id=%s)", r.flags.ProjectName, existing.ID))
	default:
		newProj, err := st.CreateProject(ctx, r.flags.ProjectName, cfg)
		if err != nil {
			return fmt.Errorf("create project: %w", err)
		}
		existing = newProj
		r.log.Info(fmt.Sprintf("project %q created (id=%s)", r.flags.ProjectName, existing.ID))
	}

	// Persist secrets: every name we'd ask os.Getenv for, ask it now
	// and store the value if non-empty.
	imported, skipped := importSecrets(ctx, st, existing.ID, cfg)
	r.log.Info(fmt.Sprintf("secrets imported: %d, skipped (unset in env): %d", imported, skipped))

	return nil
}

// importSecrets enumerates every credential name the yaml-mode boundary
// would resolve for this config (declared secrets, provider env vars,
// bucket creds, registry $VARs) and persists any whose env value is
// non-empty. Returns (imported, skipped) counts.
func importSecrets(ctx context.Context, st *store.Store, projectID string, cfg *config.Config) (int, int) {
	names := map[string]struct{}{}

	// 1. declared secrets
	for _, n := range cfg.Secrets {
		names[n] = struct{}{}
	}
	// 2. provider env vars — read what ResolveProviderInputs reads.
	for _, n := range []string{"CLOUDFLARE_API_TOKEN", "CF_API_KEY", "CF_ACCOUNT_ID", "CF_ZONE_ID", "CF_ZONE", "HCLOUD_TOKEN"} {
		names[n] = struct{}{}
	}
	// 3. bucket provider creds (only when configured)
	if cfg.Providers.Storage != "" {
		schema, err := providers.CredentialSchemaForBucket(cfg.Providers.Storage)
		if err == nil {
			for _, f := range schema.Fields {
				names[f.EnvVar] = struct{}{}
			}
		}
	}
	// 4. registry $VARs
	for _, def := range cfg.Registry {
		for _, ref := range []string{def.Username, def.Password} {
			if strings.HasPrefix(ref, "$") {
				names[strings.TrimPrefix(ref, "$")] = struct{}{}
			}
		}
	}

	imported, skipped := 0, 0
	for n := range names {
		v := os.Getenv(n)
		if v == "" {
			skipped++
			continue
		}
		if err := st.SetSecret(ctx, projectID, n, v); err == nil {
			imported++
		} else {
			skipped++
		}
	}
	return imported, skipped
}
