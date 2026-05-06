package build

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
)

// DockerRunner is the production Runner: shells out to the operator's
// local docker CLI. Authentication comes from the YAML's `registry:`
// block (passed via Login below) — operators don't need to be
// pre-logged-in.
type DockerRunner struct{}

// Preflight verifies daemon + buildx. Returns explicit, actionable
// errors so the operator knows what to install / start.
func (DockerRunner) Preflight(ctx context.Context) error {
	if err := run(ctx, "docker", "info"); err != nil {
		return fmt.Errorf("docker daemon not reachable — is Docker Desktop running? (`docker info` failed)")
	}
	if err := run(ctx, "docker", "buildx", "version"); err != nil {
		return fmt.Errorf("docker buildx not available — install via Docker Desktop, or `docker buildx install`")
	}
	return nil
}

// Login runs `docker login <host> -u <user> --password-stdin` so the
// password never appears in argv. Stdout swallowed to keep deploy
// output clean (it normally prints "Login Succeeded" + auth warnings).
func (DockerRunner) Login(ctx context.Context, host, username, password string) error {
	cmd := exec.CommandContext(ctx, "docker", "login", host, "-u", username, "--password-stdin")
	cmd.Stdin = strings.NewReader(password)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	// Stdout discarded.
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker login %s: %w: %s", host, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// Build invokes `docker buildx build --push -t <tag> -f <dockerfile>
// <context>` and streams output to the supplied writers.
func (DockerRunner) Build(ctx context.Context, req Request, stdout, stderr io.Writer) error {
	args := []string{
		"buildx", "build", "--push",
		"-t", req.Tag,
		"-f", filepath.Join(req.Context, req.Dockerfile),
		"--progress=plain",
		req.Context,
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

// run is a tiny helper: silent docker invocation, error on non-zero
// exit. Used by Preflight for the daemon/buildx checks where output
// isn't useful.
func run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.Run()
}
