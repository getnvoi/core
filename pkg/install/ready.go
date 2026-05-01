package install

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/getnvoi/core/pkg/log"
	"github.com/getnvoi/core/pkg/ssh"
)

// WaitNodeReady polls kubectl on the given shell until the named node
// shows Ready=True. Used after primary install (the primary checks
// itself) and after secondary/worker joins (any master can poll).
//
// 3-minute timeout — k3s typically reaches Ready within 30-60s on a
// fresh node, but apt-update / pull cycles on slow networks can push
// it longer. Tighter timeouts mask real connectivity issues; looser
// ones make CI feedback slow.
func WaitNodeReady(ctx context.Context, sh ssh.Shell, hostname string, lg log.Log) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	first := true
	for {
		out, err := Kubectl(ctx, sh, "get", "nodes", "-o",
			`jsonpath='{range .items[*]}{.status.conditions[?(@.type=="Ready")].status},{.metadata.name}{"\n"}{end}'`)
		if err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				parts := strings.SplitN(strings.Trim(line, "'"), ",", 2)
				if len(parts) == 2 && parts[0] == "True" && nodeNameMatches(parts[1], hostname) {
					return nil
				}
			}
		}
		if first {
			lg.Info(fmt.Sprintf("waiting for node %s to be Ready...", hostname))
			first = false
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("node %s not Ready: %w", hostname, ctx.Err())
		case <-tick.C:
		}
	}
}

// nodeNameMatches handles k3s's hostname normalization. k3s node
// names are the lowercased hostname the kernel reports.
func nodeNameMatches(actual, expected string) bool {
	return strings.EqualFold(strings.TrimSpace(actual), expected)
}
