package main

import (
	"github.com/spf13/cobra"

	internalcli "github.com/getnvoi/core/internal/cli"
	"github.com/getnvoi/core/pkg/log"
	"github.com/getnvoi/core/pkg/runtime"

	_ "github.com/getnvoi/core/pkg/providers/cloudflare"
	_ "github.com/getnvoi/core/pkg/providers/hetzner"
	_ "github.com/getnvoi/core/pkg/providers/postgres"
	_ "github.com/getnvoi/core/pkg/providers/postmark"
	_ "github.com/getnvoi/core/pkg/providers/twilio"
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
	root.PersistentFlags().StringVarP(&r.flags.ConfigPath, "config", "c", "nvoi.yaml", "path to YAML config")
	root.PersistentFlags().BoolVar(&r.flags.JSON, "json", false, "stream machine-readable JSONL output")

	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		r.log = log.New(r.flags.JSON)
		built, err := internalcli.PrepareRuntime(cmd.Context(), r.flags, r.log)
		if err != nil {
			return err
		}
		r.runtime = built
		return nil
	}

	root.AddCommand(
		deployCmd(r),
		planCmd(r),
		destroyCmd(r),
		sshCmd(r),
		kubectlCmd(r),
		execCmd(r),
		logsCmd(r),
		monitorCmd(r),
	)
	return root
}

func resolveSecrets(names []string, getenv func(string) string) (map[string]string, error) {
	return internalcli.ResolveSecrets(names, getenv)
}
