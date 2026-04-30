package main

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/getnvoi/core/internal/runner"
)

func destroyCmd(r *rt) *cobra.Command {
	return &cobra.Command{
		Use:   "destroy",
		Short: "Compile + init + destroy",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWith(cmd.Context(), r.runtime, func(ctx context.Context, run *runner.Runner) error {
				r.runtime.Log.Step("tf-init")
				if err := run.Init(ctx); err != nil {
					return err
				}

				// Plan-then-apply, mirroring deploy. Saving the destroy
				// plan to disk lets the drain step inspect WHAT'S going
				// away before tf actually starts removing it — same
				// pattern detachNode + drainTunnel use on the deploy
				// path. Path is RELATIVE to terraform's cwd
				// (rt.WorkDir); don't filepath.Join.
				const planPath = "destroy.tfplan"
				r.runtime.Log.Step("tf-plan-destroy")
				hasChanges, err := run.PlanDestroyWithOut(ctx, planPath)
				if err != nil {
					return err
				}
				if !hasChanges {
					r.runtime.Log.Info("nothing to destroy")
					return nil
				}

				// Pre-apply drain: kill the cloudflared agent if the
				// plan removes the tunnel object. CF rejects tunnel
				// DELETE with active connections — see
				// terraform-provider-cloudflare#5255. Same primitive
				// as the deploy path.
				if err := drainTunnel(ctx, r.runtime, run, planPath); err != nil {
					return err
				}

				r.runtime.Log.Step("tf-destroy")
				return run.ApplyPlan(ctx, planPath)
			})
		},
	}
}
