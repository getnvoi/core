package install

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/getnvoi/core/pkg/internal/utils"
	"github.com/getnvoi/core/pkg/ssh"
)

// KubectlSpec carries the inputs to a `sudo k3s kubectl <args>` run
// against a master shell. Shared by KubectlStream (long-running stream)
// and KubectlExec (target + user-args wrapper) so the writer pair and
// shell handle have one canonical home.
type KubectlSpec struct {
	Shell  ssh.Shell // master shell — workers can't kubectl
	Args   []string  // verb + flags + positional args (joined with spaces)
	Stdout io.Writer
	Stderr io.Writer
}

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

// KubectlStream runs `sudo k3s kubectl <spec.Args>` against spec.Shell
// with stdout/stderr streamed to spec.Stdout/spec.Stderr. For verbose
// ops where the operator wants progress in real time (apply -f,
// port-forward, logs follow, …).
func KubectlStream(ctx context.Context, spec KubectlSpec) error {
	return spec.Shell.RunStream(ctx, "sudo k3s kubectl "+strings.Join(spec.Args, " "), spec.Stdout, spec.Stderr)
}

// KubectlExec runs `kubectl exec <target> -- <spec.Args>` over
// spec.Shell, streaming stdout/stderr to the writers. Each entry in
// spec.Args is shell-quoted before being joined into the remote
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
//
// Implementation reuses KubectlStream by prepending `exec target --`
// to the args — single source of truth for the kubectl-over-SSH path.
func KubectlExec(ctx context.Context, target string, spec KubectlSpec) error {
	if target == "" {
		return fmt.Errorf("kubectl exec: empty target")
	}
	if len(spec.Args) == 0 {
		return fmt.Errorf("kubectl exec: empty command")
	}
	wrapped := make([]string, 0, 3+len(spec.Args))
	wrapped = append(wrapped, "exec", target, "--")
	for _, a := range spec.Args {
		wrapped = append(wrapped, utils.ShellQuote(a))
	}
	spec.Args = wrapped
	return KubectlStream(ctx, spec)
}
