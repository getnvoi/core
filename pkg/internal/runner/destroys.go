package runner

import (
	"context"
	"fmt"
	"sort"

	tfjson "github.com/hashicorp/terraform-json"
)

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
	return planTypeDestroys(plan, serverResourceType), nil
}

// planTypeDestroys is the shared pure walker — any future plan-gated
// drain target wants the same primitive: "names of resources of type T
// that the plan will delete." Replacement (delete+create) counts as a
// destroy — the underlying object is going away even if a new one with
// the same address takes its place.
//
// Pure — pulled out so tests can pass a hand-crafted *tfjson.Plan
// instead of needing a real tofu binary + plan file on disk.
func planTypeDestroys(plan *tfjson.Plan, resourceType string) []string {
	var names []string
	for _, rc := range plan.ResourceChanges {
		if rc.Type != resourceType || rc.Change == nil {
			continue
		}
		for _, a := range rc.Change.Actions {
			if a == tfjson.ActionDelete {
				names = append(names, rc.Name)
				break
			}
		}
	}
	sort.Strings(names)
	return names
}
