package deploy

import (
	"context"

	"github.com/getnvoi/core/internal/build"
	"github.com/getnvoi/core/internal/log"
	"github.com/getnvoi/core/internal/runtime"
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
//   - infraLg   — tf-init / tf-plan / tf-apply / endpoints / detach / drain-tunnel
//   - buildLg   — build phase (docker login + buildx)
//   - clusterLg — ssh open + k3s install + workloads + ingress
func Run(ctx context.Context, rt *runtime.Runtime) error {
	// Cluster kind is the default scope of the Session — install /
	// kube / caddy / tunnel work all live there. Infra-phase steps
	// (tf-init, tf-plan, endpoints, predrains) re-scope to KindInfra
	// via local Sub() calls. Build phase gets its own KindBuild
	// scope handed straight to build.All.
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
			// predrains run BEFORE tf-apply but log under kind=infra
			// (preparation for an infra mutation), so we swap Lg for
			// the drain calls and restore after.
			s.Lg, infraLg = infraLg, s.Lg
			if err := s.detachNode(ctx, planPath); err != nil {
				return err
			}
			if err := s.drainTunnel(ctx, planPath); err != nil {
				return err
			}
			s.Lg, infraLg = infraLg, s.Lg

			infraLg.Step("tf-apply")
			if err := s.Run.ApplyPlan(ctx, planPath); err != nil {
				return err
			}
		} else {
			infraLg.Info("no terraform changes")
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

		// ── workloads + ingress: kube tunnel via primary's shell ──
		// Skipped only when there's nothing for the in-cluster
		// pipeline to do — no services, no secrets to publish, no
		// registry to set up, no domains to front.
		if len(rt.Cfg.Services) == 0 &&
			len(rt.Cfg.Registry) == 0 &&
			len(rt.Cfg.Secrets) == 0 &&
			len(rt.Cfg.Domains) == 0 {
			return nil
		}
		return s.deployWorkloads(ctx)
	})
}
