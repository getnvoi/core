package postmark_test

import (
	"strings"
	"testing"

	"github.com/getnvoi/core/pkg/providers"
	"github.com/getnvoi/core/pkg/providers/postmark"
)

func fixtureSpec() providers.AlertSpec {
	return providers.AlertSpec{
		Provider: "postmark",
		Fields: map[string]interface{}{
			"token": "server-token-xyz",
			"from":  "alerts@nvoi.to",
			"to":    []interface{}{"ops@nvoi.to", "oncall@nvoi.to"},
		},
	}
}

func TestPostmark_BuildReceiver_Happy(t *testing.T) {
	p := postmark.New()
	r, err := p.BuildReceiver(fixtureSpec())
	if err != nil {
		t.Fatalf("BuildReceiver: %v", err)
	}

	if r.Type != "postmark" {
		t.Errorf("Type = %q, want postmark", r.Type)
	}
	if r.Settings["from"] != "alerts@nvoi.to" {
		t.Errorf("Settings.from = %q", r.Settings["from"])
	}
	if r.Settings["to"] != "ops@nvoi.to,oncall@nvoi.to" {
		t.Errorf("Settings.to = %q", r.Settings["to"])
	}
	if len(r.Secrets) != 1 {
		t.Fatalf("len(Secrets) = %d, want 1", len(r.Secrets))
	}
	if r.Secrets[0].Name != "postmark-creds" || r.Secrets[0].Key != "token" {
		t.Errorf("Secrets[0] = %+v", r.Secrets[0])
	}
	if r.Secrets[0].From != "server-token-xyz" {
		t.Errorf("Secrets[0].From = %q", r.Secrets[0].From)
	}
}

func TestPostmark_BuildReceiver_MissingToken(t *testing.T) {
	spec := fixtureSpec()
	delete(spec.Fields, "token")
	_, err := postmark.New().BuildReceiver(spec)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "token") {
		t.Errorf("error should name the missing field: %v", err)
	}
}

func TestPostmark_BuildReceiver_MissingFrom(t *testing.T) {
	spec := fixtureSpec()
	delete(spec.Fields, "from")
	_, err := postmark.New().BuildReceiver(spec)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPostmark_IsRegistered(t *testing.T) {
	if !providers.IsRegisteredEmail("postmark") {
		t.Fatal("postmark not registered via init()")
	}
	resolved, err := providers.ResolveEmail("postmark")
	if err != nil {
		t.Fatalf("ResolveEmail: %v", err)
	}
	if _, err := resolved.BuildReceiver(fixtureSpec()); err != nil {
		t.Errorf("resolved provider BuildReceiver: %v", err)
	}
}
