# ZFS Branching — The Headline Feature

This is the differentiator. No SaaS Postgres ships sub-second
copy-on-write branching as a YAML primitive of the deploy substrate.
We did, in `../nvoi`, and we're bringing it back.

**Postgres is the reference implementation of `DatabaseProvider`.**
Every method on the interface lands on real work in
`pkg/providers/postgres/`, backed by OpenEBS ZFS-LocalPV:
`Snapshot` → `zfs snapshot`, `Branch` → snapshot + clone-PVC + sibling
StatefulSet, `Rollback` → in-place PVC swap, `Migrate` → cross-node
move with dump+restore. Other providers (`planetscale`, `turso`,
`rds-postgres`, `neon`) implement what their vendor API supports and
return `ErrUnsupported` for the rest. See `01-contract.md` for the
interface; this doc is the postgres-side deep dive.

## What it is

**Snapshot a running Postgres DB in O(100ms) and spin up a sibling
StatefulSet bound to a CoW clone of its data.** Writes to the branch
diverge lazily — disk cost starts near-zero and grows only with
actual changes. Perfect for per-PR preview environments, "what if I
run this migration", and CI test fixtures that need real production-
shaped data.

```
$ nvoi database snapshot app --label pre-deploy
nvoi-myapp-prod-db-app-pre-deploy

$ nvoi database branch app pr-142
branch pr-142 ready — service nvoi-myapp-prod-db-app-br-pr-142:5432
                     (snapshot nvoi-myapp-prod-db-app-snap-br-pr-142)

$ DATABASE_URL=postgres://…@nvoi-myapp-prod-db-app-br-pr-142:5432/app \
    go run ./cmd/migrate

$ nvoi database branch-delete app pr-142
# StatefulSet → Service → PVC → VolumeSnapshot all gone, ZFS dataset destroyed.
```

## Stack

```
┌─────────────────────────────────────────────────────────────┐
│ User-facing primitives (postgres package)                   │
│  Snapshot()       → VolumeSnapshot via kubectl-on-master    │
│  Branch()         → snapshot + clone-PVC + StatefulSet      │
│  DeleteBranch()   → typed delete via kube.Client            │
│  ListSnapshots()  → kubectl get on master                   │
└─────────────────────────────────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────────┐
│ Kubernetes layer                                            │
│  - VolumeSnapshot (snapshot.storage.k8s.io/v1)              │
│  - VolumeSnapshotClass                                       │
│  - PVC with dataSource pointing at the VolumeSnapshot       │
│  - StorageClass with provisioner=zfs.csi.openebs.io         │
└─────────────────────────────────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────────┐
│ OpenEBS ZFS-LocalPV (CSI driver)                            │
│  Installed via `sudo k3s kubectl apply -f                   │
│  github.com/openebs/zfs-localpv/v2.9.1/deploy/              │
│  zfs-operator.yaml` (pinned) on the master.                 │
│  - Translates VolumeSnapshot → `zfs snapshot pool/dataset@…`│
│  - Translates clone-PVC → `zfs clone src@snap dest`         │
│  - DaemonSet runs only on nodes labeled `openebs.io/zfs=true`│
└─────────────────────────────────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────────┐
│ Node layer (PrepareNode via SSH)                            │
│  1. apt install zfsutils-linux (idempotent via dpkg-query)  │
│  2. truncate /var/lib/nvoi-zfs/pool.img (size = 2 × DB size │
│     clamped to leave 8GiB root reserve)                     │
│  3. zpool create -o ashift=12 -O compression=lz4 -m none    │
│     nvoi-zfs /var/lib/nvoi-zfs/pool.img                     │
│  Idempotent: re-runs are no-ops once the pool exists.       │
└─────────────────────────────────────────────────────────────┘
```

## Why ZFS-LocalPV (not Longhorn, not Rook-Ceph, not raw hostPath)

- **CoW is native**: `zfs snapshot` is metadata-only, ~ms.
- **Quota is hard**: `size: 20` becomes a refquota on the dataset; writes
  past it ENOSPC at the pod, the node stays healthy.
- **Single-node simplicity**: no replicated storage layer (we don't
  need it — Postgres handles its own replication, and the substrate's
  story for HA-Postgres is "use the `rds-postgres` or `neon` engine
  instead, where the vendor handles HA").
- **File-backed pool on shared VMs**: ~5% perf hit vs raw device,
  production-acceptable, runs on a stock Hetzner cax21. No requirement
  for dedicated NVMe.
- **PVC dataSource works out of the box**: standard Kubernetes
  VolumeSnapshot → PVC clone semantics, no operator-specific CRDs.

## Why file-backed by default (not dedicated disk)

The old code in `../nvoi/pkg/provider/postgres/zfs.go` left a TODO for
"dedicated disk on bare-metal classes (Hetzner ax, AWS i-series)". We
inherit that TODO. v1 ships file-backed only:

- **Operator UX**: works on every server type without YAML changes.
- **Cost**: an extra dedicated NVMe block device is a hot config that
  not every operator wants to opt into.
- **5% hit is real but tolerable**: ZFS on a file vs. raw device.
  Measured in the original work; acceptable for the vast majority of
  workloads.

When bare-metal becomes a real ask: dispatch on server type in
`PrepareNode`, use `/dev/nvme1n1` for hetzner.ax-class. Keep the
file-backed path as the fallback.

## What lives where

- **Pinned CSI manifest URL**: `pkg/providers/postgres/zfs.go::zfsCSIManifestURL`.
  Updating it = a deliberate commit. v2.9.1 was the last validated
  version in `../nvoi`; we may want to bump on re-introduction.
- **CSI install**: `EnsureZFSCSI(ctx, masterSSH)` — `kubectl apply -f`
  the pinned URL. Idempotent. CLAUDE.md endorses this kubectl-on-master
  pattern for "third-party RBAC bundles we never modify" (it does
  the same for kube-vip).
- **Node prep**: `PrepareNode(ctx, nodeSSH, sizeGiB)` — package-local,
  called from `Reconcile` only when `req.NodeSSH != nil` (tests can
  skip it).
- **Snapshot/Branch primitives**: `pkg/providers/postgres/branching.go`.
  Typed kube apply for PVC/Service/StatefulSet; heredoc-via-SSH apply
  for VolumeSnapshot/VolumeSnapshotClass (kinds not in core client-go).
- **Naming**: `pkg/naming` grows
  `KubeDatabasePVC(name)`, `KubeDatabaseSnapshot(dbName, label)`,
  `KubeDatabaseBranch(dbName, branchName)`,
  `KubeDatabaseBranchSnapshot(dbName, branchName)`. The 63-char
  DNS-1123 cap on Service names is enforced by the Branch primitive
  upfront (composite name validation).

## Risks / sharp edges

- **The composite name 63-char limit**: app + env + db name + branch
  name all concatenate. The `Branch` primitive validates the composite
  name upfront and errors with a clear "shorten the app/env/db/branch
  names" message rather than letting kubectl reject mid-apply.
- **Branches share the source node**: pools are node-local. Cross-node
  branching isn't supported. The branch's pod gets the same `nvoi-role:
  <server>` nodeSelector as the source.
- **ZFS arc memory**: ZFS uses RAM as a cache by default (up to 50% of
  RAM). On a cax11 (2GB RAM), this can squeeze workloads. The CSI
  driver doesn't tune arc; operators with tight nodes may want a
  `zfs_arc_max` sysctl. v1 ships without that tuning — surface only if
  operators hit the issue.
- **PrepareNode runs over SSH**: failure modes include "node doesn't
  reach the apt repo", "node has no /var/lib free", "node isn't Debian/
  Ubuntu". Errors bubble up cleanly. Non-Debian nodes are unsupported
  (matches the rest of the substrate's assumption: k3s on Ubuntu).
- **Pool grow path**: if the operator bumps `size:` from 20 → 100, the
  pool image needs to grow. `truncate -s 100G` on an existing file is
  expand-only, then `zpool online -e` reads the new size. The old code
  had a TODO for this; v1 may ship without auto-grow and require manual
  intervention. Document the gap.

## What the headline buys us in marketing terms

- **Per-PR preview DBs in CI**: `nvoi database branch app pr-${SHA}` →
  preview environment gets its own real-data copy. Without ZFS, this is
  `pg_dump | pg_restore` (minutes, GB of disk). With ZFS, it's ~100ms
  and 0 bytes of new disk until writes happen.
- **"What if" migrations**: branch → run the migration on the branch
  → keep or discard. No staging environment required.
- **CI fixtures with real schema**: snapshot the prod DB before
  anonymisation runs in a branch off the snapshot, anonymise, ship the
  result to CI's test bucket.

This is the value prop. We should not lose it during the
re-introduction.
