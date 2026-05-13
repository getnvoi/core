package postgres

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/getnvoi/core/pkg/internal/kube"
	"github.com/getnvoi/core/pkg/providers"
)

// Rollback performs an in-place restore of the primary DB from one
// of its snapshots. The Service stays the same, the DSN stays the
// same — clients don't reconfigure. Different from Restore (which
// replays an external dump) and from Branch (which creates a
// sibling).
//
// Mechanics:
//
//  1. Scale the primary StatefulSet to 0 (releases the PVC).
//  2. Delete the primary PVC.
//  3. Recreate the primary PVC with dataSource = the snapshot.
//  4. Scale the StatefulSet back to 1.
//  5. Wait for Ready.
//
// Target downtime: O(seconds) for the pod restart + zfs clone
// (metadata-only).
//
// req.MasterSSH isn't required — the snapshot must already exist
// (operator created it via `nvoi database snapshot`). We don't
// take a fresh one as part of rollback; the operator owns when the
// rollback target was captured.
func Rollback(ctx context.Context, req providers.DatabaseRequest, snapshotID string) error {
	if req.Kube == nil {
		return fmt.Errorf("postgres.Rollback: kube client required")
	}
	if snapshotID == "" {
		return fmt.Errorf("postgres.Rollback: snapshotID required (use `nvoi database snapshots %s` to list)", req.Name)
	}
	if err := validateName("snapshot id", snapshotID); err != nil {
		return err
	}

	provider := &Provider{}
	var emitter kube.ProgressEmitter
	if req.Log != nil {
		emitter = logEmitter{log: req.Log}
	}

	// 1. Scale the StatefulSet to 0. The cleanest path is to delete
	// and recreate via ApplyOwned with replicas=0, but for an
	// in-place rollback we want to preserve the StatefulSet's
	// identity (UID, generation history). Two-step:
	//   a. Delete the StatefulSet (releases the PVC binding).
	//   b. Delete the PVC.
	//   c. Recreate PVC with dataSource = snapshot.
	//   d. Recreate the StatefulSet via Reconcile (which uses the
	//      same name and binds the new PVC).
	if err := req.Kube.DeleteByName(ctx, req.Namespace, req.FullName); err != nil {
		return fmt.Errorf("rollback: delete StatefulSet: %w", err)
	}
	if err := req.Kube.DeletePVC(ctx, req.Namespace, req.PVCName); err != nil {
		return fmt.Errorf("rollback: delete PVC: %w", err)
	}

	// Apply the clone-PVC bound to the snapshot.
	scope := kube.Scope{Namespace: req.Namespace, Owner: kube.OwnerDatabases}
	if err := req.Kube.ApplyOwned(ctx, scope, buildRollbackPVC(req, snapshotID)); err != nil {
		return fmt.Errorf("rollback: apply clone PVC: %w", err)
	}

	// Recreate the StatefulSet + Service via Reconcile. Reconcile
	// returns the StorageClass too — re-applying it is a no-op.
	plan, err := provider.Reconcile(ctx, req)
	if err != nil {
		return fmt.Errorf("rollback: reconcile new workloads: %w", err)
	}
	for _, obj := range plan.Workloads {
		// Skip the PVC — we already applied a snapshot-sourced one.
		if _, isPVC := obj.(*corev1.PersistentVolumeClaim); isPVC {
			continue
		}
		if err := req.Kube.ApplyOwned(ctx, scope, obj); err != nil {
			return fmt.Errorf("rollback: apply %T: %w", obj, err)
		}
	}

	if err := req.Kube.WaitForStatefulSetReady(ctx, req.Namespace, req.FullName, emitter); err != nil {
		return fmt.Errorf("rollback: new pod rollout: %w", err)
	}
	if _, err := provider.ExecSQL(ctx, req, "SELECT 1"); err != nil {
		return fmt.Errorf("rollback: post-rollback health check: %w", err)
	}
	return nil
}

// buildRollbackPVC is the rollback-specific PVC: same name as the
// normal primary PVC, but dataSource references the chosen snapshot
// so the CSI driver clones from it on bind. Shape mirrors buildPVC
// (postgres.go) — kept here, not de-duplicated, because the
// dataSource field is the only meaningful difference and inlining
// reads more clearly than passing a parameter into buildPVC.
func buildRollbackPVC(req providers.DatabaseRequest, snapshotName string) runtime.Object {
	sc := ZFSStorageClassName
	snapshotAPI := "snapshot.storage.k8s.io"
	return &corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PersistentVolumeClaim"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.PVCName,
			Namespace: req.Namespace,
			Labels:    labelsFor(req),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: &sc,
			DataSource: &corev1.TypedLocalObjectReference{
				APIGroup: &snapshotAPI,
				Kind:     "VolumeSnapshot",
				Name:     snapshotName,
			},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(fmt.Sprintf("%dGi", req.Spec.Size)),
				},
			},
		},
	}
}
