package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/getnvoi/core/pkg/deploy"
	"github.com/getnvoi/core/pkg/providers"
)

// databaseCmd builds the `nvoi database …` verb tree. Cobra wiring
// + flag parsing + result rendering only — the actual verb bodies
// live in pkg/deploy/database_verbs.go (so cmd/cli stays clear of
// pkg/internal/* imports).
//
// Every verb funnels engine-unsupported errors through
// renderUnsupported, which translates ErrUnsupported into an
// operator-facing message naming the engines that DO support the
// verb.
func databaseCmd(r *rt) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "database",
		Short: "Operate the databases declared in nvoi.yaml",
	}
	// Verb scope for v1 is intentionally non-destructive against the
	// primary's live data: sql / snapshot* / branch* / backup* only.
	// restore / rollback / migrate land in a follow-up PR with proper
	// hardening — they replace the primary's volume, need
	// interactive confirmation, mid-flight failure runbooks, and a
	// dry-run mode before they're operator-safe.
	cmd.AddCommand(
		dbSQLCmd(r),
		dbBackupCmd(r),
		dbSnapshotCmd(r),
		dbSnapshotsCmd(r),
		dbSnapshotDeleteCmd(r),
		dbBranchCmd(r),
		dbBranchesCmd(r),
		dbBranchDeleteCmd(r),
	)
	return cmd
}

// ── sql ──────────────────────────────────────────────────────────────

func dbSQLCmd(r *rt) *cobra.Command {
	return &cobra.Command{
		Use:   "sql <name> <statement>",
		Short: "Execute one SQL statement against a configured database",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := deploy.DatabaseSQL(cmd.Context(), r.runtime, args[0], args[1])
			if err != nil {
				return mapUnsupported(err, "sql", engineOf(r, args[0]))
			}
			renderSQL(os.Stdout, res)
			return nil
		},
	}
}

func renderSQL(w io.Writer, res *providers.SQLResult) {
	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	if len(res.Columns) > 0 {
		fmt.Fprintln(tw, strings.Join(res.Columns, "\t"))
	}
	for _, row := range res.Rows {
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	tw.Flush()
	fmt.Fprintf(w, "(%s rows)\n", strconv.FormatInt(res.RowsAffected, 10))
}

// ── backup ───────────────────────────────────────────────────────────

func dbBackupCmd(r *rt) *cobra.Command {
	cmd := &cobra.Command{Use: "backup", Short: "Backup operations"}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "now <name>",
			Short: "Trigger a backup now (one-shot Job from the scheduled template)",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				ref, err := deploy.DatabaseBackupNow(cmd.Context(), r.runtime, args[0])
				if err != nil {
					return mapUnsupported(err, "backup", engineOf(r, args[0]))
				}
				fmt.Println(ref.ID)
				return nil
			},
		},
		&cobra.Command{
			Use:   "list <name>",
			Short: "List backup artifacts in the per-DB bucket",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				refs, err := deploy.DatabaseListBackups(cmd.Context(), r.runtime, args[0])
				if err != nil {
					return err
				}
				for _, ref := range refs {
					fmt.Printf("%s\t%s\t%s\t%d\n", ref.ID, ref.Kind, ref.CreatedAt, ref.SizeBytes)
				}
				return nil
			},
		},
		buildBackupDownload(r),
	)
	return cmd
}

func buildBackupDownload(r *rt) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "download <name> <backup-id>",
		Short: "Download a backup to stdout or a file",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			outPath, _ := cmd.Flags().GetString("output")
			var w io.Writer = os.Stdout
			if outPath != "" {
				f, err := os.Create(outPath)
				if err != nil {
					return err
				}
				defer f.Close()
				w = f
			}
			return deploy.DatabaseDownloadBackup(cmd.Context(), r.runtime, args[0], args[1], w)
		},
	}
	cmd.Flags().StringP("output", "o", "", "write backup to file instead of stdout")
	return cmd
}

// ── snapshot ─────────────────────────────────────────────────────────

func dbSnapshotCmd(r *rt) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snapshot <name>",
		Short: "Create a point-in-time snapshot (postgres/ZFS)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			label, _ := cmd.Flags().GetString("label")
			ref, err := deploy.DatabaseSnapshot(cmd.Context(), r.runtime, args[0], label)
			if err != nil {
				return mapUnsupported(err, "snapshot", engineOf(r, args[0]))
			}
			fmt.Println(ref.Name)
			return nil
		},
	}
	cmd.Flags().String("label", "", "DNS-1123 label appended to the snapshot name (default: UTC timestamp)")
	return cmd
}

func dbSnapshotsCmd(r *rt) *cobra.Command {
	return &cobra.Command{
		Use:   "snapshots <name>",
		Short: "List snapshots for a database",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			snaps, err := deploy.DatabaseListSnapshots(cmd.Context(), r.runtime, args[0])
			if err != nil {
				return mapUnsupported(err, "snapshots", engineOf(r, args[0]))
			}
			for _, s := range snaps {
				fmt.Println(s.Name)
			}
			return nil
		},
	}
}

func dbSnapshotDeleteCmd(r *rt) *cobra.Command {
	return &cobra.Command{
		Use:   "snapshot-delete <name> <snapshot-name>",
		Short: "Delete a snapshot",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := deploy.DatabaseDeleteSnapshot(cmd.Context(), r.runtime, args[0], args[1]); err != nil {
				return mapUnsupported(err, "snapshot-delete", engineOf(r, args[0]))
			}
			return nil
		},
	}
}

// ── branch ───────────────────────────────────────────────────────────

func dbBranchCmd(r *rt) *cobra.Command {
	return &cobra.Command{
		Use:   "branch <name> <branch-name>",
		Short: "Create a CoW-cloned branch of a database",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := deploy.DatabaseBranch(cmd.Context(), r.runtime, args[0], args[1])
			if err != nil {
				return mapUnsupported(err, "branch", engineOf(r, args[0]))
			}
			fmt.Printf("branch %s ready — %s (snapshot %s)\n", ref.Name, ref.Endpoint, ref.Snapshot)
			return nil
		},
	}
}

func dbBranchesCmd(r *rt) *cobra.Command {
	return &cobra.Command{
		Use:   "branches <name>",
		Short: "List branches of a database",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			refs, err := deploy.DatabaseListBranches(cmd.Context(), r.runtime, args[0])
			if err != nil {
				return mapUnsupported(err, "branches", engineOf(r, args[0]))
			}
			for _, b := range refs {
				fmt.Printf("%s\t%s\n", b.Name, b.Endpoint)
			}
			return nil
		},
	}
}

func dbBranchDeleteCmd(r *rt) *cobra.Command {
	return &cobra.Command{
		Use:   "branch-delete <name> <branch-name>",
		Short: "Delete a branch (workloads + snapshot)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := deploy.DatabaseDeleteBranch(cmd.Context(), r.runtime, args[0], args[1]); err != nil {
				return mapUnsupported(err, "branch-delete", engineOf(r, args[0]))
			}
			return nil
		},
	}
}

// engineOf is a small lookup for mapUnsupported's error message —
// the verb knows the db name; we read the engine from cfg.
func engineOf(r *rt, dbName string) string {
	if r.runtime == nil || r.runtime.Cfg == nil {
		return ""
	}
	return r.runtime.Cfg.Databases[dbName].Engine
}

// mapUnsupported translates ErrUnsupported into an operator-facing
// message with a concrete pointer to which engines DO support the
// verb. Non-ErrUnsupported errors pass through unchanged.
//
// Supported-engines lists are hand-maintained — when a new engine
// implements a capability, add it here. Compile-time visible, so
// PRs that add capabilities also have to think about the operator-
// facing message.
func mapUnsupported(err error, verb, engine string) error {
	if err == nil || !errors.Is(err, providers.ErrUnsupported) {
		return err
	}
	supported := map[string][]string{
		"snapshot":        {"postgres"},
		"snapshots":       {"postgres"},
		"snapshot-delete": {"postgres"},
		"branch":          {"postgres"},
		"branches":        {"postgres"},
		"branch-delete":   {"postgres"},
		"sql":             {"postgres"},
		"backup":          {"postgres"},
	}
	list, ok := supported[verb]
	if !ok || len(list) == 0 {
		return fmt.Errorf("engine %q does not support %s", engine, verb)
	}
	return fmt.Errorf("engine %q does not support %s (supported: %s)", engine, verb, strings.Join(list, ", "))
}
