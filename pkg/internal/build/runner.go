// Package build runs `docker buildx build --push` for every service
// declared with a `build:` block in YAML. Conditional: invoked only
// when at least one service has build set. Runs PRE-tf-apply so
// build failures abort before any infra change.
//
// Auth flow: the `registry:` YAML block is the single source of
// truth for credentials. Before the first build, we run
// `docker login <host> -u <user> --password-stdin` for every host
// that any build pushes to. The same creds get rendered into the
// cluster-side imagePullSecret. Operators do not need to be
// pre-logged-in — the YAML carries everything.
package build

import (
	"context"
	"io"
)

// Request is one image to build + push.
type Request struct {
	ServiceName string // YAML key — for log lines
	Context     string // build context dir
	Dockerfile  string // path to Dockerfile inside Context
	Tag         string // fully-qualified destination image with deploy hash
}

// Runner is the build executor. Production uses DockerRunner (shells
// out to `docker`). Tests substitute a fake that records calls.
type Runner interface {
	// Preflight verifies docker daemon reachable + buildx plugin
	// available. Called once before the Login + Build loop. Errors
	// are explicit and actionable ("Docker Desktop not running",
	// "buildx not installed").
	Preflight(ctx context.Context) error

	// Login authenticates the local docker daemon against `host`
	// using YAML-supplied creds. Idempotent — `docker login`
	// overwrites any existing entry. Password is fed via stdin so
	// it never appears in argv (no shell history leak).
	Login(ctx context.Context, host, username, password string) error

	// Build executes `docker buildx build --push` for one Request.
	// Streams stdout/stderr to the supplied writers.
	Build(ctx context.Context, req Request, stdout, stderr io.Writer) error
}
