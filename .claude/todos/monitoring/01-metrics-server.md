# PR 1 — metrics-server addon + `OwnerAddons` taxonomy

## Goal

Install `metrics-server` as a managed, owner-labeled cluster addon on
every `nvoi deploy`. Validates the `OwnerAddons` taxonomy entry and
the "addon via shell-out kubectl-apply" pattern (same shape
cert-manager already uses). Independent of the rest of the monitoring
work — `kubectl top` works on every cluster after this lands.

## Scope

In-scope:
- `OwnerAddons` const added to `pkg/internal/kube/owned.go`.
- `pkg/internal/observability/addons.go` — applies the upstream
  metrics-server single-file manifest, waits for Available, idempotent.
- New step in `pkg/deploy/workloads.go::deployWorkloads`: after
  `node-labels`, before the workload loop.
- Unconditional — runs even when `monitor:` is unset.

Out of scope:
- HPA wiring (operator-side concern; metrics-server merely enables it).
- The `monitor:` block (separate PRs).

## Files

### New

**`pkg/internal/observability/addons.go`** (~80 lines)

```go
// Package observability — addons.go installs cluster-level addons
// (metrics-server today) owner-labeled under OwnerAddons. Idempotent.
//
// Pattern mirrors pkg/internal/kube/certmanager.go: shell-out to
// `sudo k3s kubectl apply -f <URL>` over the master's SSH session;
// then wait for the expected Deployments to be Available; then stamp
// the owner label via a label patch.
package observability

import (
    "context"
    "fmt"

    "github.com/getnvoi/core/pkg/install"
    "github.com/getnvoi/core/pkg/log"
    "github.com/getnvoi/core/pkg/ssh"
)

// MetricsServerVersion pins the metrics-server release. Bump as a
// one-line change; review release notes for breaking changes.
const MetricsServerVersion = "v0.7.2"

// MetricsServerURL is the upstream "components.yaml" release URL.
// Self-contained single-file manifest, requires master internet
// (same dependency as cert-manager + k3s install).
const MetricsServerURL = "https://github.com/kubernetes-sigs/metrics-server/releases/download/" + MetricsServerVersion + "/components.yaml"

const MetricsServerNamespace = "kube-system"

// ApplyMetricsServer kubectl-applies the upstream manifest, waits for
// the metrics-server Deployment to become Available, then labels the
// Deployment + Service + ServiceAccount + ClusterRole + APIService
// with `nvoi/owner=addons` so SweepOwned can manage their lifecycle.
//
// Idempotent — re-running is a no-op kubectl apply + a no-op label
// patch.
func ApplyMetricsServer(ctx context.Context, sh ssh.Shell, lg log.Log) error
```

**`pkg/internal/observability/addons_test.go`**

- Mocks: `pkg/testutil/sshfake.Shell` with canned responses for the
  `kubectl apply`, `kubectl wait`, `kubectl label` invocations.
- Asserts the expected command sequence in order.

### Edited

**`pkg/internal/kube/owned.go`** — add `OwnerAddons` const + comment
entry in the taxonomy table.

**`pkg/deploy/workloads.go`** — call `observability.ApplyMetricsServer`
inside `deployWorkloads`, right after the `node-labels` step, BEFORE
`applyCertInfrastructure`:

```go
s.Lg.Step("addons-metrics-server")
if err := observability.ApplyMetricsServer(ctx, primaryShell, s.Lg); err != nil {
    return fmt.Errorf("metrics-server: %w", err)
}
```

The step runs unconditionally so a pre-existing cluster gets
metrics-server retroactively on next deploy.

**`pkg/internal/kube/owned.go`** — extend the taxonomy comment table:

```
| OwnerAddons        | metrics-server + future cluster-level prereqs |
```

## kubectl flow

```bash
# Apply
sudo k3s kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/download/v0.7.2/components.yaml

# Wait (3min — metrics-server starts fast, but waitOnAvailable
# is the canonical readiness signal)
sudo k3s kubectl -n kube-system wait --for=condition=Available --timeout=180s deployment/metrics-server

# Stamp owner labels (idempotent — label patches are no-ops when
# already set). Each object in the manifest we want SweepOwned to
# manage gets:
sudo k3s kubectl -n kube-system label deployment metrics-server nvoi/owner=addons --overwrite
sudo k3s kubectl -n kube-system label service     metrics-server nvoi/owner=addons --overwrite
sudo k3s kubectl -n kube-system label serviceaccount metrics-server nvoi/owner=addons --overwrite
sudo k3s kubectl label clusterrole system:metrics-server nvoi/owner=addons --overwrite
sudo k3s kubectl label clusterrolebinding system:metrics-server nvoi/owner=addons --overwrite
sudo k3s kubectl label apiservice v1beta1.metrics.k8s.io nvoi/owner=addons --overwrite
```

Note: the label-patch is best-effort — failure to label one of the
auxiliary objects is a `Warn`, not an error. The Deployment label is
mandatory; that's what `ListOwned(KindDeployment, OwnerAddons)` would
return.

## Tests

`pkg/internal/observability/addons_test.go`:
- TestApplyMetricsServer_HappyPath — fake Shell returns success for
  every cmd; assert ordered cmd list.
- TestApplyMetricsServer_AlreadyInstalled — re-apply path is a no-op
  to the same exit code; assert no error.
- TestApplyMetricsServer_WaitTimeout — wait cmd returns non-zero;
  assert the wrapped error contains the wait output.
- TestApplyMetricsServer_LabelPatchFailure — apply + wait succeed,
  one label patch fails; assert overall return is nil + warn was
  logged (use a buffer-backed log.NewWith).

`bin/test` is the run command — already runs `./...`.

## Acceptance

1. `bin/test` green.
2. Manual: deploy `examples/minimal.yaml` on a fresh cluster →
   `kubectl top nodes` returns numbers within ~60s of deploy
   completion.
3. Manual: `kubectl get deployment -n kube-system metrics-server
   -L nvoi/owner` shows `addons` in the OWNER column.
4. Manual: subsequent `nvoi deploy` runs are no-ops for the
   metrics-server step (idempotent).

## Decisions deferred to implementation

- Whether the label-patch step is a single multi-arg `kubectl label`
  invocation or a loop. Multi-arg is one round-trip; loop is more
  readable. Pick whichever produces the cleaner error messages.
- Pin metrics-server version inline vs. via a const in
  `pkg/internal/observability/version.go`. Const is consistent with
  `pkg/internal/kube/certmanager.go`. Use a const.

## Out of scope (do NOT do in this PR)

- HPA examples in docs.
- A `MonitorSpec` config addition.
- Any `pkg/providers/*` changes.
- Any new top-level config field.
