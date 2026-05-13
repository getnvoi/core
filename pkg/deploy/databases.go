package deploy

import (
	"context"
	"fmt"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/internal/kube"
	"github.com/getnvoi/core/pkg/internal/utils"
	"github.com/getnvoi/core/pkg/naming"
	"github.com/getnvoi/core/pkg/providers"
	"github.com/getnvoi/core/pkg/providers/postgres"
	"github.com/getnvoi/core/pkg/ssh"
	"github.com/getnvoi/core/pkg/utils/s3"
)

// deployDatabases is the reconcile step for the databases: block.
// Runs inside deployWorkloads after node labels are stamped but
// BEFORE workload services apply — services consume DATABASE_*
// SecretKeyRef bindings that point at the credentials Secret this
// step writes.
//
// Phases per database:
//
//  1. (postgres only, once globally) Install OpenEBS ZFS-LocalPV CSI
//     driver via master kubectl. Idempotent.
//  2. (postgres only) Prepare the DB's node — apt-install zfsutils,
//     create file-backed zpool. Per-node SSH.
//  3. Resolve engine credentials from os-resolved $VAR map.
//  4. Resolve engine provider via providers.ResolveDatabase.
//  5. ValidateCredentials → EnsureCredentials (stamps canonical
//     Secret with url/host/port/user/password/database/sslmode).
//  6. When backup is configured: ensure the per-DB bucket on
//     providers.storage, apply retention lifecycle, stamp the
//     backup-creds Secret (BUCKET_* + AWS_*).
//  7. Reconcile (returns the per-engine cluster workloads).
//  8. ApplyOwned each workload under OwnerDatabases.
//
// At the end: SweepOwned removes nvoi-managed database resources
// no longer in YAML (parent DB only — branches survive their
// parent's reconcile by riding OwnerDatabaseBranches).
func (s *Session) deployDatabases(ctx context.Context) error {
	rt := s.Rt
	if len(rt.Cfg.Databases) == 0 {
		// Still sweep — a previously declared DB removed from YAML
		// must be reaped from the cluster on this deploy.
		return s.sweepDatabases(ctx, nil)
	}

	kc := s.kc
	if kc == nil {
		return fmt.Errorf("deployDatabases: kube client not initialized (deployWorkloads opens it before calling here)")
	}

	// Master shell — needed for CSI install + VolumeSnapshot kubectl
	// applies during branch/snapshot ops. Branch/snapshot are CLI-
	// only (not deploy-time), but the same shell threads through.
	primaryName := rt.Cfg.PrimaryMaster()
	masterShell, ok := s.shells[primaryName]
	if !ok {
		return fmt.Errorf("deployDatabases: primary master %q has no open shell", primaryName)
	}

	// 1. CSI install — once, when any selfhosted (postgres) DB
	//    declared. Skipped on SaaS-only configs (which don't ship
	//    in v1 but the gate is here regardless).
	if hasSelfhostedDB(rt.Cfg) {
		s.Lg.Step("zfs-csi")
		if err := postgres.EnsureZFSCSI(ctx, masterShell); err != nil {
			return fmt.Errorf("install zfs-localpv csi: %w", err)
		}
	}

	// 2-8. Per-database reconcile, sorted for determinism.
	for _, name := range utils.SortedKeys(rt.Cfg.Databases) {
		def := rt.Cfg.Databases[name]
		s.Lg.Step("database-" + name)
		if err := s.reconcileOneDatabase(ctx, name, def, masterShell); err != nil {
			return fmt.Errorf("databases.%s: %w", name, err)
		}
	}

	// 9. Sweep orphans.
	desired := make([]string, 0, len(rt.Cfg.Databases))
	for k := range rt.Cfg.Databases {
		desired = append(desired, k)
	}
	return s.sweepDatabases(ctx, desired)
}

// reconcileOneDatabase runs the per-DB pipeline. Extracted so the
// (future) CLI verb `nvoi database migrate` can reuse the apply path
// without duplicating the bucket / credentials / Reconcile dance.
func (s *Session) reconcileOneDatabase(ctx context.Context, name string, def config.DatabaseSpec, masterShell ssh.Shell) error {
	rt := s.Rt
	kc := s.kc
	namespace := naming.Prefix(rt.Cfg.App, rt.Cfg.Env)

	// Resolve engine provider via registry (postgres + future
	// engines blank-import in cmd/cli/main.go).
	prov, err := providers.ResolveDatabase(def.Engine, nil)
	if err != nil {
		return fmt.Errorf("resolve engine: %w", err)
	}

	// Build canonical DatabaseRequest. Spec.User/Password/Database
	// resolve $VAR via runtime.SecretValues + extra env reads.
	req := providers.DatabaseRequest{
		App:                   rt.Cfg.App,
		Env:                   rt.Cfg.Env,
		Name:                  name,
		FullName:              naming.Database(rt.Cfg.App, rt.Cfg.Env, name),
		Namespace:             namespace,
		PodName:               naming.DatabasePod(rt.Cfg.App, rt.Cfg.Env, name),
		PVCName:               naming.DatabasePVC(rt.Cfg.App, rt.Cfg.Env, name),
		BackupName:            naming.DatabaseBackupCron(rt.Cfg.App, rt.Cfg.Env, name),
		CredentialsSecretName: naming.DatabaseCredentials(rt.Cfg.App, rt.Cfg.Env, name),
		Spec: providers.DatabaseSpec{
			Engine:  def.Engine,
			Version: def.Version,
			Server:  def.Server,
			Size:    def.Size,
			Region:  def.Region,
		},
		Kube:      kc,
		MasterSSH: masterShell,
		Log:       s.Lg,
	}

	// Credentials resolution (selfhosted only — SaaS engines own
	// their creds at the vendor API level). Validator has already
	// confirmed $VAR refs resolve at the boundary; here we just
	// look them up.
	if def.Credentials != nil {
		req.Spec.User = resolveVar(def.Credentials.User, rt.SecretValues)
		req.Spec.Password = resolveVar(def.Credentials.Password, rt.SecretValues)
		req.Spec.Database = resolveVar(def.Credentials.Database, rt.SecretValues)
	}
	if def.Backup != nil {
		req.Spec.Backup = &providers.DatabaseBackupSpec{
			Schedule:  def.Backup.Schedule,
			Retention: def.Backup.Retention,
		}
	}

	// Dial per-node SSH for selfhosted engines (postgres needs it
	// for ZFS prepare-node). Validator guarantees def.Server names
	// a declared worker for selfhosted engines.
	if def.Server != "" {
		if shell, ok := s.shells[def.Server]; ok {
			req.NodeSSH = shell
		}
	}

	// Backup bucket — provisioned implicitly when backup is set.
	// providers.storage is validated non-empty by the validator
	// whenever any DB carries `backup:`.
	if def.Backup != nil {
		bucketName, bucketCreds, err := s.ensureBackupBucket(ctx, name, def)
		if err != nil {
			return fmt.Errorf("ensure backup bucket: %w", err)
		}
		req.Bucket = &providers.BucketHandle{Name: bucketName, Credentials: bucketCreds}
		req.BackupCredsSecretName = naming.DatabaseBackupCreds(rt.Cfg.App, rt.Cfg.Env, name)
		// Stamp the backup-creds Secret the CronJob/restore Job
		// envFroms. Idempotent — EnsureSecret merges into existing.
		if err := kc.EnsureSecret(ctx, namespace, kube.OwnerDatabases, req.BackupCredsSecretName,
			providers.BuildBackupCredsSecretData(bucketName, bucketCreds)); err != nil {
			return fmt.Errorf("stamp backup-creds secret: %w", err)
		}
	}

	// Pin the cmd/db image once per deploy — caller-thread idempotent.
	if s.dbImageRef == "" {
		ref, err := providers.ResolveDBImage(ctx)
		if err != nil {
			// Fall back to :latest tag for tests/dev that don't
			// reach the registry. Production builds inject the
			// pinned digest via ldflags so this branch only
			// happens locally.
			ref = providers.DBImage()
			s.Lg.Warn(fmt.Sprintf("resolve db image: %v — falling back to %s", err, ref))
		}
		s.dbImageRef = ref
	}
	req.DBImageRef = s.dbImageRef

	if err := prov.ValidateCredentials(ctx); err != nil {
		return fmt.Errorf("validate credentials: %w", err)
	}
	if _, err := prov.EnsureCredentials(ctx, kc, req); err != nil {
		return fmt.Errorf("ensure credentials secret: %w", err)
	}

	plan, err := prov.Reconcile(ctx, req)
	if err != nil {
		return fmt.Errorf("reconcile: %w", err)
	}
	scope := kube.Scope{Namespace: namespace, Owner: kube.OwnerDatabases}
	for _, obj := range plan.Workloads {
		if err := kc.ApplyOwned(ctx, scope, obj); err != nil {
			return fmt.Errorf("apply %T: %w", obj, err)
		}
	}
	return nil
}

// ensureBackupBucket provisions the per-DB backup bucket on
// providers.storage and applies the retention lifecycle. Returns
// the bucket name + S3-compatible credentials the caller needs to
// stamp into the backup-creds Secret.
//
// Bucket name is deterministic from naming.DatabaseBackupBucket;
// EnsureBucket is idempotent so re-deploy is a no-op against an
// existing bucket.
func (s *Session) ensureBackupBucket(ctx context.Context, dbName string, def config.DatabaseSpec) (string, providers.BucketCredentials, error) {
	rt := s.Rt
	if rt.StorageBucket == nil {
		return "", providers.BucketCredentials{}, fmt.Errorf("providers.storage is not configured (validator should have caught this — sanity check)")
	}
	bucketName := naming.DatabaseBackupBucket(rt.Cfg.App, rt.Cfg.Env, dbName)
	if err := rt.StorageBucket.EnsureBucket(ctx, bucketName); err != nil {
		return "", providers.BucketCredentials{}, fmt.Errorf("ensure bucket %s: %w", bucketName, err)
	}
	creds, err := rt.StorageBucket.Credentials(ctx)
	if err != nil {
		return "", providers.BucketCredentials{}, fmt.Errorf("bucket credentials: %w", err)
	}
	// Retention lifecycle goes through the standalone S3 client —
	// the BucketProvider interface doesn't expose lifecycle (v0
	// design), and SetLifecycle is a vendor-agnostic XML PUT.
	if def.Backup.Retention > 0 {
		if err := s3.SetLifecycle(creds.Endpoint, creds.AccessKeyID, creds.SecretAccessKey, creds.Region, bucketName, def.Backup.Retention); err != nil {
			return "", providers.BucketCredentials{}, fmt.Errorf("set retention on %s: %w", bucketName, err)
		}
	}
	return bucketName, creds, nil
}

// sweepDatabases deletes nvoi-owned database resources whose YAML
// keys are no longer in `desired`. Scoped to OwnerDatabases —
// branches (OwnerDatabaseBranches) survive parent-DB reconciles.
func (s *Session) sweepDatabases(ctx context.Context, desiredKeys []string) error {
	kc := s.kc
	if kc == nil {
		return nil
	}
	rt := s.Rt
	namespace := naming.Prefix(rt.Cfg.App, rt.Cfg.Env)
	scope := kube.Scope{Namespace: namespace, Owner: kube.OwnerDatabases}

	// Build the expected-name sets per Kind.
	var (
		statefulSets, services, pvcs, secrets, cronJobs []string
	)
	for _, k := range desiredKeys {
		def := rt.Cfg.Databases[k]
		secrets = append(secrets, naming.DatabaseCredentials(rt.Cfg.App, rt.Cfg.Env, k))
		if def.Server != "" {
			// Selfhosted — workloads land in-cluster.
			full := naming.Database(rt.Cfg.App, rt.Cfg.Env, k)
			statefulSets = append(statefulSets, full)
			services = append(services, full)
			pvcs = append(pvcs, naming.DatabasePVC(rt.Cfg.App, rt.Cfg.Env, k))
		}
		if def.Backup != nil {
			cronJobs = append(cronJobs, naming.DatabaseBackupCron(rt.Cfg.App, rt.Cfg.Env, k))
			secrets = append(secrets, naming.DatabaseBackupCreds(rt.Cfg.App, rt.Cfg.Env, k))
		}
	}

	for _, sweep := range []struct {
		kind    kube.Kind
		desired []string
	}{
		{kube.KindStatefulSet, statefulSets},
		{kube.KindService, services},
		{kube.KindPVC, pvcs},
		{kube.KindSecret, secrets},
		{kube.KindCronJob, cronJobs},
	} {
		if err := kc.SweepOwned(ctx, scope, sweep.kind, sweep.desired); err != nil {
			s.Lg.Warn(fmt.Sprintf("databases sweep %s: %s", sweep.kind, err))
		}
	}
	return nil
}

// hasSelfhostedDB reports whether any database in cfg uses a
// selfhosted engine. Only postgres today; the function exists so
// adding selfhosted MySQL later is a one-line case.
func hasSelfhostedDB(cfg *config.Config) bool {
	for _, db := range cfg.Databases {
		if db.Engine == "postgres" {
			return true
		}
	}
	return false
}

// resolveVar replaces a single $VAR reference with its resolved
// value from the source map. Literals pass through unchanged. The
// validator has already confirmed every $VAR resolves at the
// boundary, so missing-key here is a deploy-time programming error
// (returns empty string — engine credentials Secret apply will
// fail loudly downstream).
func resolveVar(raw string, source map[string]string) string {
	if len(raw) == 0 || raw[0] != '$' {
		return raw
	}
	if v, ok := source[raw[1:]]; ok {
		return v
	}
	return ""
}
