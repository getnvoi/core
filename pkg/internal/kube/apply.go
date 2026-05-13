package kube

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
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
func (c *Client) forceRollStaleStatefulSetPods(ctx context.Context, ns string, ss *appsv1.StatefulSet) error {
	live, err := c.CS.AppsV1().StatefulSets(ns).Get(ctx, ss.Name, metav1.GetOptions{})
	if err != nil {
		return nil
	}
	target := live.Status.UpdateRevision
	if target == "" {
		return nil
	}

	selector := metav1.FormatLabelSelector(live.Spec.Selector)
	pods, err := c.CS.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		hash := pod.Labels["controller-revision-hash"]
		if hash == target {
			continue
		}
		if podReadyForRollout(pod) {
			continue
		}
		_ = c.CS.CoreV1().Pods(ns).Delete(ctx, pod.Name, metav1.DeleteOptions{})
	}
	return nil
}

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

// applyPVC is a Get → Create-or-noop for PersistentVolumeClaims. PVC
// spec is largely immutable post-bind (storage size can grow only via
// AllowVolumeExpansion + an Update on .spec.resources; other fields
// reject mutation). Treat presence as satisfied so re-deploys stay
// idempotent against bound PVCs.
func (c *Client) applyPVC(ctx context.Context, ns string, pvc *corev1.PersistentVolumeClaim) error {
	api := c.CS.CoreV1().PersistentVolumeClaims(ns)
	_, err := api.Get(ctx, pvc.Name, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	_, err = api.Create(ctx, pvc, metav1.CreateOptions{FieldManager: FieldManager})
	return err
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
// StorageClass resources. Most fields on a StorageClass are immutable
// post-Create (Provisioner, Parameters, ReclaimPolicy,
// VolumeBindingMode); changing them requires destroy+recreate which
// would break any PVC bound to the class. Treat presence as
// satisfied.
func (c *Client) applyStorageClass(ctx context.Context, sc *storagev1.StorageClass) error {
	api := c.CS.StorageV1().StorageClasses()
	_, err := api.Get(ctx, sc.Name, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	_, err = api.Create(ctx, sc, metav1.CreateOptions{FieldManager: FieldManager})
	return err
}

// applyJob is a Get → Create for batch/v1 Jobs. Jobs are effectively
// immutable post-create: most spec fields (PodTemplate, BackoffLimit)
// reject Update. Manual backup + restore Jobs are always created
// fresh (the caller picks a unique name embedding a unix timestamp),
// so this helper short-circuits to Create when the name isn't
// already present and leaves an existing Job untouched.
func (c *Client) applyJob(ctx context.Context, ns string, j *batchv1.Job) error {
	api := c.CS.BatchV1().Jobs(ns)
	_, err := api.Get(ctx, j.Name, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	_, err = api.Create(ctx, j, metav1.CreateOptions{FieldManager: FieldManager})
	return err
}

// applyNamespace is a cluster-scoped Get → Create-or-Update. The
// namespace's spec is essentially immutable post-create, so we
// preserve ResourceVersion + Status and update labels/annotations
// only. Applied (by the tunnel pipeline creating nvoi-tunnel) but
// never swept — empty namespaces are cheap and serve as debugging
// breadcrumbs. Hence no Kind entry, no List/Delete dispatch.
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

// WaitStatefulSetReady polls until ReadyReplicas == Spec.Replicas or
// ctx expires. Used by deployDatabases to gate workload.ApplyAll on
// the postgres StatefulSet actually serving — without this, a web
// pod that binds `databases: [DATABASE=app]` starts before postgres
// finishes initdb, CrashLoops a few times, and the deploy returns
// "success" while the cluster is visibly broken to operators staring
// at https://. Same 5-minute timeout as WaitDeploymentReady — covers
// initdb + image pull + ZFS PVC bind on a cold node.
//
// Postgres pods are also a good fit for this gate because the
// container's Ready signal is not a true readiness check: the
// container is "Ready" the moment postgresd is alive, but accepting
// connections lags by initdb time on first boot. ReadyReplicas
// reflects the kubelet's readiness check, which today is just "is
// container running" because the image carries no readiness probe.
// That's still better than no wait at all — by the time
// ReadyReplicas matches Spec.Replicas, the kubelet has decided the
// pod is healthy, which in practice means postgres is past initdb on
// the first deploy and serving on every subsequent one.
func (c *Client) WaitStatefulSetReady(ctx context.Context, ns, name string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	api := c.CS.AppsV1().StatefulSets(ns)
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()

	for {
		ss, err := api.Get(ctx, name, metav1.GetOptions{})
		if err == nil && ss.Spec.Replicas != nil && ss.Status.ReadyReplicas >= *ss.Spec.Replicas && ss.Status.ObservedGeneration >= ss.Generation {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("statefulset %s/%s not ready: %w", ns, name, ctx.Err())
		case <-tick.C:
		}
	}
}
