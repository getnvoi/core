package runner

import (
	"testing"

	"github.com/getnvoi/core/pkg/runtime"
)

func TestTerraformEnv_UsesExplicitProviderInputs(t *testing.T) {
	env := terraformEnv(&runtime.Runtime{
		Providers: runtime.ProviderInputs{
			Cloudflare: &runtime.CloudflareInputs{APIToken: "cf-token"},
			Hetzner:    &runtime.HetznerInputs{Token: "hcloud-token"},
		},
	})

	if got := env["CLOUDFLARE_API_TOKEN"]; got != "cf-token" {
		t.Fatalf("cloudflare token: got %q", got)
	}
	if got := env["HCLOUD_TOKEN"]; got != "hcloud-token" {
		t.Fatalf("hetzner token: got %q", got)
	}
}
