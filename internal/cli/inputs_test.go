package cli

import (
	"strings"
	"testing"

	// blank-imported so the cloudflare bucket provider is registered;
	// ResolveBucketCreds reads the registered schema.
	_ "github.com/getnvoi/core/pkg/providers/cloudflare"
)

func TestResolveBucketCreds_AllRequiredFromEnv(t *testing.T) {
	env := map[string]string{
		"CF_API_KEY":    "key-xyz",
		"CF_ACCOUNT_ID": "acct-1",
	}
	got, err := ResolveBucketCreds("cloudflare", func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// Schema-driven — values keyed by field.Key, not by EnvVar name.
	if got["api_key"] != "key-xyz" || got["account_id"] != "acct-1" {
		t.Errorf("bucket creds: %#v", got)
	}
}

func TestResolveBucketCreds_MissingErrorIsSorted(t *testing.T) {
	_, err := ResolveBucketCreds("cloudflare", func(string) string { return "" })
	if err == nil {
		t.Fatal("expected error on empty env")
	}
	msg := err.Error()
	// Both required vars are missing; sorted list keeps the message stable.
	idxAcct := strings.Index(msg, "CF_ACCOUNT_ID")
	idxKey := strings.Index(msg, "CF_API_KEY")
	if idxAcct < 0 || idxKey < 0 {
		t.Fatalf("missing-var message must list both: %q", msg)
	}
	if idxAcct > idxKey {
		t.Errorf("missing list should be sorted alphabetically: %q", msg)
	}
}

func TestResolveBucketCreds_UnknownProviderErrors(t *testing.T) {
	_, err := ResolveBucketCreds("not-a-provider", func(string) string { return "x" })
	if err == nil {
		t.Fatal("expected error for unregistered provider")
	}
}

func TestResolveProviderInputs_CloudflareAPITokenFallsBackToAPIKey(t *testing.T) {
	env := map[string]string{
		"CF_API_KEY":    "key-fallback",
		"CF_ACCOUNT_ID": "acct-1",
	}
	in := ResolveProviderInputs(func(k string) string { return env[k] })
	if in.Cloudflare == nil {
		t.Fatal("Cloudflare inputs should be populated")
	}
	// CLOUDFLARE_API_TOKEN unset → APIToken takes the API_KEY value.
	if in.Cloudflare.APIToken != "key-fallback" {
		t.Errorf("APIToken fallback: got %q want key-fallback", in.Cloudflare.APIToken)
	}
	if in.Cloudflare.APIKey != "key-fallback" {
		t.Errorf("APIKey: got %q want key-fallback", in.Cloudflare.APIKey)
	}
	if in.Cloudflare.AccountID != "acct-1" {
		t.Errorf("AccountID: got %q", in.Cloudflare.AccountID)
	}
}

func TestResolveProviderInputs_ExplicitTokenWins(t *testing.T) {
	env := map[string]string{
		"CLOUDFLARE_API_TOKEN": "explicit",
		"CF_API_KEY":           "key-fallback",
	}
	in := ResolveProviderInputs(func(k string) string { return env[k] })
	if in.Cloudflare.APIToken != "explicit" {
		t.Errorf("explicit token should win over CF_API_KEY: got %q", in.Cloudflare.APIToken)
	}
}

func TestResolveProviderInputs_HetznerWired(t *testing.T) {
	in := ResolveProviderInputs(func(k string) string {
		if k == "HCLOUD_TOKEN" {
			return "hcloud-tok"
		}
		return ""
	})
	if in.Hetzner == nil || in.Hetzner.Token != "hcloud-tok" {
		t.Errorf("hetzner: %#v", in.Hetzner)
	}
}
