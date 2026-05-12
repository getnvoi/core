package main

import (
	"github.com/spf13/cobra"

	"github.com/getnvoi/core/pkg/deploy"
)

// monitorCmd is the cobra adapter for `nvoi monitor`. Thin: parses
// --port flag, delegates to deploy.Monitor.
//
// Blocks until ctrl-C — cobra's cmd.Context() already has SIGINT
// cancellation wired by main.go, so Monitor returns ctx.Err() and
// cobra surfaces it cleanly.
func monitorCmd(r *rt) *cobra.Command {
	var localPort int
	cmd := &cobra.Command{
		Use:   "monitor",
		Short: "Open a local tunnel to the in-cluster Grafana dashboard",
		Long: `Tunnels http://localhost:<port> to the cluster's Grafana.
Requires monitor: to be configured in nvoi.yaml and a successful prior
deploy. Blocks until ctrl-C.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return deploy.Monitor(cmd.Context(), r.runtime, localPort)
		},
	}
	cmd.Flags().IntVar(&localPort, "port", 3000, "local port to bind")
	return cmd
}
