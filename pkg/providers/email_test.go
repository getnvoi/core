package providers_test

import (
	"strings"
	"testing"

	"github.com/getnvoi/core/pkg/providers"
)

type fakeEmail struct{ tag string }

func (f fakeEmail) CredentialSchema() providers.CredentialSchema {
	return providers.CredentialSchema{Name: f.tag}
}
func (f fakeEmail) BuildReceiver(_ providers.AlertSpec) (providers.Receiver, error) {
	return providers.Receiver{Type: f.tag}, nil
}

func TestEmailRegistry_RoundTrip(t *testing.T) {
	name := "emailtest-roundtrip"
	providers.RegisterEmail(name,
		providers.CredentialSchema{Name: name},
		func() providers.EmailProvider { return fakeEmail{tag: name} },
	)

	if !providers.IsRegisteredEmail(name) {
		t.Fatalf("IsRegisteredEmail(%q) = false after RegisterEmail", name)
	}

	p, err := providers.ResolveEmail(name)
	if err != nil {
		t.Fatalf("ResolveEmail: %v", err)
	}
	rcv, err := p.BuildReceiver(providers.AlertSpec{Provider: name})
	if err != nil {
		t.Fatalf("BuildReceiver: %v", err)
	}
	if rcv.Type != name {
		t.Errorf("Type = %q, want %q", rcv.Type, name)
	}

	schema, err := providers.CredentialSchemaForEmail(name)
	if err != nil {
		t.Fatalf("CredentialSchemaForEmail: %v", err)
	}
	if schema.Name != name {
		t.Errorf("schema.Name = %q, want %q", schema.Name, name)
	}
}

func TestEmailRegistry_UnknownProvider(t *testing.T) {
	if providers.IsRegisteredEmail("nonexistent-email-xyz") {
		t.Fatal("IsRegisteredEmail should return false for unknown")
	}
	if _, err := providers.ResolveEmail("nonexistent-email-xyz"); err == nil {
		t.Fatal("ResolveEmail unknown name should error")
	} else if !strings.Contains(err.Error(), "unknown email provider") {
		t.Errorf("unexpected error message: %v", err)
	}
	if _, err := providers.CredentialSchemaForEmail("nonexistent-email-xyz"); err == nil {
		t.Fatal("CredentialSchemaForEmail unknown name should error")
	}
}

func TestEmailRegistry_DuplicateRegistrationPanics(t *testing.T) {
	name := "emailtest-dup"
	providers.RegisterEmail(name,
		providers.CredentialSchema{Name: name},
		func() providers.EmailProvider { return fakeEmail{tag: name} },
	)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on duplicate RegisterEmail")
		}
		msg, _ := r.(string)
		if !strings.Contains(msg, "duplicate email provider") {
			t.Errorf("panic message wrong: %v", r)
		}
	}()
	providers.RegisterEmail(name,
		providers.CredentialSchema{Name: name},
		func() providers.EmailProvider { return fakeEmail{tag: name} },
	)
}
