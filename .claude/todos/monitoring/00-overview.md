# Monitoring — Overview & Shared Contract

Branch: `feat/monitor`.

Adds `nvoi monitor` — a preconfigured Grafana + Prometheus/Thanos + Loki stack
that derives every dashboard, datasource, alert rule, and contact point
from the operator's existing `nvoi.yaml`. The operator never opens
Grafana JSON; the stack is opinionated, owner-labeled, sweep-reconciled.

## Philosophy fit (do not violate)

- **One YAML key activates everything.** `monitor:` block at top level.
  Presence is the toggle — no `enabled: true` field. Same convention as
  `build:` / `storage:` / `domains:`.
- **Layout is 100% inferred.** Operator does NOT customize dashboards
  per-service. The YAML describes services + domains + servers; nvoi
  decides what panels appear.
- **Owner-labeled + sweep-reconciled.** Two new owner consts
  (`OwnerAddons`, `OwnerObservability`). Drop the block → SweepOwned
  reaps the stack. Buckets persist; deleted only on `nvoi destroy`.
- **Provider pattern mirrors existing.** `SMSProvider` / `EmailProvider`
  return a portable `Receiver` struct. Grafana coupling lives in ONE
  place (`pkg/internal/observability/grafana/`). Provider impls have
  ZERO Grafana imports.
- **No bespoke daemons.** Grafana's native receivers do the actual
  Slack/Twilio/Postmark sending. nvoi orchestrates; it does not run
  runtime alert-dispatch code.
- **JSONL canonical, text projection.** New `KindMonitor` log kind
  added to the closed enum. Same projection rules.

## Final YAML shape

```yaml
monitor:
  domain:
    grafana.nvoi.to # OPTIONAL. When set, public Ingress
    # with cert-manager-issued TLS.
    # When unset, tunnel-only via
    # `nvoi monitor`.

  admin_password:
    $GRAFANA_ADMIN_PWD # REQUIRED when domain: is set.
    # $VAR resolved at cmd/cli boundary.

  alerts: # OPTIONAL. When unset, alerts
    # fire to the dashboard only.
    slack: $SLACK_WEBHOOK_URL # Plain field, single webhook URL.
    email:
      provider: postmark # Registered EmailProvider name.
      token: $POSTMARK_SERVER_TOKEN
      from: alerts@nvoi.to
      to: [ops@nvoi.to]
    sms:
      provider: twilio # Registered SMSProvider name.
      account: $TWILIO_ACCOUNT_SID
      token: $TWILIO_AUTH_TOKEN
      from: $TWILIO_FROM_NUMBER
      to: ["+33611223344"]
```

Minimum valid form: `monitor: {}` — stack on, tunnel-only access, no
alert routing.

## Shared contract referenced by every phase

### Owner taxonomy (added in `pkg/internal/kube/owned.go`)

```go
const (
    OwnerAddons        = "addons"        // metrics-server + future cluster-level prereqs
    OwnerObservability = "observability" // Prom + Thanos + Loki + Grafana + dashboards + alerts
)
```

### Namespace

`nvoi-observability` — dedicated, created by the observability deploy
phase. App workloads stay in `default`. Sweep scope is per-namespace,
strict boundary.

### Log kind (added in `pkg/log/log.go`)

```go
const KindMonitor Kind = "monitor"
```

Closed enum addition. Every observability phase step / event scopes
through `rt.Log.Sub(log.KindMonitor)`.

### Bucket naming (additive, mirrors `nvoi-{app}-{env}-tfstate`)

- `nvoi-{app}-{env}-logs` — Loki chunks
- `nvoi-{app}-{env}-metrics` — Thanos blocks

Provisioned via the existing `BucketProvider` interface. Same credential
surface as tfstate. Deleted only on `nvoi destroy`.

### Sizing budget

Total resident memory budget for the full stack: ~1 GB.

| Component            | Mem request | CPU request |
| -------------------- | ----------- | ----------- |
| Prometheus           | 256Mi       | 250m        |
| Thanos sidecar       | 128Mi       | 100m        |
| Thanos querier       | 128Mi       | 100m        |
| Thanos store         | 128Mi       | 100m        |
| Loki                 | 256Mi       | 250m        |
| Promtail (DaemonSet) | 64Mi        | 100m        |
| Grafana              | 128Mi       | 100m        |

Fits on a cax21 worker. Tight on cax11. `pkg/config/validate.go`
emits a warn when `monitor:` set and no worker exists and the only
master is cax11-class.

## Sequencing (PR breakdown)

| PR  | File                     | Depends on |
| --- | ------------------------ | ---------- |
| 1   | `01-metrics-server.md`   | —          |
| 2   | `02-notify-providers.md` | —          |
| 3   | `03-config.md`           | 02         |
| 4   | `04-manifests.md`        | 03         |
| 5   | `05-dashboards.md`       | 04         |
| 6   | `06-deploy-phase.md`     | 04, 05     |
| 7   | `07-monitor-verb.md`     | 06         |

PRs 1-3 ship before any cluster-side observability code exists.
PR 1 is independently useful (`kubectl top` works after it lands).

## Cross-cutting invariants every phase must respect

1. **No `os.*` outside cmd/ boundary** (except the three sanctioned
   exceptions: `runtime.Build`, `runner/install.go`, `config.LoadFile`).
2. **ctx is a parameter end-to-end. Never on a struct.**
3. **>4 args = struct.** Bundle until you fit.
4. **`*runtime.Runtime` is read-only after Build.** Do not mutate.
5. **Every kube write goes through `kc.ApplyOwned(scope, obj)`.**
   Stamps `nvoi/owner=<scope.Owner>` automatically.
6. **Every kube cleanup goes through `kc.SweepOwned`.**
7. **NOTHING writes to os.Stdout/Stderr.** Everything through
   `pkg/log`.
8. **Session methods take only (ctx, narrow-input).** rt/run/lg are
   fields.
9. **Tests use `pkg/testutil/sshfake` + `pkg/testutil/hcltest`.**
   No new test infra unless genuinely missing.

## Open questions resolved during planning

| Q                                              | A                                                                                        |
| ---------------------------------------------- | ---------------------------------------------------------------------------------------- |
| Backend: local PVC or object storage for Prom? | Thanos sidecar → object storage. Symmetric with Loki.                                    |
| Grafana access default?                        | `nvoi monitor` SSH tunnel. Public Ingress opt-in via `monitor.domain`.                   |
| Per-service dashboard customization?           | None. 100% inferred from existing YAML.                                                  |
| SMS provider abstraction?                      | Yes — same registry pattern as `BucketProvider`.                                         |
| Email provider abstraction?                    | Yes — symmetric with SMS. Postmark is v1 impl.                                           |
| Slack provider abstraction?                    | No — single webhook URL, no alternatives to abstract.                                    |
| Receiver wire format?                          | Portable `Receiver` struct. Grafana YAML lives in `pkg/internal/observability/grafana/`. |
| Notification send path?                        | Grafana sends. nvoi renders config. No bespoke daemon.                                   |
| metrics-server gated by monitor?               | No — independent addon, useful regardless.                                               |
