package kube

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GetStatefulSet returns the StatefulSet named `name` in `ns`, or
// (nil, nil) when it doesn't exist. The "exists-or-not" shape is
// deliberate — callers are doing a probe (does this workload already
// live here?), not a load-bearing read, so funneling NotFound through
// an explicit nil is cleaner than forcing every call site to import
// apierrors.IsNotFound.
//
// Used by the databases reconciler to detect node-pin drift before
// touching a database: the existing StatefulSet's nodeSelector is the
// source of truth for which node the data physically lives on, and a
// mismatch with cfg surfaces a "pending migration" warning rather than
// silently moving data.
func (c *Client) GetStatefulSet(ctx context.Context, ns, name string) (*appsv1.StatefulSet, error) {
	ss, err := c.CS.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get statefulset %s/%s: %w", ns, name, err)
	}
	return ss, nil
}

// DeletePVC removes a PersistentVolumeClaim. Idempotent — NotFound is
// treated as already-gone. Used by `nvoi database migrate` to clear
// the old node's data volume after the backup has been captured; the
// underlying PV (ZFS dataset under OpenEBS ZFS-LocalPV) is reclaimed
// by the CSI driver when the claim goes away.
func (c *Client) DeletePVC(ctx context.Context, ns, name string) error {
	err := c.CS.CoreV1().PersistentVolumeClaims(ns).Delete(ctx, name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete pvc %s/%s: %w", ns, name, err)
	}
	return nil
}

// DeleteByName removes the Deployment, StatefulSet, and Service named
// `name`. Idempotent — NotFound on any of them is treated as
// already-gone. Used by `nvoi database migrate` (teardown step), by
// `nvoi database branch-delete`, and by other reconcile paths that
// drop a workload set keyed on a shared base name.
func (c *Client) DeleteByName(ctx context.Context, ns, name string) error {
	if err := ignoreNotFound(c.CS.AppsV1().Deployments(ns).Delete(ctx, name, metav1.DeleteOptions{})); err != nil {
		return fmt.Errorf("delete deployment/%s: %w", name, err)
	}
	if err := ignoreNotFound(c.CS.AppsV1().StatefulSets(ns).Delete(ctx, name, metav1.DeleteOptions{})); err != nil {
		return fmt.Errorf("delete statefulset/%s: %w", name, err)
	}
	if err := ignoreNotFound(c.CS.CoreV1().Services(ns).Delete(ctx, name, metav1.DeleteOptions{})); err != nil {
		return fmt.Errorf("delete service/%s: %w", name, err)
	}
	return nil
}
