package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/getnvoi/core/internal/install"
	"github.com/getnvoi/core/internal/runner"
	"github.com/getnvoi/core/internal/ssh"
)

func logsCmd(r *rt) *cobra.Command {
	var (
		follow bool
		tail   int64
		since  string
	)
	cmd := &cobra.Command{
		Use:   "logs <service>",
		Short: "Stream a service's pod logs via kubectl logs on the master",
		Long: `Stream the logs of a service's pod.

Resolves <service> against services: in nvoi.yaml. Stateful services
(those with a storage: block) tail statefulset/<name> (pod 0);
stateless services tail deployment/<name> (kubectl picks one pod).

Output streams unmolested to stdout/stderr — pipeable to grep / jq /
tee. Same plumbing as nvoi exec.

Examples:
  nvoi logs web
  nvoi logs web -f
  nvoi logs postgres --tail=100
  nvoi logs web --since=5m`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			service := args[0]
			target, err := execTarget(r.runtime.Cfg, service)
			if err != nil {
				return err
			}

			kArgs := buildLogsArgs(target, follow, tail, since)
			primary := r.runtime.Cfg.PrimaryMaster()
			return runWith(cmd.Context(), r.runtime, func(ctx context.Context, run *runner.Runner) error {
				return runOnNode(ctx, r.runtime, run, primary, func(sh *ssh.Client) error {
					return install.KubectlStream(ctx, sh, os.Stdout, os.Stderr, kArgs...)
				})
			})
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "stream logs continuously")
	cmd.Flags().Int64Var(&tail, "tail", -1, "lines of recent log to display (default: all)")
	cmd.Flags().StringVar(&since, "since", "", "show logs since duration (e.g. 5m, 1h, 24h)")
	return cmd
}

// buildLogsArgs assembles the kubectl argv from the resolved target
// + flag values. Pure — no SSH, no I/O — so it tests in isolation
// without a fake shell.
//
// Order is fixed so the resulting argv is deterministic across runs
// (matters for log-comparison tests and operator muscle memory).
func buildLogsArgs(target string, follow bool, tail int64, since string) []string {
	out := []string{"logs", target}
	if follow {
		out = append(out, "--follow")
	}
	if tail >= 0 {
		out = append(out, fmt.Sprintf("--tail=%d", tail))
	}
	if since != "" {
		out = append(out, "--since="+since)
	}
	return out
}
