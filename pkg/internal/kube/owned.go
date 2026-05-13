package kube

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// LabelOwner is the single discriminator SweepOwned + ListOwned scope
// every list call by. Stamped on every nvoi-managed object via
// ApplyOwned. Operators reading `kubectl get ... -L nvoi/owner` see
// which reconcile step claims the resource.
const LabelOwner = "nvoi/owner"

// Owner values are a closed taxonomy — one per logical reconcile step.
// Each step's sweep can only see its own resources, so cross-step
// orphan cleanup never accidentally deletes another step's work.
//
// Adding a new step = one new const. No exclusion lists, no per-step
// grep filters, no name allowlists.
const (
	OwnerServices      = "services"      // Deployment + StatefulSet + Service per cfg.Services entry
	OwnerRegistry      = "registry"      // dockerconfigjson Secret for imagePullSecrets
	OwnerAppSecrets    = "app-secrets"   // Opaque Secret holding cfg.Secrets values
	OwnerIngress       = "ingress"       // per-service Ingress resources (Traefik consumes them)
	OwnerTunnel        = "tunnel"        // cloudflared Deployment + token Secret (ingress=cloudflare)
	OwnerAddons        = "addons"        // cluster-level prerequisites (metrics-server, future addons)
	OwnerObservability = "observability" // Prom + Thanos + Loki + Grafana stack

	// OwnerDatabases scopes every per-database PRIMARY workload: the
	// StatefulSet (postgres), Service, PVC, credentials Secret, backup
	// CronJob, backup-creds Secret. The reconcile sweep deletes only
	// objects in this owner set when a YAML entry is removed.
	OwnerDatabases = "databases"

	// OwnerDatabaseBranches scopes ephemeral postgres branches —
	// separate owner so SweepOwned on `databases` never eats branches
	// (which have their own lifecycle via `nvoi database branch-delete`).
	OwnerDatabaseBranches = "database-branches"
)

// Kind names a typed resource kind ApplyOwned / SweepOwned / ListOwned
// support. Closed allowlist — every entry maps to one dispatch case;
// unknown kinds error explicitly.
type Kind string

const (
	KindDeployment         Kind = "Deployment"
	KindStatefulSet        Kind = "StatefulSet"
	KindService            Kind = "Service"
	KindSecret             Kind = "Secret"
	KindConfigMap          Kind = "ConfigMap"
	KindPVC                Kind = "PersistentVolumeClaim"
	KindIngress            Kind = "Ingress"
	KindDaemonSet          Kind = "DaemonSet"          // Promtail pod-log shipper
	KindNamespace          Kind = "Namespace"          // nvoi-observability + future per-app namespaces
	KindServiceAccount     Kind = "ServiceAccount"     // Promtail node-discovery identity
	KindRole               Kind = "Role"               // namespaced: Grafana sidecar configmap-watch
	KindRoleBinding        Kind = "RoleBinding"        // namespaced: Grafana SA → Role binding
	KindClusterRole        Kind = "ClusterRole"        // Promtail pod-list permission
	KindClusterRoleBinding Kind = "ClusterRoleBinding" // Promtail SA → ClusterRole binding
	KindCronJob            Kind = "CronJob"            // batch/v1 — scheduled DB backups
	KindJob                Kind = "Job"                // batch/v1 — one-shot manual backups + restores
	KindStorageClass       Kind = "StorageClass"       // storage.k8s.io/v1 — cluster-scoped (postgres ZFS-LocalPV)
)

// Scope is the (namespace, owner) pair every owned-resource operation
// is keyed by. ApplyOwned stamps nvoi/owner=<Owner>; ListOwned and
// SweepOwned filter by it. Bundled because the pair always travels
// together — every call site that touches one needs the other.
type Scope struct {
	Namespace string
	Owner     string
}

// ApplyOwned upserts obj in scope.Namespace, stamping
// nvoi/owner=<scope.Owner> on its labels. Dispatches to the per-kind
// typed apply helper. Existing labels on obj are merged (not replaced).
//
// Every nvoi-managed write goes through this path — no other surface
// stamps the owner label.
func (c *Client) ApplyOwned(ctx context.Context, scope Scope, obj runtime.Object) error {
	if scope.Owner == "" {
		return fmt.Errorf("ApplyOwned: owner required")
	}
	accessor, ok := obj.(metav1.Object)
	if !ok {
		return fmt.Errorf("ApplyOwned: object %T does not expose metadata", obj)
	}
	labels := accessor.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels[LabelOwner] = scope.Owner
	accessor.SetLabels(labels)

	ns := scope.Namespace
	switch o := obj.(type) {
	case *appsv1.Deployment:
		return c.applyDeployment(ctx, ns, o)
	case *appsv1.StatefulSet:
		return c.applyStatefulSet(ctx, ns, o)
	case *appsv1.DaemonSet:
		return c.applyDaemonSet(ctx, ns, o)
	case *corev1.Service:
		return c.applyService(ctx, ns, o)
	case *corev1.Secret:
		return c.applySecret(ctx, ns, o)
	case *corev1.ConfigMap:
		return c.applyConfigMap(ctx, ns, o)
	case *corev1.PersistentVolumeClaim:
		return c.applyPVC(ctx, ns, o)
	case *corev1.Namespace:
		return c.applyNamespace(ctx, o)
	case *corev1.ServiceAccount:
		return c.applyServiceAccount(ctx, ns, o)
	case *networkingv1.Ingress:
		return c.applyIngress(ctx, ns, o)
	case *rbacv1.Role:
		return c.applyRole(ctx, ns, o)
	case *rbacv1.RoleBinding:
		return c.applyRoleBinding(ctx, ns, o)
	case *rbacv1.ClusterRole:
		return c.applyClusterRole(ctx, o)
	case *rbacv1.ClusterRoleBinding:
		return c.applyClusterRoleBinding(ctx, o)
	case *batchv1.CronJob:
		return c.applyCronJob(ctx, ns, o)
	case *batchv1.Job:
		return c.applyJob(ctx, ns, o)
	case *storagev1.StorageClass:
		return c.applyStorageClass(ctx, o)
	default:
		return fmt.Errorf("ApplyOwned: unsupported kind %T", obj)
	}
}

// SweepOwned deletes every resource of `kind` in scope.Namespace
// carrying nvoi/owner=<scope.Owner> whose name is NOT in `desired`.
// Pass desired=nil to sweep ALL resources for that owner+kind.
//
// Owner-scoped: each reconcile step's sweep can never see another
// step's resources. NotFound on Delete is silently ignored —
// concurrent reconciles or manual cleanup are valid no-ops.
func (c *Client) SweepOwned(ctx context.Context, scope Scope, kind Kind, desired []string) error {
	if scope.Owner == "" {
		return fmt.Errorf("SweepOwned: owner required")
	}
	keep := make(map[string]bool, len(desired))
	for _, n := range desired {
		keep[n] = true
	}
	names, err := c.ListOwned(ctx, scope, kind)
	if err != nil {
		return err
	}
	for _, name := range names {
		if keep[name] {
			continue
		}
		if err := c.deleteByKind(ctx, scope.Namespace, kind, name); err != nil {
			return fmt.Errorf("sweep %s/%s: %w", kind, name, err)
		}
	}
	return nil
}

// ListOwned returns the names of every resource of `kind` in
// scope.Namespace carrying nvoi/owner=<scope.Owner>. Read-only mirror
// of SweepOwned.
//
// Dispatches once on Kind to pick the right typed-client List call,
// then extracts names via meta.ExtractList — one shared loop body
// instead of one per kind.
func (c *Client) ListOwned(ctx context.Context, scope Scope, kind Kind) ([]string, error) {
	if scope.Owner == "" {
		return nil, fmt.Errorf("ListOwned: owner required")
	}
	list, err := c.listOwned(ctx, scope.Namespace, scope.Owner, kind)
	if err != nil {
		return nil, err
	}
	items, err := meta.ExtractList(list)
	if err != nil {
		return nil, fmt.Errorf("extract list (%s): %w", kind, err)
	}
	names := make([]string, 0, len(items))
	for _, item := range items {
		acc, ok := item.(metav1.Object)
		if !ok {
			continue
		}
		names = append(names, acc.GetName())
	}
	return names, nil
}

// listOwned is the per-kind typed-client dispatch. Returns a
// runtime.Object that meta.ExtractList walks; the caller doesn't care
// about the concrete list type.
func (c *Client) listOwned(ctx context.Context, ns, owner string, kind Kind) (runtime.Object, error) {
	opts := metav1.ListOptions{LabelSelector: ownerSelector(owner)}
	switch kind {
	case KindDeployment:
		return c.CS.AppsV1().Deployments(ns).List(ctx, opts)
	case KindStatefulSet:
		return c.CS.AppsV1().StatefulSets(ns).List(ctx, opts)
	case KindDaemonSet:
		return c.CS.AppsV1().DaemonSets(ns).List(ctx, opts)
	case KindService:
		return c.CS.CoreV1().Services(ns).List(ctx, opts)
	case KindSecret:
		return c.CS.CoreV1().Secrets(ns).List(ctx, opts)
	case KindConfigMap:
		return c.CS.CoreV1().ConfigMaps(ns).List(ctx, opts)
	case KindPVC:
		return c.CS.CoreV1().PersistentVolumeClaims(ns).List(ctx, opts)
	case KindIngress:
		return c.CS.NetworkingV1().Ingresses(ns).List(ctx, opts)
	case KindNamespace:
		return c.CS.CoreV1().Namespaces().List(ctx, opts)
	case KindServiceAccount:
		return c.CS.CoreV1().ServiceAccounts(ns).List(ctx, opts)
	case KindRole:
		return c.CS.RbacV1().Roles(ns).List(ctx, opts)
	case KindRoleBinding:
		return c.CS.RbacV1().RoleBindings(ns).List(ctx, opts)
	case KindClusterRole:
		return c.CS.RbacV1().ClusterRoles().List(ctx, opts)
	case KindClusterRoleBinding:
		return c.CS.RbacV1().ClusterRoleBindings().List(ctx, opts)
	case KindCronJob:
		return c.CS.BatchV1().CronJobs(ns).List(ctx, opts)
	case KindJob:
		return c.CS.BatchV1().Jobs(ns).List(ctx, opts)
	case KindStorageClass:
		return c.CS.StorageV1().StorageClasses().List(ctx, opts)
	default:
		return nil, fmt.Errorf("listOwned: unsupported kind %q", kind)
	}
}

// deleteByKind is SweepOwned's per-kind delete dispatch. Same closed
// switch — adding a new kind means one case here, one in listOwned,
// one in ApplyOwned.
func (c *Client) deleteByKind(ctx context.Context, ns string, kind Kind, name string) error {
	opts := metav1.DeleteOptions{}
	switch kind {
	case KindDeployment:
		return ignoreNotFound(c.CS.AppsV1().Deployments(ns).Delete(ctx, name, opts))
	case KindStatefulSet:
		return ignoreNotFound(c.CS.AppsV1().StatefulSets(ns).Delete(ctx, name, opts))
	case KindDaemonSet:
		return ignoreNotFound(c.CS.AppsV1().DaemonSets(ns).Delete(ctx, name, opts))
	case KindService:
		return ignoreNotFound(c.CS.CoreV1().Services(ns).Delete(ctx, name, opts))
	case KindSecret:
		return ignoreNotFound(c.CS.CoreV1().Secrets(ns).Delete(ctx, name, opts))
	case KindConfigMap:
		return ignoreNotFound(c.CS.CoreV1().ConfigMaps(ns).Delete(ctx, name, opts))
	case KindPVC:
		return ignoreNotFound(c.CS.CoreV1().PersistentVolumeClaims(ns).Delete(ctx, name, opts))
	case KindIngress:
		return ignoreNotFound(c.CS.NetworkingV1().Ingresses(ns).Delete(ctx, name, opts))
	case KindNamespace:
		return ignoreNotFound(c.CS.CoreV1().Namespaces().Delete(ctx, name, opts))
	case KindServiceAccount:
		return ignoreNotFound(c.CS.CoreV1().ServiceAccounts(ns).Delete(ctx, name, opts))
	case KindRole:
		return ignoreNotFound(c.CS.RbacV1().Roles(ns).Delete(ctx, name, opts))
	case KindRoleBinding:
		return ignoreNotFound(c.CS.RbacV1().RoleBindings(ns).Delete(ctx, name, opts))
	case KindClusterRole:
		return ignoreNotFound(c.CS.RbacV1().ClusterRoles().Delete(ctx, name, opts))
	case KindClusterRoleBinding:
		return ignoreNotFound(c.CS.RbacV1().ClusterRoleBindings().Delete(ctx, name, opts))
	case KindCronJob:
		return ignoreNotFound(c.CS.BatchV1().CronJobs(ns).Delete(ctx, name, opts))
	case KindJob:
		// Deleting a Job leaves its pods around by default; propagate
		// foreground deletion so the pods go too.
		bg := metav1.DeletePropagationBackground
		return ignoreNotFound(c.CS.BatchV1().Jobs(ns).Delete(ctx, name, metav1.DeleteOptions{PropagationPolicy: &bg}))
	case KindStorageClass:
		// Cluster-scoped — ignore ns.
		return ignoreNotFound(c.CS.StorageV1().StorageClasses().Delete(ctx, name, opts))
	default:
		return fmt.Errorf("deleteByKind: unsupported kind %q", kind)
	}
}

func ownerSelector(owner string) string { return fmt.Sprintf("%s=%s", LabelOwner, owner) }

func ignoreNotFound(err error) error {
	if err == nil || apierrors.IsNotFound(err) {
		return nil
	}
	return err
}
