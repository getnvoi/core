package install

import (
	"context"
	"fmt"
	"strings"
)

// SecondaryJoinSpec carries the inputs to a secondary master's
// `--server <primary>:6443 --token <X>` join. Self carries its own
// SSH/Log via Node; Primary is referenced for its private IP only.
type SecondaryJoinSpec struct {
	Self      Node
	Primary   Node
	Token     string
	ExtraSANs []string // extra TLS SANs (LB IPs in HA so kubectl-via-LB validates)
}

// JoinSecondaryMaster joins a master to an existing etcd cluster via
// `--server <primary-priv>:6443 --token <X>`. Idempotent: skips if
// local node is already Ready.
//
// ExtraSANs same as primary — every master's apiserver cert lists the
// same SANs so workers and kubectl validate against any master.
func JoinSecondaryMaster(ctx context.Context, spec SecondaryJoinSpec) error {
	self, primary, lg := spec.Self, spec.Primary, spec.Self.Log
	if installed, _ := localNodeReady(ctx, self.Shell); installed {
		lg.Info(fmt.Sprintf("k3s master %s already Ready", self.Name))
		return nil
	}

	iface, err := discoverPrivateInterface(ctx, self.Shell, self.Private)
	if err != nil {
		return fmt.Errorf("master %s: %w", self.Name, err)
	}

	sans := dedupSANs(append([]string{self.Private, self.IPv4}, spec.ExtraSANs...))
	execArgs := strings.Join([]string{
		"server",
		"--server https://" + primary.Private + ":6443",
		"--token " + spec.Token,
		"--disable traefik",
		"--disable servicelb",
		"--write-kubeconfig-mode 644",
		"--node-ip " + self.Private,
		"--advertise-address " + self.Private,
		tlsSans(sans),
		"--cluster-cidr " + clusterCIDR,
		"--service-cidr " + serviceCIDR,
		"--flannel-backend vxlan",
		"--flannel-iface " + iface,
	}, " ")
	cmd := fmt.Sprintf(`curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC=%q sh -`, execArgs)

	lg.Info(fmt.Sprintf("joining k3s master %s → primary %s (priv %s)...", self.Name, primary.Name, primary.Private))
	if err := self.Shell.RunStream(ctx, cmd, lg.Stream(), lg.Stream()); err != nil {
		return fmt.Errorf("k3s master join on %s: %w", self.Name, err)
	}
	return nil
}
