package build

import (
	"context"
	"fmt"
	"sort"

	"github.com/getnvoi/core/internal/config"
	"github.com/getnvoi/core/internal/log"
	"github.com/getnvoi/core/internal/runtime"
)

// Plan returns the per-service build Requests derived from rt.Cfg.
// Empty when no service has build: set — caller skips the whole
// build phase.
//
// Tag = "<image>:<deploy-hash>". Sorted by service name for
// deterministic ordering across runs.
func Plan(rt *runtime.Runtime) []Request {
	var out []Request
	for name, svc := range rt.Cfg.Services {
		if !svc.HasBuild() {
			continue
		}
		out = append(out, Request{
			ServiceName: name,
			Context:     svc.Build.Context,
			Dockerfile:  svc.Build.Dockerfile,
			Tag:         svc.Image + ":" + rt.DeployHash,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ServiceName < out[j].ServiceName })
	return out
}

// HostsToPush extracts the unique registry hosts every Request pushes
// to. Used to drive the per-host docker login loop.
func HostsToPush(reqs []Request, services map[string]config.ServiceSpec) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range reqs {
		host := services[r.ServiceName].ImageHost()
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		out = append(out, host)
	}
	sort.Strings(out)
	return out
}

// All runs the full build phase: preflight, login per host using
// YAML-supplied credentials, then per-service build. Aborts on first
// failure — we never half-build.
func All(ctx context.Context, rt *runtime.Runtime, runner Runner, lg log.Log) error {
	reqs := Plan(rt)
	if len(reqs) == 0 {
		return nil
	}

	lg.Step("build-preflight")
	if err := runner.Preflight(ctx); err != nil {
		return fmt.Errorf("build preflight: %w", err)
	}

	hosts := HostsToPush(reqs, rt.Cfg.Services)
	for _, host := range hosts {
		reg, ok := rt.Cfg.Registry[host]
		if !ok {
			// Validator should have rejected this; defensive guard.
			return fmt.Errorf("no registry: entry for host %s — validator should have caught this", host)
		}
		lg.Step("docker-login-" + host)
		if err := runner.Login(ctx, host, reg.Username, reg.Password); err != nil {
			return fmt.Errorf("docker login %s: %w", host, err)
		}
	}

	for _, req := range reqs {
		lg.Step("build-" + req.ServiceName)
		lg.Info(fmt.Sprintf("building %s → %s", req.ServiceName, req.Tag))
		if err := runner.Build(ctx, req, lg.Stream(), lg.Stream()); err != nil {
			return fmt.Errorf("build %s: %w", req.ServiceName, err)
		}
	}
	return nil
}
