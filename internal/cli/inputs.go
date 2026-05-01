package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/log"
	"github.com/getnvoi/core/pkg/providers"
	"github.com/getnvoi/core/pkg/runtime"
	"github.com/getnvoi/core/pkg/state"
)

func LoadConfig(path string) (*config.Config, error) {
	return config.LoadFile(path)
}

func ConfigureState(ctx context.Context, cfg *config.Config, bp providers.BucketProvider, lg log.Log) (*state.Backend, error) {
	b, err := state.Configure(ctx, cfg, bp, lg)
	if err != nil {
		return nil, err
	}
	out := state.Backend(b)
	return &out, nil
}

func ResolveBucketCreds(provider string, getenv func(string) string) (map[string]string, error) {
	schema, err := providers.CredentialSchemaForBucket(provider)
	if err != nil {
		return nil, err
	}
	creds := make(map[string]string, len(schema.Fields))
	var missing []string
	for _, f := range schema.Fields {
		v := getenv(f.EnvVar)
		if f.Required && v == "" {
			missing = append(missing, f.EnvVar)
			continue
		}
		creds[f.Key] = v
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("bucket provider %q: missing env vars: %s", provider, strings.Join(missing, ", "))
	}
	return creds, nil
}

func ResolveProviderInputs(getenv func(string) string) runtime.ProviderInputs {
	apiToken := getenv("CLOUDFLARE_API_TOKEN")
	apiKey := getenv("CF_API_KEY")
	if apiToken == "" {
		apiToken = apiKey
	}
	return runtime.ProviderInputs{
		Cloudflare: &runtime.CloudflareInputs{
			APIToken:  apiToken,
			APIKey:    apiKey,
			AccountID: getenv("CF_ACCOUNT_ID"),
			ZoneID:    getenv("CF_ZONE_ID"),
			Zone:      getenv("CF_ZONE"),
		},
		Hetzner: &runtime.HetznerInputs{
			Token: getenv("HCLOUD_TOKEN"),
		},
	}
}
