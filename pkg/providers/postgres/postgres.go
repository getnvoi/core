// Package postgres is the reference implementation of
// providers.DatabaseProvider — selfhosted PostgreSQL running in the
// cluster on OpenEBS ZFS-LocalPV.
//
// V1 scope is intentionally non-destructive against the primary's
// live volume: provisioning, credentials, ExecSQL, BackupNow,
// ListBackups, DownloadBackup, Snapshot, Branch. The destructive
// verbs (Restore replaying a dump into the primary, Migrate moving
// data across nodes, Rollback swapping the primary's PVC for a
// snapshot clone) land in a follow-up PR — they share a known
// Terminating-PVC race that took down nvoi.to during dogfood and
// need real interactive confirmation + mid-flight failure runbooks
// before they're operator-safe.
//
// File layout inside the package:
//
//	postgres.go    Provider, Reconcile, EnsureCredentials, ExecSQL,
//	               BackupNow, ListBackups, DownloadBackup.
//	zfs.go         ZFS-LocalPV CSI install + per-node zpool prep +
//	               StorageClass builder.
//	branching.go   Snapshot/Branch primitives backed by VolumeSnapshot
//	               + clone-PVC.
//	register.go    providers.RegisterDatabase("postgres", …).
package postgres

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/getnvoi/core/pkg/internal/kube"
	"github.com/getnvoi/core/pkg/providers"
)

// defaultPostgresVersion is the fallback tag when cfg omits
// databases.X.version. Keep aligned with the latest "current" stable
// postgres major; bumping here is a deliberate commit.
const defaultPostgresVersion = "17"

// ImageFor returns the container image reference for a postgres pod
// given a user-declared version string (from databases.X.version).
// Empty → default. Always the -alpine flavor to keep image pulls
// small on the DB node.
//
// Single source of truth for postgres image naming —
// buildStatefulSet AND the branch StatefulSet builder both call it so
// version bumps propagate to both the primary and every branch in
// one place.
func ImageFor(version string) string {
	if version == "" {
		version = defaultPostgresVersion
	}
	return "postgres:" + version + "-alpine"
}

// Provider is the postgres DatabaseProvider. Stateless — every call
// resolves names + addresses through req. The factory in register.go
// returns a singleton (no per-call construction state).
type Provider struct{}

// ValidateCredentials is a no-op for selfhosted postgres — operator-
// supplied credentials are checked at the YAML validator level
// (every $VAR resolves; user / password / database are non-empty).
// SaaS engines override this to ping the vendor API.
func (p *Provider) ValidateCredentials(context.Context) error { return nil }

// Close releases any per-provider resources. No-op for postgres
// (state is per-call, not per-provider).
func (p *Provider) Close() error { return nil }

// EnsureCredentials stamps the canonical credentials Secret. Every
// engine writes the same key shape — url/host/port/user/password/
// database/sslmode — so apps that consume DATABASE_URL/HOST/PORT/USER/
// PASSWORD work identically across postgres / SaaS engines.
func (p *Provider) EnsureCredentials(ctx context.Context, kc *kube.Client, req providers.DatabaseRequest) (providers.DatabaseCredentials, error) {
	creds := credentials(req)
	if kc != nil {
		if err := kc.EnsureSecret(ctx, req.Namespace, kube.OwnerDatabases, req.CredentialsSecretName, map[string]string{
			"url":      creds.URL,
			"host":     creds.Host,
			"port":     strconv.Itoa(creds.Port),
			"user":     creds.User,
			"password": creds.Password,
			"database": creds.Database,
			"sslmode":  creds.SSLMode,
		}); err != nil {
			return providers.DatabaseCredentials{}, err
		}
	}
	return creds, nil
}

// Reconcile returns the cluster workloads postgres needs: the ZFS
// StorageClass (cluster-scoped, idempotent apply on every deploy),
// the per-DB Service, the PVC bound to that SC, the StatefulSet, and
// — when backup is configured — the uniform backup CronJob from
// pkg/providers/database_backups.go.
//
// ZFS prepare-node (apt install + zpool create) runs on req.NodeSSH
// before workloads emit, when set. Skipped for tests that don't wire
// SSH. The CSI driver install is the reconciler's responsibility (it
// runs once per deploy, not once per DB).
func (p *Provider) Reconcile(ctx context.Context, req providers.DatabaseRequest) (*providers.DatabasePlan, error) {
	if req.NodeSSH != nil && req.Spec.Size > 0 {
		if err := PrepareNode(ctx, req.NodeSSH, req.Spec.Size); err != nil {
			return nil, fmt.Errorf("postgres.PrepareNode: %w", err)
		}
	}

	workloads := []runtime.Object{
		buildZFSStorageClass(),
		buildService(req),
		buildPVC(req),
		buildStatefulSet(req),
	}
	if req.Spec.Backup != nil && req.Spec.Backup.Schedule != "" {
		workloads = append(workloads, providers.BuildBackupCronJob(req))
	}
	return &providers.DatabasePlan{Workloads: workloads}, nil
}

// Delete is a no-op for postgres selfhosted — the StatefulSet /
// Service / PVC / Secret / CronJob die with the namespace on
// teardown, or stay on re-deploy drift. The PVC's lifecycle (and
// thus the ZFS dataset's) is owned by the StorageClass ReclaimPolicy
// (Delete) — when a DB entry is removed from YAML, SweepOwned
// deletes the PVC and the CSI driver runs `zfs destroy`.
func (p *Provider) Delete(context.Context, providers.DatabaseRequest) error { return nil }

// ExecSQL runs one statement against the in-cluster Service via
// kubectl-style pod-exec on the StatefulSet's pod-0. The CSV output
// parses into a typed SQLResult — operators get tabular rendering
// in the CLI without nvoi shelling out to psql.
//
// The exec runs INSIDE the postgres pod itself, so we target
// localhost rather than the Service DNS. Service-name resolution
// works for clients outside the pod, but pods don't always have the
// service mesh's DNS hooks for their own Service (and shouldn't need
// it — connecting to the local instance is canonical for pod-exec).
func (p *Provider) ExecSQL(ctx context.Context, req providers.DatabaseRequest, stmt string) (*providers.SQLResult, error) {
	if req.Kube == nil {
		return nil, fmt.Errorf("postgres.ExecSQL requires kube client")
	}
	dsn := buildDSN("127.0.0.1", 5432, req.Spec.User, req.Spec.Password, req.Spec.Database, "disable")
	var stdout, stderr bytes.Buffer
	if err := req.Kube.Exec(ctx, kube.ExecRequest{
		Namespace: req.Namespace,
		Pod:       req.PodName,
		Command: []string{
			"psql",
			dsn,
			"--csv",
			"-c",
			stmt,
		},
		Stdout: &stdout,
		Stderr: &stderr,
	}); err != nil {
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return nil, err
	}
	return parseCSV(stdout.Bytes())
}

// BackupNow instantiates a one-off Job from the scheduled CronJob's
// template. The CronJob is the source of truth for image / env /
// bucket creds — `backup now` doesn't diverge from the scheduled
// path. Returns the Job name as the BackupRef ID; the bucket key
// itself is timestamped by the image at upload time.
func (p *Provider) BackupNow(ctx context.Context, req providers.DatabaseRequest) (*providers.BackupRef, error) {
	if req.Kube == nil {
		return nil, fmt.Errorf("postgres.BackupNow requires kube client")
	}
	if req.Spec.Backup == nil || req.Spec.Backup.Schedule == "" {
		return nil, fmt.Errorf("postgres backup schedule is not configured")
	}
	jobName := fmt.Sprintf("%s-manual-%d", req.BackupName, time.Now().Unix())
	if err := req.Kube.CreateJobFromCronJob(ctx, req.Namespace, req.BackupName, jobName); err != nil {
		return nil, err
	}
	return &providers.BackupRef{
		ID:        jobName,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Kind:      "dump",
	}, nil
}

// ListBackups / DownloadBackup delegate to the shared bucket
// substrate. Postgres is engine-agnostic here — the same helpers
// serve every DatabaseProvider via the same gzipped-dump bucket
// layout. Restore-from-backup is intentionally NOT exposed via the
// CLI in v1 (destructive against the primary's live data — see the
// comment above databaseCmd in cmd/cli/database.go).
func (p *Provider) ListBackups(ctx context.Context, req providers.DatabaseRequest) ([]providers.BackupRef, error) {
	return providers.BucketListBackups(ctx, req)
}

func (p *Provider) DownloadBackup(ctx context.Context, req providers.DatabaseRequest, id string, w io.Writer) error {
	return providers.BucketDownloadBackup(ctx, req, id, w)
}

// ── DatabaseProvider: Snapshot / Branch ──────────────────────────────
//
// Snapshot and Branch are non-destructive to the primary — they
// create sibling objects (snapshots, branch StatefulSets) without
// touching the live volume. Migrate and Rollback are deferred to a
// follow-up PR.

func (p *Provider) Snapshot(ctx context.Context, req providers.DatabaseRequest, label string) (providers.SnapshotRef, error) {
	if req.MasterSSH == nil {
		return providers.SnapshotRef{}, fmt.Errorf("postgres.Snapshot: master ssh required")
	}
	return Snapshot(ctx, req.MasterSSH, req.App, req.Env, req.Name, label)
}

func (p *Provider) ListSnapshots(ctx context.Context, req providers.DatabaseRequest) ([]providers.SnapshotRef, error) {
	if req.MasterSSH == nil {
		return nil, fmt.Errorf("postgres.ListSnapshots: master ssh required")
	}
	return ListSnapshots(ctx, req.MasterSSH, req.App, req.Env, req.Name)
}

func (p *Provider) DeleteSnapshot(ctx context.Context, req providers.DatabaseRequest, id string) error {
	if req.MasterSSH == nil {
		return fmt.Errorf("postgres.DeleteSnapshot: master ssh required")
	}
	return DeleteSnapshot(ctx, req.MasterSSH, req.Namespace, id)
}

func (p *Provider) Branch(ctx context.Context, req providers.DatabaseRequest, branchName string) (providers.BranchRef, error) {
	if req.Kube == nil || req.MasterSSH == nil {
		return providers.BranchRef{}, fmt.Errorf("postgres.Branch: kube client + master ssh required")
	}
	return Branch(ctx, req.Kube, req.MasterSSH, BranchSource{
		App: req.App, Env: req.Env,
		DBName: req.Name, Size: req.Spec.Size,
		Version: req.Spec.Version, ServerRole: req.Spec.Server,
	}, branchName)
}

func (p *Provider) ListBranches(ctx context.Context, req providers.DatabaseRequest) ([]providers.BranchRef, error) {
	if req.Kube == nil {
		return nil, fmt.Errorf("postgres.ListBranches: kube client required")
	}
	return ListBranches(ctx, req.Kube, req.App, req.Env, req.Name)
}

func (p *Provider) DeleteBranch(ctx context.Context, req providers.DatabaseRequest, branchName string) error {
	if req.Kube == nil {
		return fmt.Errorf("postgres.DeleteBranch: kube client required")
	}
	return DeleteBranch(ctx, req.Kube, req.MasterSSH, req.App, req.Env, req.Name, branchName)
}

// credentials builds the DSN + decomposed fields from the operator-
// supplied user / password / database. Host is the in-cluster
// Service DNS (FullName). Port is fixed 5432 for postgres. SSLMode
// is disable inside the cluster — TLS terminates at the cluster
// boundary, in-cluster traffic is private.
func credentials(req providers.DatabaseRequest) providers.DatabaseCredentials {
	return providers.DatabaseCredentials{
		URL:      buildDSN(req.FullName, 5432, req.Spec.User, req.Spec.Password, req.Spec.Database, "disable"),
		Host:     req.FullName,
		Port:     5432,
		User:     req.Spec.User,
		Password: req.Spec.Password,
		Database: req.Spec.Database,
		SSLMode:  "disable",
	}
}

func buildDSN(host string, port int, user, password, database, sslmode string) string {
	q := url.Values{}
	if sslmode != "" {
		q.Set("sslmode", sslmode)
	}
	u := &url.URL{
		Scheme:   "postgres",
		Host:     net.JoinHostPort(host, strconv.Itoa(port)),
		Path:     "/" + database,
		RawPath:  "/" + url.PathEscape(database),
		RawQuery: q.Encode(),
		User:     url.UserPassword(user, password),
	}
	return u.String()
}

// parseCSV converts psql --csv output (header row + N data rows)
// into a typed SQLResult. RowsAffected = number of data rows; for
// writes (INSERT / UPDATE) postgres returns "INSERT 0 1" etc. as
// stderr noise which the caller of ExecSQL discards.
func parseCSV(b []byte) (*providers.SQLResult, error) {
	r := csv.NewReader(bytes.NewReader(b))
	rows, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	res := &providers.SQLResult{}
	if len(rows) == 0 {
		return res, nil
	}
	res.Columns = rows[0]
	if len(rows) > 1 {
		res.Rows = rows[1:]
		res.RowsAffected = int64(len(rows) - 1)
	}
	return res, nil
}

// labelsFor returns the per-workload label set. Owner stamping
// (nvoi/owner=databases) happens at ApplyOwned time, NOT here, so
// the apply boundary stays the single source of truth.
func labelsFor(req providers.DatabaseRequest) map[string]string {
	out := map[string]string{}
	for k, v := range req.Labels {
		out[k] = v
	}
	return out
}

func buildService(req providers.DatabaseRequest) runtime.Object {
	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.FullName,
			Namespace: req.Namespace,
			Labels:    labelsFor(req),
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app.kubernetes.io/name": req.FullName},
			Ports: []corev1.ServicePort{{
				Name:       "postgres",
				Port:       5432,
				TargetPort: intstr.FromInt(5432),
			}},
		},
	}
}

func buildPVC(req providers.DatabaseRequest) runtime.Object {
	sc := ZFSStorageClassName
	return &corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PersistentVolumeClaim"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.PVCName,
			Namespace: req.Namespace,
			Labels:    labelsFor(req),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			// Pinned to ZFS-LocalPV. size: → refquota on the dataset,
			// enforced at the block layer by the CSI driver. Without
			// an explicit StorageClassName the cluster default
			// (k3s local-path) would silently win and size: would
			// revert to advisory.
			StorageClassName: &sc,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(fmt.Sprintf("%dGi", req.Spec.Size)),
				},
			},
		},
	}
}

func buildStatefulSet(req providers.DatabaseRequest) runtime.Object {
	replicas := int32(1)
	return &appsv1.StatefulSet{
		TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "StatefulSet"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.FullName,
			Namespace: req.Namespace,
			Labels:    labelsFor(req),
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &replicas,
			ServiceName: req.FullName,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app.kubernetes.io/name": req.FullName},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app.kubernetes.io/name": req.FullName},
				},
				Spec: corev1.PodSpec{
					NodeSelector: map[string]string{"nvoi-role": req.Spec.Server},
					Containers: []corev1.Container{{
						Name:  "postgres",
						Image: ImageFor(req.Spec.Version),
						Env: []corev1.EnvVar{
							{Name: "POSTGRES_USER", Value: req.Spec.User},
							{Name: "POSTGRES_PASSWORD", Value: req.Spec.Password},
							{Name: "POSTGRES_DB", Value: req.Spec.Database},
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
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: req.PVCName},
						},
					}},
				},
			},
		},
	}
}
