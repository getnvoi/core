package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/deploy"
	"github.com/getnvoi/core/pkg/install"
	"github.com/getnvoi/core/pkg/log"
	"github.com/getnvoi/core/pkg/ssh"
)

func execCmd(r *rt) *cobra.Command {
	return &cobra.Command{
		Use:   "exec <service> -- <cmd> [args...]",
		Short: "Run a command in a service's pod via kubectl exec on the master",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			service, execArgs, err := parseExecArgs(cmd, args)
			if err != nil {
				return err
			}
			target, err := execTarget(r.runtime.Cfg, service)
			if err != nil {
				return err
			}
			return deploy.RunWithSession(cmd.Context(), r.runtime, log.KindCluster, func(ctx context.Context, s *deploy.Session) error {
				return s.OnPrimary(ctx, func(sh *ssh.Client) error {
					return install.KubectlExec(ctx, target, install.KubectlSpec{
						Shell:  sh,
						Args:   execArgs,
						Stdout: os.Stdout,
						Stderr: os.Stderr,
					})
				})
			})
		},
	}
}

func parseExecArgs(cmd *cobra.Command, args []string) (string, []string, error) {
	dashIdx := cmd.ArgsLenAtDash()
	if dashIdx == -1 {
		return "", nil, fmt.Errorf("missing -- separator: nvoi exec <service> -- <cmd> [args]")
	}
	if dashIdx != 1 {
		return "", nil, fmt.Errorf("expected exactly one positional arg before -- (the service name), got %d", dashIdx)
	}
	execArgs := args[dashIdx:]
	if len(execArgs) == 0 {
		return "", nil, fmt.Errorf("missing command after --")
	}
	return args[0], execArgs, nil
}

func execTarget(cfg *config.Config, service string) (string, error) {
	svc, ok := cfg.Services[service]
	if !ok {
		return "", fmt.Errorf("services.%s: not declared in nvoi.yaml", service)
	}
	if svc.IsStateful() {
		return "statefulset/" + service, nil
	}
	return "deployment/" + service, nil
}
