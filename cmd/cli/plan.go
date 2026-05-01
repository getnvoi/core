package main

import (
	"github.com/spf13/cobra"

	"github.com/getnvoi/core/pkg/deploy"
)

func planCmd(r *rt) *cobra.Command {
	return &cobra.Command{
		Use:   "plan",
		Short: "Compile + init + plan",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return deploy.Plan(cmd.Context(), r.runtime)
		},
	}
}
