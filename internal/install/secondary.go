package install

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/tf/internal/log"
	"github.com/getnvoi/tf/internal/ssh"
)

// JoinSecondaryMaster joins a master to an existing etcd cluster via
// `--server <primary-priv>:6443 --token <X>`. Idempotent: skips if
// local node is already Ready.
//
// extraSANs same as primary — every master's apiserver cert lists the
// same SANs so workers and kubectl validate against any master.
func JoinSecondaryMaster(ctx context.Context, sh *ssh.Client, self, primary Node, token string, extraSANs []string, lg log.Log) error {
	if installed, _ := localNodeReady(ctx, sh); installed {
		lg.Info(fmt.Sprintf("k3s master %s already Ready", self.Name))
		return nil
	}

	iface, err := discoverPrivateInterface(ctx, sh, self.Private)
	if err != nil {
		return fmt.Errorf("master %s: %w", self.Name, err)
	}

	sans := dedupSANs(append([]string{self.Private, self.IPv4}, extraSANs...))
	execArgs := strings.Join([]string{
		"server",
		"--server https://" + primary.Private + ":6443",
		"--token " + token,
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
	if err := sh.RunStream(ctx, cmd, lg.Stream(), lg.Stream()); err != nil {
		return fmt.Errorf("k3s master join on %s: %w", self.Name, err)
	}
	return nil
}
