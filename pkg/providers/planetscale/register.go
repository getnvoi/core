package planetscale

import "github.com/getnvoi/core/pkg/providers"

// init registers the planetscale engine with the database provider
// registry.
//
// Required env vars (resolved at the cmd/ boundary, validated by
// the bucket-style credential schema):
//
//	PLANETSCALE_SERVICE_TOKEN — `<id>:<secret>` from the dashboard
//	PLANETSCALE_ORG           — your org slug
//
// PLANETSCALE_BASE_URL is optional — defaults to
// https://api.planetscale.com/v1. Useful for testing against a
// mock server.
func init() {
	providers.RegisterDatabase("planetscale", providers.CredentialSchema{
		Name: "planetscale",
		Fields: []providers.CredentialField{
			{Key: "service_token", EnvVar: "PLANETSCALE_SERVICE_TOKEN", Required: true},
			{Key: "organization", EnvVar: "PLANETSCALE_ORG", Required: true},
			{Key: "base_url", EnvVar: "PLANETSCALE_BASE_URL", Required: false},
		},
	}, func(creds map[string]string) providers.DatabaseProvider {
		return New(creds)
	})
}
