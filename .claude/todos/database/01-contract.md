# Database — Normalized Provider Contract

One flat interface, postgres-shaped. Five provider packages today
(`postgres`, `planetscale`, `turso`, `rds-postgres`, `neon`). Each
implements as much of the interface as the backend supports natively;
the rest returns `ErrUnsupported`. The CLI dispatches uniformly; gaps
surface to operators as explicit messages, not as silent no-ops.

## The DatabaseProvider interface

Defined in `pkg/providers/database.go`. Mirror of
`pkg/providers/bucket.go::BucketProvider`. Postgres dictates the
shape — the interface includes everything a fully-managed postgres
substrate can do.

```go
// pkg/providers/database.go

// ErrUnsupported is returned by provider methods whose backend doesn't
// natively offer the operation. Sentinel — callers errors.Is(err,
// ErrUnsupported) and surface a clear "engine X doesn't support Y"
// message. NOT a silent no-op.
var ErrUnsupported = errors.New("operation not supported by this engine")

type DatabaseProvider interface {
    // ── Lifecycle ─────────────────────────────────────────────────
    ValidateCredentials(ctx context.Context) error
    EnsureCredentials(ctx context.Context, kc *kube.Client, req DatabaseRequest) (DatabaseCredentials, error)
    Reconcile(ctx context.Context, req DatabaseRequest) (*DatabasePlan, error)
    Delete(ctx context.Context, req DatabaseRequest) error

    // ── Universal ops (every engine implements; postgres is the model) ──
    ExecSQL(ctx context.Context, req DatabaseRequest, stmt string) (*SQLResult, error)
    BackupNow(ctx context.Context, req DatabaseRequest) (*BackupRef, error)
    ListBackups(ctx context.Context, req DatabaseRequest) ([]BackupRef, error)
    DownloadBackup(ctx context.Context, req DatabaseRequest, id string, w io.Writer) error
    Restore(ctx context.Context, req DatabaseRequest, backupKey string) error

    // ── Snapshot/Branch/Migrate/Rollback (best-effort per engine) ──
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
```

**Postgres** implements every method with real work (ZFS-LocalPV does
all of it).

**Every other provider** returns `ErrUnsupported` for methods their
backend can't natively do — and real work for those it can:

```go
// pkg/providers/turso/runtime.go (abridged)
func (p *Provider) Snapshot(ctx context.Context, req DatabaseRequest, label string) (SnapshotRef, error) {
    return SnapshotRef{}, providers.ErrUnsupported  // Turso has no addressable snapshots
}

func (p *Provider) Branch(ctx context.Context, req DatabaseRequest, branchName string) (BranchRef, error) {
    // Real work: call Turso API to create a child database from the parent.
    payload := map[string]any{
        "name":         names.TursoBranch(req.Name, branchName),
        "from_db":      req.FullName,
        "from_db_type": "database",
    }
    ...
}
```

## Why flat (not capability sub-interfaces)

Earlier draft had `Snapshotter`, `Brancher`, `Migrator`, `Rollbacker`
as sub-interfaces; the CLI dispatched via type assertion. Dropped
because:

1. **`ErrUnsupported` reads cleaner at the CLI.** `prov.Branch(...)`
   always; check the error. One dispatch pattern across every verb.
2. **No type assertion ceremony.** Every verb implementation is the
   same shape: call the method, handle the error (translating
   `ErrUnsupported` into the operator-facing message).
3. **Adding a new method is one change.** Add `Replicate` to the
   interface; every provider compiles to it (Go interface satisfaction
   forces them to implement the stub returning `ErrUnsupported`).
   Capability interfaces would have made adding a new capability a
   coordination job across N packages.
4. **The interface IS the contract.** What postgres can do is what
   nvoi promises operators they can attempt against any engine.
   Other engines say "no" with `ErrUnsupported`; postgres says "yes"
   by doing the work.

## Secrets normalization (NON-NEGOTIABLE)

The single most important contract on top of the interface. Every
provider's `EnsureCredentials` produces a credentials Secret with the
**same key shape**, regardless of engine. Every service that
references a database gets the **same five canonical env vars**,
regardless of engine. Apps stay identical when an operator switches
postgres → neon → rds → planetscale; the substrate hides the engine.

### Canonical env vars in the consuming service's pod

```
DATABASE_URL        Fully-formed DSN:
                    postgres://user:pw@host:5432/db?sslmode=require   (postgres, neon, rds)
                    mysql://user:pw@host:3306/db?ssl-mode=REQUIRED    (planetscale)
                    libsql://db.turso.io?authToken=…                  (turso)
DATABASE_HOST       hostname / Service DNS
DATABASE_PORT       port (string — k8s env vars are always strings)
DATABASE_USER       SQL user
DATABASE_PASSWORD   SQL password
```

These five names are **the contract**. Apps depend on them.

### Credentials Secret shape (Kubernetes-side)

Every `DatabaseProvider.EnsureCredentials` writes this exact key shape
into the named Secret. Lowercase keys (Go-side reads use
`kc.GetSecretValue(ns, name, "url")`); env vars are wired through
SecretKeyRef which preserves the requested env-var name.

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: nvoi-{app}-{env}-db-{name}-credentials      # owned by pkg/naming
type: Opaque
stringData:
  url:      <fully-formed DSN>           # mandatory
  host:     <host>                       # mandatory
  port:     <port>                       # mandatory (stringified)
  user:     <user>                       # mandatory
  password: <pw>                         # mandatory
  database: <dbname>                     # internal — used by cmd/db image
  sslmode:  <sslmode|"">                 # internal — used by cmd/db image
```

**Provider contract: a provider that can't fill all five mandatory
keys does not ship.** This is what guarantees the operator contract
holds across every engine. A vendor whose API doesn't return the host
(unlikely, but) makes the engine unimplementable, not the contract
optional.

### Env wiring on the consuming Deployment

The workload builder generates SecretKeyRef entries — NOT envFrom.
envFrom doesn't uppercase the Secret's lowercase keys, so the lowercase
`url/host/port/user/password` keys would produce lowercase env vars and
apps would silently fail. Explicit SecretKeyRef per env var keeps the
canonical names intact.

```yaml
env:
  - name: DATABASE_URL
    valueFrom: { secretKeyRef: { name: nvoi-{app}-{env}-db-{name}-credentials, key: url } }
  - name: DATABASE_HOST
    valueFrom: { secretKeyRef: { name: …, key: host } }
  - name: DATABASE_PORT
    valueFrom: { secretKeyRef: { name: …, key: port } }
  - name: DATABASE_USER
    valueFrom: { secretKeyRef: { name: …, key: user } }
  - name: DATABASE_PASSWORD
    valueFrom: { secretKeyRef: { name: …, key: password } }
```

### Multi-database services — prefix-based aliasing

When a service consumes multiple databases, the operator chooses
distinct prefixes. Each prefix expands to the full 5-var bundle.

```yaml
services:
  api:
    databases:
      - DATABASE=app          # → DATABASE_URL / DATABASE_HOST / DATABASE_PORT / DATABASE_USER / DATABASE_PASSWORD
      - REPORTS=analytics     # → REPORTS_URL  / REPORTS_HOST  / REPORTS_PORT  / REPORTS_USER  / REPORTS_PASSWORD
      - EDGE=cache            # → EDGE_URL     / EDGE_HOST     / EDGE_PORT     / EDGE_USER     / EDGE_PASSWORD
```

Default prefix is `DATABASE` when the operator writes only the
database name (`databases: [app]`) — a service with exactly one DB
gets the canonical names with zero ceremony.

Validator rejects:
- Duplicate prefixes within one service.
- A prefix referencing a database name that doesn't exist in `databases:`.

### Where the names live

`pkg/naming/naming.go` owns every Kubernetes name in this subsystem
(single source of truth — same pattern as `naming.Server`,
`naming.StateBucket` today):

```go
func (n *Names) KubeDatabase(name string) string           // StatefulSet, Service, base name
func (n *Names) KubeDatabasePVC(name string) string        // PVC for postgres
func (n *Names) KubeDatabasePod(name string) string        // first replica (StatefulSet pod 0)
func (n *Names) KubeDatabaseCredentials(name string) string // the Secret described above
func (n *Names) KubeDatabaseBackupCron(name string) string  // the backup CronJob
func (n *Names) KubeDatabaseBackupCreds(name string) string // bucket-creds Secret
func (n *Names) KubeDatabaseBackupBucket(name string) string // S3 bucket name
func (n *Names) KubeDatabaseSnapshot(dbName, label string) string         // VolumeSnapshot (postgres)
func (n *Names) KubeDatabaseBranch(dbName, branchName string) string      // branch workloads (postgres)
func (n *Names) KubeDatabaseBranchSnapshot(dbName, branchName string) string // snapshot for a branch
```

All composite-name length checks (63-char DNS-1123 cap on Service
names; 253-char cap on PVC names) happen here, not at the call site.

## DatabaseRequest — the shared bundle

```go
type DatabaseRequest struct {
    // Identity
    Name                  string  // YAML key, e.g. "app"
    FullName              string  // nvoi-{app}-{env}-db-{name}
    Namespace             string
    PodName               string  // first replica of the StatefulSet (postgres only)
    PVCName               string  // postgres only
    BackupName            string  // CronJob name
    CredentialsSecretName string  // url/host/port/user/password/database/sslmode
    BackupCredsSecretName string  // BUCKET_*, AWS_*

    // Build-time pin
    DBImageRef            string  // digest-pinned `docker.io/nvoi/db@sha256:…`

    // Spec (engine-agnostic shape; each engine reads the keys it cares about)
    Spec                  DatabaseSpec
    Labels                map[string]string

    // Optional handles — populated per call site
    Bucket                *BucketHandle  // when backup is configured
    Kube                  *kube.Client   // master kube tunnel
    NodeSSH               ssh.Shell      // postgres only (ZFS prepare-node)
    Log                   log.Sub        // kind-scoped logger
}
```

One struct. Adding a field never breaks the interface.

## Per-engine credential schemas

Each provider's `register.go` declares the env vars the cmd/cli
boundary resolves before calling the factory. Same shape as bucket
provider registration.

| Engine          | Required env vars                                    |
|-----------------|------------------------------------------------------|
| `postgres`      | (none — managed in-cluster; creds in YAML)           |
| `planetscale`   | `PLANETSCALE_SERVICE_TOKEN`, `PLANETSCALE_ORG`       |
| `turso`         | `TURSO_API_TOKEN`, `TURSO_ORG`                       |
| `rds-postgres`  | `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_REGION` |
| `neon`          | `NEON_API_KEY` (+ `NEON_BASE_URL` optional)          |

```go
// Example: pkg/providers/turso/register.go
func init() {
    // Provisioning side — HCL emitter.
    compile.RegisterInfra("turso", &Emitter{})

    // Runtime side — Go API adapter.
    providers.RegisterDatabase("turso", CredentialSchema{
        Name: "turso",
        Fields: []CredentialField{
            {Key: "api_token",    EnvVar: "TURSO_API_TOKEN", Required: true},
            {Key: "organization", EnvVar: "TURSO_ORG",       Required: true},
        },
    }, func(creds map[string]string) DatabaseProvider {
        return New(creds)
    })
}
```

## Provisioning vs runtime — two layers, one package

For every external engine the `pkg/providers/<engine>/` package owns
two layers:

### Layer A — HCL emitter (provisioning)

Called during `nvoi deploy`. Emits Terraform HCL via the existing
`compile.RegisterInfra` registry — same mechanism `hetzner` and
`cloudflare` use. tofu plan/apply CREATES the cloud-side resources
(PlanetScale database, Turso DB, RDS instance, Neon project). Outputs
flow back to nvoi as tofu output values → stamped into the
credentials Secret by `EnsureCredentials`.

### Layer B — Runtime adapter (operator-facing ops)

Called when the operator runs `nvoi database branch`, `nvoi database
snapshot`, `nvoi database sql`, etc. Implements `DatabaseProvider`
methods by calling the vendor's HTTP API directly from Go. **Never**
shells out to the vendor's CLI — `pscale`, `turso`, `aws` CLIs are
not in our trust boundary. Each provider package owns its API client
(HTTP requests, auth, retries, error translation).

Why both:
- Provisioning is slow, declarative, and idempotent → tofu fits.
- Runtime ops are interactive and frequent → tofu plan-apply per
  branch would be unusable. Go HTTP client is fast and direct.
- One package keeps the cloud-side and operator-side surfaces of
  one engine together. No artificial split.

For **postgres** there's only Layer B + a kube-side reconciler —
nothing to emit to tofu, because data lives in-cluster.

## CLI dispatch — single shape, ErrUnsupported flows up

```go
// cmd/cli/database.go — branch verb
func runDatabaseBranch(prov DatabaseProvider, req DatabaseRequest, branchName string) error {
    ref, err := prov.Branch(ctx, req, branchName)
    if errors.Is(err, providers.ErrUnsupported) {
        return fmt.Errorf(
            "databases.%s: engine %q does not support branching. "+
                "Branching engines: postgres, planetscale, turso, neon",
            req.Name, req.Spec.Engine,
        )
    }
    if err != nil {
        return err
    }
    fmt.Fprintf(out, "branch %s ready — %s\n", ref.Name, ref.Endpoint)
    return nil
}
```

Same shape every verb. The supported-engines list in the error
message is built from a small constant table per verb (we update it
as engines gain capabilities — compile-time visible, not magic).

## The unified CLI surface

```
# Universal — every engine
nvoi database sql       <name> "<sql>"
nvoi database backup now      <name>
nvoi database backup list     <name>
nvoi database backup download <name> <id> [-o file]
nvoi database restore   <name> [--from-backup ID | --latest] --yes

# Snapshot — postgres, rds-postgres
nvoi database snapshot        <name> [--label LABEL]
nvoi database snapshots       <name>
nvoi database snapshot-delete <name> <snap>

# Branch — postgres, planetscale, turso, neon
nvoi database branch          <name> <branch>
nvoi database branches        <name>
nvoi database branch-delete   <name> <branch>

# Migrate — postgres only (selfhosted node move)
nvoi database migrate         <name>

# Rollback — postgres, neon
nvoi database rollback        <name> --to <snap> --yes
```

Example failure mode (operator runs an unsupported verb):

```
$ nvoi database snapshot edge          # edge is engine: turso
Error: databases.edge: engine "turso" does not support snapshots.
Snapshot engines: postgres, rds-postgres.

$ nvoi database migrate events         # events is engine: planetscale
Error: databases.events: engine "planetscale" does not support node
migration (SaaS engines have no node). Use `nvoi database backup` +
`nvoi database restore` to move data to a different region.
```

Each `ErrUnsupported` site has a hand-written error message — generic
"unsupported" isn't enough; we want the operator to know what to do
instead (use snapshot, use the vendor's UI, use backup/restore, etc.).

## Validator rules (pkg/config/validate.go)

- `engine` must be one of the five registered values: `postgres`,
  `planetscale`, `turso`, `rds-postgres`, `neon`. Anything else hard-
  errors at validate-time with the list of supported engines.
- **`postgres`**: `server:` required, `size:` required, `version:`
  optional, `credentials:` required (every `$VAR` must resolve).
  `region:` rejected. `instance_class:` rejected.
- **`planetscale`**: `region:` required (validated against
  PlanetScale's region list at the emitter). `server:` / `size:` /
  `credentials:` / `version:` rejected.
- **`turso`**: `region:` required (Turso "group" name). `server:` /
  `size:` / `credentials:` rejected.
- **`rds-postgres`**: `region:` required (AWS region). `instance_class:`
  required (e.g. `db.t3.micro`). `version:` optional. `server:` /
  `size:` / `credentials:` rejected.
- **`neon`**: `region:` required (Neon region id). `server:` / `size:`
  / `credentials:` rejected.
- DB-on-master rejected for `postgres`: `server: cp` / `server:
  <master-name>` errors out.
- DB-shares-node-with-services rejected for `postgres`: if
  `databases.X.server` matches any `services.Y.server`, error. The
  postgres node is exclusive (ZFS + PVCs).
- `backup:` set ⇒ `providers.storage` must be configured.
- `services.X.databases: [VAR=name]` must reference a defined
  database name.

## Failure modes — what we explicitly DON'T paper over

- **Tofu provisioning failure** (vendor API returns 5xx during
  CREATE): surfaced by the standard infra step. No partial-success
  retry magic.
- **CSI install failure** (postgres only): hard error at
  `EnsureZFSCSI`. ZFS is a precondition for any postgres workload.
- **Node pin drift** for postgres (live nodeSelector ≠ `cfg.server`):
  warning, keep serving from old pod, point operator at `nvoi
  database migrate <name>`. Deploy never fails on drift.
- **Backup-image digest unresolved**: hard fail the deploy with a
  clear message ("did you push `docker.io/nvoi/db:<tag>`?"). Better
  than ImagePullBackOff at 3am.
- **Vendor API rate limit during branch/snapshot**: surfaced verbatim
  from the vendor — we don't swallow it, we don't retry blindly. The
  operator sees "PlanetScale: 429 Too Many Requests" and decides.
- **`ErrUnsupported`**: never crashes the CLI. Always renders as the
  operator-facing "engine X doesn't support Y" message, with the list
  of engines that DO support it.

## Adding a sixth engine (concretely)

```
pkg/providers/<engine>/
  compile.go              ~150 LOC. EmitInfra emits vendor HCL.
                          Outputs endpoint, credentials.
  runtime.go              ~200 LOC. EnsureCredentials reads tofu output,
                          stamps Secret. Reconcile emits backup CronJob.
                          BackupNow/Restore/ListBackups/Download are
                          one-liners over the shared helpers. Branch /
                          Snapshot / Rollback / Migrate: real work where
                          the vendor API supports it, ErrUnsupported
                          otherwise.
  register.go             ~20 LOC. compile.RegisterInfra +
                          providers.RegisterDatabase with credential
                          schema.
  <engine>_test.go        ~150 LOC. HCL parse + emitter golden test +
                          ErrUnsupported assertions for unsupported verbs.
cmd/cli/main.go           +1 line. Blank-import the package.
pkg/config/validate.go    +N lines. Per-engine field constraints.
internal/cli/inputs.go    +N lines. Per-engine credential resolution.
```

Total: ~500 LOC for a wholly new engine, fully integrated. CLI verbs
work day one. ErrUnsupported is honest about gaps.
