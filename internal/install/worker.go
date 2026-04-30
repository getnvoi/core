package install

import (
	"context"
	"fmt"
	"strings"
)

// WorkerJoinSpec carries the inputs to a worker's k3s-agent install.
// Target is the apiserver address the agent connects to: LB private IP
// in HA, primary's private IP otherwise (computed by
// Endpoints.WorkerJoinTarget). Always a private IP — workers never
// reach the apiserver via public network.
type WorkerJoinSpec struct {
	Self   Node
	Target string
	Token  string
}

// JoinWorker installs the k3s agent on a worker. Idempotent: skips
// if k3s-agent is already active.
func JoinWorker(ctx context.Context, spec WorkerJoinSpec) error {
	self, lg := spec.Self, spec.Self.Log
	if _, err := self.Shell.Run(ctx, "systemctl is-active --quiet k3s-agent"); err == nil {
		lg.Info(fmt.Sprintf("k3s worker %s already joined", self.Name))
		return nil
	}

	iface, err := discoverPrivateInterface(ctx, self.Shell, self.Private)
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
		spec.Target, spec.Token, execArgs,
	)

	lg.Info(fmt.Sprintf("joining k3s worker %s → %s...", self.Name, spec.Target))
	if err := self.Shell.RunStream(ctx, cmd, lg.Stream(), lg.Stream()); err != nil {
		return fmt.Errorf("k3s worker join on %s: %w", self.Name, err)
	}
	return nil
}
