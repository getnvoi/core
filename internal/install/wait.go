// Package install runs the post-tofu-apply bootstrap stages: wait
// for SSH, ensure swap, install/join k3s. All operations idempotent —
// safe to re-run on a converged cluster.
package install

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/getnvoi/core/internal/log"
	"github.com/getnvoi/core/internal/ssh"
)

// DefaultUser is the unprivileged SSH login cloud-init creates.
// Matches cloudinit.Render's hardcoded user.
const DefaultUser = "deploy"

// WaitForSSH polls Dial until the box accepts SSH or ctx expires.
// Cloud-init typically takes 20-60s on Hetzner before the deploy user
// is ready. Auth failures fall through (transient — sshd not yet up
// with the right keys); host-key/protocol errors do too. Only ctx
// cancellation aborts.
func WaitForSSH(ctx context.Context, addr string, key []byte, lg log.Log) (*ssh.Client, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	hostPort := addr
	if !strings.Contains(hostPort, ":") {
		hostPort = addr + ":22"
	}

	deadline := time.NewTicker(2 * time.Second)
	defer deadline.Stop()

	for {
		client, err := ssh.Dial(ctx, hostPort, DefaultUser, key)
		if err == nil {
			return client, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("ssh wait %s: %w (last error: %v)", hostPort, ctx.Err(), err)
		case <-deadline.C:
			lg.Info(fmt.Sprintf("waiting for ssh on %s...", hostPort))
		}
	}
}
