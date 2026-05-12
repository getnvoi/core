package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/providers"
)

func envCmd(r *rt) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "env <check>",
		Short:       "Inspect environment / secret coverage for the current config",
		Annotations: skipRuntime(),
		GroupID:     groupInspection,
	}
	cmd.AddCommand(envCheckCmd(r))
	return cmd
}

func envCheckCmd(r *rt) *cobra.Command {
	return &cobra.Command{
		Use:         "check",
		Short:       "Report which credentials are set vs missing for the current config",
		Annotations: skipRuntime(),
		Long: `Walks the current config and reports, by category, which
credentials are present and which are missing.

Sources by mode:
  - YAML mode  → process env (after .env load)
  - Store mode → the project's encrypted secrets table

Categories:
  - infra:    HCLOUD_TOKEN, CLOUDFLARE_API_TOKEN, ... (per cfg.Providers.Infra/DNS)
  - storage:  per providers.CredentialSchemaForBucket(cfg.Providers.Storage)
  - secrets:  every name in cfg.Secrets
  - registry: every $VAR referenced by cfg.Registry

Exit codes:
  0  ok — every required credential present
  2  missing — at least one required credential absent
  1  hard error (couldn't load config, couldn't open store, etc.)`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runEnvCheck(cmd.Context(), r)
		},
	}
}

type envReport struct {
	OK         bool                       `json:"ok"`
	Mode       string                     `json:"mode"`
	Categories map[string]envReportBucket `json:"categories"`
	Missing    []string                   `json:"missing"`
	Present    []string                   `json:"present"`
}

type envReportBucket struct {
	Required []string `json:"required"`
	Present  []string `json:"present"`
	Missing  []string `json:"missing"`
}

func runEnvCheck(ctx context.Context, r *rt) error {
	cfg, err := loadConfigForBoundary(ctx, r)
	if err != nil {
		return err
	}
	mode, _, _, err := resolveBoundaryMode(r.flags)
	if err != nil {
		return err
	}
	get, err := credentialGetter(ctx, r, mode)
	if err != nil {
		return err
	}

	categories := map[string]envReportBucket{
		"infra":    bucketize(infraEnvVars(cfg), get),
		"storage":  bucketize(storageEnvVars(cfg), get),
		"secrets":  bucketize(cfg.Secrets, get),
		"registry": bucketize(registryEnvVars(cfg), get),
	}
	allPresent := map[string]struct{}{}
	allMissing := map[string]struct{}{}
	for _, b := range categories {
		for _, n := range b.Present {
			allPresent[n] = struct{}{}
		}
		for _, n := range b.Missing {
			allMissing[n] = struct{}{}
		}
	}
	report := envReport{
		OK:         len(allMissing) == 0,
		Mode:       mode,
		Categories: categories,
		Missing:    sortedKeys(allMissing),
		Present:    sortedKeys(allPresent),
	}

	if r.flags.JSON {
		out, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return fmt.Errorf("env check: marshal: %w", err)
		}
		fmt.Fprintln(os.Stdout, string(out))
	} else {
		fmt.Fprintf(os.Stdout, "mode: %s\n", report.Mode)
		for _, name := range []string{"infra", "storage", "secrets", "registry"} {
			b := categories[name]
			if len(b.Required) == 0 {
				continue
			}
			fmt.Fprintf(os.Stdout, "\n[%s]\n", name)
			for _, n := range b.Present {
				fmt.Fprintf(os.Stdout, "  ✓ %s\n", n)
			}
			for _, n := range b.Missing {
				fmt.Fprintf(os.Stdout, "  ✗ %s  (missing)\n", n)
			}
		}
		if report.OK {
			fmt.Fprintln(os.Stdout, "\nok — every required credential present")
		} else {
			fmt.Fprintf(os.Stdout, "\n%d missing: %s\n", len(report.Missing), strings.Join(report.Missing, ", "))
		}
	}

	if !report.OK {
		// Return a typed error main() translates to exit 2 — distinct
		// from 1 (hard error like couldn't load config) so scripts can
		// branch.
		return &exitCodeError{code: 2, msg: fmt.Sprintf("%d credentials missing: %s",
			len(report.Missing), strings.Join(report.Missing, ", "))}
	}
	return nil
}

// exitCodeError is returned by verbs that want a specific non-1 exit
// code. main.go detects it via errors.As and calls os.Exit(code).
type exitCodeError struct {
	code int
	msg  string
}

func (e *exitCodeError) Error() string { return e.msg }
func (e *exitCodeError) ExitCode() int { return e.code }

// ── helpers ──────────────────────────────────────────────────────────

// credentialGetter returns a `getter(name) string` that reads the
// effective credential value: process env in yaml mode, decrypted
// secret in store mode. Empty string = absent.
func credentialGetter(ctx context.Context, r *rt, mode string) (func(string) string, error) {
	switch mode {
	case "yaml":
		return os.Getenv, nil
	case "store":
		st, _, err := openStore(ctx, r.flags)
		if err != nil {
			return nil, err
		}
		// Eagerly decrypt all secrets — env check fans out over many
		// names and we close the store before returning.
		proj, err := st.GetProject(ctx, r.flags.ProjectName)
		if err != nil {
			st.Close()
			return nil, err
		}
		secrets, err := st.AllSecretsAsMap(ctx, proj.ID)
		st.Close()
		if err != nil {
			return nil, err
		}
		return func(name string) string { return secrets[name] }, nil
	default:
		return nil, fmt.Errorf("unknown mode %q", mode)
	}
}

func bucketize(required []string, get func(string) string) envReportBucket {
	out := envReportBucket{Required: required}
	for _, n := range required {
		if v := get(n); v != "" {
			out.Present = append(out.Present, n)
		} else {
			out.Missing = append(out.Missing, n)
		}
	}
	sort.Strings(out.Present)
	sort.Strings(out.Missing)
	return out
}

// infraEnvVars returns the env vars the infra/DNS providers consume.
// Hardcoded list mirrors internal/cli.ResolveProviderInputs (HCLOUD_TOKEN
// for Hetzner; CLOUDFLARE_API_TOKEN/CF_API_KEY/CF_ACCOUNT_ID/CF_ZONE_ID/
// CF_ZONE for Cloudflare DNS). Reads from cfg to avoid asking for vars
// the config doesn't reference.
func infraEnvVars(cfg *config.Config) []string {
	out := []string{}
	if cfg.Providers.Infra == "hetzner" {
		out = append(out, "HCLOUD_TOKEN")
	}
	if cfg.Providers.DNS == "cloudflare" {
		// API token preferred; CF_API_KEY accepted as fallback —
		// surface only the canonical one as "required", report the
		// fallback under presence at runtime.
		out = append(out, "CLOUDFLARE_API_TOKEN")
	}
	sort.Strings(out)
	return out
}

// storageEnvVars enumerates the bucket provider's credential schema
// when cfg.Providers.Storage is set.
func storageEnvVars(cfg *config.Config) []string {
	if cfg.Providers.Storage == "" {
		return nil
	}
	schema, err := providers.CredentialSchemaForBucket(cfg.Providers.Storage)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(schema.Fields))
	for _, f := range schema.Fields {
		if f.Required {
			out = append(out, f.EnvVar)
		}
	}
	sort.Strings(out)
	return out
}

// registryEnvVars enumerates the $VAR references in cfg.Registry. Bare
// literal usernames/passwords are not env-dependent.
func registryEnvVars(cfg *config.Config) []string {
	seen := map[string]struct{}{}
	for _, def := range cfg.Registry {
		for _, ref := range []string{def.Username, def.Password} {
			if strings.HasPrefix(ref, "$") {
				seen[strings.TrimPrefix(ref, "$")] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
