package deploy

import (
	"context"

	"github.com/getnvoi/core/internal/runner"
	"github.com/getnvoi/core/internal/runtime"
)

// Plan is the plan verb's workflow: compile + init + plan. Read-only,
// no apply. Used to preview what `nvoi deploy` would change.
func Plan(ctx context.Context, rt *runtime.Runtime) error {
	return WithRunner(ctx, rt, func(ctx context.Context, run *runner.Runner) error {
		rt.Log.Step("tf-init")
		if err := run.Init(ctx); err != nil {
			return err
		}
		rt.Log.Step("tf-plan")
		_, err := run.Plan(ctx)
		return err
	})
}
