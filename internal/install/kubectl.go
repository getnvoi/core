package install

import (
	"context"
	"io"
	"strings"

	"github.com/getnvoi/core/internal/ssh"
)

// Kubectl runs `sudo k3s kubectl <args>` on a master shell and returns
// combined output. Works without kubeconfig setup because k3s embeds
// kubectl as a subcommand that reads /etc/rancher/k3s/k3s.yaml
// directly via root.
//
// Workers cannot run this — they're agents only, no kube-apiserver
// access. Caller must pass a master shell.
//
// Args are joined with spaces; quote/escape at the call site if a
// single arg contains shell-special characters.
func Kubectl(ctx context.Context, sh *ssh.Client, args ...string) ([]byte, error) {
	return sh.Run(ctx, "sudo k3s kubectl "+strings.Join(args, " "))
}

// KubectlStream runs `sudo k3s kubectl <args>` with output streamed to
// the given writers. For verbose ops where the operator wants progress
// in real time (apply -f, port-forward, logs follow, …).
func KubectlStream(ctx context.Context, sh *ssh.Client, stdout, stderr io.Writer, args ...string) error {
	return sh.RunStream(ctx, "sudo k3s kubectl "+strings.Join(args, " "), stdout, stderr)
}
