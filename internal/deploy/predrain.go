package deploy

import (
	"context"
	"fmt"

	"github.com/getnvoi/core/internal/compile"
	"github.com/getnvoi/core/internal/config"
	"github.com/getnvoi/core/internal/detach"
	"github.com/getnvoi/core/internal/install"
	"github.com/getnvoi/core/internal/kube"
	"github.com/getnvoi/core/internal/naming"
	"github.com/getnvoi/core/internal/runner"
	"github.com/getnvoi/core/internal/runtime"
	"github.com/getnvoi/core/internal/ssh"
)

// predrainBody is the per-action work a predrain runs once it has SSH
// to the survivor / control-plane master. The action is responsible
// for whatever drain-style work the destroy flavour needs (sweep
// kube-side resources, kubectl-drain via the master, etc.).
//
// Failures here are NOT fatal — predrain warns and the caller
// proceeds to tf-apply, which will surface the underlying provider
// error (e.g. CF "active connections") with full context if the drain
// was actually required.
type predrainBody func(ctx context.Context, rt *runtime.Runtime, sh *ssh.Client) error

// predrain is the shared skeleton for the two pre-tf-apply drain
// steps (detachNode + drainTunnel):
//
//  1. Filter the saved plan for resources of `resourceType` going to
//     Delete (or Replace, which is delete+create).
//  2. If empty, return nil — no work.
//  3. Pick a "control" master (one NOT in the leaving set for nodes;
//     primary for tunnels).
//  4. Look up the control master's IP in eps (cached on the session)
//     or read it fresh.
//  5. Dial SSH, hand off to body(ctx, rt, sh).
//  6. Warn-and-continue on EVERY failure path so a misconfigured
//     drain can't block tf-apply.
//
// label is the operator-facing step name printed before the work
// starts (e.g. "drain-tunnel", "detach"). leaving is reported as part
// of the info line so the operator sees what's being drained.
func predrain(
	ctx context.Context,
	rt *runtime.Runtime,
	run *runner.Runner,
	planPath string,
	resourceType string,
	control string,
	leaving []string,
	label string,
	body predrainBody,
) error {
	if len(leaving) == 0 {
		return nil
	}

	// Read CURRENT state (pre-apply) to find the control master's IP.
	eps, err := run.Endpoints(ctx)
	if err != nil {
		// Cold start: state file may not have outputs yet (first
		// apply). In that case there's nothing to drain — the
		// cluster doesn't exist and the "destroys" are spurious.
		// Warn-and-continue.
		rt.Log.Warn(fmt.Sprintf("read endpoints for %s: %s — skipping", label, err))
		return nil
	}
	srv, ok := eps.Servers[control]
	if !ok || srv.IPv4 == "" {
		rt.Log.Warn(fmt.Sprintf("control master %s not in current state — skipping %s", control, label))
		return nil
	}

	rt.Log.Step(label)
	rt.Log.Info(fmt.Sprintf("draining %d %s(s): %v", len(leaving), resourceType, leaving))

	sh, err := ssh.Dial(ctx, srv.IPv4+":22", install.DefaultUser, rt.SSHPrivKey)
	if err != nil {
		rt.Log.Warn(fmt.Sprintf("ssh control %s for %s: %s — skipping", control, label, err))
		return nil
	}
	defer sh.Close()

	if err := body(ctx, rt, sh); err != nil {
		rt.Log.Warn(fmt.Sprintf("%s: %s", label, err))
	}
	return nil
}

// detachNode inspects the saved plan, identifies servers about to be
// destroyed (including replacements — change of server_type, region,
// etc.), and detaches each from the cluster via a surviving master
// (drain + kubectl delete node). Best-effort — failures warn but
// don't block apply.
//
// Skipped when:
//   - no nodes are being destroyed
//   - all masters are being destroyed (no surviving control plane)
//   - we can't dial the survivor (state may not yet have IPs for a
//     freshly-created cluster — detach is moot if nothing exists)
func detachNode(ctx context.Context, rt *runtime.Runtime, run *runner.Runner, planPath string) error {
	serverType, err := compile.ServerResourceType(rt.Cfg.Providers.Infra)
	if err != nil {
		return fmt.Errorf("server resource type: %w", err)
	}
	leaving, err := run.PlannedNodeDestroys(ctx, planPath, serverType)
	if err != nil {
		return fmt.Errorf("plan destroys: %w", err)
	}

	survivor := pickSurvivorMaster(rt.Cfg, leaving)
	if survivor == "" && len(leaving) > 0 {
		rt.Log.Warn(fmt.Sprintf("all masters being destroyed (leaving: %v) — skipping detach", leaving))
		return nil
	}

	return predrain(ctx, rt, run, planPath, serverType, survivor, leaving, "detach",
		func(ctx context.Context, rt *runtime.Runtime, sh *ssh.Client) error {
			nodes := make([]detach.Node, len(leaving))
			for i, key := range leaving {
				nodes[i] = detach.Node{
					Hostname: naming.Server(rt.Cfg.App, rt.Cfg.Env, key),
					Role:     rt.Cfg.Servers[key].Role,
				}
			}
			detach.Nodes(ctx, sh, nodes, rt.Log)
			return nil
		},
	)
}

// drainTunnel inspects the saved plan and, if any tunnel resource is
// going to delete (or replace), pre-emptively kills the in-cluster
// cloudflared agent before tf-apply runs. CF's API rejects tunnel
// DELETE while connections are alive — see
// terraform-provider-cloudflare#5255 — and the provider has no
// force_destroy, no /connections cleanup call, and no retry. Killing
// the agent here drops the connections so tf-apply succeeds in one
// pass.
//
// Plan-driven: zero cost on no-op deploys (filter returns empty);
// fires automatically on `nvoi destroy`, on Caddy←tunnel mode
// switches, and on tunnel-replacement (rename, secret rotation).
//
// Same shape as detachNode — best-effort, warn-and-continue on
// kube-unreachable so a misconfigured drain can't block tf-apply.
// If sweep fails, tf-apply will surface CF's "active connections"
// error with the same clarity it does today.
func drainTunnel(ctx context.Context, rt *runtime.Runtime, run *runner.Runner, planPath string) error {
	if rt.Cfg.Providers.Tunnel == "" {
		return nil
	}
	tunnelType, err := compile.TunnelResourceType(rt.Cfg.Providers.Tunnel)
	if err != nil {
		return fmt.Errorf("tunnel resource type: %w", err)
	}
	leaving, err := run.PlannedTunnelDestroys(ctx, planPath, tunnelType)
	if err != nil {
		return fmt.Errorf("plan tunnel destroys: %w", err)
	}

	primary := rt.Cfg.PrimaryMaster()
	return predrain(ctx, rt, run, planPath, tunnelType, primary, leaving, "drain-tunnel",
		func(ctx context.Context, rt *runtime.Runtime, sh *ssh.Client) error {
			kc, err := kube.New(ctx, sh)
			if err != nil {
				return fmt.Errorf("kube tunnel: %w", err)
			}
			defer kc.Close()

			// Sweep every owner=tunnel-agent resource — pod
			// termination drops cloudflared's outbound connections,
			// which clears CF's active-connections gate on the
			// subsequent tf-apply DELETE.
			for _, kind := range []kube.Kind{
				kube.KindDeployment, kube.KindSecret, kube.KindConfigMap,
			} {
				if err := kc.SweepOwned(ctx, "default", kube.OwnerTunnelAgent, kind, nil); err != nil {
					rt.Log.Warn(fmt.Sprintf("sweep tunnel-agent %s: %s", kind, err))
				}
			}
			return nil
		},
	)
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
	// determinism — pick first alphabetical
	pick := candidates[0]
	for _, c := range candidates[1:] {
		if c < pick {
			pick = c
		}
	}
	return pick
}
