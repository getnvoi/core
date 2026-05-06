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
// removal.
//
// Single SSH per server per deploy — no fresh dial here.
//
// Order matters: labels MUST land before workloads. New pods
// scheduled with a nodeSelector on a not-yet-labeled node hang
// Pending until the label arrives.
//
// Stamps s.kc on the session so the ingress phase reuses it.
func (s *Session) deployWorkloads(ctx context.Context) error {
	rt, eps, shells := s.Rt, s.eps, s.shells
	primaryName := rt.Cfg.PrimaryMaster()
	primaryShell, ok := shells[primaryName]
	if !ok {
		return fmt.Errorf("primary master %s has no open shell", primaryName)
	}

	s.Lg.Step("kube-tunnel")
	kc, err := kube.New(ctx, primaryShell)
	if err != nil {
		return fmt.Errorf("build kube client: %w", err)
	}
	s.kc = kc
	defer func() {
		_ = kc.Close()
		s.kc = nil
	}()

	s.Lg.Step("node-labels")
	for _, key := range utils.SortedKeys(rt.Cfg.Servers) {
		hostname := naming.Server(rt.Cfg.App, rt.Cfg.Env, key)
		if err := kc.LabelNode(ctx, hostname, workload.LabelNvoiRole, key); err != nil {
			return fmt.Errorf("label node %s: %w", key, err)
		}
		s.Lg.Info(fmt.Sprintf("labeled %s with %s=%s", hostname, workload.LabelNvoiRole, key))
	}

	s.Lg.Step("workloads")
	if err := workload.ApplyAll(ctx, rt, kc, s.Lg); err != nil {
		return err
	}

	if len(eps.Servers) == 0 || len(rt.Cfg.Domains) == 0 {
		return nil
	}
	return s.deployIngress(ctx)
}

// deployIngress is the post-workloads ingress reconcile. EnsureCaddy
// in kube-system, reload its config via the admin API (atomic listener
// swap), per-domain WaitForCaddyCert + WaitForCaddyHTTPS from inside
// the pod.
func (s *Session) deployIngress(ctx context.Context) error {
	rt, kc := s.Rt, s.kc

	s.Lg.Step("caddy")
	if err := kc.EnsureCaddy(ctx); err != nil {
		return fmt.Errorf("ensure caddy: %w", err)
	}

	// Resolve per-service ports from the live Services we just applied.
	routes := make([]kube.CaddyRoute, 0, len(rt.Cfg.Domains))
	for _, svcName := range utils.SortedKeys(rt.Cfg.Domains) {
		port, err := kc.GetServicePort(ctx, "default", svcName)
		if err != nil {
			return fmt.Errorf("ingress: service %q port: %w", svcName, err)
		}
		routes = append(routes, kube.CaddyRoute{
			Service: svcName,
			Port:    port,
			Domains: rt.Cfg.Domains[svcName],
		})
	}

	configJSON, err := kube.BuildCaddyConfig(kube.CaddyConfigInput{
		Namespace: "default",
		Routes:    routes,
		ACMEEmail: rt.Cfg.ACMEEmail,
	})
	if err != nil {
		return fmt.Errorf("build caddy config: %w", err)
	}

	s.Lg.Step("caddy-reload")
	if err := kc.ReloadCaddyConfig(ctx, configJSON); err != nil {
		return err
	}
	s.Lg.Info("caddy config loaded")

	// Per-domain cert + HTTPS verification. Warn-and-continue posture:
	// timeouts surface but don't fail the deploy. Caddy retries ACME.
	for _, svcName := range utils.SortedKeys(rt.Cfg.Domains) {
		for _, domain := range rt.Cfg.Domains[svcName] {
			s.Lg.Step("cert-" + domain)
			if err := kc.WaitForCaddyCert(ctx, domain); err != nil {
				s.Lg.Warn(fmt.Sprintf("%s: certificate not issued in time — next deploy re-verifies (%v)", domain, err))
				continue
			}
			s.Lg.Info(fmt.Sprintf("certificate ready: %s", domain))

			s.Lg.Step("https-" + domain)
			if err := kc.WaitForCaddyHTTPS(ctx, domain, "/healthz"); err != nil {
				s.Lg.Warn(fmt.Sprintf("https://%s/healthz: probe failed — next deploy re-verifies (%v)", domain, err))
				continue
			}
			s.Lg.Info(fmt.Sprintf("https live: https://%s/", domain))
		}
	}
	return nil
}
