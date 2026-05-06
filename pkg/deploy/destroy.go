package deploy

import (
	"context"

	"github.com/getnvoi/core/pkg/log"
	"github.com/getnvoi/core/pkg/runtime"
)

// Destroy is the destroy verb's workflow.
//
// Plan-then-apply, same shape as deploy minus the workload phase.
// Path is RELATIVE to terraform's cwd (rt.WorkDir); don't filepath.Join.
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

		s.Lg.Step("tf-destroy")
		return s.Run.ApplyPlan(ctx, planPath)
	})
}
