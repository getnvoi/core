package observability

// stack.go composes every observability manifest into a single
// ordered slice the deploy phase (PR 6) iterates with
// kc.ApplyOwned. Caller stamps OwnerObservability via the scope;
// individual builders pre-stamp the same value on objectLabels so a
// manifest pulled in isolation (e.g. dumped to YAML for debug)
// describes itself correctly.
//
// Order roughly follows kubernetes dependency:
//
//   1. Secrets        (referenced by pods + Ingress TLS)
//   2. ConfigMaps     (referenced by pods)
//   3. ServiceAccount + ClusterRole + Binding (referenced by pods)
//   4. Workloads      (StatefulSets, Deployments, DaemonSet)
//   5. Services       (target the workloads)
//   6. Ingress        (targets the Service) — only when domain set
//
// kubectl apply is eventually consistent so strict ordering isn't
// required, but the deterministic sequence keeps log diffs readable.

import (
	apiruntime "k8s.io/apimachinery/pkg/runtime"

	"github.com/getnvoi/core/pkg/runtime"
)

// BuildStack composes every typed manifest in apply order. Caller
// (pkg/deploy.deployObservability) iterates and calls
// kc.ApplyOwned(observabilityScope, obj) on each. Returns the slice +
// the explicit list of declared names per Kind so the caller's
// reconcile-removal step can pass them to SweepOwned.
//
// All builders are pure — no I/O, no env access. Failure modes are
// limited to "credentials invalid" surfaced by the caller's earlier
// EnsureBuckets call.
func BuildStack(rt *runtime.Runtime, creds BucketCreds) (objects []apiruntime.Object, manifest DeclaredNames) {
	objects = []apiruntime.Object{}

	// ── Secrets ─────────────────────────────────────────────────────
	objstoreSecret := buildThanosObjstoreSecret(creds)
	adminSecret := buildGrafanaAdminSecret(rt)
	objects = append(objects, objstoreSecret, adminSecret)
	manifest.Secrets = []string{objstoreSecret.Name, adminSecret.Name}

	// ── ConfigMaps ──────────────────────────────────────────────────
	promCM := buildPrometheusConfigMap()
	lokiCM := buildLokiConfigMap(creds)
	promtailCM := buildPromtailConfigMap()
	grafanaCM := buildGrafanaConfigMap(rt)
	objects = append(objects, promCM, lokiCM, promtailCM, grafanaCM)
	manifest.ConfigMaps = []string{promCM.Name, lokiCM.Name, promtailCM.Name, grafanaCM.Name}

	// ── RBAC for Promtail (cluster-scoped) ──────────────────────────
	sa, cr, crb := buildPromtailRBAC()
	objects = append(objects, sa, cr, crb)
	manifest.ServiceAccounts = []string{sa.Name}
	manifest.ClusterRoles = []string{cr.Name}
	manifest.ClusterRoleBindings = []string{crb.Name}

	// ── Workloads ───────────────────────────────────────────────────
	promSS := buildPrometheusStatefulSet()
	lokiSS, lokiSvc := buildLoki()
	promtailDS := buildPromtailDaemonSet()
	thanosQuerier, thanosQuerierSvc := buildThanosQuerier()
	thanosStore, thanosStoreSvc := buildThanosStore()
	grafanaDep := buildGrafanaDeployment()
	objects = append(objects, promSS, lokiSS, promtailDS, thanosQuerier, thanosStore, grafanaDep)
	manifest.StatefulSets = []string{promSS.Name, lokiSS.Name}
	manifest.DaemonSets = []string{promtailDS.Name}
	manifest.Deployments = []string{thanosQuerier.Name, thanosStore.Name, grafanaDep.Name}

	// ── Services ────────────────────────────────────────────────────
	promSvcs := buildPrometheusServices()
	grafanaSvc := buildGrafanaService()
	for _, s := range promSvcs {
		objects = append(objects, s)
	}
	objects = append(objects, lokiSvc, thanosQuerierSvc, thanosStoreSvc, grafanaSvc)
	manifest.Services = []string{
		promSvcs[0].Name, promSvcs[1].Name,
		lokiSvc.Name, thanosQuerierSvc.Name, thanosStoreSvc.Name, grafanaSvc.Name,
	}

	// ── Ingress (optional) ──────────────────────────────────────────
	if ing := buildGrafanaIngress(rt); ing != nil {
		objects = append(objects, ing)
		manifest.Ingresses = []string{ing.Name}
	}

	return objects, manifest
}

// DeclaredNames is what the caller hands to SweepOwned for each
// Kind — every name we just applied. SweepOwned(... names) removes
// any same-kind same-owner object NOT in this list, so the manifest
// is the source of truth for "what should exist."
//
// Going by-Kind rather than a flat list makes the sweep loop in PR 6
// mechanical: one SweepOwned per Kind, each with its name slice.
type DeclaredNames struct {
	Secrets             []string
	ConfigMaps          []string
	ServiceAccounts     []string
	ClusterRoles        []string
	ClusterRoleBindings []string
	StatefulSets        []string
	DaemonSets          []string
	Deployments         []string
	Services            []string
	Ingresses           []string
}

