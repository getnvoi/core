package cloudflare

import "github.com/getnvoi/tf/internal/providers"

// BucketSchema declares the env vars cmd/cli reads to construct the
// R2 BucketProvider. Same names as nvoi's CF_API_KEY / CF_ACCOUNT_ID.
var BucketSchema = providers.CredentialSchema{
	Name: "cloudflare",
	Fields: []providers.CredentialField{
		{Key: "api_key", EnvVar: "CF_API_KEY", Required: true},
		{Key: "account_id", EnvVar: "CF_ACCOUNT_ID", Required: true},
	},
}

func init() {
	providers.RegisterBucket("cloudflare", BucketSchema, func(creds map[string]string) providers.BucketProvider {
		return NewBucket(creds)
	})
}
