package kube

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
)

// ApplyDeployment is the typed Get-then-Create-or-Update for
// Deployment. Wraps Update in retry-on-conflict because controllers
// (the Deployment controller, replicaset controller) bump
// ResourceVersion async between our Get and Update — standard
// client-go hygiene.
//
// Status is preserved from the existing object (it's an apiserver
// subresource — modifying it via Update has no effect, but writing
// Spec.Replicas without preserving Status can confuse readiness probes
// in edge cases).
func (c *Client) ApplyDeployment(ctx context.Context, ns string, dep *appsv1.Deployment) error {
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

// ApplyService is the same pattern for Service. ClusterIP is a special
// case — the apiserver assigns one on first Create and rejects
// Updates that change it, so we always preserve existing.Spec.ClusterIP.
func (c *Client) ApplyService(ctx context.Context, ns string, svc *corev1.Service) error {
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
		svc.ResourceVersion = existing.ResourceVersion
		svc.Spec.ClusterIP = existing.Spec.ClusterIP
		_, err = api.Update(ctx, svc, metav1.UpdateOptions{FieldManager: FieldManager})
		return err
	})
}

// ApplySecret upserts a Secret. Same Get-then-Create-or-Update +
// retry-on-conflict pattern. Used for the registry-auth Secret.
func (c *Client) ApplySecret(ctx context.Context, ns string, sec *corev1.Secret) error {
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

// DeleteDeployment removes a Deployment. Idempotent: NotFound returns
// nil so reconcile-on-removal can run on every deploy without
// caring whether the Deployment ever existed.
func (c *Client) DeleteDeployment(ctx context.Context, ns, name string) error {
	err := c.CS.AppsV1().Deployments(ns).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

// DeleteService removes a Service. Same idempotency contract.
func (c *Client) DeleteService(ctx context.Context, ns, name string) error {
	err := c.CS.CoreV1().Services(ns).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

// ListNvoiDeployments returns the names of every Deployment in the
// namespace tagged with `nvoi/owner=nvoi`. Used by reconcile-on-removal:
// any name in this list whose YAML entry is gone gets deleted.
func (c *Client) ListNvoiDeployments(ctx context.Context, ns string) ([]string, error) {
	list, err := c.CS.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{
		LabelSelector: "nvoi/owner=nvoi",
	})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(list.Items))
	for _, d := range list.Items {
		out = append(out, d.Name)
	}
	return out, nil
}

// ListNvoiServices is the Service equivalent of ListNvoiDeployments.
func (c *Client) ListNvoiServices(ctx context.Context, ns string) ([]string, error) {
	list, err := c.CS.CoreV1().Services(ns).List(ctx, metav1.ListOptions{
		LabelSelector: "nvoi/owner=nvoi",
	})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(list.Items))
	for _, s := range list.Items {
		out = append(out, s.Name)
	}
	return out, nil
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
