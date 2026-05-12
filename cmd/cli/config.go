package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	internalcli "github.com/getnvoi/core/internal/cli"
	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/runtime"
)

func configCmd(r *rt) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "config <show|replace>",
		Short:       "Inspect or replace a project's config (yaml or store)",
		Annotations: skipRuntime(),
		GroupID:     groupInspection,
	}
	cmd.AddCommand(configShowCmd(r), configReplaceCmd(r))
	return cmd
}

// ── show ─────────────────────────────────────────────────────────────

func configShowCmd(r *rt) *cobra.Command {
	return &cobra.Command{
		Use:         "show",
		Short:       "Print the parsed + validated config",
		Annotations: skipRuntime(),
		Long: `Loads and validates the config, then prints it. Without --json,
prints the canonical YAML. With --json (or under the root --json flag),
prints one JSON object.

Source resolves to:
  - YAML mode  → the file at -c (default nvoi.yaml in cwd)
  - Store mode → the ConfigYAML column for --project, decoded + revalidated`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runConfigShow(cmd.Context(), r)
		},
	}
}

func runConfigShow(ctx context.Context, r *rt) error {
	cfg, err := loadConfigForBoundary(ctx, r)
	if err != nil {
		return err
	}
	if r.flags.JSON {
		out, err := configAsJSON(cfg)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(append(out, '\n'))
		return err
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("config show: marshal yaml: %w", err)
	}
	_, err = os.Stdout.Write(out)
	return err
}

// ── replace ──────────────────────────────────────────────────────────

func configReplaceCmd(r *rt) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:         "replace",
		Short:       "Replace the project's config from a JSON object on stdin",
		Annotations: skipRuntime(),
		Long: `Reads a JSON object from stdin matching the *config.Config shape
(same fields as nvoi.yaml). Validates via the standard validator and
writes back atomically.

DESTRUCTIVE: replaces the entire config. Requires confirmation at the
tty unless --force is passed. Agent tools always pass --force; humans
get the prompt by default.

Targets:
  - YAML mode  → atomic write to the file at -c (tmp + rename)
  - Store mode → UpdateProjectConfig for --project (transactional)

Errors include the validator's message verbatim so callers (humans
or agent tools) can fix the input and retry.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runConfigReplace(cmd.Context(), r, force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "skip confirmation prompt (required for non-tty / agent use)")
	return cmd
}

func runConfigReplace(ctx context.Context, r *rt, force bool) error {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("config replace: read stdin: %w", err)
	}
	if len(raw) == 0 {
		return errors.New("config replace: empty stdin (pipe a JSON object)")
	}
	cfg, err := parseConfigJSON(raw)
	if err != nil {
		return fmt.Errorf("config replace: %w", err)
	}

	mode, yamlPath, _, err := resolveBoundaryMode(r.flags)
	if err != nil {
		return err
	}
	target := yamlPath
	if mode == "store" {
		target = fmt.Sprintf("store project %q", r.flags.ProjectName)
		if err := requireProject(r.flags); err != nil {
			return err
		}
	}

	if !force {
		if err := confirmDestructive(fmt.Sprintf(
			"Replace config at %s? This overwrites the existing config and cannot be undone.",
			target,
		)); err != nil {
			return err
		}
	}

	switch mode {
	case "yaml":
		return writeYAMLAtomic(yamlPath, cfg)
	case "store":
		st, _, err := openStore(ctx, r.flags)
		if err != nil {
			return err
		}
		defer st.Close()
		if err := st.UpdateProjectConfig(ctx, r.flags.ProjectName, cfg); err != nil {
			return fmt.Errorf("config replace: %w", err)
		}
		r.log.Info(fmt.Sprintf("project %q config updated", r.flags.ProjectName))
		return nil
	default:
		return fmt.Errorf("config replace: unknown mode %q", mode)
	}
}

// confirmDestructive shows the message + a yes/no prompt on stderr.
// Refuses to proceed when stdin isn't interactive (a script / agent
// piping data MUST pass --force explicitly — silently auto-confirming
// from a piped 'y' is a footgun).
func confirmDestructive(msg string) error {
	if !isStdinTerminal() {
		return errors.New("config replace: non-interactive stdin; pass --force to confirm")
	}
	fmt.Fprintf(os.Stderr, "%s\nType 'yes' to proceed: ", msg)
	sc := bufio.NewScanner(os.Stdin)
	if !sc.Scan() {
		return errors.New("config replace: no input")
	}
	if strings.TrimSpace(sc.Text()) != "yes" {
		return errors.New("config replace: aborted by user")
	}
	return nil
}

// writeYAMLAtomic marshals cfg to YAML and writes via tmp + rename so
// a crash mid-write leaves the original file intact.
func writeYAMLAtomic(path string, cfg *config.Config) error {
	yml, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal yaml: %w", err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".nvoi-config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(yml); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("fsync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// ── shared helpers (used by config show/replace + env check) ─────────

// loadConfigForBoundary returns the effective *config.Config without
// building a full *runtime.Runtime. Picks yaml or store mode based on
// flags + filesystem state. The verbs that consume this never need
// SSH keys, provider creds, or state-backend resolution — those are
// downstream of the actual deploy/plan/destroy verbs.
func loadConfigForBoundary(ctx context.Context, r *rt) (*config.Config, error) {
	mode, yamlPath, _, err := resolveBoundaryMode(r.flags)
	if err != nil {
		return nil, err
	}
	switch mode {
	case "yaml":
		// Load .env alongside the yaml so env-check sees it. Cheap;
		// no harm if the file doesn't exist.
		if envPath := internalcli.ResolveDotEnv(yamlPath); envPath != "" {
			_ = internalcli.LoadDotEnv(envPath)
		}
		return config.LoadFile(yamlPath)
	case "store":
		if err := requireProject(r.flags); err != nil {
			return nil, err
		}
		st, _, err := openStore(ctx, r.flags)
		if err != nil {
			return nil, err
		}
		defer st.Close()
		proj, err := st.GetProject(ctx, r.flags.ProjectName)
		if err != nil {
			return nil, err
		}
		return proj.ParsedConfig()
	default:
		return nil, fmt.Errorf("unknown mode %q", mode)
	}
}

// resolveBoundaryMode picks the effective mode without building a
// Runtime. Returns (mode, yamlPath, dbPath, err). Mutual exclusion +
// smart defaults applied here too — kept in sync with
// internal/cli.PrepareRuntime by exercising the same applySmartDefaults
// helper.
func resolveBoundaryMode(flags runtime.Flags) (string, string, string, error) {
	if flags.DatabasePath != "" && flags.ConfigPath != "" {
		return "", "", "", errors.New("--database and --config are mutually exclusive")
	}
	cfgPath, dbPath, _, err := internalcli.ApplySmartDefaults(flags.ConfigPath, flags.DatabasePath)
	if err != nil {
		return "", "", "", err
	}
	if dbPath != "" {
		expanded, err := internalcli.ExpandHome(dbPath)
		if err != nil {
			return "", "", "", err
		}
		return "store", "", expanded, nil
	}
	return "yaml", cfgPath, "", nil
}

// configAsJSON yaml-marshals cfg then yaml-unmarshals into a generic
// map, then json-marshals. Round-trip through yaml ensures custom
// UnmarshalYAML methods (BuildSpec) produce stable shapes. yaml.v3
// produces map[string]any so json.Marshal accepts the result.
func configAsJSON(cfg *config.Config) ([]byte, error) {
	yml, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal yaml: %w", err)
	}
	var generic any
	if err := yaml.Unmarshal(yml, &generic); err != nil {
		return nil, fmt.Errorf("unmarshal yaml: %w", err)
	}
	out, err := json.MarshalIndent(generic, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal json: %w", err)
	}
	return out, nil
}

// parseConfigJSON does the reverse: JSON → yaml bytes → config.ParseYAML.
// Validate runs inside ParseYAML.
func parseConfigJSON(data []byte) (*config.Config, error) {
	var generic any
	if err := json.Unmarshal(data, &generic); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	yml, err := yaml.Marshal(generic)
	if err != nil {
		return nil, fmt.Errorf("yaml re-marshal: %w", err)
	}
	cfg, err := config.ParseYAML(yml)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

// isStdinTerminal returns true when os.Stdin is connected to a tty.
// Used by confirmDestructive to refuse non-interactive runs without
// --force.
func isStdinTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
