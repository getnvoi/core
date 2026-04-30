package workload

import (
	"context"
	"fmt"
	"sort"

	"github.com/getnvoi/core/internal/kube"
	"github.com/getnvoi/core/internal/log"
	"github.com/getnvoi/core/internal/runtime"
)

// ApplyAll applies every nvoi-managed manifest:
//   - registry-auth Secret (when registry: declared)
//   - nvoi-secrets Secret (when secrets: declared)
//   - one Deployment OR StatefulSet per cfg.Services entry
//     (kind dispatched by svc.Storage presence)
//   - one Service per cfg.Services entry (ClusterIP, or headless for
//     stateful)
//
// Then runs ReconcileRemoval to delete nvoi-managed Deployments /
// StatefulSets / Services whose YAML entry is gone.
//
// Idempotent: re-running on an unchanged cluster is a string of
// Get-then-Update calls (Apply paths preserve ResourceVersion + the
// fields that must not be mutated, like Service.ClusterIP).
func ApplyAll(ctx context.Context, rt *runtime.Runtime, kc *kube.Client, lg log.Log) error {
	if sec, err := BuildRegistrySecret(rt); err != nil {
		return fmt.Errorf("build registry-auth: %w", err)
	} else if sec != nil {
		lg.Step("registry-secret")
		if err := kc.ApplySecret(ctx, namespace, sec); err != nil {
			return fmt.Errorf("apply registry-auth: %w", err)
		}
	}

	if sec := BuildAppSecret(rt); sec != nil {
		lg.Step("app-secret")
		if err := kc.ApplySecret(ctx, namespace, sec); err != nil {
			return fmt.Errorf("apply nvoi-secrets: %w", err)
		}
	}

	// Sorted iteration → deterministic apply order, easier-to-read logs.
	names := make([]string, 0, len(rt.Cfg.Services))
	for name := range rt.Cfg.Services {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		svc := rt.Cfg.Services[name]
		lg.Step("workload-" + name)

		if svc.IsStateful() {
			ss := BuildStatefulSet(rt, name, svc)
			if err := kc.ApplyStatefulSet(ctx, namespace, ss); err != nil {
				return fmt.Errorf("apply statefulset %s: %w", name, err)
			}
		} else {
			dep := BuildDeployment(rt, name, svc)
			if err := kc.ApplyDeployment(ctx, namespace, dep); err != nil {
				return fmt.Errorf("apply deployment %s: %w", name, err)
			}
		}
		ksvc := BuildService(rt, name, svc)
		if err := kc.ApplyService(ctx, namespace, ksvc); err != nil {
			return fmt.Errorf("apply service %s: %w", name, err)
		}
	}

	return ReconcileRemoval(ctx, rt, kc, lg)
}

// ReconcileRemoval deletes nvoi-managed Deployments / StatefulSets /
// Services whose YAML entry has been removed. Filter is `nvoi/owner=nvoi`
// (set by every Build*); name-not-in-cfg.Services means stale.
//
// Stateful and stateless services share a namespace of names — a
// service that flips between Deployment ↔ StatefulSet (via adding /
// removing `storage:`) shows up as "current kind exists, other kind
// orphan" and gets cleaned up here on the next deploy.
func ReconcileRemoval(ctx context.Context, rt *runtime.Runtime, kc *kube.Client, lg log.Log) error {
	declared := make(map[string]bool, len(rt.Cfg.Services))
	for name := range rt.Cfg.Services {
		declared[name] = true
	}
	// Stateful services declare BOTH a StatefulSet AND a Service. A
	// stateless service declares Deployment + Service. So the same
	// "is it declared?" rule applies to all three lists; the kind a
	// declared name takes is determined by svc.IsStateful() at apply
	// time. ReconcileRemoval doesn't need to know that — it just
	// deletes anything whose name isn't declared at all.

	deps, err := kc.ListNvoiDeployments(ctx, namespace)
	if err != nil {
		return fmt.Errorf("list nvoi deployments: %w", err)
	}
	for _, name := range deps {
		// A name that's declared AS stateful needs the orphan
		// Deployment from the previous (stateless) deploy removed.
		// So we delete unless declared AND currently stateless.
		if declared[name] && !rt.Cfg.Services[name].IsStateful() {
			continue
		}
		lg.Info(fmt.Sprintf("removing stale Deployment %s", name))
		if err := kc.DeleteDeployment(ctx, namespace, name); err != nil {
			return fmt.Errorf("delete deployment %s: %w", name, err)
		}
	}

	sets, err := kc.ListNvoiStatefulSets(ctx, namespace)
	if err != nil {
		return fmt.Errorf("list nvoi statefulsets: %w", err)
	}
	for _, name := range sets {
		// Symmetric: delete unless declared AND currently stateful.
		if declared[name] && rt.Cfg.Services[name].IsStateful() {
			continue
		}
		lg.Info(fmt.Sprintf("removing stale StatefulSet %s", name))
		if err := kc.DeleteStatefulSet(ctx, namespace, name); err != nil {
			return fmt.Errorf("delete statefulset %s: %w", name, err)
		}
	}

	svcs, err := kc.ListNvoiServices(ctx, namespace)
	if err != nil {
		return fmt.Errorf("list nvoi services: %w", err)
	}
	for _, name := range svcs {
		if declared[name] {
			continue
		}
		lg.Info(fmt.Sprintf("removing stale Service %s", name))
		if err := kc.DeleteService(ctx, namespace, name); err != nil {
			return fmt.Errorf("delete service %s: %w", name, err)
		}
	}

	return nil
}
