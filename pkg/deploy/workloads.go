package deploy

import (
	"context"
	"fmt"

	"github.com/getnvoi/core/pkg/internal/kube"
	"github.com/getnvoi/core/pkg/internal/utils"
	"github.com/getnvoi/core/pkg/naming"
	"github.com/getnvoi/core/pkg/workload"
)

// deployWorkloads builds the typed kube client over the primary's
// existing SSH connection (the same one that installed k3s — kept
// alive by the caller's `defer closeShells`), labels every node with
// `nvoi-role=<yaml-key>` so workload nodeSelector / nodeAffinity
// match, then applies every nvoi-managed manifest and reconciles
// removal. Finally stands up cloudflared so its first reconnect
// finds upstream Services already present.
//
// Single SSH per server per deploy — no fresh dial here.
//
// Order:
//  1. Node labels MUST land before workloads — pods scheduled with a
//     nodeSelector on a not-yet-labeled node hang Pending.
//  2. Databases reconcile BEFORE workload.ApplyAll — services that
//     bind `databases: [PREFIX=name]` resolve SecretKeyRef against
//     the per-DB credentials Secret which only exists after the
//     databases step writes it.
//  3. cloudflared Deployment lands AFTER workload Services exist —
//     its first reconnect immediately finds the upstream Service DNS
//     resolvable.
//
// Stamps s.kc on the session so verbs that need a kube client after
// deploy can reuse it.
func (s *Session) deployWorkloads(ctx context.Context) error {
	rt, eps, shells := s.Rt, s.eps, s.shells
	primaryName := rt.Cfg.PrimaryMaster()
	primaryShell, ok := shells[primaryName]
	if !ok {
		return fmt.Errorf("primary master %s has no open shell", primaryName)
	}

	if s.kc == nil {
		s.Lg.Step("kube-tunnel")
		kc, err := kube.New(ctx, primaryShell)
		if err != nil {
			return fmt.Errorf("build kube client: %w", err)
		}
		s.kc = kc
	}
	kc := s.kc

	s.Lg.Step("node-labels")
	for _, key := range utils.SortedKeys(rt.Cfg.Servers) {
		hostname := naming.Server(rt.Cfg.App, rt.Cfg.Env, key)
		if err := kc.LabelNode(ctx, hostname, workload.LabelNvoiRole, key); err != nil {
			return fmt.Errorf("label node %s: %w", key, err)
		}
		s.Lg.Info(fmt.Sprintf("labeled %s with %s=%s", hostname, workload.LabelNvoiRole, key))
	}

	// Databases reconcile — runs BEFORE workload.ApplyAll so the
	// canonical credentials Secret is in place when services'
	// SecretKeyRef bindings resolve. Installs ZFS CSI (once globally)
	// + per-DB workloads + backup CronJobs. Idempotent.
	if err := s.deployDatabases(ctx); err != nil {
		return fmt.Errorf("databases: %w", err)
	}

	s.Lg.Step("workloads")
	if err := workload.ApplyAll(ctx, rt, kc, s.Lg); err != nil {
		return err
	}

	// cloudflared lands AFTER workload Services so its first reconnect
	// finds upstream Service DNS resolvable. Activated by the presence
	// of domains: (the only ingress path).
	if len(rt.Cfg.Domains) > 0 {
		if eps == nil || !eps.HasTunnel() {
			return fmt.Errorf("ingress: domains set but tofu tunnel output is empty (run plan + apply first)")
		}
		if err := kc.ApplyTunnel(ctx, s.Lg, kube.TunnelSpec{
			Token:    eps.Tunnel.Token,
			Replicas: 2,
		}); err != nil {
			return fmt.Errorf("apply tunnel: %w", err)
		}
	} else {
		if err := kc.SweepTunnel(ctx, s.Lg); err != nil {
			return fmt.Errorf("sweep abandoned tunnel: %w", err)
		}
	}

	return nil
}
