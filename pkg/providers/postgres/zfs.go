package postgres

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/getnvoi/core/pkg/ssh"
)

// corev1Delete is corev1.PersistentVolumeReclaimDelete bound to a
// package-level var so we can take its address when stamping the
// StorageClass's ReclaimPolicy (the field is *string-ish).
var corev1Delete = corev1.PersistentVolumeReclaimDelete

// ZFS prepare-node phase.
//
// Runs before the first apply of a postgres StatefulSet on a given
// node. Installs zfsutils-linux and creates a file-backed zpool so
// the OpenEBS ZFS-LocalPV CSI driver (installed cluster-side once
// per deploy) has a pool to carve datasets out of. Every step
// idempotent — second run is a no-op.
//
// Device strategy dispatches on server type:
//   - file-backed pool on shared-VM classes (Hetzner cax/cx, AWS
//     t-series / m-series, Scaleway DEV/PRO): pool lives at
//     /var/lib/nvoi-zfs/pool.img on the root filesystem, sized
//     2x the database's declared size (clamped at 80% of root
//     free).  ~5% perf hit vs raw device, production-safe.
//   - dedicated disk on bare-metal classes (Hetzner ax, AWS i-series):
//     pool on /dev/nvme1n1 or equivalent. TODO follow-up.
//
// Why here (postgres provider) and not in pkg/internal/cloudinit:
// a cluster with no databases never needs ZFS. The IaaS provider
// provisions generic servers; the DatabaseProvider decides what
// storage substrate to build on top.

// ZFSStorageClassName is the StorageClass every postgres PVC binds
// to. The OpenEBS ZFS-LocalPV CSI driver provisions a ZFS dataset
// on the per-node pool (zpoolName below) whenever a PVC references
// this SC. Exported because postgres.buildPVC stamps it on the spec.
const ZFSStorageClassName = "nvoi-zfs-localpv"

const (
	// zpoolName is the single pool every DB on a node shares. The
	// CSI driver carves per-PVC datasets out of it. One pool per
	// node is the OpenEBS ZFS-LocalPV convention.
	zpoolName = "nvoi-zfs"

	// poolImagePath is where the file-backed pool lives on
	// shared-VM nodes. Chosen under /var/lib so it shares the root
	// disk's extended attributes (tune2fs, journaling) without
	// cluttering /root or /home.
	poolImagePath = "/var/lib/nvoi-zfs/pool.img"

	// rootReserveGiB is the free space we leave on the root disk
	// when sizing a file-backed pool — OS + containerd images +
	// k3s state + other PVs under local-path (caddy, k3s snapshots).
	// Conservative on small cax11 nodes (40 GB total → 8 GB floor
	// for system).
	rootReserveGiB = 8
)

// buildZFSStorageClass returns the cluster-scoped StorageClass that
// binds postgres PVCs to the OpenEBS ZFS-LocalPV CSI driver. Applied
// idempotently by postgres.Reconcile on every deploy so the SC is
// always present if any DB declares a PVC against it.
//
// `poolname=<zpoolName>` tells the CSI which pool on the node to
// carve datasets from. `fstype=zfs` keeps the mount ZFS-native (no
// ext4 layer on top). `WaitForFirstConsumer` defers binding until
// the pod schedules, so the CSI provisions the dataset on the right
// node when pods have nodeSelector constraints.
//
// ReclaimPolicy=Delete: when the PVC is deleted (e.g. during
// `nvoi database migrate` teardown), the ZFS dataset is destroyed
// too. No orphan datasets on the old node.
func buildZFSStorageClass() runtime.Object {
	reclaim := corev1Delete
	binding := storagev1.VolumeBindingWaitForFirstConsumer
	allowExpand := true
	return &storagev1.StorageClass{
		TypeMeta:   metav1.TypeMeta{APIVersion: "storage.k8s.io/v1", Kind: "StorageClass"},
		ObjectMeta: metav1.ObjectMeta{Name: ZFSStorageClassName},
		Provisioner: "zfs.csi.openebs.io",
		Parameters: map[string]string{
			"poolname":    zpoolName,
			"fstype":      "zfs",
			"compression": "lz4",
		},
		ReclaimPolicy:        &reclaim,
		VolumeBindingMode:    &binding,
		AllowVolumeExpansion: &allowExpand,
	}
}

// zfsCSIManifestURL is the pinned OpenEBS ZFS-LocalPV manifest
// applied to the cluster. Pinned to a specific release tag so
// deploys are reproducible — bumping the version is a deliberate
// commit. Contains the CSIDriver + CRDs (ZFSVolume, ZFSSnapshot, …)
// + RBAC + controller StatefulSet + node-plugin DaemonSet +
// namespace.
//
// The DaemonSet lands a pod on every node tagged
// openebs.io/zfs=true. We label the DB node during PrepareNode (via
// the deploy step that calls EnsureZFSCSI). Inspecting a pinned
// manifest rather than tracking latest avoids the "vendor changed
// the RBAC surface on Tuesday" failure mode.
const zfsCSIManifestURL = "https://raw.githubusercontent.com/openebs/zfs-localpv/v2.9.1/deploy/zfs-operator.yaml"

// EnsureZFSCSI installs the OpenEBS ZFS-LocalPV CSI driver if it
// isn't already present. Runs `sudo k3s kubectl apply -f
// <pinned-url>` on the master — idempotent because `kubectl apply`
// is idempotent by design (it patches what's changed, creates what's
// new, leaves unchanged objects alone).
//
// Why via k3s kubectl + master SSH instead of go-client apply: the
// manifest carries kinds we don't (and shouldn't) plumb through
// pkg/internal/kube/apply.go piecemeal — CustomResourceDefinition,
// CSIDriver, ServiceAccount, ClusterRole[Binding], DaemonSet. k3s
// bundles kubectl; shelling out keeps the CSI install a pure
// upstream concern. CLAUDE.md endorses this pattern for "third-party
// RBAC bundles we never modify" (same as kube-vip).
//
// Network dependency: the master node must reach
// raw.githubusercontent.com at deploy time. Same trust boundary as
// pulling container images — if the network is segmented, operators
// pre-seed.
func EnsureZFSCSI(ctx context.Context, masterSSH ssh.Shell) error {
	if masterSSH == nil {
		return fmt.Errorf("postgres.EnsureZFSCSI: master ssh client required")
	}
	cmd := fmt.Sprintf(`sudo k3s kubectl apply -f %s`, zfsCSIManifestURL)
	if _, err := masterSSH.Run(ctx, cmd); err != nil {
		return fmt.Errorf("apply openebs zfs-localpv manifest: %w", err)
	}
	return nil
}

// PrepareNode installs zfsutils + creates a file-backed zpool on the
// target node via SSH. Idempotent — every step checks for existing
// state and short-circuits. The caller (postgres.Reconcile) runs
// this once per deploy against the DB node; re-runs are near-free.
//
// sizeGiB is the database's declared size (databases.X.size). The
// pool is sized 2x that value so snapshots + CoW overhead have room;
// clamped to leave rootReserveGiB free on the root filesystem.
func PrepareNode(ctx context.Context, sh ssh.Shell, sizeGiB int) error {
	if sh == nil {
		return fmt.Errorf("postgres.PrepareNode: ssh client required")
	}
	if sizeGiB <= 0 {
		return fmt.Errorf("postgres.PrepareNode: sizeGiB must be > 0")
	}

	if err := ensureZFSUtils(ctx, sh); err != nil {
		return fmt.Errorf("install zfsutils: %w", err)
	}
	if err := ensureZPool(ctx, sh, sizeGiB); err != nil {
		return fmt.Errorf("create zpool: %w", err)
	}
	return nil
}

// ensureZFSUtils installs zfsutils-linux if not already present.
// Uses `dpkg-query` for the idempotency check rather than `apt list
// --installed | grep` because dpkg-query has a dedicated exit code
// for "not installed" — avoids the fragile output parse.
func ensureZFSUtils(ctx context.Context, sh ssh.Shell) error {
	out, _ := sh.Run(ctx, `dpkg-query -W -f='${Status}' zfsutils-linux 2>/dev/null || true`)
	if strings.Contains(string(out), "install ok installed") {
		return nil
	}
	// `DEBIAN_FRONTEND=noninteractive` keeps apt from prompting
	// about config files; `-y` auto-accepts. Combining update +
	// install in one line avoids a stale-cache race when the
	// image's apt cache is older than the repo state.
	cmd := `sudo DEBIAN_FRONTEND=noninteractive sh -c 'apt-get update -q && apt-get install -y -q zfsutils-linux'`
	if _, err := sh.Run(ctx, cmd); err != nil {
		return fmt.Errorf("apt install: %w", err)
	}
	return nil
}

// ensureZPool creates the file-backed zpool if it doesn't already
// exist. The pool image file, its parent directory, and the pool
// itself are each created independently so a partial previous run
// (e.g. image exists but pool wasn't imported) converges cleanly.
//
// Pool size: 2 * sizeGiB, capped so the root FS retains at least
// rootReserveGiB free. `df --output=avail` returns KB; we convert to
// GiB for the comparison.
func ensureZPool(ctx context.Context, sh ssh.Shell, sizeGiB int) error {
	// Already imported? `zpool list -H -o name` is silent on absence
	// and prints the pool name on hit.
	out, _ := sh.Run(ctx, `zpool list -H -o name 2>/dev/null || true`)
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == zpoolName {
			return nil
		}
	}

	poolGiB, err := resolvePoolSize(ctx, sh, sizeGiB)
	if err != nil {
		return err
	}

	mkDir := `sudo mkdir -p /var/lib/nvoi-zfs`
	if _, err := sh.Run(ctx, mkDir); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	// `truncate` is sparse by design — the file claims poolGiB but
	// consumes only the blocks ZFS writes to.
	mkImg := fmt.Sprintf(`sudo sh -c 'test -f %s || truncate -s %dG %s'`, poolImagePath, poolGiB, poolImagePath)
	if _, err := sh.Run(ctx, mkImg); err != nil {
		return fmt.Errorf("create pool image: %w", err)
	}

	// `-o ashift=12` matches the 4K sector size most modern storage
	// uses. `-O compression=lz4` gets us ~2x savings on text-heavy
	// DB data for near-zero CPU cost. `-m none` skips auto-mounting
	// the root dataset (the CSI driver manages mounts).
	createPool := fmt.Sprintf(
		`sudo zpool create -o ashift=12 -O compression=lz4 -m none %s %s`,
		zpoolName, poolImagePath,
	)
	if _, err := sh.Run(ctx, createPool); err != nil {
		return fmt.Errorf("zpool create: %w", err)
	}
	return nil
}

// resolvePoolSize decides how big to make the file-backed pool.
// Starts at 2 * sizeGiB (room for snapshots + CoW overhead), then
// clamps against the root filesystem's free space so we don't fill
// the disk.
func resolvePoolSize(ctx context.Context, sh ssh.Shell, sizeGiB int) (int, error) {
	wanted := 2 * sizeGiB
	// `df --output=avail` is GNU coreutils, present on Ubuntu/Debian.
	out, err := sh.Run(ctx, `df --output=avail -B1G / | tail -n 1`)
	if err != nil {
		return 0, fmt.Errorf("df avail: %w", err)
	}
	availGiB := 0
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &availGiB); err != nil {
		return 0, fmt.Errorf("parse df output %q: %w", string(out), err)
	}
	maxPool := availGiB - rootReserveGiB
	if maxPool < sizeGiB {
		return 0, fmt.Errorf("node has only %dGiB free (want %dGiB for zpool + %dGiB reserve) — use a bigger server", availGiB, sizeGiB, rootReserveGiB)
	}
	if wanted > maxPool {
		wanted = maxPool
	}
	return wanted, nil
}
