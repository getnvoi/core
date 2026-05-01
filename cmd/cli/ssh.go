package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/deploy"
	"github.com/getnvoi/core/pkg/log"
	"github.com/getnvoi/core/pkg/ssh"
)

func sshCmd(r *rt) *cobra.Command {
	return &cobra.Command{
		Use:   "ssh [target] -- <command>",
		Short: "Run a shell command on a cluster node via SSH",
		Args:  cobra.ArbitraryArgs,
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
			return deploy.RunWithSession(cmd.Context(), r.runtime, log.KindCluster, func(ctx context.Context, s *deploy.Session) error {
				return s.OnNode(ctx, target, func(sh *ssh.Client) error {
					return sh.RunStream(ctx, command, s.Lg.Stream(), s.Lg.Stream())
				})
			})
		},
	}
}

func listServerNames(cfg *config.Config) string {
	names := make([]string, 0, len(cfg.Servers))
	for n := range cfg.Servers {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
