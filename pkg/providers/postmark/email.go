// Package postmark implements the Postmark email notify provider.
// Postmark exposes SMTP at smtp.postmarkapp.com:587 with auth =
// (server-token, server-token) — Grafana's built-in "email" receiver
// type handles the SMTP wire. This provider's job is to render the
// portable Receiver carrying the SMTP wiring + secret refs.
//
// Registered in init() via providers.RegisterEmail. Blank-imported in
// cmd/cli/root.go so registration runs at process start.
package postmark

import (
	"strings"

	"github.com/getnvoi/core/pkg/providers"
)

// Schema declares the env var operators reference in their nvoi.yaml
// `monitor.alerts.email` block. Postmark's auth model uses one
// "server token" that doubles as both SMTP user and password.
var Schema = providers.CredentialSchema{
	Name: "postmark",
	Fields: []providers.CredentialField{
		{Key: "token", EnvVar: "POSTMARK_SERVER_TOKEN", Required: true},
	},
}

const secretName = "postmark-creds"

type provider struct{}

// New returns a stateless Postmark email provider. Exported for
// direct construction in tests.
func New() providers.EmailProvider { return provider{} }

func (provider) CredentialSchema() providers.CredentialSchema { return Schema }

// BuildReceiver extracts the per-deploy Postmark fields and builds a
// portable Receiver. Type="postmark" lets the Grafana orchestrator
// emit an SMTP-style contact point pointing at smtp.postmarkapp.com:587.
// The token Secret feeds Grafana's SMTP auth (Postmark uses the same
// token for username and password).
func (provider) BuildReceiver(spec providers.AlertSpec) (providers.Receiver, error) {
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
		Type: "postmark",
		Settings: map[string]string{
			"from": from,
			"to":   strings.Join(to, ","),
		},
		Secrets: []providers.SecretRef{
			{Name: secretName, Key: "token", From: token},
		},
	}, nil
}
