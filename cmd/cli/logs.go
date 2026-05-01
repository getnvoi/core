package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/getnvoi/core/pkg/deploy"
	"github.com/getnvoi/core/pkg/install"
	"github.com/getnvoi/core/pkg/log"
	"github.com/getnvoi/core/pkg/ssh"
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
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := execTarget(r.runtime.Cfg, args[0])
			if err != nil {
				return err
			}
			kArgs := buildLogsArgs(target, follow, tail, since)
			return deploy.RunWithSession(cmd.Context(), r.runtime, log.KindCluster, func(ctx context.Context, s *deploy.Session) error {
				return s.OnPrimary(ctx, func(sh *ssh.Client) error {
					return install.KubectlStream(ctx, install.KubectlSpec{
						Shell:  sh,
						Args:   kArgs,
						Stdout: os.Stdout,
						Stderr: os.Stderr,
					})
				})
			})
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "stream logs continuously")
	cmd.Flags().Int64Var(&tail, "tail", -1, "lines of recent log to display (default: all)")
	cmd.Flags().StringVar(&since, "since", "", "show logs since duration (e.g. 5m, 1h, 24h)")
	return cmd
}

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
