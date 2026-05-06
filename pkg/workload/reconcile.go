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

		var workload runtime.Object
		if svc.IsStateful() {
			workload = buildStatefulSet(r, name, svc)
		} else {
			workload = buildDeployment(r, name, svc)
		}
		if err := kc.ApplyOwned(ctx, servicesScope, workload); err != nil {
			return fmt.Errorf("apply workload %s: %w", name, err)
		}
		if err := kc.ApplyOwned(ctx, servicesScope, buildService(r, name, svc)); err != nil {
			return fmt.Errorf("apply service %s: %w", name, err)
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
	declared := make([]string, 0, len(r.Cfg.Services))
	declaredStateful := make([]string, 0)
	declaredStateless := make([]string, 0)
	for _, name := range utils.SortedKeys(r.Cfg.Services) {
		declared = append(declared, name)
		if r.Cfg.Services[name].IsStateful() {
			declaredStateful = append(declaredStateful, name)
		} else {
			declaredStateless = append(declaredStateless, name)
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

	_ = lg // currently silent on per-sweep info; future per-name logging hook lives here
	return nil
}
