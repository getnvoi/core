// Package deploy is the orchestration layer above internal/build,
// internal/install, internal/detach, internal/kube, internal/runner,
// internal/workload. Three exported entry points — Run / Destroy / Plan —
// back the matching cobra verbs in cmd/cli. Everything else in this
// package is unexported per-stage glue.
//
// cmd/cli stays a thin OS boundary (env, disk, alias expansion,
// runtime.Build); the deploy/destroy/plan workflows live here.
package deploy

import (
	"github.com/getnvoi/core/internal/kube"
	"github.com/getnvoi/core/internal/runner"
	"github.com/getnvoi/core/internal/runtime"
	"github.com/getnvoi/core/internal/ssh"
)

// session carries the per-deploy state shared across the install +
// workloads + ingress phases. Built incrementally in Run:
//
//   - rt + run are seeded at construction.
//   - eps populated after run.Endpoints.
//   - shells populated after openShells.
//   - kc populated by deployWorkloads (kube tunnel via primary's shell).
//
// Methods on *session take only ctx — every other dependency is a field.
// Eliminates the (rt, eps, shells) ↔ (rt, shells, eps) arg-order
// papercut the free-function shape used to carry.
type session struct {
	rt     *runtime.Runtime
	run    *runner.Runner
	eps    *runner.Endpoints
	shells map[string]*ssh.Client
	kc     *kube.Client
}
