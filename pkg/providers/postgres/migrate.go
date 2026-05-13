package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/core/pkg/internal/kube"
	"github.com/getnvoi/core/pkg/providers"
)

// Migrate moves the database to the node named by req.Spec.Server.
// Composes BackupNow → wait → teardown old StatefulSet+PVC → apply
// per cfg (new node) → wait pod ready → Restore from the fresh
// backup → SELECT 1 health check.
//
// Idempotent: re-running after success is a no-op (the live node
// already matches req.Spec.Server). Destructive in-flight: writes
// since the captured backup are lost on completion. Operators are
// expected to run this only when they've edited cfg.databases.X.server
// in YAML and run `nvoi deploy` first (which warned about pending
// migration).
func Migrate(ctx context.Context, req providers.DatabaseRequest) error {
	if req.Kube == nil {
		return fmt.Errorf("postgres.Migrate: kube client required")
	}
	if req.Spec.Server == "" {
		return fmt.Errorf("postgres.Migrate: spec.server required (no node to migrate to)")
	}
	if req.Bucket == nil || req.BackupCredsSecretName == "" {
		return fmt.Errorf("postgres.Migrate: backup: must be configured (providers.storage + backup schedule). The migrate path stages data through the bucket")
	}

	// 1. Read live state. If the existing StatefulSet's nodeSelector
	//    already matches cfg, we're a no-op.
	existing, err := req.Kube.GetStatefulSet(ctx, req.Namespace, req.FullName)
	if err != nil {
		return fmt.Errorf("read live StatefulSet: %w", err)
	}
	if existing == nil {
		return fmt.Errorf("postgres.Migrate: no live StatefulSet found for %s — nothing to migrate (run `nvoi deploy` first)", req.FullName)
	}
	current := existing.Spec.Template.Spec.NodeSelector["nvoi-role"]
	if current == req.Spec.Server {
		return nil // already on target node
	}

	// 2. Fresh backup. CreateJobFromCronJob clones the scheduled
	//    template so manual + scheduled backups are identical.
	provider := &Provider{}
	backupRef, err := provider.BackupNow(ctx, req)
	if err != nil {
		return fmt.Errorf("migrate: backup: %w", err)
	}
	var emitter kube.ProgressEmitter
	if req.Log != nil {
		emitter = logEmitter{log: req.Log}
	}
	if err := req.Kube.WaitForJob(ctx, req.Namespace, backupRef.ID, emitter); err != nil {
		return fmt.Errorf("migrate: backup job %s: %w", backupRef.ID, err)
	}

	// 3. Locate the fresh backup key — lexicographically-largest
	//    object is the most recent (image names objects
	//    YYYYMMDDTHHMMSSZ.sql.gz).
	freshKey, err := latestBackupKey(ctx, req)
	if err != nil {
		return fmt.Errorf("migrate: locate fresh backup: %w", err)
	}

	// 4. Teardown old StatefulSet + Service + PVC. The credentials
	//    Secret + backup-creds Secret stay in place — the new pod
	//    reuses them.
	if err := req.Kube.DeleteByName(ctx, req.Namespace, req.FullName); err != nil {
		return fmt.Errorf("migrate: teardown old workloads: %w", err)
	}
	if err := req.Kube.DeletePVC(ctx, req.Namespace, req.PVCName); err != nil {
		return fmt.Errorf("migrate: teardown old pvc: %w", err)
	}

	// 5. Apply new workloads per cfg. Same Reconcile path the
	//    normal deploy uses — guarantees the recreated workloads
	//    are identical in shape to a fresh apply.
	plan, err := provider.Reconcile(ctx, req)
	if err != nil {
		return fmt.Errorf("migrate: apply on %s: %w", req.Spec.Server, err)
	}
	scope := kube.Scope{Namespace: req.Namespace, Owner: kube.OwnerDatabases}
	for _, obj := range plan.Workloads {
		if err := req.Kube.ApplyOwned(ctx, scope, obj); err != nil {
			return fmt.Errorf("migrate: apply %T: %w", obj, err)
		}
	}

	// 6. Wait for the new pod to become Ready. Postgres initializes
	//    PGDATA on first boot of a fresh PVC — restoring before
	//    that completes will fail.
	if err := req.Kube.WaitForStatefulSetReady(ctx, req.Namespace, req.FullName, emitter); err != nil {
		return fmt.Errorf("migrate: new pod rollout: %w", err)
	}

	// 7. Restore the fresh backup. Same RunRestoreJob the standalone
	//    `restore` verb uses.
	if err := provider.Restore(ctx, req, freshKey); err != nil {
		return fmt.Errorf("migrate: restore: %w", err)
	}

	// 8. Health check — SELECT 1 is the simplest connectivity probe.
	if _, err := provider.ExecSQL(ctx, req, "SELECT 1"); err != nil {
		return fmt.Errorf("migrate: post-restore health check: %w", err)
	}
	return nil
}

// latestBackupKey lists the bucket and returns the most-recent key.
// Keys are ISO-ordered (YYYYMMDDTHHMMSSZ.sql.gz) so lexicographic
// max = chronological max.
func latestBackupKey(ctx context.Context, req providers.DatabaseRequest) (string, error) {
	refs, err := providers.BucketListBackups(ctx, req)
	if err != nil {
		return "", err
	}
	if len(refs) == 0 {
		return "", fmt.Errorf("no backup objects found in bucket after BackupNow — did the upload step finish?")
	}
	latest := refs[0].ID
	for _, r := range refs[1:] {
		if r.ID > latest {
			latest = r.ID
		}
	}
	return latest, nil
}

// logEmitter adapts log.Log to kube.ProgressEmitter for the migrate
// path's WaitForJob / WaitForStatefulSetReady progress events. Same
// shape as the one in pkg/providers/database_backups.go — package-
// local copy avoids exporting a one-method helper across the
// boundary.
type logEmitter struct{ log interface{ Info(msg string) } }

func (e logEmitter) Progress(msg string) { e.log.Info(strings.TrimSpace(msg)) }
