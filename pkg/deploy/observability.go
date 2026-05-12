package deploy

// observability.go is the deploy-phase entry point for the monitor:
// block. Hooks into Run after deployWorkloads. Unconditional call:
// when rt.Monitor == nil it sweeps the observability scope (cleans up
// any stack left over from a previous deploy that had monitor: set);
// when present, it stands up the full Prom+Thanos+Loki+Grafana stack.
//
// Idempotent — re-running against an unchanged config is a string of
// no-op kubectl Apply calls + a clean sweep of orphans (none).

import (
	"context"
	"fmt"

	apiruntime "k8s.io/apimachinery/pkg/runtime"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/core/pkg/internal/kube"
	"github.com/getnvoi/core/pkg/internal/observability"
	"github.com/getnvoi/core/pkg/log"
)

// deployObservability stands up the observability stack OR sweeps any
// prior stack when monitor: is unset. Idempotent.
//
// Caller: deploy.Run, after deployWorkloads. Requires s.kc to be
// populated (deployWorkloads sets it via kube.New); deployObservability
// reuses the same tunneled kube client.
//
// Pre-conditions:
//   - s.kc != nil (kube tunnel already open)
//   - rt.Backend != nil when rt.Monitor != nil (validated upstream)
//
// Errors propagate from bucket provisioning, manifest assembly, or
// individual ApplyOwned calls. Grafana not-yet-Ready is a Warn, not
// an error — first deploy can complete before Grafana finishes image
// pull + start.
func (s *Session) deployObservability(ctx context.Context) error {
	mlg := s.Rt.Log.Sub(log.KindMonitor)

	scope := kube.Scope{
		Namespace: observability.Namespace,
		Owner:     kube.OwnerObservability,
	}

	// rt.Monitor == nil: sweep any prior stack and return. Buckets
	// persist — deletion is `nvoi destroy` only. Namespace stays too
	// (cheap; re-create on next monitor: deploy).
	if s.Rt.Cfg.Monitor == nil {
		return s.sweepObservability(ctx, mlg, scope)
	}

	// ── Buckets ─────────────────────────────────────────────────────
	mlg.Step("monitor-buckets")
	creds, err := observability.EnsureBuckets(ctx, s.Rt, mlg)
	if err != nil {
		return fmt.Errorf("monitor buckets: %w", err)
	}

	// ── Namespace ───────────────────────────────────────────────────
	mlg.Step("monitor-namespace")
	if err := s.ensureObservabilityNamespace(ctx); err != nil {
		return fmt.Errorf("monitor namespace: %w", err)
	}

	// ── Stack: typed manifests for the whole stack ──────────────────
	mlg.Step("monitor-stack")
	objects, declared, err := observability.BuildStack(s.Rt, creds)
	if err != nil {
		return fmt.Errorf("monitor build stack: %w", err)
	}
	for _, obj := range objects {
		if err := s.kc.ApplyOwned(ctx, scope, obj); err != nil {
			return fmt.Errorf("apply %s: %w", typeName(obj), err)
		}
	}

	// ── Reconcile removal: sweep orphans ────────────────────────────
	mlg.Step("monitor-sweep")
	if err := s.reconcileObservabilityRemoval(ctx, scope, declared); err != nil {
		return fmt.Errorf("monitor sweep: %w", err)
	}

	// ── Wait for Grafana ────────────────────────────────────────────
	// Warn-only: first deploy may complete before image pull finishes.
	// Operator can `nvoi monitor` once it's ready; subsequent deploys
	// hit the Ready path instantly.
	mlg.Step("monitor-wait")
	if err := s.kc.WaitDeploymentReady(ctx, observability.Namespace, "grafana"); err != nil {
		mlg.Warn(fmt.Sprintf("grafana not yet Ready: %v (subsequent deploys / `nvoi monitor` will pick up the running pod)", err))
	} else {
		mlg.Info("grafana ready")
	}
	return nil
}

// ensureObservabilityNamespace creates the nvoi-observability namespace
// via kc.ApplyOwned. Idempotent — second call is a no-op label patch.
// Namespace stamping (nvoi/owner=observability) goes through the same
// path as every other owned resource.
func (s *Session) ensureObservabilityNamespace(ctx context.Context) error {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: observability.Namespace},
	}
	return s.kc.ApplyOwned(ctx, kube.Scope{Owner: kube.OwnerObservability}, ns)
}

// sweepObservability tears down the full stack when monitor: is unset.
// Sweeps every Kind we manage with nil desired-name (purge all).
// Buckets persist (deleted only on `nvoi destroy`); namespace persists
// (cheap; reused on next monitor: deploy).
func (s *Session) sweepObservability(ctx context.Context, lg log.Log, scope kube.Scope) error {
	lg.Step("monitor-teardown")
	for _, k := range []kube.Kind{
		kube.KindDeployment, kube.KindStatefulSet, kube.KindDaemonSet,
		kube.KindConfigMap, kube.KindSecret, kube.KindService,
		kube.KindIngress,
		kube.KindServiceAccount, kube.KindClusterRole, kube.KindClusterRoleBinding,
	} {
		if err := s.kc.SweepOwned(ctx, scope, k, nil); err != nil {
			return fmt.Errorf("sweep %s: %w", k, err)
		}
	}
	return nil
}

// reconcileObservabilityRemoval sweeps each Kind down to its declared
// names. Per-Kind sweep so dropping (say) a service from cfg.Services
// doesn't accidentally affect dashboards or alert rules — they're
// declared independently by BuildStack.
func (s *Session) reconcileObservabilityRemoval(
	ctx context.Context,
	scope kube.Scope,
	declared observability.DeclaredNames,
) error {
	sweeps := []struct {
		kind  kube.Kind
		names []string
	}{
		{kube.KindDeployment, declared.Deployments},
		{kube.KindStatefulSet, declared.StatefulSets},
		{kube.KindDaemonSet, declared.DaemonSets},
		{kube.KindService, declared.Services},
		{kube.KindConfigMap, declared.ConfigMaps},
		{kube.KindSecret, declared.Secrets},
		{kube.KindServiceAccount, declared.ServiceAccounts},
		{kube.KindClusterRole, declared.ClusterRoles},
		{kube.KindClusterRoleBinding, declared.ClusterRoleBindings},
		{kube.KindIngress, declared.Ingresses},
	}
	for _, sw := range sweeps {
		if err := s.kc.SweepOwned(ctx, scope, sw.kind, sw.names); err != nil {
			return fmt.Errorf("sweep %s: %w", sw.kind, err)
		}
	}
	return nil
}

// typeName extracts a human-friendly type tag for log messages —
// e.g. "*v1.Deployment". Used in apply-error wrapping so the
// operator knows WHICH object failed without traceback noise.
func typeName(obj apiruntime.Object) string {
	return fmt.Sprintf("%T", obj)
}
