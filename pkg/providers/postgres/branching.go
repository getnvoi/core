package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/getnvoi/core/pkg/internal/kube"
	"github.com/getnvoi/core/pkg/naming"
	"github.com/getnvoi/core/pkg/providers"
	"github.com/getnvoi/core/pkg/ssh"
)

// ZFS-backed snapshot + branch primitives.
//
// Typed kinds (PVC, Service, StatefulSet) go through *kube.Client +
// ApplyOwned. snapshot.storage.k8s.io kinds (VolumeSnapshot,
// VolumeSnapshotClass) aren't in core client-go, so they apply via
// `sudo k3s kubectl apply -f -` over master SSH — same pattern as
// EnsureZFSCSI. Keeps nvoi free of the external-snapshotter vendored
// client for a narrow-surface feature.

const (
	// SnapshotClassName is the VolumeSnapshotClass every snapshot
	// is created under. Points at the same ZFS CSI driver the
	// StorageClass binds to.
	SnapshotClassName = "nvoi-zfs-snapshots"

	// snapshotAPIVersion pins the VolumeSnapshot API group/version
	// we emit manifests for. `v1` (not `v1beta1`) is GA since
	// external-snapshotter v5.0.
	snapshotAPIVersion = "snapshot.storage.k8s.io/v1"

	// brLabel identifies workloads created by `nvoi database
	// branch` so list/delete can target them without hitting the
	// source DB.
	brLabel = "nvoi/branch-of"

	// snapOfLabel ties a VolumeSnapshot back to its source PVC for
	// list/cleanup operations.
	snapOfLabel = "nvoi/snapshot-of"
)

// dns1123Re matches the DNS-1123 label rules (lowercase alnum +
// hyphens, must start/end alnum, ≤63 chars). Composite branch /
// snapshot names get validated against it before any kubectl apply.
// Inline (not regexp.MustCompile) — keeps the validator pure.
var validNameChar = func(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-'
}

// validateName rejects strings that would break either the kube
// object name OR the heredoc-interpolated YAML we ship to kubectl.
// Empty / leading-or-trailing dash / non-DNS-1123 char → error.
func validateName(kind, s string) error {
	if s == "" {
		return fmt.Errorf("%s: empty", kind)
	}
	if len(s) > 63 {
		return fmt.Errorf("%s %q exceeds 63 char DNS-1123 limit", kind, s)
	}
	if s[0] == '-' || s[len(s)-1] == '-' {
		return fmt.Errorf("%s %q must not start or end with a hyphen", kind, s)
	}
	for _, r := range s {
		if !validNameChar(r) {
			return fmt.Errorf("%s %q: invalid character %q (allowed: a-z, 0-9, -)", kind, s, r)
		}
	}
	return nil
}

// ── Snapshot primitives ─────────────────────────────────────────────

// EnsureSnapshotClass installs the VolumeSnapshotClass via kubectl
// on master. Idempotent. Called by Snapshot before creating a
// snapshot — without the class existing, the snapshot request would
// fail.
func EnsureSnapshotClass(ctx context.Context, masterSSH ssh.Shell) error {
	if masterSSH == nil {
		return fmt.Errorf("postgres.EnsureSnapshotClass: master ssh required")
	}
	manifest := fmt.Sprintf(`apiVersion: %s
kind: VolumeSnapshotClass
metadata:
  name: %s
driver: zfs.csi.openebs.io
deletionPolicy: Delete
`, snapshotAPIVersion, SnapshotClassName)
	return applyManifest(ctx, masterSSH, manifest)
}

// Snapshot creates a user-requested VolumeSnapshot of the DB's PVC.
// Label is optional metadata; when empty a timestamp-based name is
// used. Returns the snapshot's name so callers can later reference
// it for clone / rollback.
//
// Label is validated DNS-1123 — it lands in an object name and in
// shell-interpolated YAML inside a kubectl heredoc, so unvalidated
// input is both a correctness hazard (kube rejects the object) and
// an injection vector (escape the heredoc).
//
// Concurrency note: VolumeSnapshot creation is async on the CSI
// controller's side — the object lands immediately but ReadyToUse
// flips true only after `zfs snapshot` completes (usually
// O(100ms)). PVC-from-snapshot binding handles the wait implicitly
// — the CSI controller blocks the PVC until the source snapshot is
// Ready — so callers don't need to poll themselves.
func Snapshot(ctx context.Context, masterSSH ssh.Shell, app, env, dbName, label string) (providers.SnapshotRef, error) {
	if masterSSH == nil {
		return providers.SnapshotRef{}, fmt.Errorf("postgres.Snapshot: master ssh required")
	}
	if label == "" {
		label = time.Now().UTC().Format("20060102t150405z")
	}
	if err := validateName("snapshot label", label); err != nil {
		return providers.SnapshotRef{}, err
	}
	snapName := naming.DatabaseSnapshot(app, env, dbName, label)
	if err := applyVolumeSnapshot(ctx, masterSSH, app, env, dbName, snapName); err != nil {
		return providers.SnapshotRef{}, err
	}
	return providers.SnapshotRef{
		Name:      snapName,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Kind:      "zfs",
	}, nil
}

// applyVolumeSnapshot is the name-agnostic apply primitive both
// Snapshot (user-facing, label-derived name) and Branch (snapshot
// name derived from naming.DatabaseBranchSnapshot) compose onto.
// Doing the manifest-render + kubectl-apply in one place keeps the
// YAML template in a single location and stops callers from
// duplicating the EnsureSnapshotClass prerequisite.
func applyVolumeSnapshot(ctx context.Context, masterSSH ssh.Shell, app, env, dbName, snapName string) error {
	if err := EnsureSnapshotClass(ctx, masterSSH); err != nil {
		return err
	}
	namespace := naming.Namespace
	pvcName := naming.DatabasePVC(app, env, dbName)

	manifest := fmt.Sprintf(`apiVersion: %s
kind: VolumeSnapshot
metadata:
  name: %s
  namespace: %s
  labels:
    %s: %s
spec:
  volumeSnapshotClassName: %s
  source:
    persistentVolumeClaimName: %s
`, snapshotAPIVersion, snapName, namespace, snapOfLabel, pvcName, SnapshotClassName, pvcName)

	if err := applyManifest(ctx, masterSSH, manifest); err != nil {
		return fmt.Errorf("apply VolumeSnapshot %s: %w", snapName, err)
	}
	return nil
}

// ListSnapshots returns every VolumeSnapshot in the namespace
// tagged as belonging to the given DB. Names come back in kubectl's
// ordering (stable by creation timestamp).
func ListSnapshots(ctx context.Context, masterSSH ssh.Shell, app, env, dbName string) ([]providers.SnapshotRef, error) {
	if masterSSH == nil {
		return nil, fmt.Errorf("postgres.ListSnapshots: master ssh required")
	}
	namespace := naming.Namespace
	pvcName := naming.DatabasePVC(app, env, dbName)
	cmd := fmt.Sprintf(
		`sudo k3s kubectl -n %s get volumesnapshot -l %s=%s -o name 2>/dev/null || true`,
		namespace, snapOfLabel, pvcName,
	)
	out, err := masterSSH.Run(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	var snaps []providers.SnapshotRef
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// `kubectl get -o name` emits `volumesnapshot/NAME`.
		if i := strings.LastIndex(line, "/"); i >= 0 {
			line = line[i+1:]
		}
		snaps = append(snaps, providers.SnapshotRef{Name: line, Kind: "zfs"})
	}
	return snaps, nil
}

// DeleteSnapshot removes a VolumeSnapshot. The ZFS-LocalPV CSI runs
// `zfs destroy` on the underlying dataset (ReclaimPolicy=Delete on
// the SnapshotClass). Idempotent via --ignore-not-found. Snapshot
// name is validated DNS-1123 so it can't inject into the kubectl
// command.
func DeleteSnapshot(ctx context.Context, masterSSH ssh.Shell, namespace, snapshotName string) error {
	if masterSSH == nil {
		return fmt.Errorf("postgres.DeleteSnapshot: master ssh required")
	}
	if err := validateName("snapshot name", snapshotName); err != nil {
		return err
	}
	cmd := fmt.Sprintf(
		`sudo k3s kubectl -n %s delete volumesnapshot %s --ignore-not-found`,
		namespace, snapshotName,
	)
	if _, err := masterSSH.Run(ctx, cmd); err != nil {
		return fmt.Errorf("delete snapshot %s: %w", snapshotName, err)
	}
	return nil
}

// ── Branch primitives ──────────────────────────────────────────────

// BranchSource is the subset of a DatabaseRequest the branch helper
// needs. Derived names come from pkg/naming so the convention
// stays in one place. Image version flows from cfg.databases.X.version
// — empty falls back to defaultPostgresVersion.
type BranchSource struct {
	App, Env   string // for naming.* helpers
	DBName     string // databases.<name> key
	Size       int    // PVC storage request in GiB
	Version    string // postgres version (empty → default)
	ServerRole string // nvoi-role label of the DB node (branch shares the node)
}

// Branch creates a sibling postgres StatefulSet bound to a CoW clone
// of the source DB's data. Composes over Snapshot: take a fresh
// snapshot of the source PVC, create a new PVC with dataSource
// pointing at it, apply a sibling StatefulSet + Service pointing at
// the new PVC.
//
// Naming: branches live under `{source}-br-{branch}` so siblings
// don't collide with the primary or with each other. The branch's
// Service DNS is the same name — consumer tooling connects to it
// instead of the primary's Service.
//
// Idempotent on re-run: ApplyOwned reconciles each object. Callers
// wanting a fresh snapshot should Snapshot → Branch rather than
// re-running Branch against the same branch name.
//
// kc may be nil only in tests that stub the apply path; production
// callers always pass the master kube client.
func Branch(ctx context.Context, kc *kube.Client, masterSSH ssh.Shell, src BranchSource, branchName string) (providers.BranchRef, error) {
	if masterSSH == nil {
		return providers.BranchRef{}, fmt.Errorf("postgres.Branch: master ssh required")
	}
	if kc == nil {
		return providers.BranchRef{}, fmt.Errorf("postgres.Branch: kube client required")
	}
	if err := validateName("branch", branchName); err != nil {
		return providers.BranchRef{}, err
	}
	if src.Size <= 0 {
		return providers.BranchRef{}, fmt.Errorf("postgres.Branch: source size must be > 0")
	}

	// The branch workload name becomes a k8s Service name → DNS-1123
	// label (63-char cap). validateName(branchName) alone isn't
	// enough because the final name is a composite of several
	// already-validated segments. Check the composite upfront so we
	// fail cleanly instead of mid-apply with an opaque kube error.
	branchWorkload := naming.DatabaseBranch(src.App, src.Env, src.DBName, branchName)
	if err := validateName("branch composite name", branchWorkload); err != nil {
		return providers.BranchRef{}, fmt.Errorf("%w — shorten app / env / database / branch names", err)
	}
	namespace := naming.Namespace

	// 1. Snapshot the source PVC. naming.DatabaseBranchSnapshot
	// encodes the branch lineage in the snapshot's name so
	// list/delete by prefix can trace a branch back to its seed.
	snapName := naming.DatabaseBranchSnapshot(src.App, src.Env, src.DBName, branchName)
	if err := applyVolumeSnapshot(ctx, masterSSH, src.App, src.Env, src.DBName, snapName); err != nil {
		return providers.BranchRef{}, fmt.Errorf("snapshot source: %w", err)
	}

	scope := kube.Scope{Namespace: namespace, Owner: kube.OwnerDatabaseBranches}

	// 2. PVC cloned from the snapshot. Owner=database-branches (not
	// databases) so reconcile.Databases' parent-DB sweep doesn't
	// eat ephemeral branches when the parent DB is reconciled.
	if err := kc.ApplyOwned(ctx, scope, buildBranchPVC(src, branchWorkload, snapName)); err != nil {
		return providers.BranchRef{}, fmt.Errorf("apply branch PVC: %w", err)
	}

	// 3. Sibling Service.
	if err := kc.ApplyOwned(ctx, scope, buildBranchService(src, branchWorkload)); err != nil {
		return providers.BranchRef{}, fmt.Errorf("apply branch Service: %w", err)
	}

	// 4. Sibling StatefulSet bound to the clone PVC.
	if err := kc.ApplyOwned(ctx, scope, buildBranchStatefulSet(src, branchWorkload)); err != nil {
		return providers.BranchRef{}, fmt.Errorf("apply branch StatefulSet: %w", err)
	}

	return providers.BranchRef{
		Name:     branchName,
		Endpoint: fmt.Sprintf("%s.%s.svc.cluster.local:5432", branchWorkload, namespace),
		Snapshot: snapName,
	}, nil
}

// DeleteBranch removes every workload created for a branch — the
// sibling StatefulSet, Service, PVC, and the VolumeSnapshot the PVC
// was cloned from. ReclaimPolicy=Delete on both the StorageClass and
// the SnapshotClass means the underlying ZFS datasets are destroyed
// too. Idempotent.
func DeleteBranch(ctx context.Context, kc *kube.Client, masterSSH ssh.Shell, app, env, dbName, branchName string) error {
	if kc == nil {
		return fmt.Errorf("postgres.DeleteBranch: kube client required")
	}
	if err := validateName("branch", branchName); err != nil {
		return err
	}
	namespace := naming.Namespace
	branchWorkload := naming.DatabaseBranch(app, env, dbName, branchName)

	// StatefulSet + Service first — evicts the pod before its PVC
	// goes.
	if err := kc.DeleteByName(ctx, namespace, branchWorkload); err != nil {
		return fmt.Errorf("delete branch workloads: %w", err)
	}
	// PVC next — CSI runs zfs destroy on the clone dataset.
	if err := kc.DeletePVC(ctx, namespace, branchWorkload); err != nil {
		return fmt.Errorf("delete branch pvc: %w", err)
	}
	// VolumeSnapshot last — CSI runs zfs destroy on the snapshot.
	// Via kubectl because VolumeSnapshot isn't in our typed surface.
	if masterSSH != nil {
		snapName := naming.DatabaseBranchSnapshot(app, env, dbName, branchName)
		if err := DeleteSnapshot(ctx, masterSSH, namespace, snapName); err != nil {
			return err
		}
	}
	return nil
}

// ListBranches returns every branch workload (StatefulSet) bound to
// the given source DB. Uses the branch-of label so list semantics
// mirror DeleteBranch's sweep target.
func ListBranches(ctx context.Context, kc *kube.Client, app, env, dbName string) ([]providers.BranchRef, error) {
	if kc == nil {
		return nil, fmt.Errorf("postgres.ListBranches: kube client required")
	}
	namespace := naming.Namespace
	scope := kube.Scope{Namespace: namespace, Owner: kube.OwnerDatabaseBranches}
	names, err := kc.ListOwned(ctx, scope, kube.KindStatefulSet)
	if err != nil {
		return nil, fmt.Errorf("list branches: %w", err)
	}
	// We get every workload owned by database-branches in the
	// namespace; filter to those whose name matches the per-DB
	// branch prefix.
	prefix := naming.Database(app, env, dbName) + "-br-"
	var out []providers.BranchRef
	for _, n := range names {
		if !strings.HasPrefix(n, prefix) {
			continue
		}
		branchName := strings.TrimPrefix(n, prefix)
		out = append(out, providers.BranchRef{
			Name:     branchName,
			Endpoint: fmt.Sprintf("%s.%s.svc.cluster.local:5432", n, namespace),
			Snapshot: naming.DatabaseBranchSnapshot(app, env, dbName, branchName),
		})
	}
	return out, nil
}

// ── Typed branch builders ──────────────────────────────────────────

func buildBranchPVC(src BranchSource, name, snapshotName string) *corev1.PersistentVolumeClaim {
	sc := ZFSStorageClassName
	snapshotAPI := "snapshot.storage.k8s.io"
	return &corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PersistentVolumeClaim"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: naming.Namespace,
			Labels:    branchLabels(src),
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
					corev1.ResourceStorage: resource.MustParse(fmt.Sprintf("%dGi", src.Size)),
				},
			},
		},
	}
}

func buildBranchService(src BranchSource, name string) *corev1.Service {
	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: naming.Namespace,
			Labels:    branchLabels(src),
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app.kubernetes.io/name": name},
			Ports: []corev1.ServicePort{{
				Name:       "postgres",
				Port:       5432,
				TargetPort: intstr.FromInt(5432),
			}},
		},
	}
}

func buildBranchStatefulSet(src BranchSource, name string) *appsv1.StatefulSet {
	replicas := int32(1)
	credsSecret := naming.DatabaseCredentials(src.App, src.Env, src.DBName)
	secretKeyRef := func(key string) *corev1.EnvVarSource {
		return &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: credsSecret},
				Key:                  key,
			},
		}
	}
	podLabels := map[string]string{"app.kubernetes.io/name": name}
	for k, v := range branchLabels(src) {
		podLabels[k] = v
	}
	return &appsv1.StatefulSet{
		TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "StatefulSet"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: naming.Namespace,
			Labels:    branchLabels(src),
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &replicas,
			ServiceName: name,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app.kubernetes.io/name": name},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: podLabels},
				Spec: corev1.PodSpec{
					// Branch shares the source node — the ZFS clone
					// lives in the same pool. Cross-node branching
					// isn't supported (pools are node-local).
					NodeSelector: map[string]string{"nvoi-role": src.ServerRole},
					Containers: []corev1.Container{{
						Name:  "postgres",
						Image: ImageFor(src.Version),
						Env: []corev1.EnvVar{
							// Credentials come from the source DB's
							// Secret — ZFS clones carry pg_roles
							// bit-exact so auth works identically.
							{Name: "POSTGRES_USER", ValueFrom: secretKeyRef("user")},
							{Name: "POSTGRES_PASSWORD", ValueFrom: secretKeyRef("password")},
							{Name: "POSTGRES_DB", ValueFrom: secretKeyRef("database")},
						},
						Ports: []corev1.ContainerPort{{ContainerPort: 5432, Name: "postgres"}},
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "data",
							MountPath: "/var/lib/postgresql/data",
						}},
					}},
					Volumes: []corev1.Volume{{
						Name: "data",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: name},
						},
					}},
				},
			},
		},
	}
}

// branchLabels ties siblings back to the source DB for list/delete.
// brLabel = nvoi/branch-of=<source-pvc-name>.
func branchLabels(src BranchSource) map[string]string {
	return map[string]string{
		brLabel: naming.DatabasePVC(src.App, src.Env, src.DBName),
	}
}

// applyManifest pipes a YAML manifest into `kubectl apply -f -` via
// SSH. Used only for kinds outside core client-go (VolumeSnapshot,
// VolumeSnapshotClass). Typed kinds go through kube.Client.ApplyOwned
// like the rest of the codebase.
func applyManifest(ctx context.Context, masterSSH ssh.Shell, manifest string) error {
	cmd := fmt.Sprintf(
		"sudo k3s kubectl apply -f - <<'NVOI_EOF'\n%s\nNVOI_EOF\n",
		strings.TrimSpace(manifest),
	)
	if _, err := masterSSH.Run(ctx, cmd); err != nil {
		return fmt.Errorf("kubectl apply: %w", err)
	}
	return nil
}
