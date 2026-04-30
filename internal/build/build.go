package build

import (
	"context"
	"fmt"
	"sort"

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

// All runs the full build phase: preflight, then a `docker login`
// for every host declared in `registry:`, then per-service build.
// Aborts on first failure — we never half-build.
//
// Login policy: the operator wrote the creds down, we use them. We
// do NOT inspect image strings to "decide" which registries to
// authenticate against — declared creds are the contract, full stop.
// Anything weirder than that is a footgun: a host-less image
// (`nvoi/web`) would otherwise silently fall through to whatever the
// operator's local ~/.docker/config.json carries, which is almost
// never the auth they intended to ship the deploy with.
func All(ctx context.Context, rt *runtime.Runtime, runner Runner, lg log.Log) error {
	reqs := Plan(rt)
	if len(reqs) == 0 {
		return nil
	}

	lg.Step("build-preflight")
	if err := runner.Preflight(ctx); err != nil {
		return fmt.Errorf("build preflight: %w", err)
	}

	// Sorted iteration → deterministic log order across runs.
	hosts := make([]string, 0, len(rt.Cfg.Registry))
	for h := range rt.Cfg.Registry {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)

	for _, host := range hosts {
		reg := rt.Cfg.Registry[host]
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
