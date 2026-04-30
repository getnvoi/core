package deploy

import (
	"context"

	"github.com/getnvoi/core/internal/runner"
	"github.com/getnvoi/core/internal/runtime"
)

// Destroy is the destroy verb's workflow.
//
// Plan-then-apply, mirroring deploy. Saving the destroy plan to disk
// lets the drain step inspect WHAT'S going away before tf actually
// starts removing it — same pattern detachNode + drainTunnel use on
// the deploy path. Path is RELATIVE to terraform's cwd (rt.WorkDir);
// don't filepath.Join.
func Destroy(ctx context.Context, rt *runtime.Runtime) error {
	return WithRunner(ctx, rt, func(ctx context.Context, run *runner.Runner) error {
		rt.Log.Step("tf-init")
		if err := run.Init(ctx); err != nil {
			return err
		}

		const planPath = "destroy.tfplan"
		rt.Log.Step("tf-plan-destroy")
		hasChanges, err := run.PlanDestroyWithOut(ctx, planPath)
		if err != nil {
			return err
		}
		if !hasChanges {
			rt.Log.Info("nothing to destroy")
			return nil
		}

		// Pre-apply drain: kill the cloudflared agent if the plan
		// removes the tunnel object. CF rejects tunnel DELETE with
		// active connections — see terraform-provider-cloudflare#5255.
		// Same primitive as the deploy path.
		if err := drainTunnel(ctx, rt, run, planPath); err != nil {
			return err
		}

		rt.Log.Step("tf-destroy")
		return run.ApplyPlan(ctx, planPath)
	})
}
