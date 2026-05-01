package deploy

import (
	"context"
	"fmt"

	"github.com/getnvoi/core/pkg/compile"
	"github.com/getnvoi/core/pkg/kube"
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
	for _, key := range sortedStringKeys(rt.Cfg.Servers) {
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

// deployIngress is the post-workloads ingress reconcile. Two modes,
// selected by cfg.Providers.Tunnel:
//
//   - Tunnel mode (set): apply the cloudflared agent (Deployment +
//     Secret with the token from `terraform output`), wait Ready,
//     sweep any leftover Caddy from a prior Caddy-mode deploy.
//     DNS records are tf-managed (CNAME → local.tunnel_cname); nvoi
//     does not touch DNS imperatively.
//
//   - Caddy mode (unset): EnsureCaddy in kube-system, reload its
//     config via the admin API (atomic listener swap), per-domain
//     WaitForCaddyCert + WaitForCaddyHTTPS from inside the pod,
//     sweep any leftover tunnel-agent workloads from a prior
//     tunnel-mode deploy.
func (s *Session) deployIngress(ctx context.Context) error {
	rt, kc := s.Rt, s.kc
	if rt.Cfg.Providers.Tunnel != "" {
		return s.deployTunnelIngress(ctx)
	}

	s.Lg.Step("caddy")
	if err := kc.EnsureCaddy(ctx); err != nil {
		return fmt.Errorf("ensure caddy: %w", err)
	}

	// Resolve per-service ports from the live Services we just applied.
	routes := make([]kube.CaddyRoute, 0, len(rt.Cfg.Domains))
	for _, svcName := range sortedStringKeys(rt.Cfg.Domains) {
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
	for _, svcName := range sortedStringKeys(rt.Cfg.Domains) {
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

	// Cross-mode cleanup: if a previous deploy ran in tunnel mode,
	// the cloudflared / ngrok agent workloads are still around.
	// Purge them now that Caddy is serving.
	s.Lg.Step("purge-tunnel-agent")
	if err := purgeOwner(ctx, kc, kube.Scope{Namespace: "default", Owner: kube.OwnerTunnelAgent}); err != nil {
		s.Lg.Warn(fmt.Sprintf("purge tunnel-agent (cross-mode cleanup): %v", err))
	}
	return nil
}

// deployTunnelIngress is the tunnel-mode counterpart to the Caddy
// path. terraform owns every cloud resource — tunnel object, ingress
// config, AND public DNS records (CNAME → local.tunnel_cname). nvoi
// owns ONLY the kube-side agent:
//
//  1. Apply cloudflared agent (Deployment + Secret) using the token
//     from `terraform output tunnel_token`.
//  2. Wait for the agent Deployment to reach Ready so subsequent
//     deploys can rely on it.
//  3. Sweep orphan Caddy from kube-system (cross-mode cleanup —
//     reclaims hostPort 80/443 if the prior deploy was Caddy-mode).
//
// Brief 502 window during initial deploy or Caddy → tunnel mode
// switch is accepted by design: tf flips DNS records (A → CNAME or
// fresh CNAME) atomically with `tf-apply`, the cloudflared agent
// only comes up in this workload phase, so traffic hits the CF
// edge before the agent has registered. ~30s–2min, one-time per
// app. Documented in CLAUDE.md.
func (s *Session) deployTunnelIngress(ctx context.Context) error {
	rt, eps, kc := s.Rt, s.eps, s.kc
	if eps.TunnelToken == "" {
		return fmt.Errorf("tunnel mode: terraform output tunnel_token is empty")
	}

	tun, err := compile.ResolveTunnel(rt.Cfg.Providers.Tunnel)
	if err != nil {
		return fmt.Errorf("resolve tunnel emitter: %w", err)
	}

	s.Lg.Step("tunnel-agent")
	workloads, err := tun.AgentWorkloads(eps.TunnelToken)
	if err != nil {
		return fmt.Errorf("build tunnel-agent workloads: %w", err)
	}
	for _, w := range workloads {
		if err := kc.ApplyOwned(ctx, kube.Scope{Namespace: "default", Owner: kube.OwnerTunnelAgent}, w.Obj); err != nil {
			return fmt.Errorf("apply tunnel-agent %s/%s: %w", w.Kind, w.Name, err)
		}
		s.Lg.Info(fmt.Sprintf("applied tunnel-agent %s/%s", w.Kind, w.Name))
	}

	s.Lg.Step("tunnel-agent-ready")
	if err := kc.WaitDeploymentReady(ctx, "default", "cloudflared"); err != nil {
		return fmt.Errorf("wait cloudflared ready: %w", err)
	}

	s.Lg.Step("purge-caddy")
	if err := purgeOwner(ctx, kc, kube.Scope{Namespace: kube.CaddyNamespace, Owner: kube.OwnerCaddy}); err != nil {
		s.Lg.Warn(fmt.Sprintf("purge caddy (cross-mode cleanup): %v", err))
	}
	return nil
}

// purgeOwner deletes every resource of every Kind carrying
// nvoi/owner=<scope.Owner> in scope.Namespace. Used for cross-mode
// ingress transitions (caddy ↔ tunnel-agent).
func purgeOwner(ctx context.Context, kc *kube.Client, scope kube.Scope) error {
	for _, kind := range []kube.Kind{
		kube.KindDeployment, kube.KindStatefulSet, kube.KindService,
		kube.KindSecret, kube.KindConfigMap, kube.KindPVC,
	} {
		if err := kc.SweepOwned(ctx, scope, kind, nil); err != nil {
			return fmt.Errorf("sweep %s: %w", kind, err)
		}
	}
	return nil
}
