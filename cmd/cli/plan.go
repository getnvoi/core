package main

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/getnvoi/tf/internal/runner"
)

func planCmd(r *rt) *cobra.Command {
	return &cobra.Command{
		Use:   "plan",
		Short: "Compile + init + plan",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWith(cmd.Context(), r.runtime, func(ctx context.Context, run *runner.Runner) error {
				r.runtime.Log.Step("tf-init")
				if err := run.Init(ctx); err != nil {
					return err
				}
				r.runtime.Log.Step("tf-plan")
				_, err := run.Plan(ctx)
				return err
			})
		},
	}
}
