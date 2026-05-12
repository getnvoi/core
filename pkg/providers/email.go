package providers

import "fmt"

// EmailProvider builds a portable Receiver from a resolved AlertSpec.
// Symmetric with SMSProvider — same registry pattern, same statelessness.
//
// Postmark is the v1 impl; future providers (SendGrid, SES, generic
// SMTP, …) register the same way.
type EmailProvider interface {
	BuildReceiver(spec AlertSpec) (Receiver, error)
	CredentialSchema() CredentialSchema
}

// EmailFactory constructs a fresh EmailProvider. Stateless.
type EmailFactory func() EmailProvider

type emailReg struct {
	schema  CredentialSchema
	factory EmailFactory
}

var emailProviders = map[string]emailReg{}

func RegisterEmail(name string, schema CredentialSchema, factory EmailFactory) {
	if _, dup := emailProviders[name]; dup {
		panic(fmt.Sprintf("providers: duplicate email provider %q (programming error)", name))
	}
	emailProviders[name] = emailReg{schema: schema, factory: factory}
}

func ResolveEmail(name string) (EmailProvider, error) {
	reg, ok := emailProviders[name]
	if !ok {
		return nil, fmt.Errorf("unknown email provider %q", name)
	}
	return reg.factory(), nil
}

func IsRegisteredEmail(name string) bool {
	_, ok := emailProviders[name]
	return ok
}

func CredentialSchemaForEmail(name string) (CredentialSchema, error) {
	reg, ok := emailProviders[name]
	if !ok {
		return CredentialSchema{}, fmt.Errorf("unknown email provider %q", name)
	}
	return reg.schema, nil
}
