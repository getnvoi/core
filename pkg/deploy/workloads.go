package deploy

import (
	"context"
	"fmt"

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
//  2. cert-manager + ClusterIssuer + Certificates MUST land before
//     workload Ingresses — Ingresses reference TLS Secret names that
//     cert-manager creates in response to the Certificate resources.
//     (Ingress applies even before cert lands; Traefik just refuses
//     TLS until the Secret exists. Cert is async; warn-and-continue.)
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

	// Cluster-level addons (metrics-server today). Independent of the
	// observability stack — every deploy gets them so `kubectl top`
	// and any HPA wiring work cluster-wide regardless of whether the
	// operator activates the full monitor: stack. Owner=addons; sweep
	// scope independent of services / ingress / app-secrets / registry.
	if err := observability.ApplyMetricsServer(ctx, primaryShell, s.Lg); err != nil {
		return fmt.Errorf("metrics-server: %w", err)
	}

	// Ingress prerequisites: install cert-manager, apply ClusterIssuer
	// + per-domain Certificates BEFORE workload.ApplyAll runs (which
	// applies Ingress resources referencing the cert Secrets).
	if len(rt.Cfg.Domains) > 0 {
		if err := s.applyCertInfrastructure(ctx, primaryShell); err != nil {
			return err
		}
	}
	_ = eps // reserved for future per-endpoint logic
	_ = primaryShell

	s.Lg.Step("workloads")
	if err := workload.ApplyAll(ctx, rt, kc, s.Lg); err != nil {
		return err
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
	if y := kube.BuildSolverSecretsYAML(secrets); y != nil {
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
