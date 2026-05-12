package main

import (
	"github.com/spf13/cobra"

	internalcli "github.com/getnvoi/core/internal/cli"
	"github.com/getnvoi/core/pkg/log"
	"github.com/getnvoi/core/pkg/runtime"

	_ "github.com/getnvoi/core/pkg/providers/cloudflare"
	_ "github.com/getnvoi/core/pkg/providers/hetzner"

	_ "github.com/getnvoi/core/pkg/store/keyring/env"
	_ "github.com/getnvoi/core/pkg/store/keyring/file"
	_ "github.com/getnvoi/core/pkg/store/keyring/os"
)

// Cobra command-group IDs. Surface in --help as "Lifecycle",
// "Inspection", "Local store" sections — keeps the new store-mgmt
// verbs visually segregated from the deploy verbs.
const (
	groupLifecycle  = "lifecycle"
	groupInspection = "inspection"
	groupStore      = "store"
)

type rt struct {
	flags   runtime.Flags
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
	// Persistent flags. Defaults are empty so PrepareRuntime can apply
	// smart defaulting based on what exists on disk.
	root.PersistentFlags().StringVarP(&r.flags.ConfigPath, "config", "c", "", "path to YAML config (default nvoi.yaml when present; mutually exclusive with --database)")
	root.PersistentFlags().BoolVar(&r.flags.JSON, "json", false, "stream machine-readable JSONL output")
	root.PersistentFlags().StringVar(&r.flags.DatabasePath, "database", "", "path to SQLite database (default ~/.nvoi/db.sqlite when present; mutually exclusive with --config)")
	root.PersistentFlags().StringVar(&r.flags.Keyring, "keyring", "auto", "master-key source: auto | os | env[:VAR] | file:<path>")
	root.PersistentFlags().StringVar(&r.flags.ProjectName, "project", "", "project name within the database (required with --database)")

	// Help groups — visual separation in `nvoi --help`.
	root.AddGroup(
		&cobra.Group{ID: groupLifecycle, Title: "Lifecycle:"},
		&cobra.Group{ID: groupInspection, Title: "Inspection:"},
		&cobra.Group{ID: groupStore, Title: "Local store (use with --database):"},
	)

	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		r.log = log.New(r.flags.JSON)
		// Store-mgmt verbs open the store themselves (different
		// boundary than yaml/store-runtime). Skip PrepareRuntime for
		// them — they don't need a *runtime.Runtime.
		if cmd.Annotations[skipRuntimeAnno] == "true" {
			return nil
		}
		built, err := internalcli.PrepareRuntime(cmd.Context(), r.flags, r.log)
		if err != nil {
			return err
		}
		r.runtime = built
		return nil
	}

	root.AddCommand(
		// Lifecycle
		withGroup(deployCmd(r), groupLifecycle),
		withGroup(planCmd(r), groupLifecycle),
		withGroup(destroyCmd(r), groupLifecycle),

		// Inspection
		withGroup(sshCmd(r), groupInspection),
		withGroup(kubectlCmd(r), groupInspection),
		withGroup(execCmd(r), groupInspection),
		withGroup(logsCmd(r), groupInspection),
		configCmd(r),
		envCmd(r),

		// Local store (each verb sets its own GroupID in its constructor)
		setupCmd(r),
		importCmd(r),
		exportCmd(r),
		rekeyCmd(r),
		projectsCmd(r),
		projectCmd(r),
		secretsCmd(r),
		secretCmd(r),
	)
	return root
}

// withGroup is a one-liner to tag pre-existing verb constructors that
// don't set GroupID themselves. New verbs should set GroupID in their
// constructor directly.
func withGroup(cmd *cobra.Command, id string) *cobra.Command {
	cmd.GroupID = id
	return cmd
}

func resolveSecrets(names []string, getenv func(string) string) (map[string]string, error) {
	return internalcli.ResolveSecrets(names, getenv)
}
