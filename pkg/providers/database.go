// Database provider contract.
//
// One flat interface, postgres-shaped. Every registered engine
// implements every method; engines whose backend doesn't natively
// support an operation return ErrUnsupported and the CLI surfaces a
// clean operator-facing message ("engine X doesn't support Y").
//
// Postgres is the reference implementation — full coverage via
// OpenEBS ZFS-LocalPV. Other engines (planetscale, turso, rds-postgres,
// neon) live as sibling packages under pkg/providers/<engine>/ with
// the same two-layer shape:
//
//   - Layer A: HCL emitter (compile.RegisterInfra) — provisioning.
//   - Layer B: Runtime adapter (providers.RegisterDatabase) — ops.
//
// We never shell out to vendor CLIs. Runtime ops go through Go HTTP/SDK
// clients owned in-package by us — pscale/turso/aws CLIs are not in
// our trust boundary.
//
// Secrets normalization (NON-NEGOTIABLE): every provider's
// EnsureCredentials writes the same key shape into the credentials
// Secret named by naming.DatabaseCredentials — url / host / port /
// user / password / database / sslmode. Consuming services receive the
// canonical five env vars (DATABASE_URL/HOST/PORT/USER/PASSWORD) via
// SecretKeyRef pointed at those keys. A provider that can't fill all
// five mandatory keys doesn't ship.
package providers

import (
	"context"
	"errors"
	"fmt"
	"io"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/getnvoi/core/pkg/log"
	"github.com/getnvoi/core/pkg/ssh"
)

// ErrUnsupported is returned by DatabaseProvider methods whose backend
// doesn't natively offer the operation. Sentinel — call sites do
// `errors.Is(err, ErrUnsupported)` and surface a clean engine-specific
// message to the operator. NOT a silent no-op: every Unsupported path
// has a hand-written CLI-side message telling the operator what to do
// instead (use snapshot, use the vendor's UI, etc.).
var ErrUnsupported = errors.New("operation not supported by this engine")

// DatabaseProvider is the per-engine contract. One interface, shaped
// by what postgres (the reference implementation) can do; other engines
// cover what their vendor API natively supports and return
// ErrUnsupported for the rest.
type DatabaseProvider interface {
	// ── Lifecycle ────────────────────────────────────────────────
	// ValidateCredentials checks that the operator-supplied creds
	// (vendor API tokens, etc.) actually reach the vendor. Called
	// once per CLI invocation before any other method.
	ValidateCredentials(ctx context.Context) error

	// EnsureCredentials provisions / fetches the DB's connection
	// material and writes it to req.CredentialsSecretName with the
	// canonical key shape: url, host, port, user, password, database,
	// sslmode. Idempotent — re-reads existing Secret first when the
	// vendor's API permits it. Returns the resolved credentials so the
	// reconciler can populate DATABASE_URL_<NAME> in services' env.
	//
	// The kc argument is *kube.Client; passed as `any` here to break
	// an import cycle (pkg/config imports pkg/providers for AlertSpec
	// while pkg/internal/kube transitively reaches pkg/config via
	// compile→runtime). Engine implementations cast to *kube.Client.
	EnsureCredentials(ctx context.Context, kc any, req DatabaseRequest) (DatabaseCredentials, error)

	// Reconcile returns the kube workloads this engine needs in the
	// cluster. Postgres: StorageClass + PVC + Service + StatefulSet +
	// (when backup is configured) backup CronJob. SaaS engines:
	// (when backup is configured) backup CronJob only — the data
	// lives at the vendor, no in-cluster workload.
	Reconcile(ctx context.Context, req DatabaseRequest) (*DatabasePlan, error)

	// Delete tears down vendor-side resources. For postgres this is a
	// no-op (cluster teardown reaps the namespace). For SaaS engines
	// it calls the vendor API to delete the project/database. Called
	// only by `nvoi destroy` paths.
	Delete(ctx context.Context, req DatabaseRequest) error

	// ── Universal ops — every engine implements with real work ───
	ExecSQL(ctx context.Context, req DatabaseRequest, stmt string) (*SQLResult, error)
	BackupNow(ctx context.Context, req DatabaseRequest) (*BackupRef, error)
	ListBackups(ctx context.Context, req DatabaseRequest) ([]BackupRef, error)
	DownloadBackup(ctx context.Context, req DatabaseRequest, id string, w io.Writer) error
	Restore(ctx context.Context, req DatabaseRequest, backupKey string) error

	// ── Snapshot / Branch / Migrate / Rollback — best-effort ─────
	// Each method does real work where the vendor API supports it
	// and returns ErrUnsupported otherwise. Hand-written CLI-side
	// messages tell the operator what to do instead.
	Snapshot(ctx context.Context, req DatabaseRequest, label string) (SnapshotRef, error)
	ListSnapshots(ctx context.Context, req DatabaseRequest) ([]SnapshotRef, error)
	DeleteSnapshot(ctx context.Context, req DatabaseRequest, id string) error

	Branch(ctx context.Context, req DatabaseRequest, branchName string) (BranchRef, error)
	ListBranches(ctx context.Context, req DatabaseRequest) ([]BranchRef, error)
	DeleteBranch(ctx context.Context, req DatabaseRequest, branchName string) error

	Migrate(ctx context.Context, req DatabaseRequest) error
	Rollback(ctx context.Context, req DatabaseRequest, snapshotID string) error

	Close() error
}

// DatabaseRequest is the per-call bundle every DatabaseProvider method
// receives. Names + identity come from naming.Database* helpers;
// Spec is the YAML-derived shape; Bucket / Kube / NodeSSH / Log are
// optional handles populated per call site.
type DatabaseRequest struct {
	// Identity (all from pkg/naming)
	Name                  string // YAML key — e.g. "app"
	FullName              string // naming.Database(app, env, name)
	Namespace             string
	PodName               string // naming.DatabasePod — postgres only
	PVCName               string // naming.DatabasePVC — postgres only
	BackupName            string // naming.DatabaseBackupCron
	CredentialsSecretName string // naming.DatabaseCredentials
	BackupCredsSecretName string // naming.DatabaseBackupCreds — when backup is set

	// Build-time pin for the uniform backup/restore image. Resolved
	// once per deploy by ResolveDBImage and threaded into every Job /
	// CronJob the provider emits. Empty falls back to `:latest` for
	// tests that don't exercise a live registry.
	DBImageRef string

	// Spec is the YAML-derived shape — engine-agnostic struct, each
	// provider reads the keys it cares about.
	Spec   DatabaseSpec
	Labels map[string]string

	// Bucket is populated when backup is configured. Carries the
	// S3-compatible credentials the backup image uses for sigv4
	// PUT/GET. Provisioned implicitly by the reconciler on the
	// providers.storage backend.
	Bucket *BucketHandle

	// Kube is the master-tunneled clientset (`*kube.Client` at
	// runtime). Required for cluster-side operations (ExecSQL via
	// pod-exec, Secret writes, Job submission for backup/restore).
	//
	// Declared as `any` to break an import cycle (pkg/config imports
	// pkg/providers for AlertSpec while pkg/internal/kube transitively
	// reaches pkg/config via compile→runtime). Engine implementations
	// type-assert to *kube.Client at the top of every method that
	// needs it.
	Kube any

	// NodeSSH is the per-node shell — postgres uses it for the ZFS
	// prepare-node phase (apt-install zfsutils, zpool create). nil
	// for SaaS engines and for callers that don't need host-level
	// access (tests, list/download ops).
	NodeSSH ssh.Shell

	// Log is the kind-scoped logger. Provider methods emit
	// info/success/warning events through it; the deploy pipeline
	// merges them into the canonical JSONL stream.
	Log log.Log
}

// DatabaseSpec is the YAML-derived shape — flat struct, engine reads
// what it cares about. Validator gates which fields are valid for
// which engine.
type DatabaseSpec struct {
	Engine   string              // postgres | planetscale | turso | rds-postgres | neon | …
	Version  string              // postgres: e.g. "17"
	Server   string              // postgres only — DB-node YAML key
	Size     int                 // postgres only — GiB, ZFS quota
	User     string              // postgres only — resolved $VAR
	Password string              // postgres only — resolved $VAR
	Database string              // postgres only — resolved $VAR
	Region   string              // SaaS only — vendor-specific region id
	Backup   *DatabaseBackupSpec // any engine — needs providers.storage
}

// DatabaseBackupSpec is the reconciled backup policy for one database.
// The bucket itself is provisioned implicitly by the reconciler — the
// spec carries only schedule / retention; the provider's Reconcile
// decides whether to emit a CronJob and what schedule / retention to
// set on the bucket lifecycle policy.
type DatabaseBackupSpec struct {
	Schedule  string // cron expression (e.g. "0 3 * * *")
	Retention int    // days; passed to BucketProvider.SetLifecycle
}

// DatabaseCredentials is what EnsureCredentials returns and what the
// Secret carries. All seven fields populated; the consuming service's
// pod gets the first five as DATABASE_URL/HOST/PORT/USER/PASSWORD.
type DatabaseCredentials struct {
	URL      string
	Host     string
	Port     int
	User     string
	Password string
	Database string
	SSLMode  string
}

// DatabasePlan is the set of cluster workloads Reconcile produces.
// Applied by the reconcile step through kube.Client.ApplyOwned with
// scope.Owner = kube.OwnerDatabases.
type DatabasePlan struct {
	Workloads []runtime.Object
}

// BucketHandle bundles the per-database backup bucket name + creds
// that the backup CronJob / restore Job envFroms (via the
// BackupCredsSecretName Secret).
type BucketHandle struct {
	Name        string
	Credentials BucketCredentials
}

// SQLResult is the result-set shape every ExecSQL returns. Columns +
// Rows are nil-or-empty when the statement was a write (RowsAffected
// is the relevant field then).
type SQLResult struct {
	Columns      []string
	Rows         [][]string
	RowsAffected int64
}

// BackupRef identifies one bucket-resident backup artifact. ID is the
// S3 object key (deterministic: `YYYYMMDDTHHMMSSZ.sql.gz`); CreatedAt
// is RFC3339; Kind is always "dump" today (uniform pipeline = uniform
// artifact shape).
type BackupRef struct {
	ID        string
	CreatedAt string
	SizeBytes int64
	Kind      string
}

// SnapshotRef identifies one engine-side snapshot. For postgres,
// Name is the VolumeSnapshot object name. For SaaS engines that
// support snapshots, Name is the vendor's snapshot id.
type SnapshotRef struct {
	Name      string
	CreatedAt string
	Kind      string // "zfs" | "rds" | …
}

// BranchRef identifies one engine-side branch. Endpoint is the
// connection string consumers use (in-cluster Service DNS for
// postgres; vendor host for SaaS). Snapshot names the source the
// branch was forked from when applicable.
type BranchRef struct {
	Name     string
	Endpoint string
	Snapshot string
}

// ── Registry ─────────────────────────────────────────────────────────
//
// Mirror of bucketProviders in registry.go. Each engine's register.go
// calls RegisterDatabase via init() with its credential schema and
// factory.

// DatabaseFactory builds a DatabaseProvider from resolved credentials.
type DatabaseFactory func(creds map[string]string) DatabaseProvider

type databaseReg struct {
	schema  CredentialSchema
	factory DatabaseFactory
}

var databaseProviders = map[string]databaseReg{}

// RegisterDatabase is called from an engine package's init().
func RegisterDatabase(name string, schema CredentialSchema, factory DatabaseFactory) {
	if _, dup := databaseProviders[name]; dup {
		panic(fmt.Sprintf("providers: duplicate database engine %q (programming error)", name))
	}
	databaseProviders[name] = databaseReg{schema: schema, factory: factory}
}

// CredentialSchemaForDatabase returns the env-resolution schema for
// the named engine so the cmd/ boundary can pre-resolve credentials
// before calling the factory.
func CredentialSchemaForDatabase(name string) (CredentialSchema, error) {
	reg, ok := databaseProviders[name]
	if !ok {
		return CredentialSchema{}, fmt.Errorf("unknown database engine %q", name)
	}
	return reg.schema, nil
}

// ResolveDatabase returns a DatabaseProvider for the named engine
// with explicit resolved credentials.
func ResolveDatabase(name string, creds map[string]string) (DatabaseProvider, error) {
	reg, ok := databaseProviders[name]
	if !ok {
		return nil, fmt.Errorf("unknown database engine %q", name)
	}
	return reg.factory(creds), nil
}

// IsRegisteredDatabase reports whether `name` has been registered as a
// database engine — used by the validator to reject unknown values of
// `databases.X.engine` before any infra mutation.
func IsRegisteredDatabase(name string) bool {
	_, ok := databaseProviders[name]
	return ok
}

// RegisteredDatabaseEngines returns the sorted list of registered
// engine names. Used by validator error messages so the operator
// sees the full set of supported engines when they typo one.
func RegisteredDatabaseEngines() []string {
	out := make([]string, 0, len(databaseProviders))
	for name := range databaseProviders {
		out = append(out, name)
	}
	// stable order for error messages
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
