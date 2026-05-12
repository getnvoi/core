package providers

import "fmt"

// SMSProvider builds a portable Receiver from a resolved AlertSpec.
// Implementations know their vendor (Twilio, Vonage, …) and the
// shape of their CredentialSchema. They do NOT know about Grafana —
// the orchestrator in pkg/internal/observability/grafana translates
// the Receiver to contact-point YAML.
//
// Stateless: SMSFactory returns a fresh provider instance per
// resolution; no creds-at-construction since notify-provider
// credentials live IN the AlertSpec (per-deploy), unlike BucketProvider
// where credentials are operator-wide (resolved at module load).
type SMSProvider interface {
	BuildReceiver(spec AlertSpec) (Receiver, error)
	CredentialSchema() CredentialSchema
}

// SMSFactory constructs a fresh SMSProvider. Notify providers are
// stateless — the factory takes no args.
type SMSFactory func() SMSProvider

type smsReg struct {
	schema  CredentialSchema
	factory SMSFactory
}

var smsProviders = map[string]smsReg{}

// RegisterSMS is called from a vendor package's init() (mirrors
// RegisterBucket). Duplicate registration is a programming error and
// panics — the registry is fixed at startup, never racy at runtime.
func RegisterSMS(name string, schema CredentialSchema, factory SMSFactory) {
	if _, dup := smsProviders[name]; dup {
		panic(fmt.Sprintf("providers: duplicate sms provider %q (programming error)", name))
	}
	smsProviders[name] = smsReg{schema: schema, factory: factory}
}

// ResolveSMS returns a stateless SMSProvider instance for the named
// vendor. Unknown name → error (caught upstream by config.Validate
// before reaching deploy).
func ResolveSMS(name string) (SMSProvider, error) {
	reg, ok := smsProviders[name]
	if !ok {
		return nil, fmt.Errorf("unknown sms provider %q", name)
	}
	return reg.factory(), nil
}

// IsRegisteredSMS reports whether the name has been registered —
// used by config.Validate to reject unknown providers early.
func IsRegisteredSMS(name string) bool {
	_, ok := smsProviders[name]
	return ok
}

// CredentialSchemaForSMS returns the registered schema. Used by
// boundary code that needs to know which env vars a given provider
// consumes (e.g. tooling that pre-validates `.env`).
func CredentialSchemaForSMS(name string) (CredentialSchema, error) {
	reg, ok := smsProviders[name]
	if !ok {
		return CredentialSchema{}, fmt.Errorf("unknown sms provider %q", name)
	}
	return reg.schema, nil
}
