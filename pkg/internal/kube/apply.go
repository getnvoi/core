package kube

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
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

// applyStatefulSet preserves VolumeClaimTemplates (immutable post-Create)
// + Status. Reconcile-on-config-change for the templates is a future
// concern; today an operator who changes storage size for an existing
// StatefulSet must `nvoi destroy` and redeploy.
func (c *Client) applyStatefulSet(ctx context.Context, ns string, ss *appsv1.StatefulSet) error {
	api := c.CS.AppsV1().StatefulSets(ns)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
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
	})
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

// ensureNamespace creates ns if absent; no-op if present. Used by
// system-workload appliers — cert-manager applies its own namespace
// via the upstream manifest, but in-Go appliers (cloudflared, future
// observability stack) materialize their namespace explicitly.
func (c *Client) ensureNamespace(ctx context.Context, ns string) error {
	if _, err := c.CS.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{}); err == nil {
		return nil
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	_, err := c.CS.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: ns},
	}, metav1.CreateOptions{FieldManager: FieldManager})
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
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
