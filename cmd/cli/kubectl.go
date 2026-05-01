package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/getnvoi/core/pkg/deploy"
	"github.com/getnvoi/core/pkg/install"
	"github.com/getnvoi/core/pkg/log"
	"github.com/getnvoi/core/pkg/ssh"
)

func kubectlCmd(r *rt) *cobra.Command {
	return &cobra.Command{
		Use:   "kubectl -- <args...>",
		Short: "Run kubectl on the primary master via SSH",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dashIdx := cmd.ArgsLenAtDash()
			if dashIdx == -1 {
				return fmt.Errorf("missing -- separator: nvoi kubectl -- <args>")
			}
			if dashIdx > 0 {
				return fmt.Errorf("unexpected positional args before -- (got %d)", dashIdx)
			}
			kArgs := args[dashIdx:]
			if len(kArgs) == 0 {
				return fmt.Errorf("missing kubectl args after --")
			}
			return deploy.RunWithSession(cmd.Context(), r.runtime, log.KindCluster, func(ctx context.Context, s *deploy.Session) error {
				return s.OnPrimary(ctx, func(sh *ssh.Client) error {
					return install.KubectlStream(ctx, install.KubectlSpec{
						Shell:  sh,
						Args:   kArgs,
						Stdout: s.Lg.Stream(),
						Stderr: s.Lg.Stream(),
					})
				})
			})
		},
	}
}
