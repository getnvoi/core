# PR 5 — Dashboard + alert rule generator

## Goal

Generate Grafana dashboards and alert rules from `cfg.Services`,
`cfg.Domains`, `cfg.Servers`, and `rt.DeployHash`. Output as
owner-labeled ConfigMaps that the Grafana sidecar (built in PR 4)
picks up automatically. After this PR, the generator is callable
and unit-tested with golden expectations — PR 6 wires it into the
deploy pipeline.

## Scope

In-scope:
- `pkg/internal/observability/dashboards.go` — five dashboard
  generators (overview, per-service, ingress, cluster, deploys).
- `pkg/internal/observability/grafana/alertrules.go` — alert rule
  generator.
- Embedded `.json.tmpl` files for dashboards.
- Golden-file tests.

Out of scope:
- Datasource provisioning (PR 6).
- Contact point provisioning (PR 6).
- Notification policy (PR 6).
- Wiring into deploy (PR 6).

## Dashboards (one ConfigMap each)

All ConfigMaps live in `nvoi-observability` namespace, labeled
`grafana_dashboard=1` + `nvoi/owner=observability` + `nvoi/dashboard=<name>`.
The Grafana sidecar (configured in PR 4) watches for this label and
provisions the dashboard.

ConfigMap name convention: `dashboard-<name>` (e.g.
`dashboard-overview`).

### Dashboard 1 — Overview

`buildOverviewDashboard(cfg *config.Config) *corev1.ConfigMap`

Inputs:
- `cfg.Services` — one panel per service showing ready/desired
  replicas + restart count over last 1h.
- `cfg.Domains` — one panel per public domain showing req/s.

Layout: traffic-light grid. Green if replicas ready ≥ desired AND
restarts == 0 over 1h. Yellow if restarts > 0. Red if ready < desired.

### Dashboard 2 — Per-service

`buildServicesDashboard(cfg *config.Config) *corev1.ConfigMap`

ONE dashboard with one "row" per `cfg.Services` entry (Grafana
"repeating row" mechanism). Each row contains:
- Replica gauge (ready / desired).
- CPU usage (container_cpu_usage_seconds_total via cAdvisor).
- Memory working set.
- Restart count over 1h.
- Container log stream from Loki, filtered by
  `{namespace="default", service="<name>"}` (matches the
  `nvoi/service` pod label that Promtail propagates as a Loki
  stream label).
- Recent deploys overlay — annotation query against Prometheus
  metric `kube_pod_labels{label_nvoi_deploy_hash=~".+"}` to mark
  deploy-hash transitions.

The "repeating row" feature in Grafana takes a single dashboard JSON
that templates over a variable list (`$service`) supplied at
dashboard-build time. Implementation: the variable's options list
is hardcoded into the dashboard JSON by the generator —
`{"options": [{"text":"web","value":"web"}, ...]}` — derived from
`cfg.Services` keys. Avoids datasource queries for the variable.

### Dashboard 3 — Ingress

`buildIngressDashboard(cfg *config.Config) *corev1.ConfigMap`

Inputs:
- `cfg.Domains` — per-domain panels.
- cert-manager metrics — cert expiry tile.

Panels:
- Per-domain req/s by status class (2xx / 3xx / 4xx / 5xx) using
  Traefik's exposed `traefik_service_requests_total` metric, grouped
  by `service` label (which Traefik sets from the Service name).
- Per-domain p50 / p95 / p99 latency from
  `traefik_service_request_duration_seconds_bucket`.
- Cert expiry tile from
  `certmanager_certificate_expiration_timestamp_seconds`, formatted
  as days remaining.

Omitted entirely if `cfg.Domains` is empty — the dashboard is
generated but contains a single "No domains configured" text panel.

### Dashboard 4 — Cluster

`buildClusterDashboard(cfg *config.Config) *corev1.ConfigMap`

Panels:
- Node Ready status (`kube_node_status_condition{condition="Ready"}`).
- Per-node CPU + mem from kubelet metrics.
- Per-node disk pressure / memory pressure.
- etcd cluster health (only when N≥2 masters) — `etcd_server_has_leader`,
  `etcd_server_leader_changes_seen_total`.
- Per-service-with-storage PVC fill — `kubelet_volume_stats_used_bytes
  / kubelet_volume_stats_capacity_bytes`. Iterates `cfg.Services`
  where `svc.Storage != nil`.

### Dashboard 5 — Deploys

`buildDeploysDashboard(cfg *config.Config) *corev1.ConfigMap`

Single dashboard showing deploy history as a time-series of
`nvoi/deploy-hash` label transitions. Useful for correlating a
regression to a specific deploy.

Annotation queries on EVERY other dashboard reference this same
data so deploys overlay as vertical lines on any time-series.

## Template strategy

```
pkg/internal/observability/dashboards/
    overview.json.tmpl
    services.json.tmpl
    ingress.json.tmpl
    cluster.json.tmpl
    deploys.json.tmpl
```

Embedded via `//go:embed` and rendered with `text/template`. Template
context per dashboard:

```go
type dashboardCtx struct {
    AppName    string
    EnvName    string
    Services   []serviceCtx
    Domains    []domainCtx
    Servers    []serverCtx
    HasWorkers bool
    HasHA      bool   // ≥ 2 masters
    Namespace  string // "default" today; future per-app namespaces
}

type serviceCtx struct {
    Name      string
    HasStorage bool
    Domains   []string  // domains routed to this service
}

type domainCtx struct {
    Host    string
    Service string
}

type serverCtx struct {
    Name string
    Role string
    Type string
}
```

Template renders the full Grafana dashboard JSON, including repeating
row variable options when applicable.

## Alert rules

`pkg/internal/observability/grafana/alertrules.go`:

```go
// BuildAlertRules generates the Grafana unified-alerting rule group
// YAML from cfg + the deploy hash. Output is a single ConfigMap
// labeled grafana_alert=1 (sidecar drops into
// /etc/grafana/provisioning/alerting/).
func BuildAlertRules(cfg *config.Config) (*corev1.ConfigMap, error)
```

Rule groups:

### Per-service rules
For every entry in `cfg.Services`:
- **{name}-replicas-not-ready**:
  `kube_deployment_status_replicas_ready{deployment="{name}",namespace="default"}
   < kube_deployment_spec_replicas{deployment="{name}",namespace="default"}`
  for 5m → severity=warning, summary=`{name} has fewer replicas ready than desired`.
- **{name}-crashloop**:
  `rate(kube_pod_container_status_restarts_total{namespace="default",
   pod=~"{name}-.*"}[5m]) > 0` for 10m → severity=warning.

For stateful services (`svc.Storage != nil`):
- **{name}-pvc-full**:
  `kubelet_volume_stats_used_bytes / kubelet_volume_stats_capacity_bytes > 0.85`
  for 10m → severity=warning, summary=`{name} PVC > 85% full`.

### Per-domain rules
For every entry in `cfg.Domains`:
- **{service}-5xx-rate**:
  `sum(rate(traefik_service_requests_total{code=~"5..",service=~"{service}.*"}[5m]))
   / sum(rate(traefik_service_requests_total{service=~"{service}.*"}[5m])) > 0.01`
  for 5m → severity=critical, summary=`{service} 5xx rate > 1%`.

### Per-cert rules
For every domain in `cfg.Domains`:
- **{domain}-cert-expiring**:
  `(certmanager_certificate_expiration_timestamp_seconds{name="<sanitized>"} - time())
   / 86400 < 7` → severity=warning, summary=`cert for {domain} expires
  in < 7 days`.

### Cluster rules
- **node-not-ready**:
  `kube_node_status_condition{condition="Ready",status="true"} == 0`
  for 5m → severity=critical.
- **etcd-no-leader** (only when N≥2 masters):
  `max(etcd_server_has_leader) == 0` for 1m → severity=critical.

All rules use the default notification policy (built in PR 6), which
routes to every configured channel.

## Tests

`pkg/internal/observability/dashboards_test.go`:
- TestBuildOverview_Empty — no services, no domains; dashboard
  renders without errors.
- TestBuildOverview_TwoServices — golden file: same services as
  `examples/ha.yaml`; assert ConfigMap.Data["dashboard.json"]
  matches a checked-in golden.
- TestBuildServices_RepeatingRowVariableOptions — variable.options
  list matches cfg.Services keys exactly, sorted.
- TestBuildIngress_NoDomains — dashboard renders with a "No domains"
  placeholder panel.
- TestBuildCluster_HA — etcd panels appear when len(masters) >= 2.
- TestBuildCluster_NoHA — etcd panels absent when single master.
- TestBuildCluster_NoStateful — PVC panels absent when no service
  has Storage set.

`pkg/internal/observability/grafana/alertrules_test.go`:
- TestBuildAlertRules_PerServiceShape — for cfg.Services = {web,
  postgres}, asserts a rule group exists for each, with each
  expected rule name.
- TestBuildAlertRules_StatefulHasPVCRule — postgres (HasStorage) gets
  a `postgres-pvc-full` rule; web (no storage) does not.
- TestBuildAlertRules_HACluster — etcd-no-leader rule appears only
  when len(masters) >= 2.

Golden files live in
`pkg/internal/observability/testdata/dashboards/*.json` — small
fixtures, not full dashboards. Focus on structural assertions, not
byte-for-byte JSON. Helpers in `pkg/internal/observability/testutil.go`
that parse the rendered JSON and assert specific fields (panel count,
variable options) rather than diffing whole files (Grafana JSON has
generated IDs that flake byte-diffs).

## Acceptance

1. `bin/test` green.
2. Manual: run a generator against a fixture YAML; copy the rendered
   ConfigMap into a kind/k3d cluster with a Grafana already running;
   visually verify the dashboard renders with sensible panels.
3. ConfigMaps owner-labeled correctly; SweepOwned would manage
   their lifecycle.

## Decisions deferred to implementation

- Whether to ship one ConfigMap per dashboard or one ConfigMap per
  "kind" (one for all dashboards). One per dashboard is cleaner for
  SweepOwned + per-dashboard updates, but ConfigMap counts add up.
  Decision: one per dashboard for v1; revisit if cluster object
  counts become a real concern.
- Template language. text/template is sufficient for the JSON
  patterns here. html/template is overkill.
- Whether the dashboard JSON uses Grafana 11+ schema or stays on 10
  for broader compatibility. Match the Grafana version pinned in
  PR 4's `buildGrafana`.

## Out of scope (do NOT do in this PR)

- Wiring into deploy (PR 6).
- Datasource provisioning (PR 6).
- Contact point provisioning (PR 6).
- The `nvoi monitor` verb (PR 7).
