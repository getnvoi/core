package install

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/getnvoi/core/internal/ssh"
	"github.com/getnvoi/core/internal/utils"
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
func Kubectl(ctx context.Context, sh ssh.Shell, args ...string) ([]byte, error) {
	return sh.Run(ctx, "sudo k3s kubectl "+strings.Join(args, " "))
}

// KubectlStream runs `sudo k3s kubectl <args>` with output streamed to
// the given writers. For verbose ops where the operator wants progress
// in real time (apply -f, port-forward, logs follow, …).
func KubectlStream(ctx context.Context, sh ssh.Shell, stdout, stderr io.Writer, args ...string) error {
	return sh.RunStream(ctx, "sudo k3s kubectl "+strings.Join(args, " "), stdout, stderr)
}

// KubectlExec runs `kubectl exec <target> -- <userArgs>` over the
// supplied shell, streaming stdout/stderr to the writers. Each entry
// in userArgs is shell-quoted before being joined into the remote
// command line so things like `SELECT COUNT(*) FROM visits` reach the
// container as a single argument unmolested by the master's bash.
//
// target is a kubectl resource shorthand (`deployment/<name>`,
// `statefulset/<name>`, or a literal pod name). It is NOT shell-quoted
// — these are DNS-1123 names with no shell-special characters by spec.
//
// v1 is non-interactive only: stdin is closed, no PTY allocation. Long
// commands stream output back as they run; on completion the remote
// process's exit code surfaces as the function's error.
func KubectlExec(ctx context.Context, sh ssh.Shell, target string, userArgs []string, stdout, stderr io.Writer) error {
	if target == "" {
		return fmt.Errorf("kubectl exec: empty target")
	}
	if len(userArgs) == 0 {
		return fmt.Errorf("kubectl exec: empty command")
	}
	parts := make([]string, 0, 3+len(userArgs))
	parts = append(parts, "exec", target, "--")
	for _, a := range userArgs {
		parts = append(parts, utils.ShellQuote(a))
	}
	return sh.RunStream(ctx, "sudo k3s kubectl "+strings.Join(parts, " "), stdout, stderr)
}
