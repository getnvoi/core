package deploy

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/install"
	"github.com/getnvoi/core/pkg/internal/compile"
	"github.com/getnvoi/core/pkg/internal/detach"
	"github.com/getnvoi/core/pkg/naming"
	"github.com/getnvoi/core/pkg/ssh"
)

// predrainSpec describes one pre-tf-apply drain step. Run / Destroy
// build a spec per drain target (nodes leaving → detachNode) and hand
// it to (*Session).predrain.
//
// Body closes over whatever it needs from the enclosing scope (Session
// + spec data); predrain itself stays narrow.
type predrainSpec struct {
	PlanPath     string
	ResourceType string                                          // tf resource type to filter the plan for (e.g. "hcloud_server")
	Control      string                                          // YAML key of the master to dial for the drain (survivor / primary)
	Leaving      []string                                        // names the plan will delete; if empty, predrain is a no-op
	Label        string                                          // operator-facing step name ("detach")
	Body         func(ctx context.Context, sh *ssh.Client) error // per-flavour drain work (kubectl drain, etc.)
}

// predrain is the shared skeleton for pre-tf-apply drain steps:
//
//  1. If spec.Leaving is empty, return nil — no work.
//  2. Look up the control master's IPv4 in the session's memoized
//     Endpoints (single tf-output read across the whole command).
//  3. Dial SSH, hand off to spec.Body(ctx, sh).
//  4. Warn-and-continue on EVERY failure path so a misconfigured
//     drain can't block tf-apply. tf-apply will surface the underlying
//     provider error (e.g. CF "active connections") if the drain was
//     actually required.
func (s *Session) predrain(ctx context.Context, spec predrainSpec) error {
	if len(spec.Leaving) == 0 {
		return nil
	}

	eps, err := s.Endpoints(ctx)
	if err != nil {
		// Cold start: state file may not have outputs yet (first
		// apply). In that case there's nothing to drain — the cluster
		// doesn't exist and the "destroys" are spurious.
		s.Lg.Warn(fmt.Sprintf("read endpoints for %s: %s — skipping", spec.Label, err))
		return nil
	}
	srv, ok := eps.Servers[spec.Control]
	if !ok || srv.IPv4 == "" {
		s.Lg.Warn(fmt.Sprintf("control master %s not in current state — skipping %s", spec.Control, spec.Label))
		return nil
	}

	s.Lg.Step(spec.Label)
	s.Lg.Info(fmt.Sprintf("draining %d %s(s): %v", len(spec.Leaving), spec.ResourceType, spec.Leaving))

	sh, err := ssh.Dial(ctx, srv.IPv4+":22", install.DefaultUser, s.Rt.SSHPrivKey)
	if err != nil {
		s.Lg.Warn(fmt.Sprintf("ssh control %s for %s: %s — skipping", spec.Control, spec.Label, err))
		return nil
	}
	defer sh.Close()

	if err := spec.Body(ctx, sh); err != nil {
		s.Lg.Warn(fmt.Sprintf("%s: %s", spec.Label, err))
	}
	return nil
}

// detachNode inspects the saved plan, identifies servers about to be
// destroyed (including replacements), and detaches each from the
// cluster via a surviving master (drain + kubectl delete node).
// Best-effort — failures warn but don't block apply.
func (s *Session) detachNode(ctx context.Context, planPath string) error {
	serverType, err := compile.ServerResourceType(s.Rt.Cfg.Providers.Infra)
	if err != nil {
		return fmt.Errorf("server resource type: %w", err)
	}
	leaving, err := s.Run.PlannedNodeDestroys(ctx, planPath, serverType)
	if err != nil {
		return fmt.Errorf("plan destroys: %w", err)
	}

	survivor := pickSurvivorMaster(s.Rt.Cfg, leaving)
	if survivor == "" && len(leaving) > 0 {
		s.Lg.Warn(fmt.Sprintf("all masters being destroyed (leaving: %v) — skipping detach", leaving))
		return nil
	}

	return s.predrain(ctx, predrainSpec{
		PlanPath:     planPath,
		ResourceType: serverType,
		Control:      survivor,
		Leaving:      leaving,
		Label:        "detach",
		Body: func(ctx context.Context, sh *ssh.Client) error {
			nodes := make([]detach.Node, len(leaving))
			for i, key := range leaving {
				nodes[i] = detach.Node{
					Hostname: naming.Server(s.Rt.Cfg.App, s.Rt.Cfg.Env, key),
					Role:     s.Rt.Cfg.Servers[key].Role,
				}
			}
			detach.Nodes(ctx, sh, nodes, s.Lg)
			return nil
		},
	})
}

// pickSurvivorMaster returns the YAML key of any master NOT in the
// leaving set — used as the SSH source for detach. Picks the
// alphabetically-first survivor for determinism.
func pickSurvivorMaster(cfg *config.Config, leaving []string) string {
	leavingSet := make(map[string]bool, len(leaving))
	for _, d := range leaving {
		leavingSet[d] = true
	}
	candidates := make([]string, 0, len(cfg.Servers))
	for name, srv := range cfg.Servers {
		if srv.Role == "master" && !leavingSet[name] {
			candidates = append(candidates, name)
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	pick := candidates[0]
	for _, c := range candidates[1:] {
		if c < pick {
			pick = c
		}
	}
	return pick
}

// drainCertificates deletes cert-manager Certificate resources cluster-wide
// before tofu destroys the cluster. cert-manager's Challenge finalizer
// runs the DNS-01 solver's Cleanup() in response, which removes the
// `_acme-challenge.<domain>` TXT records via the DNS provider's API.
// Without this step, those scratch TXT records orphan in the operator's
// zone — tofu doesn't manage them (cert-manager wrote them out-of-band)
// so `tf destroy` can't clean them up.
//
// Best-effort throughout: every failure path warns and proceeds with the
// destroy. Worst case is the TXT records stay orphan, which is the
// status quo without this step. Bounded waits (60s) keep the destroy
// from hanging if cert-manager is unhealthy.
//
// Skipped silently when:
//   - Endpoints output is unreadable (cluster never existed / state corrupt)
//   - No master is reachable (everything's already torn down)
//   - SSH to master fails (master is down — destroy will clean it up regardless)
//   - No Certificate resources exist (cluster never had domains)
func (s *Session) drainCertificates(ctx context.Context) error {
	eps, err := s.Run.Endpoints(ctx)
	if err != nil {
		s.Lg.Warn(fmt.Sprintf("drain-cert-manager: cannot read endpoints (%v); skipping", err))
		return nil
	}

	// Pick any reachable master. Destroy targets ALL nodes; we just
	// need one alive long enough to issue the kubectl delete.
	var masterName string
	for name, srv := range eps.Servers {
		if srv.Role == "master" && srv.IPv4 != "" {
			masterName = name
			break
		}
	}
	if masterName == "" {
		return nil
	}
	srv := eps.Servers[masterName]

	sh, err := ssh.Dial(ctx, srv.IPv4+":22", install.DefaultUser, s.Rt.SSHPrivKey)
	if err != nil {
		s.Lg.Warn(fmt.Sprintf("drain-cert-manager: ssh %s (%s): %v; skipping", masterName, srv.IPv4, err))
		return nil
	}
	defer sh.Close()

	out, err := install.Kubectl(ctx, sh, "get", "certificate", "-A", "-o", "name", "--ignore-not-found")
	if err != nil {
		// Likely cert-manager CRDs aren't installed (cluster never had
		// domains). That's the silent-skip case.
		return nil
	}
	if strings.TrimSpace(string(out)) == "" {
		return nil
	}

	s.Lg.Step("drain-cert-manager")
	s.Lg.Info("deleting Certificate resources; cert-manager finalizers clean up _acme-challenge TXT records via the DNS provider API")

	if out, err := install.Kubectl(ctx, sh, "delete", "certificate", "--all", "-A", "--timeout=60s"); err != nil {
		s.Lg.Warn(fmt.Sprintf("drain-cert-manager: delete: %v (out: %s)", err, strings.TrimSpace(string(out))))
		// Continue to wait — kubectl delete may have queued the
		// deletion even if the CLI returned an error.
	}
	if out, err := install.Kubectl(ctx, sh, "wait", "--for=delete", "certificate", "--all", "-A", "--timeout=60s"); err != nil {
		s.Lg.Warn(fmt.Sprintf("drain-cert-manager: wait: %v (out: %s) — proceeding to destroy; orphan TXT records possible", err, strings.TrimSpace(string(out))))
		return nil
	}
	s.Lg.Info("Certificate finalizers complete; TXT records cleaned via provider API")
	return nil
}
