package deploy

import (
	"context"
	"fmt"

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
