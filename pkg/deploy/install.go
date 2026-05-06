package deploy

import (
	"context"
	"fmt"

	"github.com/getnvoi/core/pkg/install"
	"github.com/getnvoi/core/pkg/internal/utils"
	"github.com/getnvoi/core/pkg/naming"
)

// installCluster is the post-tofu bootstrap pipeline:
//  1. ensure swap on every node
//  2. discover any existing k3s cluster (idempotency)
//  3. cold start: install --cluster-init on the primary master
//  4. join secondary masters via --server <primary>:6443
//  5. join workers via the LB private IP (or primary's private IP if N=1)
//
// Reads s.shells (pre-opened by Run); the same connections stay alive
// through the workloads phase.
//
// Always uses --cluster-init regardless of master count: the cluster
// is etcd-backed from day one, so 1↔N migration is mechanical.
func (s *Session) installCluster(ctx context.Context) error {
	rt, eps, shells := s.Rt, s.eps, s.shells
	cfg := rt.Cfg
	primaryName := cfg.PrimaryMaster()
	if primaryName == "" {
		return fmt.Errorf("config has no master with primary: true (validator should have caught this)")
	}

	// 1. Swap on every node.
	s.Lg.Step("swap")
	for _, name := range utils.SortedKeys(eps.Servers) {
		if err := install.EnsureSwap(ctx, shells[name], s.Lg); err != nil {
			return fmt.Errorf("swap %s: %w", name, err)
		}
	}

	// 2. Build typed Nodes used by the install package. Each Node
	//    bundles its Shell + Log so the install functions take
	//    `(ctx, n Node, ...)` without a separate (sh, lg) trail.
	nodes := make(map[string]install.Node, len(eps.Servers))
	for name, srv := range eps.Servers {
		nodes[name] = install.Node{
			Name:     name,
			Hostname: naming.Server(cfg.App, cfg.Env, name),
			IPv4:     srv.IPv4,
			Private:  srv.Private,
			Shell:    shells[name],
			Log:      s.Lg,
		}
	}

	// LB IPs go in every master's --tls-san list so kubectl-via-LB
	// (HA case) and worker-join-via-LB validate cleanly.
	var extraSANs []string
	if eps.HA {
		extraSANs = []string{eps.APIEndpoint.Private, eps.APIEndpoint.Public}
	}

	// 3. Discovery — does a cluster already exist?
	s.Lg.Step("k3s-discover")
	masterShells := masterShellsOnly(shells, eps)
	token, found, err := install.DiscoverToken(ctx, masterShells)
	if err != nil {
		return fmt.Errorf("discover token: %w", err)
	}

	primaryNode := nodes[primaryName]

	// 4. Cold start: install primary if no cluster yet.
	if !found {
		s.Lg.Step("k3s-primary")
		if err := install.InstallPrimaryMaster(ctx, primaryNode, extraSANs); err != nil {
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
		s.Lg.Info("cluster already exists — skipping --cluster-init")
	}

	// 5. Secondary masters join.
	s.Lg.Step("k3s-secondaries")
	for _, name := range eps.Masters() {
		if name == primaryName {
			continue
		}
		if err := install.JoinSecondaryMaster(ctx, install.SecondaryJoinSpec{
			Self:      nodes[name],
			Primary:   primaryNode,
			Token:     token,
			ExtraSANs: extraSANs,
		}); err != nil {
			return err
		}
		if err := install.WaitNodeReady(ctx, primaryNode.Shell, nodes[name].Hostname, s.Lg); err != nil {
			return err
		}
	}

	// 6. Workers join via the LB (HA) or primary's private IP (N=1).
	workers := eps.Workers()
	if len(workers) > 0 {
		s.Lg.Step("k3s-workers")
		joinTarget := eps.WorkerJoinTarget(primaryName)
		for _, name := range workers {
			if err := install.JoinWorker(ctx, install.WorkerJoinSpec{
				Self:   nodes[name],
				Target: joinTarget,
				Token:  token,
			}); err != nil {
				return err
			}
			if err := install.WaitNodeReady(ctx, primaryNode.Shell, nodes[name].Hostname, s.Lg); err != nil {
				return err
			}
		}
	}

	s.Lg.Info(fmt.Sprintf("cluster ready: %d masters, %d workers", len(eps.Masters()), len(workers)))
	return nil
}
