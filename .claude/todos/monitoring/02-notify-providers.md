# PR 2 — Notify provider infrastructure (SMS, Email)

## Goal

Add the `SMSProvider` / `EmailProvider` interfaces, the registry, and
the v1 concrete implementations (Twilio for SMS, Postmark for email).
Pure code, fully unit-testable, zero cluster impact. Lays the
foundation for PR 6 (which assembles `Receiver`s into Grafana
provisioning YAML).

## Scope

In-scope:
- `pkg/providers/notify.go` — `Receiver`, `SecretRef`, shared types.
- `pkg/providers/sms.go` — `SMSProvider` + registry.
- `pkg/providers/email.go` — `EmailProvider` + registry.
- `pkg/providers/twilio/sms.go` + `register.go`.
- `pkg/providers/postmark/email.go` + `register.go`.
- Blank-imports in `cmd/cli/main.go`.

Out of scope:
- The `monitor:` config block (PR 3).
- Grafana contact-point YAML rendering (PR 6 — lives in
  `pkg/internal/observability/grafana/`).
- Slack — stays a plain field on `MonitorSpec.Alerts` in PR 3, no
  provider abstraction.

## Architectural rationale

Providers know **vendor and credentials**. They produce a portable
`Receiver` struct. Grafana coupling lives in ONE place (PR 6). If we
ever swap Grafana for Alertmanager-direct, the provider impls do not
change.

Mirrors `BucketProvider` exactly: `BucketProvider` returns abstract
bucket + creds; consumer (`pkg/state`) wires to tofu's s3 backend.
Same shape here.

## Files

### New: `pkg/providers/notify.go`

```go
// Package providers — notify.go defines the portable notification
// receiver type and the secret-reference shape every notify provider
// returns. Consumers (the Grafana orchestrator in
// pkg/internal/observability) translate Receivers to whichever
// notification system is currently wired.
//
// Notify providers (SMS, Email) intentionally do NOT know about
// Grafana. They know their vendor and their credentials, and they
// return Receivers. The Grafana coupling lives entirely in
// pkg/internal/observability/grafana/.
package providers

// Receiver is a portable notification-receiver descriptor. Type names
// the vendor canonically ("twilio", "postmark"). Settings carries
// non-secret vendor-specific fields. Secrets carries references to
// secret values the orchestrator must materialize in the
// observability namespace before the receiver is usable.
type Receiver struct {
    Type     string
    Settings map[string]string
    Secrets  []SecretRef
}

// SecretRef is a pointer to a key in a Secret the orchestrator must
// materialize in the observability namespace. Name + Key together
// identify the destination; From is the resolved value (the
// orchestrator stamps the Secret with this).
//
// Putting the value on SecretRef (rather than passing maps around
// separately) keeps the Receiver self-contained — one struct describes
// everything the orchestrator needs to provision this channel.
type SecretRef struct {
    Name string
    Key  string
    From string
}
```

### New: `pkg/providers/sms.go`

```go
package providers

import (
    "fmt"

    "github.com/getnvoi/core/pkg/config"
    "github.com/getnvoi/core/pkg/runtime"
)

// SMSProvider builds a portable Receiver from a resolved AlertSpec.
// Implementations know their vendor (Twilio, Vonage, ...) and the
// shape of their CredentialSchema. They do NOT know about Grafana.
type SMSProvider interface {
    BuildReceiver(rt *runtime.Runtime, spec config.AlertSpec) (Receiver, error)
    CredentialSchema() map[string]string
}

// SMSFactory constructs a fresh SMSProvider per resolution.
// Constructed at Resolve time (not at Register) so the provider can
// keep zero state across calls.
type SMSFactory func() SMSProvider

// Mirrors RegisterBucket / ResolveBucket / etc. — same pattern,
// stored in package-local map guarded by sync.RWMutex.
func RegisterSMS(name string, schema map[string]string, factory SMSFactory) { /* ... */ }
func ResolveSMS(name string) (SMSProvider, error)                            { /* ... */ }
func IsRegisteredSMS(name string) bool                                       { /* ... */ }
func CredentialSchemaForSMS(name string) (map[string]string, error)          { /* ... */ }
```

### New: `pkg/providers/email.go`

Symmetric with `sms.go` — `EmailProvider`, `EmailFactory`,
`RegisterEmail` / `ResolveEmail` / `IsRegisteredEmail` /
`CredentialSchemaForEmail`. Same registry pattern.

### New: `pkg/providers/twilio/sms.go`

```go
// Package twilio — Twilio SMS provider implementation. Registered in
// init() via providers.RegisterSMS. Built-in Grafana receiver type
// "twilio" handles the actual API calls at alert-fire time; this
// provider's job is to render the Receiver descriptor that the
// Grafana orchestrator translates to a contact-point definition.
package twilio

import (
    "fmt"

    "github.com/getnvoi/core/pkg/config"
    "github.com/getnvoi/core/pkg/providers"
    "github.com/getnvoi/core/pkg/runtime"
)

// credentialSchema names the env-var refs operators supply in their
// YAML. Resolved at the cmd/cli boundary (internal/cli/secrets.go-
// adjacent code) into spec.Fields before BuildReceiver sees it.
var credentialSchema = map[string]string{
    "account": "TWILIO_ACCOUNT_SID",
    "token":   "TWILIO_AUTH_TOKEN",
    "from":    "TWILIO_FROM_NUMBER",
}

type twilioSMS struct{}

func (twilioSMS) CredentialSchema() map[string]string { return credentialSchema }

func (twilioSMS) BuildReceiver(rt *runtime.Runtime, spec config.AlertSpec) (providers.Receiver, error) {
    // Extract + validate required fields.
    account, err := stringField(spec, "account")
    if err != nil { return providers.Receiver{}, err }
    token, err := stringField(spec, "token")
    if err != nil { return providers.Receiver{}, err }
    from, err := stringField(spec, "from")
    if err != nil { return providers.Receiver{}, err }
    to, err := stringListField(spec, "to")
    if err != nil { return providers.Receiver{}, err }
    if len(to) == 0 {
        return providers.Receiver{}, fmt.Errorf("twilio: to: must contain at least one number")
    }

    return providers.Receiver{
        Type: "twilio",
        Settings: map[string]string{
            "from": from,
            "to":   strings.Join(to, ","),
        },
        Secrets: []providers.SecretRef{
            {Name: "twilio-creds", Key: "account_sid", From: account},
            {Name: "twilio-creds", Key: "auth_token",  From: token},
        },
    }, nil
}

// stringField / stringListField — small typed accessors over
// spec.Fields (map[string]interface{}). Return clear errors when
// missing / wrong-typed.
```

### New: `pkg/providers/twilio/register.go`

```go
package twilio

import "github.com/getnvoi/core/pkg/providers"

func init() {
    providers.RegisterSMS("twilio", credentialSchema, func() providers.SMSProvider {
        return twilioSMS{}
    })
}
```

### New: `pkg/providers/postmark/email.go` + `register.go`

Symmetric with twilio. Credential schema:

```go
var credentialSchema = map[string]string{
    "token": "POSTMARK_SERVER_TOKEN",
}
```

`BuildReceiver` reads `token`, `from`, `to` from `spec.Fields`,
returns `Receiver{Type: "postmark", ...}`.

### Edited: `cmd/cli/main.go`

Add blank-imports:

```go
_ "github.com/getnvoi/core/pkg/providers/postmark"
_ "github.com/getnvoi/core/pkg/providers/twilio"
```

### Note on `config.AlertSpec`

This type is defined in PR 3 (`pkg/config/config.go`). Because PR 2
references it, the PR-2 code may need to forward-define a minimal
stand-in OR PR 2 + PR 3 ship together. **Decision: PR 3 introduces
the type; PR 2 adds a temporary local interface in
`pkg/providers/notify.go` that gets removed in PR 3.**

Concretely, in PR 2 only:

```go
// Temporary — replaced in PR 3 by config.AlertSpec.
type AlertSpec struct {
    Provider string
    Fields   map[string]interface{}
}
```

And SMSProvider/EmailProvider take `AlertSpec` (this provider package
type) — PR 3 swaps the call sites to `config.AlertSpec` once that
type lands. The shape is identical so the swap is mechanical.

## Tests

`pkg/providers/sms_test.go`:
- Round-trip: Register → Resolve → assert same factory output.
- Resolve unknown name → error.
- IsRegistered true/false.
- CredentialSchemaFor returns the registered schema.

`pkg/providers/email_test.go`: symmetric.

`pkg/providers/twilio/sms_test.go`:
- Fixture spec → assert Receiver fields one-by-one.
- Missing `account` → typed error mentioning the field.
- `to` empty list → typed error.
- `to` as string instead of list → typed error.

`pkg/providers/postmark/email_test.go`: symmetric.

## Acceptance

1. `bin/test` green; new tests have ≥1 case per error path.
2. `cmd/cli/main.go` compiles with the new blank-imports.
3. `providers.ResolveSMS("twilio")` returns a non-nil provider in a
   trivial smoke test.
4. The `Receiver` returned for a fixture spec exactly matches a
   golden expectation (visual inspect during PR review).

## Decisions deferred to implementation

- Whether `stringField` / `stringListField` helpers live in
  `pkg/providers/notify.go` (shared across vendor packages) or in
  each vendor package. Lean shared.
- Error wrapping convention. Match what `pkg/providers/cloudflare/api.go`
  uses for consistency.

## Out of scope (do NOT do in this PR)

- The `monitor:` config block.
- Validation that an alerts.provider references a registered provider
  (PR 3 in `pkg/config/validate.go`).
- Any Grafana YAML.
- Slack handling.
