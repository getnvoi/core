package workload

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/getnvoi/core/pkg/internal/kube"
	"github.com/getnvoi/core/pkg/internal/utils"
	"github.com/getnvoi/core/pkg/log"
	rt2 "github.com/getnvoi/core/pkg/runtime"
)

// ApplyAll applies every nvoi-managed manifest:
//   - registry-auth Secret (owner=registry) when registry: declared
//   - nvoi-secrets Secret (owner=app-secrets) when secrets: declared
//   - one Deployment OR StatefulSet (owner=services) per cfg.Services
//     entry, dispatched by svc.Storage presence
//   - one Service (owner=services) per cfg.Services entry
//   - one Ingress (owner=ingress) per cfg.Services entry that has
//     domains in cfg.Domains. References cert-manager-issued TLS
//     Secrets — caller must apply cert-manager + Certificates first.
//
// Then runs reconcileRemoval to delete nvoi-managed resources whose
// YAML entry is gone, scoped per-owner via SweepOwned.
//
// Idempotent: every Apply path is Get-then-Create-or-Update with
// retry-on-conflict. Re-running on an unchanged cluster is a string
// of read-then-no-op API calls.
func ApplyAll(ctx context.Context, r *rt2.Runtime, kc *kube.Client, lg log.Log) error {
	registryScope := kube.Scope{Namespace: namespace, Owner: kube.OwnerRegistry}
	appSecretsScope := kube.Scope{Namespace: namespace, Owner: kube.OwnerAppSecrets}
	servicesScope := kube.Scope{Namespace: namespace, Owner: kube.OwnerServices}
	ingressScope := kube.Scope{Namespace: namespace, Owner: kube.OwnerIngress}

	// Ingress is emitted in BOTH modes (after the L7-routing refactor):
	//   Traefik mode → with TLS section, cert-manager Secret per domain.
	//   Tunnel mode  → without TLS section. cloudflared upstreams point
	//                  at the Traefik Service ClusterIP; Traefik routes
	//                  by Host header → pod (per-request LB).
	// `tunnelMode` only gates the TLS section now, not Ingress itself.
	tunnelMode := r.Cfg.DeployMode().Tunnel

	if sec, err := buildRegistrySecret(r); err != nil {
		return fmt.Errorf("build registry-auth: %w", err)
	} else if sec != nil {
		lg.Step("registry-secret")
		if err := kc.ApplyOwned(ctx, registryScope, sec); err != nil {
			return fmt.Errorf("apply registry-auth: %w", err)
		}
	}

	if sec := buildAppSecret(r); sec != nil {
		lg.Step("app-secret")
		if err := kc.ApplyOwned(ctx, appSecretsScope, sec); err != nil {
			return fmt.Errorf("apply nvoi-secrets: %w", err)
		}
	}

	for _, name := range utils.SortedKeys(r.Cfg.Services) {
		svc := r.Cfg.Services[name]
		lg.Step("workload-" + name)

		var (
			workload runtime.Object
			err      error
		)
		if svc.IsStateful() {
			workload, err = buildStatefulSet(r, name, svc)
		} else {
			workload, err = buildDeployment(r, name, svc)
		}
		if err != nil {
			return fmt.Errorf("build workload %s: %w", name, err)
		}
		if err := kc.ApplyOwned(ctx, servicesScope, workload); err != nil {
			return fmt.Errorf("apply workload %s: %w", name, err)
		}
		if err := kc.ApplyOwned(ctx, servicesScope, buildService(r, name, svc)); err != nil {
			return fmt.Errorf("apply service %s: %w", name, err)
		}

		// Ingress: emit for every service with domains, in both modes.
		// withTLS=true (traefik) → cert-manager Secret per domain.
		// withTLS=false (tunnel) → no TLS block; CF terminates at edge.
		if domains := r.Cfg.Domains[name]; len(domains) > 0 {
			lg.Step("ingress-" + name)
			if err := kc.ApplyOwned(ctx, ingressScope, buildIngress(name, svc, domains, !tunnelMode)); err != nil {
				return fmt.Errorf("apply ingress %s: %w", name, err)
			}
		}
	}

	return reconcileRemoval(ctx, r, kc, lg)
}

// reconcileRemoval deletes nvoi-managed resources whose YAML entry has
// been removed. Owner-scoped via SweepOwned — each step's sweep can
// only see its own resources, so a stale registry-auth Secret never
// gets caught by the services sweep, etc.
//
// Three sweeps cover the services owner:
//   - Deployments: keep only stateless services (a service that
//     flipped from stateless → stateful leaves an orphan Deployment
//     to clean up here).
//   - StatefulSets: keep only stateful services (symmetric flip case).
//   - Services: keep all declared (every service has one).
//
// Plus owner-singleton sweeps for registry-auth and nvoi-secrets:
// when operator removes registry: or empties secrets:, the orphan
// Secret is purged.
func reconcileRemoval(ctx context.Context, r *rt2.Runtime, kc *kube.Client, lg log.Log) error {
	// Ingress is emitted for every service with domains in BOTH modes
	// now (the L7-routing refactor keeps Traefik in the path for tunnel
	// mode too). declaredIngress mirrors the apply loop in ApplyAll —
	// SweepOwned reaps anything else under OwnerIngress.
	declared := make([]string, 0, len(r.Cfg.Services))
	declaredStateful := make([]string, 0)
	declaredStateless := make([]string, 0)
	declaredIngress := make([]string, 0)
	for _, name := range utils.SortedKeys(r.Cfg.Services) {
		declared = append(declared, name)
		if r.Cfg.Services[name].IsStateful() {
			declaredStateful = append(declaredStateful, name)
		} else {
			declaredStateless = append(declaredStateless, name)
		}
		if len(r.Cfg.Domains[name]) > 0 {
			declaredIngress = append(declaredIngress, name)
		}
	}

	servicesScope := kube.Scope{Namespace: namespace, Owner: kube.OwnerServices}
	if err := kc.SweepOwned(ctx, servicesScope, kube.KindDeployment, declaredStateless); err != nil {
		return fmt.Errorf("sweep stale deployments: %w", err)
	}
	if err := kc.SweepOwned(ctx, servicesScope, kube.KindStatefulSet, declaredStateful); err != nil {
		return fmt.Errorf("sweep stale statefulsets: %w", err)
	}
	if err := kc.SweepOwned(ctx, servicesScope, kube.KindService, declared); err != nil {
		return fmt.Errorf("sweep stale services: %w", err)
	}

	// registry-auth singleton: keep only if registry: declared.
	registryDesired := []string(nil)
	if len(r.RegistryCreds) > 0 {
		registryDesired = []string{registrySecretName}
	}
	if err := kc.SweepOwned(ctx, kube.Scope{Namespace: namespace, Owner: kube.OwnerRegistry}, kube.KindSecret, registryDesired); err != nil {
		return fmt.Errorf("sweep stale registry secret: %w", err)
	}

	// nvoi-secrets singleton: keep only if secrets: declared.
	appSecretDesired := []string(nil)
	if len(r.Cfg.Secrets) > 0 {
		appSecretDesired = []string{appSecretName}
	}
	if err := kc.SweepOwned(ctx, kube.Scope{Namespace: namespace, Owner: kube.OwnerAppSecrets}, kube.KindSecret, appSecretDesired); err != nil {
		return fmt.Errorf("sweep stale app secret: %w", err)
	}

	// Ingress: keep only services with non-empty domains. Drop a
	// service from cfg.Domains and the orphan Ingress purges next deploy.
	if err := kc.SweepOwned(ctx, kube.Scope{Namespace: namespace, Owner: kube.OwnerIngress}, kube.KindIngress, declaredIngress); err != nil {
		return fmt.Errorf("sweep stale ingresses: %w", err)
	}

	_ = lg // currently silent on per-sweep info; future per-name logging hook lives here
	return nil
}
