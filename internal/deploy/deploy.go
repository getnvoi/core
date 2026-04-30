package deploy

import (
	"context"

	"github.com/getnvoi/core/internal/build"
	"github.com/getnvoi/core/internal/runner"
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
func Run(ctx context.Context, rt *runtime.Runtime) error {
	return WithRunner(ctx, rt, func(ctx context.Context, run *runner.Runner) error {
		s := &session{rt: rt, run: run}

		// ── pre-infra: build phase ──
		// Conditional: build.All is a no-op when no service has
		// build: set. Failures abort BEFORE any infra change.
		if err := build.All(ctx, rt, build.DockerRunner{}, rt.Log); err != nil {
			return err
		}

		rt.Log.Step("tf-init")
		if err := run.Init(ctx); err != nil {
			return err
		}

		// ── plan → drain doomed nodes → apply ──
		// Plan path is RELATIVE to terraform's cwd (which is
		// already rt.WorkDir). Don't filepath.Join — that
		// double-resolves and terraform errors with "no such
		// directory."
		const planPath = "plan.tfplan"
		rt.Log.Step("tf-plan")
		hasChanges, err := run.PlanWithOut(ctx, planPath)
		if err != nil {
			return err
		}
		if hasChanges {
			if err := detachNode(ctx, rt, run, planPath); err != nil {
				return err
			}
			if err := drainTunnel(ctx, rt, run, planPath); err != nil {
				return err
			}
			rt.Log.Step("tf-apply")
			if err := run.ApplyPlan(ctx, planPath); err != nil {
				return err
			}
		} else {
			rt.Log.Info("no terraform changes")
		}

		// ── post-apply: open SSH to every server ONCE,
		// thread through install + workloads, close all at the
		// end. Single SSH per server per command — same connection
		// installs k3s AND tunnels the kube apiserver for workloads.
		rt.Log.Step("endpoints")
		eps, err := run.Endpoints(ctx)
		if err != nil {
			return err
		}
		s.eps = eps

		rt.Log.Step("ssh")
		shells, err := openShells(ctx, rt, eps)
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
