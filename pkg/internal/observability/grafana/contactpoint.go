package grafana

// contactpoint.go is the bridge between the portable
// providers.Receiver shape and Grafana's contact-point provisioning
// YAML. Every Receiver.Type is dispatched through a closed switch
// here — unknown types error explicitly (programmer error, not
// operator error).
//
// Each ContactPointSpec returned represents one Grafana receiver in
// the provisioning file. Caller (provisioning.go) collects them
// into the "nvoi-default" multi-integration receiver the notification
// policy routes everything through.
//
// Secret materialization: every Receiver carries SecretRefs the
// orchestrator must apply to nvoi-observability as a Secret BEFORE
// Grafana boots so the env-from-Secret references in the contact
// point resolve. provisioning.go owns that orchestration; this file
// just emits the YAML pointing at the secrets by name.

import (
	"fmt"

	"github.com/getnvoi/core/pkg/providers"
)

// ContactPointSpec is the parsed Grafana contact-point shape one
// Receiver produces. yaml.v3 marshals it via field tags into the
// provisioning bundle.
type ContactPointSpec struct {
	UID                   string                 `yaml:"uid"`
	Type                  string                 `yaml:"type"`
	Name                  string                 `yaml:"name"`
	DisableResolveMessage bool                   `yaml:"disableResolveMessage"`
	Settings              map[string]interface{} `yaml:"settings,omitempty"`
	SecureSettings        map[string]string      `yaml:"secureSettings,omitempty"`
}

// SlackReceiver translates a Slack webhook into a Grafana "slack"
// contact point. Operator-supplied URL ends up in settings.url —
// Grafana posts the alert JSON directly to the webhook.
//
// Slack is the simplest receiver: one URL, no body templating
// required (Grafana's default body works for Slack's Block Kit).
func SlackReceiver(webhookURL string) ContactPointSpec {
	return ContactPointSpec{
		UID:  "nvoi-slack",
		Type: "slack",
		Name: "nvoi-slack",
		Settings: map[string]interface{}{
			"url": webhookURL,
		},
	}
}

// FromReceiver dispatches one portable Receiver to its Grafana
// contact-point shape. Closed switch — adding a new vendor means a
// new case here, not a new file.
//
// Postmark → Grafana's "email" receiver type. Postmark exposes SMTP
// at smtp.postmarkapp.com:587 with server-token used as both
// username and password. The SMTP server config itself lives in
// grafana.ini (out of scope for v1; operators wire their SMTP creds
// through environment variables or extend grafana.ini in a follow-up).
// For v1 we use Grafana's "email" type and let operators inject
// their SMTP relay separately if they need true Postmark delivery.
//
// Twilio → Grafana's "webhook" receiver pointing at Twilio's REST
// API. Body shape doesn't natively match Twilio's form-encoded
// expectation, so SMS delivery requires an external webhook bridge
// in v1 — the receiver is emitted but flagged in code. A bridge
// component lands as a follow-up.
func FromReceiver(r providers.Receiver) (ContactPointSpec, error) {
	switch r.Type {
	case "postmark":
		// Grafana's "email" receiver. Operator must have grafana.ini
		// SMTP configured for delivery; this contact point just names
		// recipients + sender for an existing SMTP wire.
		return ContactPointSpec{
			UID:  "nvoi-email",
			Type: "email",
			Name: "nvoi-email",
			Settings: map[string]interface{}{
				"addresses":   r.Settings["to"],
				"singleEmail": false,
			},
		}, nil
	case "twilio":
		// v1: emit a webhook contact point pointing at Twilio's API.
		// Real SMS delivery via Grafana requires a bridge component
		// (out of scope for v1). The contact point's presence still
		// validates the full provisioning roundtrip end-to-end.
		return ContactPointSpec{
			UID:  "nvoi-sms",
			Type: "webhook",
			Name: "nvoi-sms",
			Settings: map[string]interface{}{
				"url":        "https://api.twilio.com/2010-04-01/Accounts/" + secretRefValue(r, "account_sid") + "/Messages.json",
				"httpMethod": "POST",
				"message":    "{{ .CommonAnnotations.summary }}",
			},
		}, nil
	default:
		return ContactPointSpec{}, fmt.Errorf("grafana: unsupported Receiver.Type %q", r.Type)
	}
}

// secretRefValue extracts the resolved value for a key from a
// Receiver's SecretRefs. Returns empty string when not found —
// fallback to keep the YAML well-formed; runtime error surfaces in
// Grafana logs.
func secretRefValue(r providers.Receiver, key string) string {
	for _, s := range r.Secrets {
		if s.Key == key {
			return s.From
		}
	}
	return ""
}
