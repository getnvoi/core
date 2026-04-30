package detach

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/core/internal/log"
	"github.com/getnvoi/core/internal/ssh"
)

// k3s does NOT ship etcdctl as a usable command (only `k3s
// etcd-snapshot` for snapshots — no member ops). We install
// `etcd-client` from apt on demand on the survivor master we're
// about to use for detach. ~10MB, one-time per master, idempotent.
//
// The etcdctl invocation uses k3s's bundled certs so auth Just Works
// from any master.
const etcdctlPrelude = `ETCDCTL_API=3 etcdctl ` +
	`--endpoints=https://127.0.0.1:2379 ` +
	`--cacert=/var/lib/rancher/k3s/server/tls/etcd/server-ca.crt ` +
	`--cert=/var/lib/rancher/k3s/server/tls/etcd/server-client.crt ` +
	`--key=/var/lib/rancher/k3s/server/tls/etcd/server-client.key`

// ensureEtcdctl makes sure the `etcdctl` binary is installed on the
// survivor master. Idempotent — fast-path returns when it's already
// present.
func ensureEtcdctl(ctx context.Context, sh ssh.Shell, lg log.Log) error {
	if _, err := sh.Run(ctx, "command -v etcdctl >/dev/null 2>&1"); err == nil {
		return nil
	}
	lg.Info("installing etcd-client (one-time, ~10MB)...")
	if err := sh.RunStream(ctx,
		"sudo apt-get update -qq && sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -qq etcd-client",
		lg.Stream(), lg.Stream(),
	); err != nil {
		return fmt.Errorf("apt install etcd-client: %w", err)
	}
	return nil
}

// removeEtcdMember removes a master's etcd member from the cluster.
// Required when a master is being destroyed — kubectl delete node
// only removes the apiserver record, NOT the etcd Raft membership.
// Without this, etcd retains the dead member and quorum degrades.
//
// Idempotent: returns nil if the member is already gone.
func removeEtcdMember(ctx context.Context, sh ssh.Shell, hostname string, lg log.Log) error {
	if err := ensureEtcdctl(ctx, sh, lg); err != nil {
		return err
	}
	memberID, err := findEtcdMemberID(ctx, sh, hostname)
	if err != nil {
		return fmt.Errorf("find etcd member id for %s: %w", hostname, err)
	}
	if memberID == "" {
		lg.Info(fmt.Sprintf("etcd member %s already gone", hostname))
		return nil
	}
	if _, err := sh.Run(ctx, fmt.Sprintf("sudo bash -c %q",
		etcdctlPrelude+" member remove "+memberID,
	)); err != nil {
		return fmt.Errorf("etcdctl member remove %s: %w", memberID, err)
	}
	lg.Info(fmt.Sprintf("removed etcd member %s (id=%s)", hostname, memberID))
	return nil
}

// findEtcdMemberID parses `etcdctl member list` output to find the
// hex member ID for the given hostname. k3s decorates each member's
// name with a random suffix (`<hostname>-<8-hex>`), so we prefix-match,
// not exact-match. Returns "" + nil when no matching member exists
// (already removed by k3s's cluster controller, or never registered).
//
// Output format (CSV — etcdctl default `-w simple`):
//
//	<hex-id>, started, <hostname>-<rand>, <peer-urls>, <client-urls>, false
func findEtcdMemberID(ctx context.Context, sh ssh.Shell, hostname string) (string, error) {
	out, err := sh.Run(ctx, fmt.Sprintf("sudo bash -c %q",
		etcdctlPrelude+" member list",
	))
	if err != nil {
		return "", fmt.Errorf("etcdctl member list: %w", err)
	}
	prefix := hostname + "-"
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.Split(line, ",")
		if len(parts) < 3 {
			continue
		}
		name := strings.TrimSpace(parts[2])
		if name == hostname || strings.HasPrefix(name, prefix) {
			return strings.TrimSpace(parts[0]), nil
		}
	}
	return "", nil
}
