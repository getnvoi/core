package runner

import (
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-exec/tfexec"
)

// fixture builds a tfexec output map from raw JSON literals. Mirrors
// what `tofu output -json` produces — Value is RawMessage, Type is
// unused by parseEndpoints.
func fixture(t *testing.T, kv map[string]string) map[string]tfexec.OutputMeta {
	t.Helper()
	out := make(map[string]tfexec.OutputMeta, len(kv))
	for k, v := range kv {
		out[k] = tfexec.OutputMeta{Value: json.RawMessage(v)}
	}
	return out
}

func TestParseEndpoints_Minimal(t *testing.T) {
	eps, err := parseEndpoints(fixture(t, map[string]string{
		"servers":      `{"master":{"role":"master","ipv4":"1.2.3.4","private":"10.0.1.5"}}`,
		"api_endpoint": `{"public":"1.2.3.4","private":"10.0.1.5"}`,
		"ha":           `false`,
	}))
	if err != nil {
		t.Fatalf("parseEndpoints: %v", err)
	}
	if eps.HA {
		t.Error("HA: got true, want false")
	}
	if eps.HasTunnel() {
		t.Error("HasTunnel: got true on a Traefik-mode output")
	}
	if eps.Servers["master"].IPv4 != "1.2.3.4" {
		t.Errorf("master IPv4 mismatch: %+v", eps.Servers)
	}
}

func TestParseEndpoints_TunnelPresent(t *testing.T) {
	eps, err := parseEndpoints(fixture(t, map[string]string{
		"servers":      `{"master":{"role":"master","ipv4":"1.2.3.4","private":"10.0.1.5"}}`,
		"api_endpoint": `{"public":"1.2.3.4","private":"10.0.1.5"}`,
		"tunnel":       `{"id":"uuid-1234","cname":"uuid-1234.cfargotunnel.com","token":"eyJabc"}`,
	}))
	if err != nil {
		t.Fatalf("parseEndpoints: %v", err)
	}
	if !eps.HasTunnel() {
		t.Fatal("HasTunnel: got false on a tunnel-mode output")
	}
	if eps.Tunnel.ID != "uuid-1234" {
		t.Errorf("Tunnel.ID: got %q want uuid-1234", eps.Tunnel.ID)
	}
	if eps.Tunnel.Token != "eyJabc" {
		t.Errorf("Tunnel.Token: got %q want eyJabc", eps.Tunnel.Token)
	}
	if eps.Tunnel.CName != "uuid-1234.cfargotunnel.com" {
		t.Errorf("Tunnel.CName mismatch: %q", eps.Tunnel.CName)
	}
}

func TestParseEndpoints_RequiresServers(t *testing.T) {
	_, err := parseEndpoints(fixture(t, map[string]string{
		"api_endpoint": `{"public":"1.2.3.4","private":"10.0.1.5"}`,
	}))
	if err == nil {
		t.Fatal("expected error for missing servers output")
	}
}

func TestParseEndpoints_RequiresAPIEndpoint(t *testing.T) {
	_, err := parseEndpoints(fixture(t, map[string]string{
		"servers": `{"master":{"role":"master","ipv4":"1.2.3.4"}}`,
	}))
	if err == nil {
		t.Fatal("expected error for missing api_endpoint output")
	}
}
