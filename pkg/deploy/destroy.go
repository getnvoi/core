package deploy

import (
	"context"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/log"
	"github.com/getnvoi/core/pkg/runtime"
)

// Destroy is the destroy verb's workflow.
//
// Plan-then-apply, same shape as deploy minus the workload phase.
// Path is RELATIVE to terraform's cwd (rt.WorkDir); don't filepath.Join.
func Destroy(ctx context.Context, rt *runtime.Runtime) error {
	return RunWithSession(ctx, rt, log.KindInfra, func(ctx context.Context, s *Session) error {
		s.Lg.Step("tf-init")
		if err := s.Init(ctx); err != nil {
			return err
		}

		const planPath = "destroy.tfplan"
		s.Lg.Step("tf-plan-destroy")
		hasChanges, err := s.Run.PlanDestroyWithOut(ctx, planPath)
		if err != nil {
			return err
		}
		if !hasChanges {
			s.Lg.Info("nothing to destroy")
			return nil
		}

		// Pre-destroy: drain cert-manager — Traefik mode only.
		// Deleting Certificate resources triggers cert-manager's
		// Challenge finalizers, which call the DNS-01 solver's
		// Cleanup() and remove the scratch `_acme-challenge.<domain>`
		// TXT records via the provider's API. Without this, those
		// records orphan in the operator's zone — tofu can't clean
		// them up because they were never in tfstate.
		//
		// Tunnel mode has no cert-manager / Certificates in-cluster;
		// the tunnel + CNAMEs are tofu-managed and reaped by the
		// normal tf-destroy.
		//
		// Best-effort: drain failures warn but don't block destroy.
		if rt.Cfg.Providers.IngressMode() == config.IngressTraefik {
			if err := s.drainCertificates(ctx); err != nil {
				return err
			}
		}

		s.Lg.Step("tf-destroy")
		return s.Run.ApplyPlan(ctx, planPath)
	})
}
