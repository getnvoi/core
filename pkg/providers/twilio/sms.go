// Package twilio implements the Twilio SMS notify provider. Grafana's
// built-in receiver type "twilio" handles the actual API calls at
// alert-fire time; this provider's job is to render the portable
// Receiver descriptor that the Grafana orchestrator translates to a
// contact-point definition.
//
// Registered in init() via providers.RegisterSMS. Blank-imported in
// cmd/cli/root.go so registration runs at process start.
package twilio

import (
	"strings"

	"github.com/getnvoi/core/pkg/providers"
)

// Schema declares the env vars operators reference in their nvoi.yaml
// `monitor.alerts.sms` block. Same shape as cloudflare.BucketSchema —
// the boundary (internal/cli) resolves $VAR strings against os.Getenv
// before the spec reaches BuildReceiver.
var Schema = providers.CredentialSchema{
	Name: "twilio",
	Fields: []providers.CredentialField{
		{Key: "account", EnvVar: "TWILIO_ACCOUNT_SID", Required: true},
		{Key: "token", EnvVar: "TWILIO_AUTH_TOKEN", Required: true},
		{Key: "from", EnvVar: "TWILIO_FROM_NUMBER", Required: true},
	},
}

// secretName is the Kubernetes Secret in the observability namespace
// that holds the Twilio API credentials. Single secret keyed by the
// Twilio fields cert-manager-style — the orchestrator merges
// SecretRefs with the same Name into one Secret.
const secretName = "twilio-creds"

type provider struct{}

// New returns a stateless Twilio SMS provider. Exported for direct
// construction in tests; production code path goes through
// providers.ResolveSMS.
func New() providers.SMSProvider { return provider{} }

func (provider) CredentialSchema() providers.CredentialSchema { return Schema }

// BuildReceiver extracts the per-deploy Twilio fields from the spec
// and builds a portable Receiver. Settings carries non-secret routing
// (from-number + comma-joined to-numbers); SecretRefs ship the
// account SID + auth token to the orchestrator for materialization.
func (provider) BuildReceiver(spec providers.AlertSpec) (providers.Receiver, error) {
	account, err := providers.StringField(spec, "account")
	if err != nil {
		return providers.Receiver{}, err
	}
	token, err := providers.StringField(spec, "token")
	if err != nil {
		return providers.Receiver{}, err
	}
	from, err := providers.StringField(spec, "from")
	if err != nil {
		return providers.Receiver{}, err
	}
	to, err := providers.StringListField(spec, "to")
	if err != nil {
		return providers.Receiver{}, err
	}

	return providers.Receiver{
		Type: "twilio",
		Settings: map[string]string{
			"from": from,
			"to":   strings.Join(to, ","),
		},
		Secrets: []providers.SecretRef{
			{Name: secretName, Key: "account_sid", From: account},
			{Name: secretName, Key: "auth_token", From: token},
		},
	}, nil
}
