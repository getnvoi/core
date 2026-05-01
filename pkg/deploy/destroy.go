package deploy

import (
	"context"

	"github.com/getnvoi/core/pkg/log"
	"github.com/getnvoi/core/pkg/runtime"
)

// Destroy is the destroy verb's workflow.
//
// Plan-then-apply, mirroring deploy. Saving the destroy plan to disk
// lets the drain step inspect WHAT'S going away before tf actually
// starts removing it — same pattern detachNode + drainTunnel use on
// the deploy path. Path is RELATIVE to terraform's cwd (rt.WorkDir);
// don't filepath.Join.
//
// Every step emits kind=infra (tf operations + the tunnel pre-apply
// drain that prepares for tf-destroy).
func Destroy(ctx context.Context, rt *runtime.Runtime) error {
	return RunWithSession(ctx, rt, log.KindInfra, func(ctx context.Context, s *Session) error {
		s.Lg.Step("tf-init")
		if err := s.Init(ctx); err != nil {
			return err
		}

		const planPath = "destroy.tfplan"
		s.Lg.Step("tf-plan-destroy")
		hasChanges, err := s.Run.PlanDestroyWithOut(ctx, planPath)
		if err != nil {
			return err
		}
		if !hasChanges {
			s.Lg.Info("nothing to destroy")
			return nil
		}

		// Pre-apply drain: kill the cloudflared agent if the plan
		// removes the tunnel object. CF rejects tunnel DELETE with
		// active connections — see terraform-provider-cloudflare#5255.
		// Same primitive as the deploy path.
		if err := s.drainTunnel(ctx, planPath); err != nil {
			return err
		}

		s.Lg.Step("tf-destroy")
		return s.Run.ApplyPlan(ctx, planPath)
	})
}
