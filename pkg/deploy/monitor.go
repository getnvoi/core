package deploy

// monitor.go is the `nvoi monitor` verb's workflow. Opens an SSH-
// tunneled port-forward from the operator's localhost:<port> to the
// in-cluster Grafana Service, blocks until ctx is cancelled (typically
// ctrl-C from the operator). No public network exposure required —
// even when monitor.domain is unset, `nvoi monitor` works.
//
// Same Session primitive as every other verb. RunWithSession compiles +
// tf-inits + reads endpoints; OnPrimary opens the SSH session to the
// primary master; kube.New tunnels the apiserver; kc.PortForward
// rides the same tunnel down to the Grafana pod's :3000.

import (
	"context"
	"fmt"

	"github.com/getnvoi/core/pkg/internal/kube"
	"github.com/getnvoi/core/pkg/internal/observability"
	"github.com/getnvoi/core/pkg/log"
	"github.com/getnvoi/core/pkg/runtime"
	"github.com/getnvoi/core/pkg/ssh"
)

// Monitor opens a local port-forward to the in-cluster Grafana
// Service. Blocks until ctx is cancelled.
//
// Pre-conditions:
//   - rt.Cfg.Monitor must be non-nil. Errors clearly otherwise.
//   - Cluster must exist (tofu state must report endpoints) — verified
//     by Session.OnPrimary's endpoint lookup.
//
// localPort is the operator-chosen bind port (default 3000 matches
// Grafana's pod port for memorability).
func Monitor(ctx context.Context, rt *runtime.Runtime, localPort int) error {
	if rt.Cfg.Monitor == nil {
		return fmt.Errorf("nvoi monitor: `monitor:` block not configured in nvoi.yaml")
	}

	return RunWithSession(ctx, rt, log.KindMonitor, func(ctx context.Context, s *Session) error {
		return s.OnPrimary(ctx, func(sh *ssh.Client) error {
			kc, err := kube.New(ctx, sh)
			if err != nil {
				return fmt.Errorf("kube tunnel: %w", err)
			}
			defer kc.Close()

			s.Lg.Info(fmt.Sprintf("forwarding http://localhost:%d → grafana.%s.svc:3000",
				localPort, observability.Namespace))
			s.Lg.Info(fmt.Sprintf("open http://localhost:%d in your browser", localPort))
			s.Lg.Info("press ctrl-c to disconnect")

			return kc.PortForward(ctx, observability.Namespace, "grafana", localPort, 3000)
		})
	})
}
