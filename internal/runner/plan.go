package runner

import (
	"context"

	"github.com/hashicorp/terraform-exec/tfexec"
)

// PlanWithOut runs `terraform plan -out=<path>` and returns hasChanges.
// The saved plan is binary-stable input for ApplyPlan, so the apply
// does exactly what plan promised — no race window between the two.
//
// Always -json internally; tfTransformer in the log layer normalizes
// the events into our canonical schema. The operator's `--json` flag
// only selects the rendering (JSONL vs tabbed text), not the
// terraform invocation.
func (r *Runner) PlanWithOut(ctx context.Context, planPath string) (bool, error) {
	return r.tf.PlanJSON(ctx, r.rt.Log.TFStream(), tfexec.Out(planPath))
}

// PlanDestroyWithOut runs `terraform plan -destroy -out=<path>` and
// returns hasChanges. The destroy variant of PlanWithOut: lets the
// destroy pipeline inspect WHAT'S going away before tf actually
// removes anything (the pre-apply drain step needs the plan to
// decide whether the tunnel agent has to be killed first).
func (r *Runner) PlanDestroyWithOut(ctx context.Context, planPath string) (bool, error) {
	return r.tf.PlanJSON(ctx, r.rt.Log.TFStream(), tfexec.Out(planPath), tfexec.Destroy(true))
}

// ApplyPlan applies a previously-saved plan file. Terraform won't
// re-plan; it does exactly what's in the file. Works for both
// regular plans and -destroy plans.
func (r *Runner) ApplyPlan(ctx context.Context, planPath string) error {
	return r.tf.ApplyJSON(ctx, r.rt.Log.TFStream(), tfexec.DirOrPlan(planPath))
}
