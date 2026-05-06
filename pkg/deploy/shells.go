package deploy

import (
	"context"
	"fmt"

	"github.com/getnvoi/core/pkg/install"
	"github.com/getnvoi/core/pkg/internal/utils"
	"github.com/getnvoi/core/pkg/log"
	"github.com/getnvoi/core/pkg/internal/runner"
	"github.com/getnvoi/core/pkg/runtime"
	"github.com/getnvoi/core/pkg/ssh"
)

// openShells dials SSH to every server in eps.Servers and returns
// the map. On failure mid-way, any already-open shells are closed
// so we never leak. Caller takes ownership of the returned map and
// is responsible for `defer closeShells(shells)`.
//
// lg should be the cluster-scoped logger — opening shells is a
// precondition for k3s install + kube tunnel, both kind=cluster.
func openShells(ctx context.Context, rt *runtime.Runtime, lg log.Log, eps *runner.Endpoints) (map[string]*ssh.Client, error) {
	shells := make(map[string]*ssh.Client, len(eps.Servers))
	for _, name := range utils.SortedKeys(eps.Servers) {
		srv := eps.Servers[name]
		sh, err := install.WaitForSSH(ctx, srv.IPv4, rt.SSHPrivKey, lg)
		if err != nil {
			closeShells(shells)
			return nil, fmt.Errorf("ssh %s (%s): %w", name, srv.IPv4, err)
		}
		shells[name] = sh
		lg.Info(fmt.Sprintf("ssh %s ready (%s)", name, srv.IPv4))
	}
	return shells, nil
}

func closeShells(shells map[string]*ssh.Client) {
	for _, sh := range shells {
		_ = sh.Close()
	}
}

// masterShellsOnly filters the full per-server shell map down to
// masters and returns a Shell-typed map (Go map types are invariant,
// so we widen at the boundary where install.DiscoverToken expects
// ssh.Shell rather than *ssh.Client).
func masterShellsOnly(shells map[string]*ssh.Client, eps *runner.Endpoints) map[string]ssh.Shell {
	out := make(map[string]ssh.Shell)
	for _, name := range eps.Masters() {
		if sh, ok := shells[name]; ok {
			out[name] = sh
		}
	}
	return out
}
