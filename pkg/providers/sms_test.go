package providers_test

import (
	"strings"
	"testing"

	"github.com/getnvoi/core/pkg/providers"
)

// fakeSMS is a minimal in-test SMSProvider so registry behavior can
// be exercised without depending on the production Twilio package
// (which would create a load-order coupling).
type fakeSMS struct{ tag string }

func (f fakeSMS) CredentialSchema() providers.CredentialSchema {
	return providers.CredentialSchema{Name: f.tag}
}
func (f fakeSMS) BuildReceiver(_ providers.AlertSpec) (providers.Receiver, error) {
	return providers.Receiver{Type: f.tag}, nil
}

func TestSMSRegistry_RoundTrip(t *testing.T) {
	// Use unique name to avoid colliding with twilio (which may have
	// been registered transitively by other tests in this package
	// run order).
	name := "smstest-roundtrip"
	providers.RegisterSMS(name,
		providers.CredentialSchema{Name: name},
		func() providers.SMSProvider { return fakeSMS{tag: name} },
	)

	if !providers.IsRegisteredSMS(name) {
		t.Fatalf("IsRegisteredSMS(%q) = false after RegisterSMS", name)
	}

	p, err := providers.ResolveSMS(name)
	if err != nil {
		t.Fatalf("ResolveSMS: %v", err)
	}
	rcv, err := p.BuildReceiver(providers.AlertSpec{Provider: name})
	if err != nil {
		t.Fatalf("BuildReceiver: %v", err)
	}
	if rcv.Type != name {
		t.Errorf("Type = %q, want %q", rcv.Type, name)
	}

	schema, err := providers.CredentialSchemaForSMS(name)
	if err != nil {
		t.Fatalf("CredentialSchemaForSMS: %v", err)
	}
	if schema.Name != name {
		t.Errorf("schema.Name = %q, want %q", schema.Name, name)
	}
}

func TestSMSRegistry_UnknownProvider(t *testing.T) {
	if providers.IsRegisteredSMS("nonexistent-sms-xyz") {
		t.Fatal("IsRegisteredSMS should return false for unknown")
	}
	if _, err := providers.ResolveSMS("nonexistent-sms-xyz"); err == nil {
		t.Fatal("ResolveSMS unknown name should error")
	} else if !strings.Contains(err.Error(), "unknown sms provider") {
		t.Errorf("unexpected error message: %v", err)
	}
	if _, err := providers.CredentialSchemaForSMS("nonexistent-sms-xyz"); err == nil {
		t.Fatal("CredentialSchemaForSMS unknown name should error")
	}
}

func TestSMSRegistry_DuplicateRegistrationPanics(t *testing.T) {
	name := "smstest-dup"
	providers.RegisterSMS(name,
		providers.CredentialSchema{Name: name},
		func() providers.SMSProvider { return fakeSMS{tag: name} },
	)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on duplicate RegisterSMS")
		}
		msg, _ := r.(string)
		if !strings.Contains(msg, "duplicate sms provider") {
			t.Errorf("panic message wrong: %v", r)
		}
	}()
	providers.RegisterSMS(name,
		providers.CredentialSchema{Name: name},
		func() providers.SMSProvider { return fakeSMS{tag: name} },
	)
}
