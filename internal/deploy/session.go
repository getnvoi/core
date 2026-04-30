// Package deploy is the orchestration layer above internal/build,
// internal/install, internal/detach, internal/kube, internal/runner,
// internal/workload. Three exported entry points — Run / Destroy / Plan
// — back the deploy/destroy/plan verbs. RunWithSession is a fourth
// entry point used by ad-hoc cmd/cli verbs (ssh / exec / logs /
// kubectl) that need a Session to read endpoints + dial SSH.
//
// cmd/cli stays a thin OS boundary (env, disk, alias expansion,
// runtime.Build); every workflow lives here.
package deploy

import (
	"context"
	"fmt"

	"github.com/getnvoi/core/internal/install"
	"github.com/getnvoi/core/internal/kube"
	"github.com/getnvoi/core/internal/log"
	"github.com/getnvoi/core/internal/runner"
	"github.com/getnvoi/core/internal/runtime"
	"github.com/getnvoi/core/internal/ssh"
)

// Session is the universal lifecycle bag. Carries the runtime, the tf
// runner, the kind-scoped logger, plus deploy-time state that
// accumulates as the run progresses (endpoints, shells, kube client).
//
// Every cmd/cli verb gets one (built by RunWithSession). Methods on
// *Session take only ctx + their narrow per-call inputs — the (rt,
// run, lg) triplet that used to thread through 5+ functions lives on
// the receiver.
//
// Field names are exported so cmd/cli verb closures can read what
// they need (s.Lg.Stream(), s.Rt.Cfg.PrimaryMaster()) without going
// through accessors.
type Session struct {
	Rt  *runtime.Runtime
	Run *runner.Runner
	Lg  log.Log

	// Endpoints is memoized by the Endpoints() method — tfexec's
	// `terraform output -json` is a subprocess invocation; calling it
	// once per deploy beats the 3+ calls the pre-Session shape used
	// to make.
	eps *runner.Endpoints

	// shells / kc are deploy-only. Populated inside Run after
	// openShells / kube.New respectively. Verb code (ssh / exec / etc.)
	// uses OnNode which dials a single fresh shell instead.
	shells map[string]*ssh.Client
	kc     *kube.Client

	// inited tracks whether tf-init has run on this Session's
	// Runner. tfexec's Endpoints/Plan/Apply all require an
	// initialised workdir. Init() is idempotent so verbs and Run
	// can both call it without coordination.
	inited bool
}

// RunWithSession is the entry point cmd/cli verbs use when they need
// a Session (compile + tf-init + endpoints + SSH). Wraps WithRunner:
// builds a kind-scoped Session, hands it to fn, returns fn's error.
func RunWithSession(ctx context.Context, rt *runtime.Runtime, kind log.Kind, fn func(context.Context, *Session) error) error {
	return WithRunner(ctx, rt, func(ctx context.Context, run *runner.Runner) error {
		s := &Session{Rt: rt, Run: run, Lg: rt.Log.Sub(kind)}
		return fn(ctx, s)
	})
}

// Init runs `terraform init` if it hasn't run yet on this Session's
// Runner. Idempotent — second call is a silent no-op. Caller emits
// any operator-facing Step marker (deploy.Run does, cmd/cli verbs
// don't — verbs treat init as setup noise).
func (s *Session) Init(ctx context.Context) error {
	if s.inited {
		return nil
	}
	if err := s.Run.Init(ctx); err != nil {
		return err
	}
	s.inited = true
	return nil
}

// Endpoints returns the parsed terraform output, memoized. First call
// invokes tf-init (if needed) then `terraform output -json`;
// subsequent calls return the cached pointer. This is the single read
// point — predrain, deploy post-apply, and ad-hoc verbs (ssh / exec /
// etc.) all share it, so tfexec runs the subprocess at most once per
// command.
func (s *Session) Endpoints(ctx context.Context) (*runner.Endpoints, error) {
	if s.eps != nil {
		return s.eps, nil
	}
	if err := s.Init(ctx); err != nil {
		return nil, err
	}
	eps, err := s.Run.Endpoints(ctx)
	if err != nil {
		return nil, err
	}
	s.eps = eps
	return eps, nil
}

// OnNode dials a fresh SSH connection to the named server (looking up
// its IPv4 in Endpoints), runs action with that shell, closes the
// shell. Used by the cmd/cli verbs that touch a single node — ssh,
// exec, logs, kubectl.
//
// Errors with a clear message if the server isn't in terraform state
// (operator hasn't run `nvoi deploy` yet).
func (s *Session) OnNode(ctx context.Context, target string, action func(*ssh.Client) error) error {
	eps, err := s.Endpoints(ctx)
	if err != nil {
		return err
	}
	srv, ok := eps.Servers[target]
	if !ok {
		return fmt.Errorf("server %q not in terraform state — run `nvoi deploy` first", target)
	}
	sh, err := ssh.Dial(ctx, srv.IPv4+":22", install.DefaultUser, s.Rt.SSHPrivKey)
	if err != nil {
		return err
	}
	defer sh.Close()
	return action(sh)
}

// OnPrimary is OnNode shortcut for the primary master — every verb
// that runs kubectl/exec routes through the primary.
func (s *Session) OnPrimary(ctx context.Context, action func(*ssh.Client) error) error {
	return s.OnNode(ctx, s.Rt.Cfg.PrimaryMaster(), action)
}

// Kube returns the cached kube client. Populated inside Run after
// kube.New runs against the primary's shell. Returns nil for sessions
// that haven't built the kube tunnel (cmd/cli verbs that don't need it).
func (s *Session) Kube() *kube.Client { return s.kc }
