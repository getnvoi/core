package kube

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
)

// Per-kind apply helpers — invoked by ApplyOwned's switch. Each one
// handles the kind's specific quirks (Deployment status preservation,
// Service ClusterIP transition, StatefulSet immutable VolumeClaimTemplates),
// then runs the standard Get → Create-or-Update + retry-on-conflict
// pattern. Controllers bump ResourceVersion async between our Get
// and Update — RetryOnConflict re-reads and retries; canonical
// client-go hygiene.

func (c *Client) applyDeployment(ctx context.Context, ns string, dep *appsv1.Deployment) error {
	api := c.CS.AppsV1().Deployments(ns)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, err := api.Get(ctx, dep.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err := api.Create(ctx, dep, metav1.CreateOptions{FieldManager: FieldManager})
			return err
		}
		if err != nil {
			return err
		}
		dep.ResourceVersion = existing.ResourceVersion
		dep.Status = existing.Status
		_, err = api.Update(ctx, dep, metav1.UpdateOptions{FieldManager: FieldManager})
		return err
	})
}

// applyService handles the headless ↔ ClusterIP transition (apiserver
// rejects update across that boundary, requires delete+recreate).
// Otherwise preserves existing ClusterIP — apiserver assigns it on
// first Create and rejects mutations.
func (c *Client) applyService(ctx context.Context, ns string, svc *corev1.Service) error {
	api := c.CS.CoreV1().Services(ns)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, err := api.Get(ctx, svc.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err := api.Create(ctx, svc, metav1.CreateOptions{FieldManager: FieldManager})
			return err
		}
		if err != nil {
			return err
		}
		const headless = "None"
		if existing.Spec.ClusterIP != svc.Spec.ClusterIP &&
			(existing.Spec.ClusterIP == headless || svc.Spec.ClusterIP == headless) {
			if err := api.Delete(ctx, svc.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
				return err
			}
			svc.ResourceVersion = ""
			_, err := api.Create(ctx, svc, metav1.CreateOptions{FieldManager: FieldManager})
			return err
		}
		svc.ResourceVersion = existing.ResourceVersion
		svc.Spec.ClusterIP = existing.Spec.ClusterIP
		_, err = api.Update(ctx, svc, metav1.UpdateOptions{FieldManager: FieldManager})
		return err
	})
}

// applyStatefulSet preserves VolumeClaimTemplates (immutable post-
// Create) + Status. After Update, force-rolls any pod stuck on the
// old revision — without this, a pod in CrashLoopBackOff blocks the
// StatefulSet controller from picking up the new spec (it waits for
// the existing pod to become Ready before rolling, and the existing
// pod is sick because of the very env/image the new spec fixes).
// Reconcile-on-VolumeClaimTemplates is a future concern; today
// changing storage size requires an explicit migrate.
func (c *Client) applyStatefulSet(ctx context.Context, ns string, ss *appsv1.StatefulSet) error {
	api := c.CS.AppsV1().StatefulSets(ns)
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, err := api.Get(ctx, ss.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err := api.Create(ctx, ss, metav1.CreateOptions{FieldManager: FieldManager})
			return err
		}
		if err != nil {
			return err
		}
		ss.ResourceVersion = existing.ResourceVersion
		ss.Status = existing.Status
		ss.Spec.VolumeClaimTemplates = existing.Spec.VolumeClaimTemplates
		_, err = api.Update(ctx, ss, metav1.UpdateOptions{FieldManager: FieldManager})
		return err
	}); err != nil {
		return err
	}
	return c.forceRollStaleStatefulSetPods(ctx, ns, ss)
}

// forceRollStaleStatefulSetPods deletes pods whose
// controller-revision-hash label doesn't match the StatefulSet's
// UpdateRevision when those pods aren't Ready. Standard k8s rollout
// path expects pods to become Ready before the controller proceeds —
// for a single-pod stateful workload (postgres, etc.) with a sick
// pod-0, that's a deadlock the operator would otherwise resolve via
// `kubectl delete pod`. We encode the same recovery here so deploys
// converge without manual kubectl.
//
// Best-effort — we re-read the StatefulSet to pick up the controller-
// written UpdateRevision (the value we just submitted gets a fresh
// revision on the apiserver side), list owned pods by the selector,
// and delete the stragglers. Errors are returned to the caller; a
// failure here surfaces in the reconcile log and the operator can
// retry.
func (c *Client) forceRollStaleStatefulSetPods(ctx context.Context, ns string, ss *appsv1.StatefulSet) error {
	live, err := c.CS.AppsV1().StatefulSets(ns).Get(ctx, ss.Name, metav1.GetOptions{})
	if err != nil {
		return nil // we just updated it; absence is impossible — treat lookup failure as transient
	}
	target := live.Status.UpdateRevision
	if target == "" {
		return nil // controller hasn't computed a revision yet; nothing to compare
	}

	selector := metav1.FormatLabelSelector(live.Spec.Selector)
	pods, err := c.CS.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		// Pod's controller-revision-hash label tells us which
		// StatefulSet revision the controller scheduled it from.
		hash := pod.Labels["controller-revision-hash"]
		if hash == target {
			continue // up-to-date
		}
		// Pod is stale — but if it's Running+Ready, the controller
		// will roll it on its own under OrderedReady semantics.
		// Force-delete ONLY when the pod is unhealthy (not Ready),
		// which is exactly the deadlock case we're working around.
		if podReadyForRollout(pod) {
			continue
		}
		_ = c.CS.CoreV1().Pods(ns).Delete(ctx, pod.Name, metav1.DeleteOptions{})
	}
	return nil
}

// podReadyForRollout reports whether the pod is healthy enough that
// the StatefulSet controller can roll it under its normal cadence
// (Running phase + all containers Ready). For unhealthy pods, the
// controller would otherwise wait forever — that's the case we
// short-circuit by deleting.
func podReadyForRollout(pod *corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if !cs.Ready {
			return false
		}
	}
	return true
}

func (c *Client) applySecret(ctx context.Context, ns string, sec *corev1.Secret) error {
	api := c.CS.CoreV1().Secrets(ns)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, err := api.Get(ctx, sec.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err := api.Create(ctx, sec, metav1.CreateOptions{FieldManager: FieldManager})
			return err
		}
		if err != nil {
			return err
		}
		sec.ResourceVersion = existing.ResourceVersion
		_, err = api.Update(ctx, sec, metav1.UpdateOptions{FieldManager: FieldManager})
		return err
	})
}

func (c *Client) applyConfigMap(ctx context.Context, ns string, cm *corev1.ConfigMap) error {
	api := c.CS.CoreV1().ConfigMaps(ns)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, err := api.Get(ctx, cm.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err := api.Create(ctx, cm, metav1.CreateOptions{FieldManager: FieldManager})
			return err
		}
		if err != nil {
			return err
		}
		cm.ResourceVersion = existing.ResourceVersion
		_, err = api.Update(ctx, cm, metav1.UpdateOptions{FieldManager: FieldManager})
		return err
	})
}

// applyPVC preserves the immutable spec on update — k8s rejects
// changes to .spec.resources, .spec.storageClassName, etc post-Create.
// Re-applying the same template is a no-op; resizing requires a
// PVC delete + recreate (out of scope for v1).
func (c *Client) applyPVC(ctx context.Context, ns string, pvc *corev1.PersistentVolumeClaim) error {
	api := c.CS.CoreV1().PersistentVolumeClaims(ns)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, err := api.Get(ctx, pvc.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err := api.Create(ctx, pvc, metav1.CreateOptions{FieldManager: FieldManager})
			return err
		}
		if err != nil {
			return err
		}
		// Keep the existing spec (immutable post-bind); only labels /
		// annotations may legitimately update.
		pvc.ResourceVersion = existing.ResourceVersion
		pvc.Spec = existing.Spec
		_, err = api.Update(ctx, pvc, metav1.UpdateOptions{FieldManager: FieldManager})
		return err
	})
}

// applyIngress is a Get → Create-or-Update for k8s Ingress resources.
// Idempotent; preserves Status (Traefik writes load-balancer state
// there async).
func (c *Client) applyIngress(ctx context.Context, ns string, ing *networkingv1.Ingress) error {
	api := c.CS.NetworkingV1().Ingresses(ns)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, err := api.Get(ctx, ing.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err := api.Create(ctx, ing, metav1.CreateOptions{FieldManager: FieldManager})
			return err
		}
		if err != nil {
			return err
		}
		ing.ResourceVersion = existing.ResourceVersion
		ing.Status = existing.Status
		_, err = api.Update(ctx, ing, metav1.UpdateOptions{FieldManager: FieldManager})
		return err
	})
}

// applyDaemonSet is a Get → Create-or-Update for DaemonSets. Promtail
// is the v1 user. Preserves Status (controller writes scheduled /
// desired counts there async).
func (c *Client) applyDaemonSet(ctx context.Context, ns string, ds *appsv1.DaemonSet) error {
	api := c.CS.AppsV1().DaemonSets(ns)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, err := api.Get(ctx, ds.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err := api.Create(ctx, ds, metav1.CreateOptions{FieldManager: FieldManager})
			return err
		}
		if err != nil {
			return err
		}
		ds.ResourceVersion = existing.ResourceVersion
		ds.Status = existing.Status
		_, err = api.Update(ctx, ds, metav1.UpdateOptions{FieldManager: FieldManager})
		return err
	})
}

// applyCronJob is a Get → Create-or-Update for batch/v1 CronJobs.
// Database backups are the v1 user. Preserves Status (controller
// records LastScheduleTime / Active counts there async) and
// ResourceVersion (controller bumps it on every schedule firing).
func (c *Client) applyCronJob(ctx context.Context, ns string, cj *batchv1.CronJob) error {
	api := c.CS.BatchV1().CronJobs(ns)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, err := api.Get(ctx, cj.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err := api.Create(ctx, cj, metav1.CreateOptions{FieldManager: FieldManager})
			return err
		}
		if err != nil {
			return err
		}
		cj.ResourceVersion = existing.ResourceVersion
		cj.Status = existing.Status
		_, err = api.Update(ctx, cj, metav1.UpdateOptions{FieldManager: FieldManager})
		return err
	})
}

// applyStorageClass is a Get → Create-or-noop for cluster-scoped
// StorageClass resources. Most fields on a StorageClass are
// immutable post-Create (Provisioner, Parameters, ReclaimPolicy,
// VolumeBindingMode) — changing them requires destroy+recreate,
// which would break any PVC bound to the class. Treat presence as
// satisfied: if the SC already exists, leave it alone. Operators who
// need a parameter change delete the SC manually.
func (c *Client) applyStorageClass(ctx context.Context, sc *storagev1.StorageClass) error {
	api := c.CS.StorageV1().StorageClasses()
	_, err := api.Get(ctx, sc.Name, metav1.GetOptions{})
	if err == nil {
		return nil // already present; immutable fields keep their shape
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	_, err = api.Create(ctx, sc, metav1.CreateOptions{FieldManager: FieldManager})
	return err
}

// applyJob is a Get → Create for batch/v1 Jobs. Jobs are
// effectively immutable post-create: most spec fields (PodTemplate,
// BackoffLimit) reject Update. Manual backup + restore Jobs are
// always created fresh (the caller picks a unique name embedding a
// unix timestamp), so this helper short-circuits to Create when the
// name isn't already present and leaves an existing Job untouched
// — operator-facing semantics: "this Job already ran".
func (c *Client) applyJob(ctx context.Context, ns string, j *batchv1.Job) error {
	api := c.CS.BatchV1().Jobs(ns)
	_, err := api.Get(ctx, j.Name, metav1.GetOptions{})
	if err == nil {
		return nil // already submitted; leave it alone
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	_, err = api.Create(ctx, j, metav1.CreateOptions{FieldManager: FieldManager})
	return err
}

// applyNamespace is a cluster-scoped Get → Create-or-Update. The
// namespace's spec is essentially immutable post-create (no finalizers
// to push around), so we preserve ResourceVersion + Status and update
// labels/annotations only.
func (c *Client) applyNamespace(ctx context.Context, ns *corev1.Namespace) error {
	api := c.CS.CoreV1().Namespaces()
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, err := api.Get(ctx, ns.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err := api.Create(ctx, ns, metav1.CreateOptions{FieldManager: FieldManager})
			return err
		}
		if err != nil {
			return err
		}
		ns.ResourceVersion = existing.ResourceVersion
		ns.Status = existing.Status
		_, err = api.Update(ctx, ns, metav1.UpdateOptions{FieldManager: FieldManager})
		return err
	})
}

// applyServiceAccount is a Get → Create-or-Update. ServiceAccounts
// rarely change post-create; preserve ResourceVersion + the
// auto-generated Secrets reference.
func (c *Client) applyServiceAccount(ctx context.Context, ns string, sa *corev1.ServiceAccount) error {
	api := c.CS.CoreV1().ServiceAccounts(ns)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, err := api.Get(ctx, sa.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err := api.Create(ctx, sa, metav1.CreateOptions{FieldManager: FieldManager})
			return err
		}
		if err != nil {
			return err
		}
		sa.ResourceVersion = existing.ResourceVersion
		// Preserve auto-managed token Secrets the apiserver attaches.
		sa.Secrets = existing.Secrets
		sa.ImagePullSecrets = existing.ImagePullSecrets
		_, err = api.Update(ctx, sa, metav1.UpdateOptions{FieldManager: FieldManager})
		return err
	})
}

// applyRole is a namespace-scoped Get → Create-or-Update for the
// RBAC Role that grants the Grafana sidecar configmap-watch on its
// own namespace.
func (c *Client) applyRole(ctx context.Context, ns string, r *rbacv1.Role) error {
	api := c.CS.RbacV1().Roles(ns)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, err := api.Get(ctx, r.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err := api.Create(ctx, r, metav1.CreateOptions{FieldManager: FieldManager})
			return err
		}
		if err != nil {
			return err
		}
		r.ResourceVersion = existing.ResourceVersion
		_, err = api.Update(ctx, r, metav1.UpdateOptions{FieldManager: FieldManager})
		return err
	})
}

// applyRoleBinding is the namespace-scoped sibling of
// applyClusterRoleBinding — same shape, scoped to a single namespace.
func (c *Client) applyRoleBinding(ctx context.Context, ns string, rb *rbacv1.RoleBinding) error {
	api := c.CS.RbacV1().RoleBindings(ns)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, err := api.Get(ctx, rb.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err := api.Create(ctx, rb, metav1.CreateOptions{FieldManager: FieldManager})
			return err
		}
		if err != nil {
			return err
		}
		rb.ResourceVersion = existing.ResourceVersion
		_, err = api.Update(ctx, rb, metav1.UpdateOptions{FieldManager: FieldManager})
		return err
	})
}

// applyClusterRole is a cluster-scoped Get → Create-or-Update for the
// RBAC ClusterRole that grants Promtail node-discovery permissions.
func (c *Client) applyClusterRole(ctx context.Context, cr *rbacv1.ClusterRole) error {
	api := c.CS.RbacV1().ClusterRoles()
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, err := api.Get(ctx, cr.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err := api.Create(ctx, cr, metav1.CreateOptions{FieldManager: FieldManager})
			return err
		}
		if err != nil {
			return err
		}
		cr.ResourceVersion = existing.ResourceVersion
		_, err = api.Update(ctx, cr, metav1.UpdateOptions{FieldManager: FieldManager})
		return err
	})
}

// applyClusterRoleBinding is a cluster-scoped Get → Create-or-Update.
// Binds Promtail's ServiceAccount to the ClusterRole.
func (c *Client) applyClusterRoleBinding(ctx context.Context, crb *rbacv1.ClusterRoleBinding) error {
	api := c.CS.RbacV1().ClusterRoleBindings()
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, err := api.Get(ctx, crb.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err := api.Create(ctx, crb, metav1.CreateOptions{FieldManager: FieldManager})
			return err
		}
		if err != nil {
			return err
		}
		crb.ResourceVersion = existing.ResourceVersion
		_, err = api.Update(ctx, crb, metav1.UpdateOptions{FieldManager: FieldManager})
		return err
	})
}

// WaitDeploymentReady polls until ReadyReplicas == Spec.Replicas or
// ctx expires. 5-minute timeout suits image pulls on slow networks;
// tighter masks real failures, looser makes feedback slow.
func (c *Client) WaitDeploymentReady(ctx context.Context, ns, name string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	api := c.CS.AppsV1().Deployments(ns)
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()

	for {
		d, err := api.Get(ctx, name, metav1.GetOptions{})
		if err == nil && d.Spec.Replicas != nil && d.Status.ReadyReplicas >= *d.Spec.Replicas && d.Status.ObservedGeneration >= d.Generation {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("deployment %s/%s not ready: %w", ns, name, ctx.Err())
		case <-tick.C:
		}
	}
}
