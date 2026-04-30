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

// ApplyService is the same pattern for Service with one extra wrinkle:
// the headless ↔ non-headless transition (ClusterIP "None" ↔ assigned
// IP) is rejected by the apiserver as an Update — it requires
// delete+recreate. Detect that case and route through Delete+Create;
// otherwise preserve the existing.Spec.ClusterIP (apiserver assigns
// ClusterIPs on first Create and rejects mutations).
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
		// Headless transition — kind change, recreate.
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

// ApplyStatefulSet is the StatefulSet sibling of ApplyDeployment.
// Same Get→Create-or-Update + retry-on-conflict pattern. Status is
// preserved from existing for the same reason as Deployment.
//
// VolumeClaimTemplates are immutable post-Create — k8s rejects
// changes. Reconcile-on-config-change for those is a future concern;
// for now an operator who changes storage size for an existing
// StatefulSet must `nvoi destroy` and redeploy.
func (c *Client) ApplyStatefulSet(ctx context.Context, ns string, ss *appsv1.StatefulSet) error {
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
		// VolumeClaimTemplates is immutable — preserve existing to
		// avoid a "field is immutable" reject every reconcile.
		ss.Spec.VolumeClaimTemplates = existing.Spec.VolumeClaimTemplates
		_, err = api.Update(ctx, ss, metav1.UpdateOptions{FieldManager: FieldManager})
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

// DeleteStatefulSet removes a StatefulSet. Same idempotency contract.
// Note: this does NOT delete the underlying PVCs k8s provisioned via
// VolumeClaimTemplates — that's k8s's policy to preserve data even
// when the StatefulSet goes away. Reclaim path is `nvoi destroy`
// (terraform tears down the nodes; hostPath volumes go with them).
func (c *Client) DeleteStatefulSet(ctx context.Context, ns, name string) error {
	err := c.CS.AppsV1().StatefulSets(ns).Delete(ctx, name, metav1.DeleteOptions{})
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

// ListNvoiStatefulSets is the StatefulSet equivalent.
func (c *Client) ListNvoiStatefulSets(ctx context.Context, ns string) ([]string, error) {
	list, err := c.CS.AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{
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
