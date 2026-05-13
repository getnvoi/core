package kube

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/core/pkg/internal/utils"
)

// pvcDeletionPollInterval is how often DeletePVC re-checks whether
// the API server has finished finalizing the PVC. Short enough to
// feel responsive on a typical CSI delete (a few seconds), long
// enough not to hammer the apiserver.
var pvcDeletionPollInterval = 1 * time.Second

// pvcDeletionTimeout caps how long DeletePVC waits for the PVC to
// actually disappear. OpenEBS ZFS-LocalPV typically destroys the
// dataset in well under a minute; 2 minutes is generous headroom
// for slow disks or backed-up CSI controllers.
var pvcDeletionTimeout = 2 * time.Minute

// SetPVCDeletionTiming overrides the polling intervals for tests so
// the suite doesn't depend on wall-clock seconds.
func SetPVCDeletionTiming(poll, max time.Duration) {
	pvcDeletionPollInterval = poll
	pvcDeletionTimeout = max
}

// PVCDeletionTimingForTest returns the current intervals so tests
// can save + restore the global values around an override. Keeps
// individual tests from leaking timing changes into the rest of the
// suite.
func PVCDeletionTimingForTest() (poll, max time.Duration) {
	return pvcDeletionPollInterval, pvcDeletionTimeout
}

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

// DeletePVC removes a PersistentVolumeClaim AND waits for the API
// server to finalize it (finalizers cleared, object gone). Idempotent —
// NotFound on the initial Delete OR during the wait is treated as
// already-gone.
//
// The wait is non-negotiable because k8s PVC deletion is async by
// design: `kubernetes.io/pvc-protection` keeps the object alive
// while any pod still references it, then the CSI driver runs its
// own finalizer to destroy the underlying volume. The PVC sits in
// Terminating state with a `deletionTimestamp` set during that
// window, which can be many seconds.
//
// Without this wait, a caller that does Delete → Apply (e.g.
// `nvoi database rollback` swapping in a snapshot-sourced PVC) sees
// the Terminating PVC on Get, treats it as "already exists", skips
// the Create, and ends up with a pod that can't schedule because
// the PVC was destined to vanish anyway. We saw exactly that in
// production: rollback returned success while the cluster served
// 500s for ~90 seconds.
//
// Used by `nvoi database migrate` to clear the old node's data
// volume after the backup has been captured, and by `nvoi database
// rollback` to swap the live volume for a snapshot clone.
func (c *Client) DeletePVC(ctx context.Context, ns, name string) error {
	api := c.CS.CoreV1().PersistentVolumeClaims(ns)
	err := api.Delete(ctx, name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete pvc %s/%s: %w", ns, name, err)
	}

	return utils.Poll(ctx, pvcDeletionPollInterval, pvcDeletionTimeout, func() (bool, error) {
		_, err := api.Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		if err != nil {
			// Transient errors keep polling; non-transient errors
			// shouldn't surface here because Get on a deleted PVC
			// returns NotFound, anything else is an apiserver hiccup
			// worth retrying.
			return false, nil
		}
		return false, nil
	})
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
