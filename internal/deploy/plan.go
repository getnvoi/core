package deploy

import (
	"context"

	"github.com/getnvoi/core/internal/log"
	"github.com/getnvoi/core/internal/runtime"
)

// Plan is the plan verb's workflow: compile + init + plan. Read-only,
// no apply. Used to preview what `nvoi deploy` would change. All
// emissions tagged kind=infra.
func Plan(ctx context.Context, rt *runtime.Runtime) error {
	return RunWithSession(ctx, rt, log.KindInfra, func(ctx context.Context, s *Session) error {
		s.Lg.Step("tf-init")
		if err := s.Init(ctx); err != nil {
			return err
		}
		s.Lg.Step("tf-plan")
		_, err := s.Run.Plan(ctx)
		return err
	})
}
