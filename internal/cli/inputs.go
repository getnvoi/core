package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/log"
	"github.com/getnvoi/core/pkg/providers"
	"github.com/getnvoi/core/pkg/runtime"
	"github.com/getnvoi/core/pkg/state"
)

// StdinConfigPath is the sentinel value of --config / -c that switches
// loading to stdin. Mirrors the standard Unix convention (cat, curl,
// jq, kubectl). Useful for hosts (desktop app, orchestrator, CI step)
// that materialize the config in memory and pipe it in rather than
// writing a file.
const StdinConfigPath = "-"

// LoadConfig reads + parses + validates the YAML at path. When path
// is the sentinel "-", reads from os.Stdin instead — letting callers
// pipe config without touching disk. Aliases and the alongside-.env
// loader are both bypassed in stdin mode (caller owns argv + env).
func LoadConfig(path string) (*config.Config, error) {
	if path == StdinConfigPath {
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf("read config from stdin: %w", err)
		}
		return config.ParseYAML(raw)
	}
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
