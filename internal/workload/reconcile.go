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
//   - one Deployment + one Service per cfg.Services entry
//
// Then runs ReconcileRemoval to delete nvoi-managed Deployments /
// Services whose YAML entry is gone.
//
// Idempotent: re-running on an unchanged cluster is a no-op string
// of Get-then-Update calls (Apply paths preserve ResourceVersion).
func ApplyAll(ctx context.Context, rt *runtime.Runtime, kc *kube.Client, lg log.Log) error {
	if sec, err := BuildRegistrySecret(rt); err != nil {
		return fmt.Errorf("build registry-auth: %w", err)
	} else if sec != nil {
		lg.Step("registry-secret")
		if err := kc.ApplySecret(ctx, namespace, sec); err != nil {
			return fmt.Errorf("apply registry-auth: %w", err)
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

		dep := BuildDeployment(rt, name, svc)
		if err := kc.ApplyDeployment(ctx, namespace, dep); err != nil {
			return fmt.Errorf("apply deployment %s: %w", name, err)
		}
		ksvc := BuildService(rt, name, svc)
		if err := kc.ApplyService(ctx, namespace, ksvc); err != nil {
			return fmt.Errorf("apply service %s: %w", name, err)
		}
	}

	return ReconcileRemoval(ctx, rt, kc, lg)
}

// ReconcileRemoval deletes nvoi-managed Deployments and Services
// whose YAML entry has been removed. Filter is `nvoi/owner=nvoi`
// (set by every Build*); name-not-in-cfg.Services means stale.
func ReconcileRemoval(ctx context.Context, rt *runtime.Runtime, kc *kube.Client, lg log.Log) error {
	declared := make(map[string]bool, len(rt.Cfg.Services))
	for name := range rt.Cfg.Services {
		declared[name] = true
	}

	deps, err := kc.ListNvoiDeployments(ctx, namespace)
	if err != nil {
		return fmt.Errorf("list nvoi deployments: %w", err)
	}
	for _, name := range deps {
		if declared[name] {
			continue
		}
		lg.Info(fmt.Sprintf("removing stale Deployment %s (no longer in YAML)", name))
		if err := kc.DeleteDeployment(ctx, namespace, name); err != nil {
			return fmt.Errorf("delete deployment %s: %w", name, err)
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
		lg.Info(fmt.Sprintf("removing stale Service %s (no longer in YAML)", name))
		if err := kc.DeleteService(ctx, namespace, name); err != nil {
			return fmt.Errorf("delete service %s: %w", name, err)
		}
	}

	return nil
}
