package deploy

import (
	"context"
	"fmt"

	"github.com/getnvoi/core/pkg/internal/build"
	"github.com/getnvoi/core/pkg/internal/compile"
	"github.com/getnvoi/core/pkg/log"
	"github.com/getnvoi/core/pkg/runtime"
)

// Run is the deploy verb's workflow.
//
// Phases (from CLAUDE.md):
//
//	build               only when any service has build: set
//	tf-init / tf-plan -out
//	detach              only when nodes leaving — drain → etcd member remove →
//	                    kubectl delete node (for masters; workers skip etcd)
//	tf-apply <plan>     applies the saved plan, no re-plan
//	endpoints           parse terraform output (servers, api_endpoint, ha)
//	openShells          one ssh.Client per server, kept alive through end
//	installCluster      swap → discover token → primary --cluster-init →
//	                    secondaries --server → workers via api_endpoint.private
//	kube-tunnel         kube.New(primaryShell) — apiserver via the same SSH
//	workloads           registry-auth Secret → Deployments → Services →
//	                    reconcile (delete nvoi-managed objects no longer in YAML)
//	defer closeShells   one close per server, end of command
//
// Each phase emits through a kind-scoped sub-logger:
//   - infraLg   — tf-init / tf-plan / tf-apply / endpoints / detach
//   - buildLg   — build phase (docker login + buildx)
//   - clusterLg — ssh open + k3s install + workloads + ingress
func Run(ctx context.Context, rt *runtime.Runtime) error {
	// Cluster kind is the default scope of the Session — install /
	// kube / caddy work all live there. Infra-phase steps (tf-init,
	// tf-plan, endpoints, predrains) re-scope to KindInfra via local
	// Sub() calls. Build phase gets its own KindBuild scope handed
	// straight to build.All.
	return RunWithSession(ctx, rt, log.KindCluster, func(ctx context.Context, s *Session) error {
		infraLg := rt.Log.Sub(log.KindInfra)
		buildLg := rt.Log.Sub(log.KindBuild)

		// ── pre-infra: build phase ──
		// Conditional: build.All is a no-op when no service has
		// build: set. Failures abort BEFORE any infra change.
		if err := build.All(ctx, rt, build.DockerRunner{}, buildLg); err != nil {
			return err
		}

		infraLg.Step("tf-init")
		if err := s.Init(ctx); err != nil {
			return err
		}

		// ── plan → drain doomed nodes → apply ──
		// Plan path is RELATIVE to terraform's cwd (rt.WorkDir).
		// Don't filepath.Join — terraform double-resolves.
		const planPath = "plan.tfplan"
		infraLg.Step("tf-plan")
		hasChanges, err := s.Run.PlanWithOut(ctx, planPath)
		if err != nil {
			return err
		}
		if hasChanges {
			// Pre-detach workload reconcile: when the plan destroys
			// nodes AND the cluster already exists, apply the new
			// workload spec FIRST so Deployment rolling-updates
			// migrate pods OFF doomed nodes before drain hits them.
			//
			// Without this, the pod's only replica is killed at
			// drain time and the new replica only schedules later in
			// the workloads phase — Service.Endpoints flips to empty
			// in between and Traefik returns 503 to clients for the
			// whole window (image pull + start-up).
			//
			// Idempotent: deployWorkloads is safe to call twice;
			// the second call (post-apply) is a no-op when nothing
			// changed.
			if err := s.preDetachWorkloadReconcile(ctx, planPath); err != nil {
				return err
			}

			// predrains run BEFORE tf-apply but log under kind=infra
			// (preparation for an infra mutation), so we swap Lg for
			// the drain calls and restore after.
			s.Lg, infraLg = infraLg, s.Lg
			if err := s.detachNode(ctx, planPath); err != nil {
				return err
			}
			s.Lg, infraLg = infraLg, s.Lg

			infraLg.Step("tf-apply")
			if err := s.Run.ApplyPlan(ctx, planPath); err != nil {
				return err
			}
		} else {
			infraLg.Info("no tofu changes")
		}

		// ── post-apply: open SSH to every server ONCE,
		// thread through install + workloads, close all at the
		// end. Single SSH per server per command — same connection
		// installs k3s AND tunnels the kube apiserver for workloads.
		infraLg.Step("endpoints")
		eps, err := s.Endpoints(ctx)
		if err != nil {
			return err
		}

		s.Lg.Step("ssh")
		shells, err := openShells(ctx, rt, s.Lg, eps)
		if err != nil {
			return err
		}
		s.shells = shells
		defer closeShells(shells)

		if err := s.installCluster(ctx); err != nil {
			return err
		}

		// ── workloads phase: ALWAYS runs when cluster phase has any
		// reason to engage (services, registry, secrets, domains, OR
		// monitor). The phase installs cluster-level addons
		// (metrics-server, kube-state-metrics) + node labels
		// regardless of whether there are app workloads — observability
		// needs those addons even on a service-less cluster.
		//
		// Short-circuit ONLY when there's literally nothing to do.
		needCluster := s.workloadsHaveContent() || s.Rt.Cfg.Monitor != nil
		if !needCluster {
			return nil
		}
		if err := s.deployWorkloads(ctx); err != nil {
			return err
		}

		// ── observability: monitor: stack (gated). Runs unconditionally
		// when monitor: was previously set, so flipping it to nil sweeps
		// the prior stack on the next deploy.
		return s.deployObservability(ctx)
	})
}

// preDetachWorkloadReconcile runs deployWorkloads against the existing
// cluster BEFORE detach + tf-apply, when the plan removes nodes. This
// gives the new workload spec (e.g. unpinned `whoami` after the
// pinned-to node is dropped) a chance to roll out via the Deployment's
// default rolling-update strategy: a new pod schedules on a surviving
// node and reaches Ready BEFORE the old pod is killed (drain) or its
// node is destroyed (tf-apply). Service.Endpoints stays non-empty
// throughout → Traefik never sees zero-endpoint backends → no 503.
//
// Skipped when:
//   - the plan has no node destroys (nothing to migrate off of);
//   - reading endpoints fails (cluster doesn't exist yet — first
//     deploy; nothing to reconcile against);
//   - the YAML has no workloads + no domains (workload phase would
//     be a no-op anyway).
//
// Uses a SEPARATE shells map than the post-apply deploy. Pre-detach,
// the cluster includes the to-be-destroyed nodes; post-apply only
// surviving nodes exist. Opening shells against the post-apply set
// would fail until tf-apply runs.
func (s *Session) preDetachWorkloadReconcile(ctx context.Context, planPath string) error {
	if !s.workloadsHaveContent() {
		return nil
	}

	serverType, err := compile.ServerResourceType(s.Rt.Cfg.Providers.Infra)
	if err != nil {
		return fmt.Errorf("server resource type: %w", err)
	}
	leaving, err := s.Run.PlannedNodeDestroys(ctx, planPath, serverType)
	if err != nil {
		return fmt.Errorf("plan destroys: %w", err)
	}
	if len(leaving) == 0 {
		return nil
	}

	// Read pre-apply endpoints DIRECTLY from the runner (NOT via
	// s.Endpoints() — that memoizes onto the Session, and we'd then
	// hand stale endpoints to the post-apply ssh/install/workloads
	// phases, which would dial servers that tf-apply just destroyed).
	eps, err := s.Run.Endpoints(ctx)
	if err != nil {
		s.Lg.Warn(fmt.Sprintf("pre-detach reconcile: cluster endpoints unavailable (%v); skipping — workloads will reconcile post-apply", err))
		return nil
	}
	if len(eps.Servers) == 0 {
		// Plan-time endpoints exist but no servers in current state
		// (cold start). Nothing to reconcile against.
		return nil
	}

	s.Lg.Step("pre-detach-reconcile")
	s.Lg.Info(fmt.Sprintf("rolling new workload spec onto existing cluster before %d node destroy(s): %v", len(leaving), leaving))

	shells, err := openShells(ctx, s.Rt, s.Lg, eps)
	if err != nil {
		return fmt.Errorf("pre-detach: open shells: %w", err)
	}
	defer closeShells(shells)

	// Stamp pre-apply state on Session so deployWorkloads sees it,
	// then RESTORE both fields so the post-apply pipeline re-reads
	// fresh state (eps=nil → Endpoints() re-fetches from the runner;
	// shells=prev → main flow opens its own shells).
	prevShells, prevEps := s.shells, s.eps
	s.shells, s.eps = shells, eps
	defer func() { s.shells, s.eps = prevShells, prevEps }()

	if err := s.deployWorkloads(ctx); err != nil {
		return fmt.Errorf("pre-detach reconcile: %w", err)
	}
	s.Lg.Info("pre-detach reconcile complete; pods migrated off doomed nodes")
	return nil
}

// workloadsHaveContent mirrors the gating in Run for the post-apply
// workloads call: skip when nothing in YAML drives the in-cluster
// pipeline. Single source of truth so pre- and post-apply gating stay
// in lockstep.
func (s *Session) workloadsHaveContent() bool {
	rt := s.Rt
	return len(rt.Cfg.Services) > 0 ||
		len(rt.RegistryCreds) > 0 ||
		len(rt.Cfg.Secrets) > 0 ||
		len(rt.Cfg.Domains) > 0
}
