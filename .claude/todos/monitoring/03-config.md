# PR 3 — `monitor:` config block + validation + secret resolution

## Goal

Add the `monitor:` top-level YAML key, the typed `MonitorSpec` /
`AlertsSpec` / `AlertSpec` types, validation rules, and the
cmd/cli-boundary secret resolution that materializes `$VAR` references
into concrete values for downstream consumers.

After this PR, the YAML parses, validates, and lands resolved values
on `rt.Monitor` — but no cluster code consumes them yet (PR 4+).

## Scope

In-scope:
- `pkg/config/config.go` — `MonitorSpec`, `AlertsSpec`, `AlertSpec`.
- `pkg/config/validate.go` — validation rules.
- `pkg/runtime/runtime.go` — `Runtime.Monitor *ResolvedMonitor`.
- `internal/cli/inputs.go` — resolution at the cmd/cli boundary.
- Update PR 2's temporary `providers.AlertSpec` callers to use
  `config.AlertSpec`.

Out of scope:
- Any cluster manifests (PR 4).
- Any dashboards (PR 5).

## YAML shape (re-stated for completeness)

```yaml
monitor:
  domain: grafana.nvoi.to
  admin_password: $GRAFANA_ADMIN_PWD
  alerts:
    slack: $SLACK_WEBHOOK_URL
    email:
      provider: postmark
      token: $POSTMARK_SERVER_TOKEN
      from:  alerts@nvoi.to
      to:    [ops@nvoi.to]
    sms:
      provider: twilio
      account: $TWILIO_ACCOUNT_SID
      token:   $TWILIO_AUTH_TOKEN
      from:    $TWILIO_FROM_NUMBER
      to:      ["+33611223344"]
```

## Files

### Edited: `pkg/config/config.go`

Add field:

```go
type Config struct {
    // ... existing ...
    Monitor *MonitorSpec `yaml:"monitor,omitempty"`
}
```

Add types:

```go
// MonitorSpec activates the observability stack when present. Mirrors
// the convention used by `build:` / `storage:` / `domains:` — presence
// is the toggle. Zero-value (`monitor: {}`) enables the stack in its
// minimum form: tunnel-only access, no alert routing.
//
// Domain, when set, gates a public Ingress for Grafana with TLS via
// the existing cert-manager wiring. Requires providers.dns to be set
// (same rule as the existing top-level `domains:` field).
//
// AdminPassword is a $VAR reference (resolved at the cmd/cli boundary
// against os.Getenv) that becomes the Grafana admin password. Required
// when Domain is set; ignored otherwise (tunnel-only access uses
// anonymous read-only login).
type MonitorSpec struct {
    Domain        string      `yaml:"domain,omitempty"`
    AdminPassword string      `yaml:"admin_password,omitempty"`
    Alerts        *AlertsSpec `yaml:"alerts,omitempty"`
}

// AlertsSpec configures notification channels. Each non-nil field is
// provisioned as a contact point + included in the default
// notification policy (every firing alert routes through every
// configured channel). When AlertsSpec is nil, alerts fire to the
// Grafana dashboard only.
type AlertsSpec struct {
    Slack string     `yaml:"slack,omitempty"` // webhook URL; $VAR resolved
    Email *AlertSpec `yaml:"email,omitempty"`
    SMS   *AlertSpec `yaml:"sms,omitempty"`
}

// AlertSpec is the YAML shape for a provider-dispatched alert channel
// (email, sms). Provider is the registered provider name (e.g.
// "twilio", "postmark"). Fields holds the vendor-specific rest; $VAR
// references inside Fields are resolved at the cmd/cli boundary
// before reaching the provider's BuildReceiver.
type AlertSpec struct {
    Provider string                 `yaml:"provider"`
    Fields   map[string]interface{} `yaml:",inline"`
}
```

`UnmarshalYAML` may be needed for `AlertSpec` depending on whether
`yaml.v3` cleanly handles the inline-rest pattern with mixed types.
Test before implementing custom unmarshaling.

### Edited: `pkg/config/validate.go`

New validation rules (each returns a clear error message):

1. `monitor.domain != ""` requires `providers.dns != ""`.
2. `monitor.domain != ""` requires `monitor.admin_password != ""`.
3. `monitor.alerts.email != nil` requires
   `providers.IsRegisteredEmail(monitor.alerts.email.provider)`.
4. `monitor.alerts.sms != nil` requires
   `providers.IsRegisteredSMS(monitor.alerts.sms.provider)`.
5. `monitor != nil` requires `providers.storage != ""` (Thanos + Loki
   need object storage; no PVC fallback in this design).
6. Warn (not error) when `monitor != nil` and the cluster lacks a
   worker AND the only master is cax11-class — sizing budget is tight.

### Edited: `pkg/runtime/runtime.go`

```go
type Runtime struct {
    // ... existing ...

    // Monitor holds the env-resolved monitor block, or nil when the
    // YAML didn't set `monitor:`. Resolved at the cmd/cli boundary
    // (internal/cli) — internal packages read it, never construct it.
    Monitor *ResolvedMonitor
}

// ResolvedMonitor is MonitorSpec with every $VAR substituted to a
// concrete value. AdminPassword, Alerts.Slack, and every Fields
// value inside Alerts.Email/SMS go through env-var resolution before
// landing here.
type ResolvedMonitor struct {
    Domain        string
    AdminPassword string
    Alerts        *ResolvedAlerts
}

type ResolvedAlerts struct {
    Slack string                // resolved webhook URL
    Email *config.AlertSpec     // Fields values are resolved
    SMS   *config.AlertSpec     // Fields values are resolved
}
```

Note: `Email`/`SMS` keep the `config.AlertSpec` type — the resolution
substitutes inside `Fields` in-place. Resolved `AlertSpec` and raw
`AlertSpec` share the same Go type; the difference is whether
`$VAR` strings have been substituted. Acceptable because the provider
contract is "values arrive resolved"; the type is transport.

### New: helper in `internal/cli/inputs.go` (or new file)

```go
// ResolveMonitor takes the raw cfg.Monitor + an env-getter and
// returns a *ResolvedMonitor with every $VAR substituted. Returns
// nil when cfg.Monitor is nil. Errors when a $VAR has no
// corresponding env value (same behavior as ResolveSecrets).
func ResolveMonitor(cfg *config.Config, getenv func(string) string) (*runtime.ResolvedMonitor, error)
```

Wire it into `PrepareRuntime` (`internal/cli/load.go`) right after
`ResolveSecrets`. Stamp `rt.Monitor` on the runtime.

### Edited: PR 2 callers

`providers.SMSProvider.BuildReceiver` and
`providers.EmailProvider.BuildReceiver` now take `config.AlertSpec`
(swap the temporary local `providers.AlertSpec` type used in PR 2).

Imports flip from `providers.AlertSpec` → `config.AlertSpec` in:
- `pkg/providers/twilio/sms.go`
- `pkg/providers/postmark/email.go`
- `pkg/providers/sms.go` (interface signature)
- `pkg/providers/email.go` (interface signature)

Delete the temporary `providers.AlertSpec` struct from PR 2.

## Tests

`pkg/config/validate_test.go` — add cases:
- `monitor: {domain: x.example.com}` with no `providers.dns` → error.
- `monitor: {domain: x.example.com}` with no `admin_password` → error.
- `monitor.alerts.email.provider: nonexistent` → error.
- `monitor.alerts.sms.provider: nonexistent` → error.
- `monitor: {}` with no `providers.storage` → error.
- Valid full spec → no error.

`pkg/config/service_test.go` (or new `monitor_test.go`):
- YAML round-trip: parse → marshal → parse, deep-equal.
- `AlertSpec.Fields` decodes mixed types (string, []string).

`internal/cli/inputs_test.go` — add cases:
- `ResolveMonitor` with all $VARs present → substitution succeeds.
- `ResolveMonitor` with a missing $VAR → error names the missing var.
- `ResolveMonitor(nil)` → returns nil, nil.

## Acceptance

1. `bin/test` green.
2. `examples/` directory: optionally add a new
   `examples/observability.yaml` showing the `monitor:` block in
   context. (Optional — keep PR small.)
3. Manual: parse the fixture YAML with the new types via a tiny
   `bin/nvoi plan` against a test YAML; assert no parse errors.

## Decisions deferred to implementation

- Where the env-var substitution logic lives. There's already a
  `utils.ResolveVar` (per CLAUDE.md). Reuse it.
- Whether to add a separate `ResolvedAlertSpec` type. Decision above
  is to reuse `config.AlertSpec`. If validation pushback during PR
  review, switch to a separate type.
- Yaml inline-rest behavior. If yaml.v3 doesn't cleanly merge a
  named field (`provider`) with an inline map, write a custom
  UnmarshalYAML for AlertSpec.

## Out of scope (do NOT do in this PR)

- Cluster manifests for the stack (PR 4).
- Bucket provisioning for Loki/Thanos (PR 4).
- Dashboards (PR 5).
- The `nvoi monitor` verb (PR 7).
