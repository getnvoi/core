# PR 4 — Bucket provisioning + observability namespace + stack manifests

## Goal

Provision the two object-storage buckets, create the
`nvoi-observability` namespace, and build the typed Kubernetes
manifests for Prometheus + Thanos sidecar/querier/store + Loki +
Promtail + Grafana. After this PR the manifests exist as typed Go
values returned by builder functions — but they are NOT yet wired
into the deploy pipeline (PR 6 does that) and Grafana has no
dashboards / datasources / contact points yet (PR 5 + PR 6).

## Scope

In-scope:
- `pkg/internal/observability/buckets.go` — provisions
  `-logs` + `-metrics` buckets via `BucketProvider`.
- `pkg/internal/observability/manifests.go` — typed manifest
  builders for the full stack.
- `pkg/internal/observability/namespace.go` — namespace ensure helper.
- Extend `pkg/providers/BucketProvider` interface if needed (see
  below).
- Per-builder unit tests with golden assertions on labels, requests,
  selectors, secret refs.

Out of scope:
- Wiring into `pkg/deploy.Run` (PR 6).
- Dashboard / alert / contact-point provisioning (PR 5 / PR 6).
- The `nvoi monitor` verb (PR 7).

## Buckets

### `pkg/providers/bucket.go` — interface check

If `BucketProvider` does not already expose a generic
`EnsureBucket(ctx, name) (*Backend, error)`, factor the tfstate
bucket logic out of `pkg/state` into the interface. Two callers
become symmetric:
- `pkg/state.Configure` calls `BucketProvider.EnsureBucket(ctx,
  "nvoi-{app}-{env}-tfstate")`.
- `pkg/internal/observability/buckets.go` calls it twice for
  `-logs` and `-metrics`.

Implementation note: the existing R2 bucket ensure code in
`pkg/providers/cloudflare/bucket.go` likely already operates on a
caller-supplied name — confirm during PR 4 and lift only what's
necessary.

### `pkg/internal/observability/buckets.go`

```go
package observability

import (
    "context"
    "fmt"

    "github.com/getnvoi/core/pkg/log"
    "github.com/getnvoi/core/pkg/naming"
    "github.com/getnvoi/core/pkg/providers"
    "github.com/getnvoi/core/pkg/runtime"
)

// BucketCreds bundles the resolved S3-compatible credentials Loki +
// Thanos consume. Same shape both stacks expect. Single source of
// truth — built once per deploy, threaded into every manifest builder
// that needs it.
type BucketCreds struct {
    Endpoint  string // e.g. https://<account>.r2.cloudflarestorage.com
    Region    string // typically "auto" for R2
    AccessKey string
    SecretKey string
    LogsBucket    string
    MetricsBucket string
}

// EnsureBuckets provisions the two observability buckets and returns
// their resolved credentials. Idempotent — re-running against an
// existing bucket is a no-op.
func EnsureBuckets(ctx context.Context, rt *runtime.Runtime, lg log.Log) (BucketCreds, error) {
    bp, err := providers.ResolveBucket(rt.Cfg.Providers.Storage)
    if err != nil { return BucketCreds{}, err }

    logsName := fmt.Sprintf("nvoi-%s-%s-logs", rt.Cfg.App, rt.Cfg.Env)
    metricsName := fmt.Sprintf("nvoi-%s-%s-metrics", rt.Cfg.App, rt.Cfg.Env)

    lg.Step("monitor-bucket-logs")
    if _, err := bp.EnsureBucket(ctx, logsName); err != nil { return BucketCreds{}, err }
    lg.Step("monitor-bucket-metrics")
    if _, err := bp.EnsureBucket(ctx, metricsName); err != nil { return BucketCreds{}, err }

    // Read credentials (already on rt via state.Configure path —
    // see existing tfstate flow for the source-of-truth read).
    return BucketCreds{
        Endpoint:      rt.Backend.Endpoint,
        Region:        rt.Backend.Region,
        AccessKey:     rt.Backend.AccessKey,
        SecretKey:     rt.Backend.SecretKey,
        LogsBucket:    logsName,
        MetricsBucket: metricsName,
    }, nil
}
```

Naming centralizes in `pkg/naming/naming.go` — add:

```go
func ObservabilityLogsBucket(app, env string)    string { return Prefix(app, env) + "-logs" }
func ObservabilityMetricsBucket(app, env string) string { return Prefix(app, env) + "-metrics" }
```

(`Prefix` already exists and returns `nvoi-{app}-{env}`.)

## Namespace

`pkg/internal/observability/namespace.go`:

```go
package observability

import (
    "context"

    corev1 "k8s.io/api/core/v1"
    apierrors "k8s.io/apimachinery/pkg/api/errors"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

    "github.com/getnvoi/core/pkg/internal/kube"
)

const Namespace = "nvoi-observability"

// EnsureNamespace creates Namespace if it doesn't exist. Idempotent.
func EnsureNamespace(ctx context.Context, kc *kube.Client) error {
    ns := &corev1.Namespace{
        ObjectMeta: metav1.ObjectMeta{
            Name:   Namespace,
            Labels: map[string]string{kube.LabelOwner: kube.OwnerObservability},
        },
    }
    _, err := kc.CS.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
    if apierrors.IsAlreadyExists(err) { return nil }
    return err
}
```

Note: namespaces aren't in the `Kind` enum today — `ApplyOwned`
doesn't dispatch them. Adding `KindNamespace` to `pkg/internal/kube/owned.go`
is the clean play (one new case in each of `listOwned` / `deleteByKind`
/ `ApplyOwned`). Do that in this PR.

## Manifest builders

`pkg/internal/observability/manifests.go` — keep each builder ~60-100
lines, single responsibility. All builders return typed
`runtime.Object`s ready for `kc.ApplyOwned`. Owner label =
`OwnerObservability`. Namespace = `nvoi-observability`.

### Prometheus + Thanos sidecar

`buildPrometheus(rt, creds) (*appsv1.StatefulSet, *corev1.Service, *corev1.ConfigMap, *corev1.Secret)`

- StatefulSet, 1 replica.
- Two containers in one pod:
  - `prometheus` — official `prom/prometheus:v2.55.x` image.
    - Args: `--config.file=/etc/prometheus/prometheus.yml`,
      `--storage.tsdb.path=/prometheus`,
      `--storage.tsdb.min-block-duration=2h`,
      `--storage.tsdb.max-block-duration=2h` (required for Thanos
      sidecar to ship blocks at 2h boundaries),
      `--web.enable-lifecycle`.
    - Mounts: `/etc/prometheus` from ConfigMap, `/prometheus` from
      emptyDir (block ships every 2h to S3 — local WAL only).
  - `thanos-sidecar` — `thanosio/thanos:v0.36.x` image.
    - Args: `sidecar`, `--prometheus.url=http://localhost:9090`,
      `--tsdb.path=/prometheus`,
      `--objstore.config-file=/etc/thanos/objstore.yml`.
    - Mounts: `/prometheus` (shared with prom container),
      `/etc/thanos` from Secret containing objstore config.
- ConfigMap: prometheus.yml with scrape configs targeting:
  - kube-state-metrics (when running — independent question, NOT in
    PR 4; can add in a future PR).
  - cadvisor / kubelet (built into k3s nodes).
  - Traefik metrics endpoint (built into k3s).
  - Loki / Grafana / Prometheus self-scrape.
  - Any pod with annotation `prometheus.io/scrape=true` (covers
    user workloads if/when PR adds the annotation).
- Secret: thanos objstore.yml with S3-compatible creds for the
  `-metrics` bucket.
- Service: ClusterIP, port 9090 (prom) + 10901 (thanos sidecar grpc).

### Thanos Querier

`buildThanosQuerier(rt) (*appsv1.Deployment, *corev1.Service)`

- Deployment, 1 replica, ~128Mi.
- Args: `query`,
  `--store=prometheus-headless.nvoi-observability.svc:10901`,
  `--store=thanos-store.nvoi-observability.svc:10901`,
  `--query.replica-label=replica`.
- Service: ClusterIP port 9090 — this is what Grafana points at as the
  Prometheus datasource (NOT the prom service directly).

### Thanos Store

`buildThanosStore(rt, creds) (*appsv1.Deployment, *corev1.Service, *corev1.Secret)`

- Deployment, 1 replica.
- Args: `store`, `--objstore.config-file=/etc/thanos/objstore.yml`,
  `--data-dir=/data`.
- Mounts: `/etc/thanos` from Secret, `/data` from emptyDir (local
  cache).
- Service: ClusterIP port 10901 (grpc, consumed by querier).
- Reuses the same Secret shape as the prom sidecar.

### Loki single-binary

`buildLoki(rt, creds) (*appsv1.StatefulSet, *corev1.Service, *corev1.ConfigMap, *corev1.Secret)`

- StatefulSet, 1 replica.
- Image: `grafana/loki:3.x`.
- Args: `-config.file=/etc/loki/config.yaml`,
  `-target=all` (single-binary mode).
- ConfigMap: loki config with `aws` storage backend pointing at the
  `-logs` bucket. PVC-less in this design — local cache in emptyDir,
  chunks always shipped to S3.
- Service: ClusterIP port 3100.

### Promtail DaemonSet

`buildPromtail(rt) (*appsv1.DaemonSet, *corev1.ConfigMap, *corev1.ServiceAccount, *rbacv1.ClusterRole, *rbacv1.ClusterRoleBinding)`

- DaemonSet, one pod per node.
- Image: `grafana/promtail:3.x`.
- Mounts: `/var/log/containers` (host), `/var/log/pods` (host),
  `/var/lib/docker/containers` (host).
- ConfigMap: promtail config with kubernetes_sd_configs +
  pipeline_stages that extract `nvoi/service` from pod labels for
  per-service log filtering in Grafana.
- ServiceAccount + ClusterRole + ClusterRoleBinding for pod
  metadata discovery.

### Grafana

`buildGrafana(rt, mon) (*appsv1.Deployment, *corev1.Service, *corev1.Secret, *corev1.ConfigMap)`

- Deployment, 1 replica.
- Image: `grafana/grafana:11.x`.
- Containers: `grafana` + `k8s-sidecar` (kiwigrid/k8s-sidecar latest).
  Sidecar watches the namespace for ConfigMaps labeled
  `grafana_dashboard=1` / `grafana_datasource=1` /
  `grafana_alert=1` and drops their content into the Grafana
  provisioning directories.
- Mounts: shared emptyDir at `/etc/grafana/provisioning/{dashboards,
  datasources,alerting}` between grafana and sidecar; the dashboard
  ConfigMaps + alert ConfigMaps come from PR 5; the datasource
  ConfigMap + contact-point ConfigMap come from PR 6.
- Env:
  - `GF_SECURITY_ADMIN_PASSWORD` from the admin Secret (when
    `mon.Domain != ""`).
  - `GF_AUTH_ANONYMOUS_ENABLED=true` + `GF_AUTH_ANONYMOUS_ORG_ROLE=Viewer`
    when `mon.Domain == ""` (tunnel-only mode).
  - `GF_SERVER_ROOT_URL=https://{mon.Domain}/` when set.
- Secret: admin password from `rt.Monitor.AdminPassword`.
- Service: ClusterIP port 3000.

### Grafana Ingress + Certificate (conditional)

`buildGrafanaIngress(mon) (*networkingv1.Ingress, []byte /*cert YAML*/)` — only
when `mon.Domain != ""`. Same shape as workload Ingresses in
`pkg/workload/ingress.go`: TLS Secret name derived from
`kube.SanitizeDNS1123(mon.Domain)+"-tls"`, references the existing
`letsencrypt` ClusterIssuer. The Certificate YAML emits in the
`nvoi-observability` namespace (cert-manager watches all namespaces).

## Owner taxonomy entries

`pkg/internal/kube/owned.go` — extend:

```go
const (
    // ... existing ...
    OwnerAddons        = "addons"
    OwnerObservability = "observability"
)
```

Also add `KindNamespace` to the `Kind` closed enum + dispatch in
`listOwned` / `deleteByKind` / `ApplyOwned`.

DaemonSet, ClusterRole, ClusterRoleBinding, ServiceAccount — Promtail
needs these. Add `KindDaemonSet`, `KindClusterRole`,
`KindClusterRoleBinding`, `KindServiceAccount` to the enum +
dispatch. **Decision: add them in this PR — they're real new kinds
the codebase will exercise.**

## Tests

`pkg/internal/observability/manifests_test.go`:
- TestBuildPrometheus_Labels — assert owner=observability stamped on
  every returned object's labels.
- TestBuildPrometheus_Containers — two containers, correct images,
  correct mounts.
- TestBuildPrometheus_ObjstoreSecret — Secret stringData contains
  endpoint + bucket from BucketCreds.
- Symmetric tests for Loki, Promtail, Grafana, Thanos querier/store.
- TestBuildGrafanaIngress_OmittedWhenDomainEmpty.
- TestBuildGrafana_AdminEnv — env var sourced from Secret when domain
  set; anonymous enabled when domain empty.

`pkg/internal/observability/buckets_test.go`:
- Mock BucketProvider; assert EnsureBucket called with correct names.

`pkg/internal/kube/owned_test.go`:
- Round-trip new kinds (KindNamespace, KindDaemonSet, etc.) through
  ApplyOwned + ListOwned + SweepOwned with a fake clientset.

## Acceptance

1. `bin/test` green.
2. Builder outputs visually inspected during PR review — labels,
   selectors, resource requests, namespace, secret refs all correct.
3. No imports of `pkg/internal/observability` from anywhere yet —
   the package compiles standalone (PR 6 wires it).

## Decisions deferred to implementation

- Whether `BucketCreds` lives in `pkg/internal/observability` or
  `pkg/providers`. Lean observability (consumer-local).
- Image version pins. Use latest stable patch as of the PR date;
  pin as consts at the top of `manifests.go` (mirrors
  `CertManagerVersion`).
- Whether to expose the Grafana admin password as a CLI-supplied flag
  vs YAML-only. YAML-only for v1 (consistent with `secrets:`).
- Whether to add `kube-state-metrics` in this PR or as a follow-up.
  Strong arg for in this PR (pre-canned dashboards expect kube-state
  metrics). Decision: include kube-state-metrics builder here.

## Out of scope (do NOT do in this PR)

- Dashboards (PR 5).
- Alert rules (PR 5).
- Datasource provisioning (PR 6).
- Contact point provisioning (PR 6).
- Wiring into `pkg/deploy.Run` (PR 6).
- `nvoi monitor` verb (PR 7).
