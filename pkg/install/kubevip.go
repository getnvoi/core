package install

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/getnvoi/core/pkg/internal/kubevip"
)

// kubeVIPManifestPath is k3s's auto-apply manifest dir. Anything dropped
// here before k3s starts is `kubectl apply`d by the embedded controller.
// Path is fixed by k3s — not nvoi's to configure.
const kubeVIPManifestPath = "/var/lib/rancher/k3s/server/manifests/kube-vip.yaml"

// WriteKubeVIPManifest drops the per-master kube-vip static-pod
// manifest at k3s's auto-apply path BEFORE the k3s install command
// runs. Required on every master in HA + tunnel mode; harmless to
// re-run (file overwrite is idempotent).
//
// Iface discovery reuses the same private-IP-to-interface mapping
// the k3s install does — kube-vip ARPs on the private subnet
// interface (eth0 / enp7s0 / ens10, varies by Hetzner image).
//
// Permissions: the manifest dir is root-owned. We pipe the rendered
// bytes through `sudo tee` so the deploy user (unprivileged) doesn't
// need write access to /var/lib/rancher.
func WriteKubeVIPManifest(ctx context.Context, self Node, vip string) error {
	iface, err := discoverPrivateInterface(ctx, self.Shell, self.Private)
	if err != nil {
		return fmt.Errorf("kube-vip iface discovery on %s: %w", self.Name, err)
	}
	manifest, err := kubevip.Manifest(kubevip.ManifestSpec{
		VIP:       vip,
		Interface: iface,
	})
	if err != nil {
		return fmt.Errorf("render kube-vip manifest for %s: %w", self.Name, err)
	}
	// base64 → decode in-place: dodges every shell-quoting hazard
	// the manifest's YAML (colons, dashes, quotes) would otherwise
	// pose to a single-quoted heredoc. The remote side decodes via
	// `base64 -d` and pipes into sudo tee.
	encoded := base64.StdEncoding.EncodeToString(manifest)
	cmd := fmt.Sprintf(
		`sudo mkdir -p /var/lib/rancher/k3s/server/manifests && echo %s | base64 -d | sudo tee %s > /dev/null`,
		encoded, kubeVIPManifestPath,
	)
	self.Log.Info(fmt.Sprintf("kube-vip: writing static-pod manifest on %s (vip=%s iface=%s)", self.Name, vip, iface))
	if _, err := self.Shell.Run(ctx, cmd); err != nil {
		return fmt.Errorf("write kube-vip manifest on %s: %w", self.Name, err)
	}
	return nil
}
