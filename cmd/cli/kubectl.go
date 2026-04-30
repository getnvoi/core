package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/getnvoi/core/internal/deploy"
	"github.com/getnvoi/core/internal/install"
	"github.com/getnvoi/core/internal/runner"
	"github.com/getnvoi/core/internal/ssh"
)

func kubectlCmd(r *rt) *cobra.Command {
	return &cobra.Command{
		Use:   "kubectl -- <args...>",
		Short: "Run kubectl on the primary master via SSH",
		Long: `Run kubectl on the primary master.

Uses sudo k3s kubectl under the hood, so works without local kubeconfig
setup. The -- separator is required so cobra doesn't try to parse
kubectl's own flags as its own.

Examples:
  nvoi kubectl -- get nodes
  nvoi kubectl -- get pods -A -o wide
  nvoi kubectl -- logs -n kube-system <pod>`,
		Args: cobra.ArbitraryArgs,
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

			primary := r.runtime.Cfg.PrimaryMaster()

			return deploy.WithRunner(cmd.Context(), r.runtime, func(ctx context.Context, run *runner.Runner) error {
				return runOnNode(ctx, r.runtime, run, primary, func(sh *ssh.Client) error {
					return install.KubectlStream(ctx, sh,
						r.runtime.Log.Stream(), r.runtime.Log.Stream(),
						kArgs...,
					)
				})
			})
		},
	}
}
