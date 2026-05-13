// Package naming centralizes every deterministic name nvoi produces.
// Same env → same names → same resources, no UUIDs. Pure string
// assembly: no env reads, no disk, no network.
package naming

import (
	"fmt"
	"path/filepath"
)

// DefaultUser is the unprivileged login every cloud-init renders.
// Matches `../nvoi/pkg/utils.DefaultUser` so SSH dispatch keeps working
// as we port more steps.
const DefaultUser = "deploy"

// CacheDirSegment is the leaf path under the operator's cache root
// (`~/.cache`) where the embedded tofu binary lands. Composed at
// the cmd/ boundary — naming never reads $HOME.
const CacheDirSegment = "nvoi"

// Prefix returns the cluster-scoped prefix shared by every resource:
// `nvoi-{app}-{env}`.
func Prefix(app, env string) string { return fmt.Sprintf("nvoi-%s-%s", app, env) }

// Namespace is the single Kubernetes namespace every nvoi-managed
// workload (services, databases, registry/app Secrets, ingresses)
// lands in. Today: `default` — same convention pkg/workload uses.
// Per-(app, env) namespacing is a future concern; until then,
// single namespace keeps the substrate simple AND keeps the
// databases reconcile path simple (no `kc.applyNamespace` dance
// before the credentials Secret can land).
const Namespace = "default"

// Server returns the provider-side server name for the given short name.
func Server(app, env, name string) string { return Prefix(app, env) + "-" + name }

// StateBucket returns the object-storage bucket holding TF remote state.
// Unused while the state backend is local; preserved as the seam to
// re-promote remote state.
func StateBucket(app, env string) string { return Prefix(app, env) + "-tfstate" }

// ObservabilityLogsBucket returns the object-storage bucket Loki ships
// chunks to. One per (app, env) — mirrors the StateBucket pattern.
// Lifecycle: created on first `nvoi deploy` with monitor: set; deleted
// only on `nvoi destroy`.
func ObservabilityLogsBucket(app, env string) string { return Prefix(app, env) + "-logs" }

// ObservabilityMetricsBucket returns the object-storage bucket Thanos
// ships Prometheus blocks to. One per (app, env). Same lifecycle as
// the logs bucket.
func ObservabilityMetricsBucket(app, env string) string { return Prefix(app, env) + "-metrics" }

// WorkDir returns the per-(app, env) tofu working directory under
// the operator's cwd: `.tf/{app}-{env}/`. Bundle files + `terraform.tfstate`
// (filename retained by OpenTofu for state-format compat) land here.
func WorkDir(app, env string) string { return filepath.Join(workDirRoot, app+"-"+env) }

// ProviderHCL returns the .tf filename a provider's emitter writes into
// the bundle: `<provider>.tf`. One file per registered provider.
func ProviderHCL(providerName string) string { return providerName + ".tf" }

const workDirRoot = ".tf"

// ── Database naming ──────────────────────────────────────────────────
//
// Single source of truth for every kubernetes-side database name. The
// reconcile step + each DatabaseProvider's EnsureCredentials + every
// CLI verb call into these helpers — never assemble names ad hoc.
//
// All names live under the cluster prefix `nvoi-{app}-{env}-db-{name}`
// so SweepOwned on the databases reconciler can never confuse them
// with non-database resources, and so `kubectl get ... | grep db-` is
// a useful operator filter.
//
// Postgres-specific bits (PVC, pod-0, snapshots, branches) ride the
// same family — non-postgres engines simply don't create those
// objects.

// Database returns the base name used for the StatefulSet (postgres)
// and the Service every engine exposes. `{app}-{env}-db-{name}`.
func Database(app, env, name string) string { return Prefix(app, env) + "-db-" + name }

// DatabasePVC returns the PVC name for selfhosted postgres. Same shape
// as the StatefulSet base — keeps `kubectl get pvc` legible (one PVC
// per DB; the StatefulSet binds it directly, not via
// VolumeClaimTemplates, so ZFS clone-PVCs can sit side-by-side).
func DatabasePVC(app, env, name string) string { return Database(app, env, name) + "-data" }

// DatabasePod returns the first (and only) replica of the StatefulSet:
// `{database}-0`. Used by ExecSQL paths that target the pod directly.
func DatabasePod(app, env, name string) string { return Database(app, env, name) + "-0" }

// DatabaseCredentials returns the Secret name holding the canonical
// connection material:
//
//	url / host / port / user / password / database / sslmode
//
// Every DatabaseProvider.EnsureCredentials writes the same key shape
// here, regardless of engine. The consuming service's container env
// vars (DATABASE_URL / DATABASE_HOST / DATABASE_PORT / DATABASE_USER /
// DATABASE_PASSWORD) wire through SecretKeyRef pointed at this Secret.
func DatabaseCredentials(app, env, name string) string {
	return Database(app, env, name) + "-credentials"
}

// DatabaseBackupCron returns the CronJob name for scheduled backups.
// One CronJob per database; the same name doubles as the basis for
// one-shot manual Jobs (`backup now` creates `{cron}-manual-<unix>`).
func DatabaseBackupCron(app, env, name string) string {
	return Database(app, env, name) + "-backup"
}

// DatabaseBackupCreds returns the Secret name holding the S3-compatible
// bucket credentials the backup CronJob / restore Job envFrom. Keys:
// BUCKET_ENDPOINT, BUCKET_NAME, AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY,
// AWS_REGION. Provisioned by the reconciler whenever cfg sets backup:.
func DatabaseBackupCreds(app, env, name string) string {
	return Database(app, env, name) + "-backup-creds"
}

// DatabaseBackupBucket returns the cloud-side object-storage bucket
// name holding gzipped logical dumps for one database. One bucket per
// database keeps lifecycle (retention) and IAM crisp: revoke one DB's
// backups by deleting one bucket.
func DatabaseBackupBucket(app, env, name string) string {
	return Prefix(app, env) + "-db-" + name + "-backups"
}

// DatabaseSnapshot returns the VolumeSnapshot name for an operator-
// triggered snapshot (postgres only). `{database}-snap-{label}`.
// Label is validated DNS-1123 by the caller.
func DatabaseSnapshot(app, env, name, label string) string {
	return Database(app, env, name) + "-snap-" + label
}

// DatabaseBranch returns the per-branch workload name (StatefulSet,
// Service, PVC for postgres — Service-DNS apps connect to). The
// composite must fit DNS-1123 (63-char Service name cap); callers
// (postgres.Branch) check this explicitly.
//
// `{database}-br-{branch}`.
func DatabaseBranch(app, env, name, branch string) string {
	return Database(app, env, name) + "-br-" + branch
}

// DatabaseBranchSnapshot returns the VolumeSnapshot name a branch's
// PVC is dataSourced from. Encoded with `snap-br-` so list/delete by
// prefix can trace a branch back to its seed.
//
// `{database}-snap-br-{branch}`.
func DatabaseBranchSnapshot(app, env, name, branch string) string {
	return Database(app, env, name) + "-snap-br-" + branch
}
