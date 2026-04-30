package install

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/tf/internal/ssh"
)

// k3s constants — match nvoi's pkg/utils/naming.go for cross-codebase
// consistency. Once a cluster's CIDRs and paths are set, they don't
// change without a teardown.
const (
	clusterCIDR    = "10.42.0.0/16" // pod network
	serviceCIDR    = "10.43.0.0/16" // ClusterIP services
	kubeconfigPath = "/etc/rancher/k3s/k3s.yaml"
	tokenPath      = "/var/lib/rancher/k3s/server/node-token"
)

// Node is one server's network identity used during k3s install.
// Caller (deploy.go) populates from the YAML key + naming.Server.
type Node struct {
	Name     string // YAML key — operator-facing identifier
	Hostname string // naming.Server(app, env, name) — the actual k3s node name set via cloud-init
	IPv4     string // public IP (TLS SAN)
	Private  string // private IP (k3s --node-ip / --advertise-address)
}

// localNodeReady is the fast idempotency check at the top of every
// install function: does the local k3s think any node is Ready?
// Returns false (not an error) when k3s is not installed.
func localNodeReady(ctx context.Context, sh *ssh.Client) (bool, error) {
	// k3s binary present? If not, no install yet — quietly return false.
	if _, err := sh.Run(ctx, "command -v k3s >/dev/null 2>&1"); err != nil {
		return false, nil
	}
	out, err := Kubectl(ctx, sh, "get", "nodes", "-o",
		`jsonpath='{.items[*].status.conditions[?(@.type=="Ready")].status}'`)
	if err != nil {
		return false, nil
	}
	return strings.Contains(string(out), "True"), nil
}

// discoverPrivateInterface finds the linux interface name carrying
// the given private IP. Hetzner image varies (enp7s0, ens10, …) so
// we discover at runtime rather than hardcode.
func discoverPrivateInterface(ctx context.Context, sh *ssh.Client, privateIP string) (string, error) {
	cmd := fmt.Sprintf(`ip -o -4 addr show | awk '/%s/{print $2}' | head -1`, privateIP)
	out, err := sh.Run(ctx, cmd)
	if err != nil {
		return "", err
	}
	iface := strings.TrimSpace(string(out))
	if iface != "" {
		return iface, nil
	}
	return "", fmt.Errorf("no interface for private ip %s", privateIP)
}

// tlsSans builds the --tls-san flag string from a deduped list of
// IPs/names. Each entry becomes its own flag.
func tlsSans(addrs []string) string {
	parts := make([]string, 0, len(addrs))
	for _, a := range addrs {
		if a == "" {
			continue
		}
		parts = append(parts, "--tls-san "+a)
	}
	return strings.Join(parts, " ")
}

// dedupSANs removes blanks and duplicates while preserving order.
func dedupSANs(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
