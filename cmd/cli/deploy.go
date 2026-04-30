package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/getnvoi/core/internal/build"
	"github.com/getnvoi/core/internal/compile"
	"github.com/getnvoi/core/internal/config"
	"github.com/getnvoi/core/internal/detach"
	"github.com/getnvoi/core/internal/install"
	"github.com/getnvoi/core/internal/kube"
	"github.com/getnvoi/core/internal/naming"
	"github.com/getnvoi/core/internal/runner"
	"github.com/getnvoi/core/internal/runtime"
	"github.com/getnvoi/core/internal/ssh"
	"github.com/getnvoi/core/internal/utils"
	"github.com/getnvoi/core/internal/workload"
)

func deployCmd(r *rt) *cobra.Command {
	return &cobra.Command{
		Use:   "deploy",
		Short: "Compile YAML → HCL, init + apply, install k3s",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			return runWith(ctx, r.runtime, func(ctx context.Context, run *runner.Runner) error {
				// ── pre-infra: build phase ──
				// Conditional: build.All is a no-op when no service has
				// build: set. Failures abort BEFORE any infra change.
				if err := build.All(ctx, r.runtime, build.DockerRunner{}, r.runtime.Log); err != nil {
					return err
				}

				r.runtime.Log.Step("tf-init")
				if err := run.Init(ctx); err != nil {
					return err
				}

				// ── plan → drain doomed nodes → apply ──
				// Plan path is RELATIVE to terraform's cwd (which is
				// already rt.WorkDir). Don't filepath.Join — that
				// double-resolves and terraform errors with "no such
				// directory."
				const planPath = "plan.tfplan"
				r.runtime.Log.Step("tf-plan")
				hasChanges, err := run.PlanWithOut(ctx, planPath)
				if err != nil {
					return err
				}
				if hasChanges {
					if err := detachNode(ctx, r.runtime, run, planPath); err != nil {
						return err
					}
					if err := drainTunnel(ctx, r.runtime, run, planPath); err != nil {
						return err
					}
					r.runtime.Log.Step("tf-apply")
					if err := run.ApplyPlan(ctx, planPath); err != nil {
						return err
					}
				} else {
					r.runtime.Log.Info("no terraform changes")
				}

				// ── post-apply: open SSH to every server ONCE,
				// thread through install + workloads, close all at
				// the end. Single SSH per server per command — same
				// pattern nvoi uses; same connection that installs k3s
				// also tunnels the kube apiserver for workloads.
				r.runtime.Log.Step("endpoints")
				eps, err := run.Endpoints(ctx)
				if err != nil {
					return err
				}
				r.runtime.Log.Step("ssh")
				shells, err := openShells(ctx, r.runtime, eps)
				if err != nil {
					return err
				}
				defer closeShells(shells)

				if err := installCluster(ctx, r.runtime, eps, shells); err != nil {
					return err
				}

				// ── workloads + ingress: kube tunnel via primary's shell ──
				// Skipped only when there's nothing for the in-cluster
				// pipeline to do — no services, no secrets to publish,
				// no registry to set up, no domains to front.
				if len(r.runtime.Cfg.Services) == 0 &&
					len(r.runtime.Cfg.Registry) == 0 &&
					len(r.runtime.Cfg.Secrets) == 0 &&
					len(r.runtime.Cfg.Domains) == 0 {
					return nil
				}
				return deployWorkloads(ctx, r.runtime, shells, eps)
			})
		},
	}
}

// openShells dials SSH to every server in eps.Servers and returns
// the map. On failure mid-way, any already-open shells are closed
// so we never leak. Caller takes ownership of the returned map and
// is responsible for `defer closeShells(shells)`.
func openShells(ctx context.Context, rt *runtime.Runtime, eps *runner.Endpoints) (map[string]*ssh.Client, error) {
	shells := make(map[string]*ssh.Client, len(eps.Servers))
	for _, name := range utils.SortedKeys(eps.Servers) {
		srv := eps.Servers[name]
		sh, err := install.WaitForSSH(ctx, srv.IPv4, rt.SSHPrivKey, rt.Log)
		if err != nil {
			closeShells(shells)
			return nil, fmt.Errorf("ssh %s (%s): %w", name, srv.IPv4, err)
		}
		shells[name] = sh
		rt.Log.Info(fmt.Sprintf("ssh %s ready (%s)", name, srv.IPv4))
	}
	return shells, nil
}

// installCluster is the post-terraform bootstrap pipeline:
//  1. ensure swap on every node
//  2. discover any existing k3s cluster (idempotency)
//  3. cold start: install --cluster-init on the primary master
//  4. join secondary masters via --server <primary>:6443
//  5. join workers via the LB private IP (or primary's private IP if N=1)
//
// Takes pre-opened shells from the caller — the same connections
// stay alive through the workloads phase via deployWorkloads(shells).
//
// Always uses --cluster-init regardless of master count: the cluster
// is etcd-backed from day one, so 1↔N migration is mechanical.
func installCluster(ctx context.Context, rt *runtime.Runtime, eps *runner.Endpoints, shells map[string]*ssh.Client) error {
	cfg := rt.Cfg
	primaryName := cfg.PrimaryMaster()
	if primaryName == "" {
		return fmt.Errorf("config has no master with primary: true (validator should have caught this)")
	}

	// 1. Swap on every node.
	rt.Log.Step("swap")
	for _, name := range utils.SortedKeys(eps.Servers) {
		if err := install.EnsureSwap(ctx, shells[name], rt.Log); err != nil {
			return fmt.Errorf("swap %s: %w", name, err)
		}
	}

	// 2. Build typed Nodes used by the install package (Hostname is
	//    derived from the YAML key via naming.Server).
	nodes := make(map[string]install.Node, len(eps.Servers))
	for name, srv := range eps.Servers {
		nodes[name] = install.Node{
			Name:     name,
			Hostname: naming.Server(cfg.App, cfg.Env, name),
			IPv4:     srv.IPv4,
			Private:  srv.Private,
		}
	}

	// LB IPs go in every master's --tls-san list so kubectl-via-LB
	// (HA case) and worker-join-via-LB validate cleanly.
	var extraSANs []string
	if eps.HA {
		extraSANs = []string{eps.APIEndpoint.Private, eps.APIEndpoint.Public}
	}

	// 3. Discovery — does a cluster already exist?
	rt.Log.Step("k3s-discover")
	masterShells := masterShellsOnly(shells, eps)
	token, found, err := install.DiscoverToken(ctx, masterShells)
	if err != nil {
		return fmt.Errorf("discover token: %w", err)
	}

	primaryNode := nodes[primaryName]

	// 4. Cold start: install primary if no cluster yet.
	if !found {
		rt.Log.Step("k3s-primary")
		if err := install.InstallPrimaryMaster(ctx, shells[primaryName], primaryNode, extraSANs, rt.Log); err != nil {
			return err
		}
		// Re-discover to get the freshly-written token.
		token, found, err = install.DiscoverToken(ctx, masterShells)
		if err != nil {
			return fmt.Errorf("re-discover token after primary install: %w", err)
		}
		if !found {
			return fmt.Errorf("primary install reported success but no token visible")
		}
	} else {
		rt.Log.Info("cluster already exists — skipping --cluster-init")
	}

	// 5. Secondary masters join.
	rt.Log.Step("k3s-secondaries")
	for _, name := range eps.Masters() {
		if name == primaryName {
			continue
		}
		if err := install.JoinSecondaryMaster(ctx, shells[name], nodes[name], primaryNode, token, extraSANs, rt.Log); err != nil {
			return err
		}
		if err := install.WaitNodeReady(ctx, shells[primaryName], nodes[name].Hostname, rt.Log); err != nil {
			return err
		}
	}

	// 6. Workers join via the LB (HA) or primary's private IP (N=1).
	workers := eps.Workers()
	if len(workers) > 0 {
		rt.Log.Step("k3s-workers")
		joinTarget := eps.WorkerJoinTarget(primaryName)
		for _, name := range workers {
			if err := install.JoinWorker(ctx, shells[name], nodes[name], joinTarget, token, rt.Log); err != nil {
				return err
			}
			if err := install.WaitNodeReady(ctx, shells[primaryName], nodes[name].Hostname, rt.Log); err != nil {
				return err
			}
		}
	}

	rt.Log.Info(fmt.Sprintf("cluster ready: %d masters, %d workers", len(eps.Masters()), len(workers)))
	return nil
}

// runWith is the lifecycle every verb shares: compile → write bundle →
// build runner → run action. Bundle and Runner live as locals — they
// are produced here, consumed here, and never stashed on a struct.
func runWith(ctx context.Context, rt *runtime.Runtime, action func(context.Context, *runner.Runner) error) error {
	rt.Log.Step("compile")
	bundle, err := compile.Compile(rt)
	if err != nil {
		return fmt.Errorf("compile: %w", err)
	}
	rt.Log.Step("write-bundle")
	if err := writeBundle(rt.WorkDir, bundle); err != nil {
		return fmt.Errorf("write bundle: %w", err)
	}
	rt.Log.Step("tf-binary")
	run, err := runner.New(ctx, rt)
	if err != nil {
		return err
	}
	return action(ctx, run)
}

// writeBundle materializes the rendered HCL files under the work
// directory. Regenerated each run — the bundle is deterministic from
// the YAML, so stale files are overwritten safely.
func writeBundle(workDir string, b *compile.Bundle) error {
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return err
	}
	files, err := b.Render()
	if err != nil {
		return err
	}
	for name, content := range files {
		path := filepath.Join(workDir, name)
		if err := os.WriteFile(path, content, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return nil
}


// masterShellsOnly filters the full per-server shell map down to
// masters and returns a Shell-typed map (Go map types are invariant,
// so we widen at the boundary where install.DiscoverToken expects
// ssh.Shell rather than *ssh.Client).
func masterShellsOnly(shells map[string]*ssh.Client, eps *runner.Endpoints) map[string]ssh.Shell {
	out := make(map[string]ssh.Shell)
	for _, name := range eps.Masters() {
		if sh, ok := shells[name]; ok {
			out[name] = sh
		}
	}
	return out
}

func closeShells(shells map[string]*ssh.Client) {
	for _, sh := range shells {
		_ = sh.Close()
	}
}

// deployWorkloads builds the typed kube client over the primary's
// existing SSH connection (the same one that installed k3s — kept
// alive by the caller's `defer closeShells`), labels every node with
// `nvoi-role=<yaml-key>` so workload nodeSelector / nodeAffinity
// match, then applies every nvoi-managed manifest and reconciles
// removal.
//
// Single SSH per server per deploy — no fresh dial here.
//
// Order matters: labels MUST land before workloads. New pods
// scheduled with a nodeSelector on a not-yet-labeled node hang
// Pending until the label arrives.
func deployWorkloads(ctx context.Context, rt *runtime.Runtime, shells map[string]*ssh.Client, eps *runner.Endpoints) error {
	primaryName := rt.Cfg.PrimaryMaster()
	primaryShell, ok := shells[primaryName]
	if !ok {
		return fmt.Errorf("primary master %s has no open shell", primaryName)
	}

	rt.Log.Step("kube-tunnel")
	kc, err := kube.New(ctx, primaryShell)
	if err != nil {
		return fmt.Errorf("build kube client: %w", err)
	}
	defer kc.Close()

	rt.Log.Step("node-labels")
	for _, key := range utils.SortedKeys(rt.Cfg.Servers) {
		hostname := naming.Server(rt.Cfg.App, rt.Cfg.Env, key)
		if err := kc.LabelNode(ctx, hostname, workload.LabelNvoiRole, key); err != nil {
			return fmt.Errorf("label node %s: %w", key, err)
		}
		rt.Log.Info(fmt.Sprintf("labeled %s with %s=%s", hostname, workload.LabelNvoiRole, key))
	}

	rt.Log.Step("workloads")
	if err := workload.ApplyAll(ctx, rt, kc, rt.Log); err != nil {
		return err
	}

	return deployIngress(ctx, rt, kc, eps)
}

// deployIngress is the post-workloads ingress reconcile. Two modes,
// selected by cfg.Providers.Tunnel:
//
//   - Tunnel mode (set): apply the cloudflared agent (Deployment +
//     Secret with the token from `terraform output`), wait Ready,
//     sweep any leftover Caddy from a prior Caddy-mode deploy.
//     DNS records are tf-managed (CNAME → local.tunnel_cname); nvoi
//     does not touch DNS imperatively.
//
//   - Caddy mode (unset): EnsureCaddy in kube-system, reload its
//     config via the admin API (atomic listener swap), per-domain
//     WaitForCaddyCert + WaitForCaddyHTTPS from inside the pod,
//     sweep any leftover tunnel-agent workloads from a prior
//     tunnel-mode deploy.
//
// Skipped when cfg.Domains is empty.
func deployIngress(ctx context.Context, rt *runtime.Runtime, kc *kube.Client, eps *runner.Endpoints) error {
	if len(rt.Cfg.Domains) == 0 {
		return nil
	}
	if rt.Cfg.Providers.Tunnel != "" {
		return deployTunnelIngress(ctx, rt, kc, eps)
	}

	rt.Log.Step("caddy")
	if err := kc.EnsureCaddy(ctx); err != nil {
		return fmt.Errorf("ensure caddy: %w", err)
	}

	// Resolve per-service ports from the live Services we just applied.
	routes := make([]kube.CaddyRoute, 0, len(rt.Cfg.Domains))
	for _, svcName := range utils.SortedKeys(rt.Cfg.Domains) {
		port, err := kc.GetServicePort(ctx, "default", svcName)
		if err != nil {
			return fmt.Errorf("ingress: service %q port: %w", svcName, err)
		}
		routes = append(routes, kube.CaddyRoute{
			Service: svcName,
			Port:    port,
			Domains: rt.Cfg.Domains[svcName],
		})
	}

	configJSON, err := kube.BuildCaddyConfig(kube.CaddyConfigInput{
		Namespace: "default",
		Routes:    routes,
		ACMEEmail: rt.Cfg.ACMEEmail,
	})
	if err != nil {
		return fmt.Errorf("build caddy config: %w", err)
	}

	rt.Log.Step("caddy-reload")
	if err := kc.ReloadCaddyConfig(ctx, configJSON); err != nil {
		return err
	}
	rt.Log.Info("caddy config loaded")

	// Per-domain cert + HTTPS verification. Warn-and-continue posture:
	// timeouts surface but don't fail the deploy. Caddy retries ACME.
	for _, svcName := range utils.SortedKeys(rt.Cfg.Domains) {
		for _, domain := range rt.Cfg.Domains[svcName] {
			rt.Log.Step("cert-" + domain)
			if err := kc.WaitForCaddyCert(ctx, domain); err != nil {
				rt.Log.Warn(fmt.Sprintf("%s: certificate not issued in time — next deploy re-verifies (%v)", domain, err))
				continue
			}
			rt.Log.Info(fmt.Sprintf("certificate ready: %s", domain))

			rt.Log.Step("https-" + domain)
			if err := kc.WaitForCaddyHTTPS(ctx, domain, "/healthz"); err != nil {
				rt.Log.Warn(fmt.Sprintf("https://%s/healthz: probe failed — next deploy re-verifies (%v)", domain, err))
				continue
			}
			rt.Log.Info(fmt.Sprintf("https live: https://%s/", domain))
		}
	}

	// Cross-mode cleanup: if a previous deploy ran in tunnel mode,
	// the cloudflared / ngrok agent workloads are still around.
	// Purge them now that Caddy is serving.
	rt.Log.Step("purge-tunnel-agent")
	if err := purgeOwner(ctx, kc, "default", kube.OwnerTunnelAgent); err != nil {
		rt.Log.Warn(fmt.Sprintf("purge tunnel-agent (cross-mode cleanup): %v", err))
	}
	return nil
}

// deployTunnelIngress is the tunnel-mode counterpart to the Caddy
// path. terraform owns every cloud resource — tunnel object, ingress
// config, AND public DNS records (CNAME → local.tunnel_cname). nvoi
// owns ONLY the kube-side agent:
//
//  1. Apply cloudflared agent (Deployment + Secret) using the token
//     from `terraform output tunnel_token`.
//  2. Wait for the agent Deployment to reach Ready so subsequent
//     deploys can rely on it.
//  3. Sweep orphan Caddy from kube-system (cross-mode cleanup —
//     reclaims hostPort 80/443 if the prior deploy was Caddy-mode).
//
// Brief 502 window during initial deploy or Caddy → tunnel mode
// switch is accepted by design: tf flips DNS records (A → CNAME or
// fresh CNAME) atomically with `tf-apply`, the cloudflared agent
// only comes up in this workload phase, so traffic hits the CF
// edge before the agent has registered. ~30s–2min, one-time per
// app. Documented in CLAUDE.md.
func deployTunnelIngress(ctx context.Context, rt *runtime.Runtime, kc *kube.Client, eps *runner.Endpoints) error {
	if eps.TunnelToken == "" {
		return fmt.Errorf("tunnel mode: terraform output tunnel_token is empty")
	}

	tun, err := compile.ResolveTunnel(rt.Cfg.Providers.Tunnel)
	if err != nil {
		return fmt.Errorf("resolve tunnel emitter: %w", err)
	}

	rt.Log.Step("tunnel-agent")
	workloads, err := tun.AgentWorkloads(rt.Cfg, eps.TunnelToken)
	if err != nil {
		return fmt.Errorf("build tunnel-agent workloads: %w", err)
	}
	for _, w := range workloads {
		if err := kc.ApplyOwned(ctx, "default", kube.OwnerTunnelAgent, w.Obj); err != nil {
			return fmt.Errorf("apply tunnel-agent %s/%s: %w", w.Kind, w.Name, err)
		}
		rt.Log.Info(fmt.Sprintf("applied tunnel-agent %s/%s", w.Kind, w.Name))
	}

	rt.Log.Step("tunnel-agent-ready")
	if err := kc.WaitDeploymentReady(ctx, "default", "cloudflared"); err != nil {
		return fmt.Errorf("wait cloudflared ready: %w", err)
	}

	rt.Log.Step("purge-caddy")
	if err := purgeOwner(ctx, kc, kube.CaddyNamespace, kube.OwnerCaddy); err != nil {
		rt.Log.Warn(fmt.Sprintf("purge caddy (cross-mode cleanup): %v", err))
	}
	return nil
}

// purgeOwner deletes every resource carrying nvoi/owner=<owner> in
// the given namespace, across every kind ApplyOwned supports. Used
// for cross-mode ingress transitions (caddy ↔ tunnel-agent).
func purgeOwner(ctx context.Context, kc *kube.Client, ns, owner string) error {
	for _, kind := range []kube.Kind{
		kube.KindDeployment, kube.KindStatefulSet, kube.KindService,
		kube.KindSecret, kube.KindConfigMap, kube.KindPVC,
	} {
		if err := kc.SweepOwned(ctx, ns, owner, kind, nil); err != nil {
			return fmt.Errorf("sweep %s: %w", kind, err)
		}
	}
	return nil
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
// kube-unreachable so a misconfigured detach can't block tf-apply.
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
	if len(leaving) == 0 {
		return nil
	}

	eps, err := run.Endpoints(ctx)
	if err != nil {
		rt.Log.Warn(fmt.Sprintf("read endpoints for tunnel-drain: %s — skipping", err))
		return nil
	}
	primary := rt.Cfg.PrimaryMaster()
	srv, ok := eps.Servers[primary]
	if !ok || srv.IPv4 == "" {
		rt.Log.Warn(fmt.Sprintf("primary master %s not in current state — skipping tunnel-drain", primary))
		return nil
	}

	rt.Log.Step("drain-tunnel")
	rt.Log.Info(fmt.Sprintf("draining %d tunnel(s) before tf-apply: %v", len(leaving), leaving))

	sh, err := ssh.Dial(ctx, srv.IPv4+":22", install.DefaultUser, rt.SSHPrivKey)
	if err != nil {
		rt.Log.Warn(fmt.Sprintf("ssh master %s for tunnel-drain: %s — skipping", primary, err))
		return nil
	}
	defer sh.Close()

	kc, err := kube.New(ctx, sh)
	if err != nil {
		rt.Log.Warn(fmt.Sprintf("kube tunnel for tunnel-drain: %s — skipping", err))
		return nil
	}
	defer kc.Close()

	// Sweep every owner=tunnel-agent resource — pod termination drops
	// cloudflared's outbound connections, which clears CF's active-
	// connections gate on the subsequent tf-apply DELETE.
	for _, kind := range []kube.Kind{
		kube.KindDeployment, kube.KindSecret, kube.KindConfigMap,
	} {
		if err := kc.SweepOwned(ctx, "default", kube.OwnerTunnelAgent, kind, nil); err != nil {
			rt.Log.Warn(fmt.Sprintf("sweep tunnel-agent %s: %s", kind, err))
		}
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
	if len(leaving) == 0 {
		return nil
	}

	survivor := pickSurvivorMaster(rt.Cfg, leaving)
	if survivor == "" {
		rt.Log.Warn(fmt.Sprintf("all masters being destroyed (leaving: %v) — skipping detach", leaving))
		return nil
	}

	// Read CURRENT state (pre-apply) to find survivor's IP.
	eps, err := run.Endpoints(ctx)
	if err != nil {
		// Cold start: state file may not have outputs yet (first apply).
		// In that case there's nothing to detach — the cluster doesn't
		// exist and the "destroys" are spurious. Warn-and-continue.
		rt.Log.Warn(fmt.Sprintf("read endpoints for detach: %s — skipping", err))
		return nil
	}
	srv, ok := eps.Servers[survivor]
	if !ok || srv.IPv4 == "" {
		rt.Log.Warn(fmt.Sprintf("survivor master %s not in current state — skipping detach", survivor))
		return nil
	}

	rt.Log.Step("detach")
	rt.Log.Info(fmt.Sprintf("detaching %d node(s) via %s: %v", len(leaving), survivor, leaving))
	sh, err := ssh.Dial(ctx, srv.IPv4+":22", install.DefaultUser, rt.SSHPrivKey)
	if err != nil {
		rt.Log.Warn(fmt.Sprintf("ssh survivor %s for detach: %s — skipping", survivor, err))
		return nil
	}
	defer sh.Close()

	nodes := make([]detach.Node, len(leaving))
	for i, key := range leaving {
		nodes[i] = detach.Node{
			Hostname: naming.Server(rt.Cfg.App, rt.Cfg.Env, key),
			Role:     rt.Cfg.Servers[key].Role,
		}
	}
	detach.Nodes(ctx, sh, nodes, rt.Log)
	return nil
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
