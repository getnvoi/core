package twilio_test

import (
	"strings"
	"testing"

	"github.com/getnvoi/core/pkg/providers"
	"github.com/getnvoi/core/pkg/providers/twilio"
)

func fixtureSpec() providers.AlertSpec {
	return providers.AlertSpec{
		Provider: "twilio",
		Fields: map[string]interface{}{
			"account": "ACxxxxxxxx",
			"token":   "secret-token",
			"from":    "+15558675309",
			"to":      []interface{}{"+33611223344", "+15559998888"},
		},
	}
}

func TestTwilio_BuildReceiver_Happy(t *testing.T) {
	p := twilio.New()
	r, err := p.BuildReceiver(fixtureSpec())
	if err != nil {
		t.Fatalf("BuildReceiver: %v", err)
	}

	if r.Type != "twilio" {
		t.Errorf("Type = %q, want twilio", r.Type)
	}
	if r.Settings["from"] != "+15558675309" {
		t.Errorf("Settings.from = %q", r.Settings["from"])
	}
	// Multiple to-numbers are comma-joined for Grafana's settings shape.
	if r.Settings["to"] != "+33611223344,+15559998888" {
		t.Errorf("Settings.to = %q", r.Settings["to"])
	}
	if len(r.Secrets) != 2 {
		t.Fatalf("len(Secrets) = %d, want 2", len(r.Secrets))
	}
	// Both secrets land in the same k8s Secret (orchestrator merges by Name).
	for _, s := range r.Secrets {
		if s.Name != "twilio-creds" {
			t.Errorf("Secrets[*].Name = %q, want twilio-creds", s.Name)
		}
	}
	// Account-sid keyed and present.
	found := map[string]string{}
	for _, s := range r.Secrets {
		found[s.Key] = s.From
	}
	if found["account_sid"] != "ACxxxxxxxx" {
		t.Errorf("account_sid From = %q", found["account_sid"])
	}
	if found["auth_token"] != "secret-token" {
		t.Errorf("auth_token From = %q", found["auth_token"])
	}
}

func TestTwilio_BuildReceiver_MissingAccount(t *testing.T) {
	spec := fixtureSpec()
	delete(spec.Fields, "account")
	_, err := twilio.New().BuildReceiver(spec)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "account") {
		t.Errorf("error should name the missing field: %v", err)
	}
}

func TestTwilio_BuildReceiver_EmptyTo(t *testing.T) {
	spec := fixtureSpec()
	spec.Fields["to"] = []interface{}{}
	_, err := twilio.New().BuildReceiver(spec)
	if err == nil {
		t.Fatal("expected error for empty to list")
	}
}

func TestTwilio_BuildReceiver_WrongToType(t *testing.T) {
	spec := fixtureSpec()
	spec.Fields["to"] = "+33611223344" // string instead of list
	_, err := twilio.New().BuildReceiver(spec)
	if err == nil {
		t.Fatal("expected error for non-list to")
	}
}

func TestTwilio_IsRegistered(t *testing.T) {
	// init() registers under the canonical name.
	if !providers.IsRegisteredSMS("twilio") {
		t.Fatal("twilio not registered via init()")
	}
	resolved, err := providers.ResolveSMS("twilio")
	if err != nil {
		t.Fatalf("ResolveSMS: %v", err)
	}
	if _, err := resolved.BuildReceiver(fixtureSpec()); err != nil {
		t.Errorf("resolved provider BuildReceiver: %v", err)
	}
}
