package install

import (
	"context"
	"strings"

	"github.com/getnvoi/core/internal/ssh"
)

// DiscoverToken queries every master in turn for the cluster's k3s
// node-token. Returns the token + true when any master is up and
// running k3s (cluster exists). Returns "" + false when no master
// has k3s yet (cold start). Errors only on unexpected failures.
//
// The convention: a running cluster is its own source of truth. We
// never re-bootstrap an existing cluster. The token is the same
// across every master (k3s shares it via etcd).
func DiscoverToken(ctx context.Context, masterShells map[string]*ssh.Client) (token string, found bool, err error) {
	for _, sh := range masterShells {
		// k3s server active AND token file exists?
		if _, e := sh.Run(ctx, "systemctl is-active --quiet k3s"); e != nil {
			continue
		}
		out, e := sh.Run(ctx, "sudo cat "+tokenPath)
		if e != nil {
			// k3s active but no token = unusual. Try next master.
			continue
		}
		t := strings.TrimSpace(string(out))
		if t == "" {
			continue
		}
		return t, true, nil
	}
	return "", false, nil
}
