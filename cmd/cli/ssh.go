package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/getnvoi/core/internal/config"
	"github.com/getnvoi/core/internal/deploy"
	"github.com/getnvoi/core/internal/install"
	"github.com/getnvoi/core/internal/runner"
	"github.com/getnvoi/core/internal/runtime"
	"github.com/getnvoi/core/internal/ssh"
)

func sshCmd(r *rt) *cobra.Command {
	return &cobra.Command{
		Use:   "ssh [target] -- <command>",
		Short: "Run a shell command on a cluster node via SSH",
		Long: `Run a shell command on a cluster node.

Default target is the primary master. Pass a server name (matching a
YAML key under servers:) to target a specific node.

Examples:
  nvoi ssh -- uptime
  nvoi ssh master-1 -- ls /var/lib/rancher/k3s
  nvoi ssh worker-1 -- systemctl status k3s-agent`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dashIdx := cmd.ArgsLenAtDash()
			if dashIdx == -1 {
				return fmt.Errorf("missing -- separator: nvoi ssh [target] -- <command>")
			}

			var target string
			switch dashIdx {
			case 0:
				target = r.runtime.Cfg.PrimaryMaster()
			case 1:
				target = args[0]
			default:
				return fmt.Errorf("at most one target before -- (got %d)", dashIdx)
			}

			command := strings.Join(args[dashIdx:], " ")
			if command == "" {
				return fmt.Errorf("empty command after --")
			}

			if _, ok := r.runtime.Cfg.Servers[target]; !ok {
				return fmt.Errorf("server %q not in config; available: %s", target, listServerNames(r.runtime.Cfg))
			}

			return deploy.WithRunner(cmd.Context(), r.runtime, func(ctx context.Context, run *runner.Runner) error {
				return runOnNode(ctx, r.runtime, run, target, func(sh *ssh.Client) error {
					return sh.RunStream(ctx, command, r.runtime.Log.Stream(), r.runtime.Log.Stream())
				})
			})
		},
	}
}

// runOnNode is the shared attach helper for ssh + kubectl:
// terraform-init → read endpoints → SSH-dial the named server →
// run action(sh) → deferred close. Hard-error if state has no record
// of the server (operator hasn't run `nvoi deploy` yet).
func runOnNode(ctx context.Context, rt *runtime.Runtime, run *runner.Runner, target string, action func(sh *ssh.Client) error) error {
	if err := run.Init(ctx); err != nil {
		return err
	}
	eps, err := run.Endpoints(ctx)
	if err != nil {
		return err
	}
	srv, ok := eps.Servers[target]
	if !ok {
		return fmt.Errorf("server %q not in terraform state — run `nvoi deploy` first", target)
	}
	sh, err := ssh.Dial(ctx, srv.IPv4+":22", install.DefaultUser, rt.SSHPrivKey)
	if err != nil {
		return err
	}
	defer sh.Close()
	return action(sh)
}

// listServerNames returns the sorted, comma-joined YAML server keys —
// used for "available: …" hints in error messages.
func listServerNames(cfg *config.Config) string {
	names := make([]string, 0, len(cfg.Servers))
	for n := range cfg.Servers {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
