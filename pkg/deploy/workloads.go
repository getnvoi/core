package deploy

import (
	"context"
	"fmt"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/internal/compile"
	"github.com/getnvoi/core/pkg/internal/kube"
	"github.com/getnvoi/core/pkg/internal/observability"
	"github.com/getnvoi/core/pkg/internal/utils"
	"github.com/getnvoi/core/pkg/naming"
	"github.com/getnvoi/core/pkg/ssh"
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
// Order matters:
//  1. Node labels MUST land before workloads — pods scheduled with a
//     nodeSelector on a not-yet-labeled node hang Pending.
//  2. Traefik mode only: cert-manager + ClusterIssuer + Certificates
//     MUST land before workload Ingresses — Ingresses reference TLS
//     Secret names that cert-manager creates in response to the
//     Certificate resources. (Ingress applies even before cert lands;
//     Traefik just refuses TLS until the Secret exists. Cert is
//     async; warn-and-continue.)
//  3. Tunnel mode only: cloudflared Deployment lands AFTER workload
//     Services exist — its first reconnect immediately finds the
//     upstream Service DNS resolvable.
//
// Mode flip reconcile: deploying with the OPPOSITE ingress mode of
// the prior deploy sweeps the abandoned side's owned objects so the
// cluster converges in one pass.
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

	// kube client opens ONCE per Run and stays alive across
	// deployWorkloads + deployObservability (caller closes via the
	// deferred kube cleanup in Run). Idempotent open — if a previous
	// phase already built kc, reuse it.
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

	// Cluster-level addons. Independent of the observability stack —
	// every deploy gets them so `kubectl top` and any HPA wiring work
	// cluster-wide. Owner=addons; sweep scope independent of services
	// / ingress / app-secrets / registry.
	//
	// metrics-server: powers `kubectl top` + HPA.
	// kube-state-metrics: exposes kube_* metrics that the monitor:
	//   stack's dashboards + alert rules reference. Installed
	//   unconditionally (cheap, ~30Mi) so flipping into `monitor:`
	//   works without an extra manual step.
	if err := observability.ApplyMetricsServer(ctx, primaryShell, s.Lg); err != nil {
		return fmt.Errorf("metrics-server: %w", err)
	}
	if err := observability.ApplyKubeStateMetrics(ctx, primaryShell, s.Lg); err != nil {
		return fmt.Errorf("kube-state-metrics: %w", err)
	}

	mode := rt.Cfg.Providers.IngressMode()
	tunnelMode := mode == config.IngressCloudflare

	// Ingress prerequisites:
	//   Traefik mode + domains → cert-manager + ClusterIssuer +
	//     per-domain Certificate resources, before workload.ApplyAll
	//     emits the matching Ingresses.
	//   Tunnel mode → skipped. CF terminates TLS at the edge; no
	//     cluster-side cert lifecycle.
	if !tunnelMode && len(rt.Cfg.Domains) > 0 {
		if err := s.applyCertInfrastructure(ctx, primaryShell); err != nil {
			return err
		}
	}

	s.Lg.Step("workloads")
	if err := workload.ApplyAll(ctx, rt, kc, s.Lg); err != nil {
		return err
	}

	// Tunnel branch: in tunnel mode, stand up cloudflared AFTER
	// workloads so its first reconnect finds upstream Services
	// already present. In Traefik mode, sweep any cloudflared
	// leftover from a prior tunnel-mode deploy (no-op when nothing
	// matches).
	if tunnelMode {
		if eps == nil || !eps.HasTunnel() {
			return fmt.Errorf("ingress: cloudflare but tofu tunnel output is empty (run plan + apply first)")
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

// applyCertInfrastructure stands up cert-manager and the per-domain
// Certificate resources whose Secrets the Ingress layer consumes.
//
// Steps:
//  1. kubectl apply the cert-manager release manifest (URL apply).
//  2. Wait for cert-manager Deployments to be Available (CRDs +
//     admission hooks must answer before ClusterIssuer / Certificate
//     creates succeed).
//  3. Apply the DNS provider's solver Secrets (resolved creds).
//  4. Apply the singleton ClusterIssuer wrapping the solver.
//  5. Apply one Certificate per declared domain (cert-manager handles
//     issuance + renewal asynchronously; we don't block on it).
func (s *Session) applyCertInfrastructure(ctx context.Context, sh ssh.Shell) error {
	rt := s.Rt

	if err := kube.ApplyCertManager(ctx, sh, s.Lg); err != nil {
		return err
	}

	dns, err := compile.ResolveDNS(rt.Cfg.Providers.DNS)
	if err != nil {
		return fmt.Errorf("resolve dns provider: %w", err)
	}
	solverYAML, secrets, err := dns.CertManagerSolver(rt)
	if err != nil {
		return fmt.Errorf("dns cert-manager solver: %w", err)
	}

	s.Lg.Step("cert-manager-secrets")
	// compile.SolverSecret and kube.SolverSecret carry identical
	// fields by design — kube owns its own type so the kube package
	// doesn't import pkg/internal/compile (which would close an
	// import cycle through pkg/runtime → pkg/config → pkg/providers
	// → pkg/internal/kube). Conversion is local + cheap.
	kubeSecrets := make([]kube.SolverSecret, len(secrets))
	for i, s := range secrets {
		kubeSecrets[i] = kube.SolverSecret{Name: s.Name, Key: s.Key, Value: s.Value}
	}
	if y := kube.BuildSolverSecretsYAML(kubeSecrets); y != nil {
		if err := kube.ApplyYAML(ctx, sh, y); err != nil {
			return fmt.Errorf("apply solver secrets: %w", err)
		}
	}

	s.Lg.Step("cluster-issuer")
	issuerYAML := kube.BuildClusterIssuerYAML(rt.Cfg.ACMEEmail, solverYAML)
	if err := kube.ApplyYAML(ctx, sh, issuerYAML); err != nil {
		return fmt.Errorf("apply cluster issuer: %w", err)
	}

	for _, svcName := range utils.SortedKeys(rt.Cfg.Domains) {
		for _, domain := range rt.Cfg.Domains[svcName] {
			s.Lg.Step("cert-" + domain)
			y, _ := kube.BuildCertificateYAML("default", domain)
			if err := kube.ApplyYAML(ctx, sh, y); err != nil {
				return fmt.Errorf("apply certificate %s: %w", domain, err)
			}
		}
	}
	return nil
}
