package providers

import (
	"fmt"
	"os"
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

// ResolveBucket returns a BucketProvider for the named provider with
// credentials resolved from os.Getenv per the registered schema. The
// only place os.Getenv is called outside cmd/cli — and this function
// is only invoked from cmd/cli's boundary path, so the rule holds.
func ResolveBucket(name string) (BucketProvider, error) {
	reg, ok := bucketProviders[name]
	if !ok {
		return nil, fmt.Errorf("unknown bucket provider %q", name)
	}
	creds := map[string]string{}
	var missing []string
	for _, f := range reg.schema.Fields {
		v := os.Getenv(f.EnvVar)
		if f.Required && v == "" {
			missing = append(missing, f.EnvVar)
			continue
		}
		creds[f.Key] = v
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("bucket provider %q: missing env vars: %v", name, missing)
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
