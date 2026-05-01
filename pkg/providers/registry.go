package providers

import (
	"fmt"
)

// CredentialField maps a logical credential key to an env var. The
// boundary (cmd/cli/main.go) reads via os.Getenv and produces a
// resolved map[string]string for the factory.
type CredentialField struct {
	Key      string // factory's canonical key, e.g. "api_key"
	EnvVar   string // shell env var, e.g. "CF_API_KEY"
	Required bool
}

// CredentialSchema is what each provider's register.go declares so the
// boundary knows what env vars to read and validate.
type CredentialSchema struct {
	Name   string
	Fields []CredentialField
}

// BucketFactory is provider's constructor — receives resolved creds.
type BucketFactory func(creds map[string]string) BucketProvider

// bucketReg keeps registered bucket providers. Populated by each
// provider's init().
type bucketReg struct {
	schema  CredentialSchema
	factory BucketFactory
}

var bucketProviders = map[string]bucketReg{}

// RegisterBucket is called from a provider package's init().
func RegisterBucket(name string, schema CredentialSchema, factory BucketFactory) {
	if _, dup := bucketProviders[name]; dup {
		panic(fmt.Sprintf("providers: duplicate bucket provider %q (programming error)", name))
	}
	bucketProviders[name] = bucketReg{schema: schema, factory: factory}
}

// CredentialSchemaForBucket returns the env-resolution schema the
// caller can use to assemble explicit credentials for the named
// provider.
func CredentialSchemaForBucket(name string) (CredentialSchema, error) {
	reg, ok := bucketProviders[name]
	if !ok {
		return CredentialSchema{}, fmt.Errorf("unknown bucket provider %q", name)
	}
	return reg.schema, nil
}

// ResolveBucket returns a BucketProvider for the named provider with
// explicit resolved credentials.
func ResolveBucket(name string, creds map[string]string) (BucketProvider, error) {
	reg, ok := bucketProviders[name]
	if !ok {
		return nil, fmt.Errorf("unknown bucket provider %q", name)
	}
	return reg.factory(creds), nil
}

// IsRegisteredBucket reports whether `name` has been registered as a
// bucket provider — used by the validator to reject unknown names
// before we try to touch any infra.
func IsRegisteredBucket(name string) bool {
	_, ok := bucketProviders[name]
	return ok
}
