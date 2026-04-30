package runner

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-exec/tfexec"
	tfjson "github.com/hashicorp/terraform-json"
)

// PlanWithOut runs `terraform plan -out=<path>` and returns hasChanges.
// The saved plan is binary-stable input for ApplyPlan, so the apply
// does exactly what plan promised — no race window between the two.
func (r *Runner) PlanWithOut(ctx context.Context, planPath string) (bool, error) {
	if r.rt.Flags.JSON {
		return r.tf.PlanJSON(ctx, r.rt.Log.TFStream(), tfexec.Out(planPath))
	}
	return r.tf.Plan(ctx, tfexec.Out(planPath))
}

// ApplyPlan applies a previously-saved plan file. Terraform won't
// re-plan; it does exactly what's in the file.
func (r *Runner) ApplyPlan(ctx context.Context, planPath string) error {
	if r.rt.Flags.JSON {
		return r.tf.ApplyJSON(ctx, r.rt.Log.TFStream(), tfexec.DirOrPlan(planPath))
	}
	return r.tf.Apply(ctx, tfexec.DirOrPlan(planPath))
}

// PlannedNodeDestroys parses the saved plan and returns the YAML keys
// of nodes being destroyed (including replacements — delete+create
// counts, since the underlying server is yanked and workloads need
// to migrate before that happens).
//
// The filter is `rc.Type == serverResourceType` — the resource type
// the active provider's emitter declares for servers. Caller resolves
// it from compile.ServerResourceType(cfg.Providers.Infra) so the
// runner stays provider-agnostic.
//
// Why type rather than YAML-key membership: a node being REMOVED
// from YAML isn't in cfg.Servers anymore at plan time. The plan's
// Delete action carries the pre-state name; only the type stays
// stable. Type-based filter catches removals AND replacements
// uniformly.
func (r *Runner) PlannedNodeDestroys(ctx context.Context, planPath, serverResourceType string) ([]string, error) {
	plan, err := r.tf.ShowPlanFile(ctx, planPath)
	if err != nil {
		return nil, fmt.Errorf("read plan file %s: %w", planPath, err)
	}
	var nodes []string
	for _, rc := range plan.ResourceChanges {
		if rc.Type != serverResourceType || rc.Change == nil {
			continue
		}
		for _, a := range rc.Change.Actions {
			if a == tfjson.ActionDelete {
				nodes = append(nodes, rc.Name)
				break
			}
		}
	}
	sort.Strings(nodes)
	return nodes, nil
}
