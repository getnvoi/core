// Package detach removes k3s nodes from the cluster cleanly:
//
//   1. drain     — cordon + evict pods (workloads reschedule to surviving nodes)
//   2. etcd-rm   — for masters only; removes the etcd member so the
//                  cluster doesn't keep trying to reach the dead one
//   3. delete    — `kubectl delete node` so the apiserver no longer
//                  tracks it (no ghost entries in `kubectl get nodes`)
//
// Sits between `terraform plan` and `terraform apply` in the deploy pipeline —
// guarantees workloads, etcd quorum, and the apiserver's node list
// all stay consistent before the underlying VM is destroyed.
//
// Best-effort by design: failures on any step warn and proceed. The
// VM is going away regardless; blocking the destroy on a kubelet
// hang or transient etcd error would leave the operator stuck.
package detach

import (
	"context"
	"fmt"

	"github.com/getnvoi/core/internal/install"
	"github.com/getnvoi/core/internal/log"
	"github.com/getnvoi/core/internal/ssh"
)

// Node identifies one node to detach. Role decides whether we run
// the master-only etcd member removal.
type Node struct {
	Hostname string // full k3s node name (naming.Server output)
	Role     string // "master" | "worker"
}

// Nodes detaches each node in order: drain → (etcd member remove for
// masters) → kubectl delete node. The supplied SSH client must point
// at a SURVIVING master — that master's k3s/etcdctl is the source of
// truth for the cluster being modified.
//
// Drain flags mirror nvoi/pkg/provider/hetzner/infra.go::drainAndDeleteServer:
//
//	--ignore-daemonsets        — DaemonSet pods can't be drained.
//	--delete-emptydir-data     — emptyDir volumes die with the VM.
//	--force                    — evict bare pods.
//	--grace-period=30          — give pods 30s to shut down cleanly.
//	--timeout=60s              — hard cap. PDB-blocked pods can't drain.
//
// `delete node` uses --ignore-not-found so re-runs on already-gone
// nodes are no-ops.
func Nodes(ctx context.Context, sh ssh.Shell, nodes []Node, lg log.Log) {
	for _, n := range nodes {
		// 1. Drain.
		lg.Info(fmt.Sprintf("draining %s...", n.Hostname))
		if _, err := install.Kubectl(ctx, sh,
			"drain", n.Hostname,
			"--ignore-daemonsets",
			"--delete-emptydir-data",
			"--force",
			"--grace-period=30",
			"--timeout=60s",
		); err != nil {
			lg.Warn(fmt.Sprintf("drain %s: %s — proceeding", n.Hostname, err))
		}

		// 2. Master-only: drop the etcd member BEFORE the kubectl
		// delete-node, so the etcd cluster stops trying to reach the
		// soon-to-be-destroyed peer. Running this after delete-node
		// also works but logs more transient errors in the meantime.
		if n.Role == "master" {
			if err := removeEtcdMember(ctx, sh, n.Hostname, lg); err != nil {
				lg.Warn(fmt.Sprintf("etcd remove %s: %s — proceeding", n.Hostname, err))
			}
		}

		// 3. Remove the apiserver's node record. Without this, a ghost
		// entry stays in `kubectl get nodes` forever after the VM is
		// destroyed.
		if _, err := install.Kubectl(ctx, sh,
			"delete", "node", n.Hostname,
			"--ignore-not-found",
		); err != nil {
			lg.Warn(fmt.Sprintf("delete node %s: %s — k8s may show ghost entry", n.Hostname, err))
			continue
		}
		lg.Info(fmt.Sprintf("detached %s from cluster", n.Hostname))
	}
}
