package install

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/core/pkg/ssh"
)

// InstallPrimaryMaster runs `--cluster-init` on the primary. The
// primary is the master designated by `primary: true` in YAML (or the
// lone master in single-master clusters). Idempotent: skips if local
// node is already Ready.
//
// extraSANs are additional names/IPs to embed in the apiserver TLS
// cert — typically the LB public+private IPs in HA so workers and
// kubectl can reach the apiserver via the LB without cert errors.
//
// Always uses --cluster-init regardless of master count: even
// single-master clusters get embedded etcd, so the 1↔N migration
// is mechanical (just add masters and redeploy).
//
// `self.Shell` is the SSH handle, `self.Log` is the progress sink —
// both bundled on Node so we don't need to take them as separate args.
func InstallPrimaryMaster(ctx context.Context, self Node, extraSANs []string) error {
	if installed, _ := localNodeReady(ctx, self.Shell); installed {
		self.Log.Info(fmt.Sprintf("k3s primary on %s already Ready", self.Name))
		return nil
	}

	iface, err := discoverPrivateInterface(ctx, self.Shell, self.Private)
	if err != nil {
		return fmt.Errorf("primary %s: %w", self.Name, err)
	}

	sans := dedupSANs(append([]string{self.Private, self.IPv4}, extraSANs...))
	execArgs := strings.Join([]string{
		"server",
		"--cluster-init",
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

	self.Log.Info(fmt.Sprintf("installing k3s primary on %s (priv %s)...", self.Name, self.Private))
	if err := self.Shell.RunStream(ctx, cmd, self.Log.Stream(), self.Log.Stream()); err != nil {
		return fmt.Errorf("k3s primary install on %s: %w", self.Name, err)
	}

	if err := setupKubeconfig(ctx, self.Shell, self.Private); err != nil {
		return fmt.Errorf("kubeconfig setup on %s: %w", self.Name, err)
	}

	if err := WaitNodeReady(ctx, self.Shell, self.Hostname, self.Log); err != nil {
		return fmt.Errorf("primary %s did not become Ready: %w", self.Name, err)
	}
	return nil
}

// setupKubeconfig copies /etc/rancher/k3s/k3s.yaml to the deploy
// user's home, rewrites 127.0.0.1 → privateIP so kube tunnels work,
// and chowns. Mirrors nvoi/pkg/infra/k3s.go::InstallK3sMaster.
//
// Private to primary.go because only the primary install runs it
// (subsequent masters inherit the kubeconfig from the cluster they
// joined; workers don't need one).
func setupKubeconfig(ctx context.Context, sh ssh.Shell, privateIP string) error {
	cmd := fmt.Sprintf(
		`mkdir -p /home/%[1]s/.kube && sudo cp %[2]s /home/%[1]s/.kube/config && sudo sed -i 's/127.0.0.1/%[3]s/g' /home/%[1]s/.kube/config && sudo chown -R %[1]s:%[1]s /home/%[1]s/.kube && chmod 600 /home/%[1]s/.kube/config`,
		DefaultUser, kubeconfigPath, privateIP,
	)
	_, err := sh.Run(ctx, cmd)
	return err
}
