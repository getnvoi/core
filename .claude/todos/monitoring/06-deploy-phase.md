# PR 6 — Observability deploy phase + Grafana provisioning glue

## Goal

Wire everything from PRs 2-5 into the `nvoi deploy` lifecycle. Add the
`KindMonitor` log kind. Translate `providers.Receiver`s into Grafana
contact-point YAML. Apply the full stack + dashboards + alert rules +
datasources + contact points + notification policy. Sweep-reconcile
on every deploy.

After this PR, `nvoi deploy` with a `monitor:` block stands up a
fully-configured Grafana cluster-internally. Operator can't reach it
yet — that's PR 7.

## Scope

In-scope:
- `pkg/log/log.go` — add `KindMonitor` const.
- `pkg/internal/observability/grafana/datasource.go` — Prometheus +
  Loki datasource ConfigMaps.
- `pkg/internal/observability/grafana/contactpoint.go` — `Receiver`
  → Grafana contact-point YAML.
- `pkg/internal/observability/grafana/notificationpolicy.go` — default
  policy routing every alert to every channel.
- `pkg/deploy/observability.go` — orchestration entry point.
- `pkg/deploy/deploy.go` — call into observability phase.
- Sweep on every deploy + full purge when `cfg.Monitor == nil`.

Out of scope:
- `nvoi monitor` verb (PR 7).
- Any new CLI verbs.

## Log kind

`pkg/log/log.go`:

```go
const (
    KindInfra   Kind = "infra"
    KindBuild   Kind = "build"
    KindCluster Kind = "cluster"
    KindMonitor Kind = "monitor"  // NEW
)
```

Closed enum extension. Every observability phase step / log entry
scopes through `rt.Log.Sub(log.KindMonitor)`. Text projection and
JSONL projection both gain `"kind":"monitor"` events.

## Grafana provisioning glue

### `pkg/internal/observability/grafana/datasource.go`

```go
// BuildDatasourceConfigMap returns the ConfigMap whose content the
// Grafana sidecar drops into /etc/grafana/provisioning/datasources/.
// Labeled grafana_datasource=1 + nvoi/owner=observability.
//
// Datasources:
//   - Prometheus → http://thanos-querier.nvoi-observability.svc:9090
//   - Loki       → http://loki.nvoi-observability.svc:3100
func BuildDatasourceConfigMap() *corev1.ConfigMap
```

Static config — no per-deploy variation. Could be a constant manifest,
but keeping it as a builder makes adding future datasources (Tempo,
Mimir) mechanical.

### `pkg/internal/observability/grafana/contactpoint.go`

```go
// Package grafana — the ONLY place in the codebase where the Grafana
// wire format leaks into nvoi. Receivers come in (portable), Grafana
// provisioning YAML goes out.
package grafana

// BuildContactPoints translates portable Receivers (one per
// configured channel) into Grafana contact-point provisioning YAML.
// Output is wrapped in a ConfigMap labeled grafana_alert=1.
//
// Per-receiver-type translation (closed switch):
//   - "slack"    → contact point of type "slack" with .settings.url
//   - "postmark" → contact point of type "email" with .settings.{from,
//                  to,host,user,password} — Postmark exposes SMTP at
//                  smtp.postmarkapp.com:587, auth = (server-token,
//                  server-token). Grafana's "email" receiver handles
//                  the SMTP wire.
//   - "twilio"   → contact point of type "twilio" — Grafana's built-in
//                  Twilio receiver type.
//
// Unknown receiver types return an error — caller passed a Receiver
// the orchestrator doesn't know how to render. This is a programmer
// error, not an operator error.
func BuildContactPoints(receivers []providers.Receiver) (*corev1.ConfigMap, error)
```

Postmark mapping: Grafana doesn't have a Postmark-native receiver, but
it has a generic email receiver that speaks SMTP. Postmark exposes
SMTP at `smtp.postmarkapp.com:587` with auth `(server_token,
server_token)`. The `BuildContactPoints` translation for type
"postmark" emits an SMTP receiver pointing at Postmark's SMTP host.

This is the one place "what notification system are we wired to" leaks
into code. If we add Alertmanager support later, we add a sibling
package `pkg/internal/observability/alertmanager/contactpoint.go` and
swap based on a config flag (or just keep Grafana — there's no
roadmap to swap).

### `pkg/internal/observability/grafana/notificationpolicy.go`

```go
// BuildNotificationPolicy returns the default routing policy: every
// firing alert routes through every configured contact point (no
// label-based filtering for v1). Operator-tweakable later via a
// `monitor.routing:` field; out of scope for v1.
func BuildNotificationPolicy(receiverNames []string) *corev1.ConfigMap
```

YAML shape:

```yaml
apiVersion: 1
policies:
  - orgId: 1
    receiver: nvoi-default       # multi-receiver via contactPoints child
    group_by: [grafana_folder, alertname]
    routes: []
```

Actually Grafana's unified alerting routing model uses a tree with
a single root receiver. To fan out to multiple receivers, the
`nvoi-default` receiver is itself a "Grafana multi-receiver" that
contains nested integrations — every contact point we built. Trade-off:
this means **one logical receiver named `nvoi-default` contains
N integrations**. The `BuildContactPoints` translation collapses all
configured channels into a single named contact point with multiple
integrations. Simplest possible default policy.

### Glue function

```go
// pkg/internal/observability/grafana/provisioning.go

// BuildAllProvisioning consumes the operator's resolved monitor
// config + the registered provider lookups, builds every Receiver,
// then assembles datasources + contact points + notification policy +
// alert rules into the ConfigMaps the Grafana sidecar consumes.
//
// Returns the slice of typed objects (ConfigMaps + Secrets) ready
// for kc.ApplyOwned.
func BuildAllProvisioning(rt *runtime.Runtime, cfg *config.Config) ([]runtime.Object, error)
```

The flow:

1. For each configured channel, look up the registered provider and
   call `BuildReceiver`.
2. Materialize each Receiver's `SecretRef[].From` values into a
   Secret in `nvoi-observability` namespace.
3. Render contact-point YAML from the Receivers.
4. Render notification-policy YAML.
5. Render datasource YAML.
6. Render alert-rule YAML (calls into PR 5's `alertrules.BuildAlertRules`).
7. Return everything as typed objects.

## Deploy phase

### `pkg/deploy/observability.go`

```go
package deploy

import (
    "context"
    "fmt"

    "github.com/getnvoi/core/pkg/internal/observability"
    "github.com/getnvoi/core/pkg/internal/observability/grafana"
    "github.com/getnvoi/core/pkg/internal/kube"
    "github.com/getnvoi/core/pkg/log"
)

// deployObservability stands up (or reconciles, or tears down) the
// observability stack. Called from Run after deployWorkloads.
//
// Behavior:
//   - cfg.Monitor == nil → full sweep of OwnerObservability scope
//     across every Kind; namespace stays (cheap), no Secrets/Configs
//     consumed. Buckets persist (deleted only on `nvoi destroy`).
//   - cfg.Monitor != nil → ensure buckets, ensure namespace, apply
//     manifests, apply provisioning, sweep orphans.
func (s *Session) deployObservability(ctx context.Context) error {
    mlg := s.Rt.Log.Sub(log.KindMonitor)

    if s.Rt.Cfg.Monitor == nil {
        return s.sweepObservability(ctx, mlg)
    }

    mlg.Step("monitor-buckets")
    creds, err := observability.EnsureBuckets(ctx, s.Rt, mlg)
    if err != nil { return fmt.Errorf("monitor buckets: %w", err) }

    mlg.Step("monitor-namespace")
    if err := observability.EnsureNamespace(ctx, s.kc); err != nil {
        return fmt.Errorf("monitor namespace: %w", err)
    }

    scope := kube.Scope{Namespace: observability.Namespace, Owner: kube.OwnerObservability}

    mlg.Step("monitor-stack")
    stack, err := observability.BuildStack(s.Rt, creds)
    if err != nil { return fmt.Errorf("build stack: %w", err) }
    for _, obj := range stack {
        if err := s.kc.ApplyOwned(ctx, scope, obj); err != nil {
            return fmt.Errorf("apply stack obj: %w", err)
        }
    }

    mlg.Step("monitor-provisioning")
    prov, err := grafana.BuildAllProvisioning(s.Rt, s.Rt.Cfg)
    if err != nil { return fmt.Errorf("build provisioning: %w", err) }
    for _, obj := range prov {
        if err := s.kc.ApplyOwned(ctx, scope, obj); err != nil {
            return fmt.Errorf("apply provisioning: %w", err)
        }
    }

    if s.Rt.Cfg.Monitor.Domain != "" {
        mlg.Step("monitor-ingress")
        if err := s.applyMonitorIngress(ctx); err != nil {
            return fmt.Errorf("monitor ingress: %w", err)
        }
    }

    mlg.Step("monitor-wait")
    if err := s.kc.WaitDeploymentReady(ctx, observability.Namespace, "grafana"); err != nil {
        // Warn, not error — Grafana not yet Ready is acceptable for
        // first deploy; operator can `nvoi monitor` once it's up.
        mlg.Warn(fmt.Sprintf("grafana not yet ready: %v", err))
    }

    mlg.Step("monitor-sweep")
    return s.reconcileObservabilityRemoval(ctx, mlg)
}
```

`BuildStack(rt, creds) ([]runtime.Object, error)` lives in
`pkg/internal/observability/manifests.go` (PR 4 builds the individual
manifests; this is the composer). Returns the full list in
apply-order:
1. Secrets (objstore creds, grafana admin)
2. ServiceAccounts + ClusterRole + ClusterRoleBinding (promtail)
3. ConfigMaps (prometheus.yml, loki.yml, promtail.yml, grafana.ini)
4. StatefulSets + DaemonSets + Deployments
5. Services

Order matters less than you'd think (k8s eventually reconciles), but
keeping the ordering deterministic helps debug log diffs.

`s.applyMonitorIngress(ctx)` mirrors `s.applyCertInfrastructure`:
generate Certificate YAML for `mon.Domain`, apply via shell-out
`kubectl apply` (same path cert-manager.go uses), then apply
typed Ingress via `kc.ApplyOwned`.

### `s.reconcileObservabilityRemoval`

Sweeps each Kind we touch:

```go
scope := kube.Scope{Namespace: observability.Namespace, Owner: kube.OwnerObservability}

// Build desired-name lists from what we just applied.
declaredDeployments := []string{"thanos-querier", "thanos-store", "grafana"}
declaredStatefulSets := []string{"prometheus", "loki"}
declaredDaemonSets := []string{"promtail"}
declaredConfigMaps := []string{
    "prometheus-config", "loki-config", "promtail-config", "grafana-config",
    "dashboard-overview", "dashboard-services", "dashboard-ingress",
    "dashboard-cluster", "dashboard-deploys",
    "datasources", "contact-points", "notification-policy", "alert-rules",
}
declaredSecrets := []string{"thanos-objstore", "grafana-admin", /* per-receiver-secret names */}
declaredServices := []string{"prometheus", "thanos-querier", "thanos-store", "loki", "grafana"}
// + Ingress when domain set

for _, kindName := range []struct{ k kube.Kind; names []string }{
    {kube.KindDeployment,  declaredDeployments},
    {kube.KindStatefulSet, declaredStatefulSets},
    {kube.KindDaemonSet,   declaredDaemonSets},
    {kube.KindConfigMap,   declaredConfigMaps},
    {kube.KindSecret,      declaredSecrets},
    {kube.KindService,     declaredServices},
    {kube.KindIngress,     ingressNames},
} {
    if err := s.kc.SweepOwned(ctx, scope, kindName.k, kindName.names); err != nil {
        return err
    }
}
```

### Full purge (`s.sweepObservability`)

When `cfg.Monitor == nil`, sweep every Kind in scope with empty
desired-name list. Removes everything. Namespace stays (cheap;
recreating costs nothing).

```go
func (s *Session) sweepObservability(ctx context.Context, lg log.Log) error {
    scope := kube.Scope{Namespace: observability.Namespace, Owner: kube.OwnerObservability}
    for _, k := range []kube.Kind{
        kube.KindDeployment, kube.KindStatefulSet, kube.KindDaemonSet,
        kube.KindConfigMap, kube.KindSecret, kube.KindService,
        kube.KindIngress,
    } {
        if err := s.kc.SweepOwned(ctx, scope, k, nil); err != nil {
            return err
        }
    }
    return nil
}
```

### Wiring into `Run`

`pkg/deploy/deploy.go::Run` — after the existing `deployWorkloads`
call:

```go
if err := s.deployWorkloads(ctx); err != nil { return err }

// Observability runs unconditionally — when cfg.Monitor is nil,
// the call is a sweep that reaps any prior stack.
return s.deployObservability(ctx)
```

The unconditional call matters: it means flipping `monitor:` from
"set" to "absent" tears the stack down on the next deploy without
operator intervention. Same shape as the existing reconcile-on-removal
guarantees in `pkg/workload/reconcile.go`.

## Tests

`pkg/internal/observability/grafana/contactpoint_test.go`:
- TestBuildContactPoints_SlackOnly — single slack receiver renders
  to single integration ConfigMap.
- TestBuildContactPoints_AllThree — slack + postmark + twilio
  collapse to one named multi-integration receiver.
- TestBuildContactPoints_Postmark_SMTPHost — postmark Receiver
  renders to email integration with smtp.postmarkapp.com host.
- TestBuildContactPoints_UnknownType — returns error.

`pkg/internal/observability/grafana/datasource_test.go`:
- Asserts Prometheus URL points at thanos-querier (NOT prometheus
  directly).
- Asserts Loki URL is correct.
- Asserts labels (grafana_datasource=1).

`pkg/internal/observability/grafana/notificationpolicy_test.go`:
- Default policy routes to nvoi-default receiver.
- group_by includes alertname.

`pkg/internal/observability/grafana/provisioning_test.go`:
- TestBuildAllProvisioning_EmptyAlerts — datasources only.
- TestBuildAllProvisioning_AllChannels — full provisioning bundle.
- TestBuildAllProvisioning_PerReceiverSecrets — every Receiver's
  SecretRef materializes into a Secret in the returned slice.

`pkg/deploy/observability_test.go`:
- Mock kube.Client. TestDeployObservability_FullStack — assert the
  set of ApplyOwned calls + their kinds.
- TestDeployObservability_NilMonitor_FullSweep — cfg.Monitor=nil
  produces a sweep of every Kind, no Apply calls.
- TestDeployObservability_DomainSet_AppliesIngress.

## Acceptance

1. `bin/test` green.
2. End-to-end manual: deploy `examples/ha.yaml` with a `monitor:`
   block added; verify in cluster:
   - Namespace `nvoi-observability` exists.
   - 5 dashboard ConfigMaps present, labeled correctly.
   - Grafana Pod Ready; `kubectl logs grafana` shows
     "successfully provisioned" for each datasource + dashboard.
   - `kubectl get -n nvoi-observability -l nvoi/owner=observability`
     returns the full expected set.
3. Manual: remove `monitor:` from YAML, redeploy. Verify all
   nvoi-observability objects (except namespace) are gone.

## Decisions deferred to implementation

- Ingress for Grafana — apply via typed `kc.ApplyOwned` (consistent
  with `pkg/workload/ingress.go`) vs shell-out `kubectl apply`
  (consistent with cert-manager). Typed is cleaner since the kube
  client already supports Ingress kind. Decision: typed.
- Wait timeout for Grafana — 5 min default (matches
  `WaitDeploymentReady`). Reasonable.
- Whether to wait for Loki + Prometheus too. Decision: only Grafana
  (it's the operator-visible surface). Loki + Prom heal async.

## Out of scope (do NOT do in this PR)

- `nvoi monitor` verb (PR 7).
- Any CLI changes.
- `nvoi destroy` bucket deletion (separate task — destroy.go gets
  one new call: drain observability buckets before tf-destroy).
  Note this in 07 follow-ups.
