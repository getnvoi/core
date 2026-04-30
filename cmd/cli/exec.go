package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/getnvoi/core/internal/config"
	"github.com/getnvoi/core/internal/install"
	"github.com/getnvoi/core/internal/runner"
	"github.com/getnvoi/core/internal/ssh"
)

func execCmd(r *rt) *cobra.Command {
	return &cobra.Command{
		Use:   "exec <service> -- <cmd> [args...]",
		Short: "Run a command in a service's pod via kubectl exec on the master",
		Long: `Run a command inside a service's pod.

Resolves <service> against services: in nvoi.yaml. Stateful services
(those with a storage: block) land on <name>-0 via statefulset/<name>;
stateless services hit any ready pod via deployment/<name> (kubectl
picks). The -- separator is required so cobra doesn't try to parse
the command's own flags.

Examples:
  nvoi exec web -- ls /
  nvoi exec postgres -- psql -U nvoi -d nvoi -tAc 'SELECT COUNT(*) FROM visits'

v1 is non-interactive only: stdin is closed, no PTY. Streams stdout
and stderr back unmolested (no log indent, no step header) so the
output is safe to pipe to grep / awk / jq. Interactive shells (-it)
land in v2 once SSH-side PTY allocation is wired.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			service, execArgs, err := parseExecArgs(cmd, args)
			if err != nil {
				return err
			}

			target, err := execTarget(r.runtime.Cfg, service)
			if err != nil {
				return err
			}

			primary := r.runtime.Cfg.PrimaryMaster()
			return runWith(cmd.Context(), r.runtime, func(ctx context.Context, run *runner.Runner) error {
				return runOnNode(ctx, r.runtime, run, primary, func(sh *ssh.Client) error {
					// Bypass the log indenter: callers will pipe this
					// output to other tools, where leading spaces and
					// step markers would corrupt every parser.
					return install.KubectlExec(ctx, sh, target, execArgs, os.Stdout, os.Stderr)
				})
			})
		},
	}
}

// parseExecArgs splits a cobra-parsed argv into (service, command-args)
// using the position of the `--` separator. The service name must
// appear exactly once before --, and at least one token must appear
// after --. Any other shape is a hard error so the operator gets a
// clear message instead of a confusing kubectl invocation.
func parseExecArgs(cmd *cobra.Command, args []string) (service string, execArgs []string, err error) {
	dashIdx := cmd.ArgsLenAtDash()
	if dashIdx == -1 {
		return "", nil, fmt.Errorf("missing -- separator: nvoi exec <service> -- <cmd> [args]")
	}
	if dashIdx != 1 {
		return "", nil, fmt.Errorf("expected exactly one positional arg before -- (the service name), got %d", dashIdx)
	}
	service = args[0]
	execArgs = args[dashIdx:]
	if len(execArgs) == 0 {
		return "", nil, fmt.Errorf("missing command after --")
	}
	return service, execArgs, nil
}

// execTarget resolves the kubectl exec target string for the named
// service: `statefulset/<name>` for stateful, `deployment/<name>` for
// stateless. Returns an error if the service isn't declared in
// cfg.Services so operators don't waste a round-trip to the master to
// find out kubectl can't resolve the resource.
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
