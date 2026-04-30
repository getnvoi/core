package install

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/core/internal/log"
	"github.com/getnvoi/core/internal/ssh"
)

// JoinWorker installs the k3s agent on a worker. target is the address
// the agent connects to (LB private IP in HA, primary's private IP
// otherwise — Endpoints.WorkerJoinTarget computes this). Always a
// private IP — workers never reach the apiserver via public network.
//
// Idempotent: skips if k3s-agent is active.
func JoinWorker(ctx context.Context, sh *ssh.Client, self Node, target, token string, lg log.Log) error {
	if _, err := sh.Run(ctx, "systemctl is-active --quiet k3s-agent"); err == nil {
		lg.Info(fmt.Sprintf("k3s worker %s already joined", self.Name))
		return nil
	}

	iface, err := discoverPrivateInterface(ctx, sh, self.Private)
	if err != nil {
		return fmt.Errorf("worker %s: %w", self.Name, err)
	}

	execArgs := strings.Join([]string{
		"agent",
		"--node-ip " + self.Private,
		"--flannel-iface " + iface,
	}, " ")
	cmd := fmt.Sprintf(
		`curl -sfL https://get.k3s.io | K3S_URL=https://%s:6443 K3S_TOKEN=%s INSTALL_K3S_EXEC=%q sh -`,
		target, token, execArgs,
	)

	lg.Info(fmt.Sprintf("joining k3s worker %s → %s...", self.Name, target))
	if err := sh.RunStream(ctx, cmd, lg.Stream(), lg.Stream()); err != nil {
		return fmt.Errorf("k3s worker join on %s: %w", self.Name, err)
	}
	return nil
}
