package main

import (
	"github.com/spf13/cobra"

	"github.com/getnvoi/core/pkg/deploy"
)

func destroyCmd(r *rt) *cobra.Command {
	return &cobra.Command{
		Use:   "destroy",
		Short: "Compile + init + destroy",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return deploy.Destroy(cmd.Context(), r.runtime)
		},
	}
}
