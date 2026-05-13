# Database — Overview & Re-introduction Plan

Branch: `feat/database` (worktree at `../tf-database`).

We're re-introducing the `databases:` block to the tofu-based tree.
Prior art: `../nvoi` (pre-nuke) had ~5150 LOC across 3 engines plus
the `cmd/db` backup/restore image. We are lifting the patterns; the
new tree's architecture (tofu-native infra, typed kube applier,
providers registry) reshapes the wiring.

## Scope — postgres-shaped, multi-engine

**Engineering effort goes into postgres.** Postgres is our managed
offering — Heroku-style, on the operator's cluster, full capability
surface via OpenEBS ZFS-LocalPV (sub-second branching, snapshots,
in-place rollback, cross-node migrate). Postgres is the **reference
implementation**: the `DatabaseProvider` interface is shaped by what
postgres can do.

**Other engines are first-class providers, best-effort coverage.**
PlanetScale, Turso, RDS, Neon each get their own `pkg/providers/
<engine>/` package. Each implements the same `DatabaseProvider`
interface, doing what the vendor's API natively supports. Methods a
vendor can't fulfil return `ErrUnsupported` — the CLI surfaces this
cleanly to the operator.

**API interactions are internal Go code.** Provisioning goes through
tofu (HCL emitter via `compile.RegisterInfra`); runtime ops (Branch,
Snapshot, Rollback, ExecSQL, etc.) go through Go HTTP/SDK clients we
own — same pattern `../nvoi` had for Neon/PlanetScale. **We never
shell out to vendor CLIs** (`pscale`, `turso`, `aws` CLIs are not in
our trust boundary). The runtime adapter is wholly under our roof.

## Engine support matrix

| Engine            | Lifecycle | SQL · Backup · Restore | Snapshot | Branch | Migrate | Rollback |
|-------------------|:---:|:---:|:---:|:---:|:---:|:---:|
| `postgres` (ours, ZFS)  | ✅ | ✅ | ✅ ZFS | ✅ ZFS clone-PVC | ✅ node move | ✅ in-place |
| `planetscale`     | ✅ | ✅ | ❌ Unsupp | ✅ native API | ❌ Unsupp | ❌ Unsupp |
| `turso`           | ✅ | ✅ | ❌ Unsupp | ✅ native API | ❌ Unsupp | ❌ Unsupp |
| `rds-postgres`    | ✅ | ✅ | ✅ AWS API | ❌ Unsupp | ❌ Unsupp | ❌ Unsupp |
| `neon`            | ✅ | ✅ | ❌ Unsupp | ✅ native API | ❌ Unsupp | ✅ native PITR |

Postgres is the only row that's all ✅. That's the marketing surface:
**"snapshots like RDS, branches like PlanetScale, in-place rollback
like Neon — on your own hardware, on the engine you control."**

## YAML shape

One YAML block, one shape (`engine` + engine-specific fields). The
validator gates which fields are valid for which engine.

```yaml
databases:
  app:                              # managed postgres
    engine: postgres
    version: "17"
    server: db-worker               # required
    size: 20                        # GiB, hard ZFS quota
    credentials:
      user: $APP_PG_USER
      password: $APP_PG_PASSWORD
      database: $APP_PG_DB
    backup: { schedule: "0 3 * * *", retention: 14 }

  events:                           # PlanetScale (provisioned via tofu)
    engine: planetscale
    region: eu-west
    backup: { schedule: "0 3 * * *", retention: 14 }

  edge:                             # Turso (libsql, edge-replicated)
    engine: turso
    region: ams                     # primary group
    backup: { schedule: "0 3 * * *", retention: 14 }

  analytics:                        # AWS RDS Postgres
    engine: rds-postgres
    instance_class: db.t3.micro
    region: eu-central-1
    backup: { schedule: "0 3 * * *", retention: 14 }

services:
  api:
    databases:
      - DATABASE_URL=app              # postgres DSN
      - EVENTS_URL=events             # planetscale DSN
      - EDGE_URL=edge                 # turso libsql URL
      - ANALYTICS_URL=analytics       # rds DSN
```

Same CLI verbs work against every entry. Best-effort coverage means
some verbs return `ErrUnsupported` cleanly on specific engines (see
`01-contract.md`).

## Architecture — three layers per engine

```
┌───────────────────────────────────────────────────────────────────┐
│  CLI surface  (cmd/cli/database.go)                               │
│  One verb set. Dispatch by engine name → DatabaseProvider.        │
│  ErrUnsupported flows up to a clear operator message.             │
└───────────────────────────────────────────────────────────────────┘
                              │ DatabaseProvider interface
                              ▼
┌───────────────────────────────────────────────────────────────────┐
│  Provider package  (pkg/providers/<engine>/)                      │
│                                                                   │
│  Layer A — HCL emitter (compile.RegisterInfra)                    │
│    Emits provisioning HCL during `nvoi deploy`. tofu plan/apply   │
│    creates DB/project/database/role/etc. Outputs flow back as     │
│    tofu output → credentials Secret.                              │
│                                                                   │
│  Layer B — Runtime adapter (providers.RegisterDatabase)           │
│    Go HTTP/SDK client owned by us. Handles Branch/Snapshot/       │
│    Rollback/etc. via vendor API calls — never via vendor CLI.     │
│    ExecSQL goes through the cmd/db Job (uniform across engines).  │
└───────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌───────────────────────────────────────────────────────────────────┐
│  Shared substrate                                                 │
│  - cmd/db image: pg_dump / mysqldump / psql / mysql → S3 sigv4    │
│  - BuildBackupCronJob / BuildRestoreJob (typed kube)              │
│  - BucketListBackups / BucketDownloadBackup (S3-compatible)       │
│  - ResolveDBImage (digest pin against Docker Hub)                 │
└───────────────────────────────────────────────────────────────────┘
```

Layer A is the cloud-side substrate. Layer B is the operator-facing
substrate. Both live in the same `pkg/providers/<engine>/` package —
one engine, one place.

## Where it slots into the new tree

```
pkg/providers/
  database.go             NEW. DatabaseProvider interface + registry +
                          ErrUnsupported sentinel + DatabaseRequest /
                          DatabaseSpec / DatabaseCredentials / BackupRef /
                          SnapshotRef / BranchRef.
  database_backups.go     NEW. BuildBackupCronJob / BuildRestoreJob /
                          BucketListBackups / BucketDownloadBackup /
                          ResolveDBImage / BuildBackupCredsSecretData.

  postgres/               NEW. Reference implementation — full coverage.
    postgres.go           Reconcile (PVC + Service + StatefulSet + backup
                          CronJob), credentials, ExecSQL via kube.Exec.
    zfs.go                EnsureZFSCSI (master kubectl), PrepareNode
                          (per-node SSH: apt-install zfsutils, zpool
                          create), buildZFSStorageClass.
    branching.go          Snapshot / ListSnapshots / DeleteSnapshot /
                          Branch / DeleteBranch (full ZFS-backed impl).
    migrate.go            Migrate (cross-node move via dump + teardown
                          + apply-new + restore).
    rollback.go           Rollback (in-place PVC swap from snapshot).
    register.go           providers.RegisterDatabase("postgres", ...).
                          No HCL emitter (no cloud side; lives in k8s).

  planetscale/            NEW. SaaS — HCL emitter + Go API adapter.
    compile.go            EmitInfra(rt) → planetscale_database HCL.
                          Outputs host/user/password/db_name.
    runtime.go            Vendor API client (Branch, ListBranches,
                          DeleteBranch via api.planetscale.com).
                          Snapshot/Migrate/Rollback return
                          ErrUnsupported. Backup/Restore via shared
                          cmd/db Job.
    register.go           compile.RegisterInfra + providers.RegisterDatabase.

  turso/                  NEW. SaaS — HCL emitter + Go API adapter.
    compile.go            EmitInfra(rt) → turso_database HCL.
    runtime.go            Vendor API client (Branch via api.turso.tech
                          `db create --from-db` equivalent). Rest
                          returns ErrUnsupported.
    register.go

  rds/                    NEW. AWS — HCL emitter + Go API adapter.
    compile.go            EmitInfra(rt) → aws_db_instance +
                          aws_db_subnet_group + aws_security_group_rule.
    runtime.go            AWS SDK client for snapshot ops
                          (CreateDBSnapshot / DescribeDBSnapshots /
                          DeleteDBSnapshot). Branch/Migrate/Rollback
                          return ErrUnsupported.
    register.go

  neon/                   NEW. SaaS — HCL emitter + Go API adapter.
    compile.go            EmitInfra(rt) → neon_project + neon_branch +
                          neon_role + neon_database.
    runtime.go            Neon API client (Branch + Rollback via PITR).
                          Snapshot/Migrate return ErrUnsupported.
    register.go

pkg/internal/kube/
  owned.go                MODIFY. Add OwnerDatabases (+ owners for
                          branches / backups if we want sub-discrim).

pkg/config/
  config.go               MODIFY. DatabaseSpec / DatabaseBackupSpec /
                          DatabaseCredsSpec types.
  validate.go             MODIFY. Per-engine field constraints,
                          providers.storage required when backup set,
                          DB-on-master rejected, DB-shares-node check,
                          $VAR-resolves check on credentials.

pkg/deploy/
  databases.go            NEW. Reconcile step between storage and
                          workloads. Installs CSI when a postgres DB
                          is present, preps nodes, calls each provider's
                          Reconcile, builds DATABASE_URL_<NAME> env map
                          for services.

cmd/cli/database.go       NEW. cobra adapter — lift ~830 LOC from
                          `../nvoi`. Same verb set; ErrUnsupported
                          mapping per verb.

cmd/db/                   NEW. Backup/restore container — lift verbatim
                          from `../nvoi`. Image contract unchanged.
  main.go
  Dockerfile

internal/cli/             MODIFY. ResolveProviderInputs grows
                          per-engine credential schemas (NEON_API_KEY,
                          PLANETSCALE_SERVICE_TOKEN, TURSO_API_TOKEN,
                          AWS_ACCESS_KEY_ID, etc.).
```

## Out of scope for this branch

- **MySQL selfhosted** (`engine: mysql` running in-cluster).
  PlanetScale covers the managed-MySQL ask; we're not in the
  selfhosted-MySQL business.
- **Per-PR ephemeral branches with TTL** (auto-GC via `nvoi/branch-ttl`
  label). The Branch primitive ships; TTL sweep waits for a real CI
  workflow that creates them.
- **Cross-node migrate without dump+restore** (ZFS-send/receive path
  for postgres). Today's `migrate` takes O(dump+restore) downtime.
- **Hetzner managed Postgres**: tofu provider doesn't exist yet
  (Hetzner offers it in beta). Slots in as another engine when ready.

## Reading order for the rest of these notes

1. **`01-contract.md`** — the `DatabaseProvider` interface, the
   `ErrUnsupported` sentinel pattern, the CLI dispatch, validator
   rules, per-engine credential schemas, the HCL/runtime split.
2. **`02-zfs-branching.md`** — the ZFS-LocalPV subsystem, the
   headline-feature deep dive. Postgres-specific.
