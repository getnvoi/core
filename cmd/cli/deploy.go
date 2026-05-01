package main

import (
	"github.com/spf13/cobra"

	"github.com/getnvoi/core/pkg/deploy"
)

func deployCmd(r *rt) *cobra.Command {
	return &cobra.Command{
		Use:   "deploy",
		Short: "Compile YAML → HCL, init + apply, install k3s",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return deploy.Run(cmd.Context(), r.runtime)
		},
	}
}
