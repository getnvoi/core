package kube

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildCaddyConfig_RequiresNamespace(t *testing.T) {
	if _, err := BuildCaddyConfig(CaddyConfigInput{}); err == nil {
		t.Error("expected error for empty namespace")
	}
}

func TestBuildCaddyConfig_EmptyRoutes_AdminOnly(t *testing.T) {
	out, err := BuildCaddyConfig(CaddyConfigInput{Namespace: "default"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	apps := got["apps"].(map[string]any)
	http := apps["http"].(map[string]any)
	servers := http["servers"].(map[string]any)
	main := servers["main"].(map[string]any)
	listen := main["listen"].([]any)
	// :80 + :443 always bound, even with no routes — keeps removing
	// the last domain from re-binding ports next deploy.
	if len(listen) != 2 {
		t.Errorf("listen ports: got %v want [:80 :443]", listen)
	}
	routes := main["routes"].([]any)
	if len(routes) != 0 {
		t.Errorf("empty routes expected, got %v", routes)
	}
}

func TestBuildCaddyConfig_RoutesAndACME(t *testing.T) {
	out, err := BuildCaddyConfig(CaddyConfigInput{
		Namespace: "default",
		Routes: []CaddyRoute{
			{Service: "web", Port: 8080, Domains: []string{"www.nvoi.to", "nvoi.to"}},
		},
		ACMEEmail: "ops@nvoi.to",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	hcl := string(out)
	for _, want := range []string{
		`"dial":"web.default.svc.cluster.local:8080"`,
		`"host":["www.nvoi.to","nvoi.to"]`,
		`"reverse_proxy"`,
		`"acme"`,
		`"email":"ops@nvoi.to"`,
		`"subjects":["www.nvoi.to","nvoi.to"]`,
	} {
		if !strings.Contains(hcl, want) {
			t.Errorf("config missing %q\n--- output ---\n%s", want, hcl)
		}
	}
}

func TestBuildCaddyConfig_ACMEEmailFallback(t *testing.T) {
	out, _ := BuildCaddyConfig(CaddyConfigInput{
		Namespace: "default",
		Routes: []CaddyRoute{
			{Service: "web", Port: 8080, Domains: []string{"www.nvoi.to"}},
		},
	})
	if !strings.Contains(string(out), `"email":"acme@www.nvoi.to"`) {
		t.Errorf("expected fallback ACME email, got: %s", out)
	}
}

func TestBuildCaddyConfig_DeterministicOrder(t *testing.T) {
	in := CaddyConfigInput{
		Namespace: "default",
		Routes: []CaddyRoute{
			{Service: "zed", Port: 1, Domains: []string{"zed.example.com"}},
			{Service: "alpha", Port: 2, Domains: []string{"alpha.example.com"}},
		},
	}
	a, _ := BuildCaddyConfig(in)
	b, _ := BuildCaddyConfig(in)
	if string(a) != string(b) {
		t.Error("BuildCaddyConfig must be deterministic across runs")
	}
	// alpha (sorted) should appear before zed in the routes array.
	if strings.Index(string(a), "alpha.example.com") > strings.Index(string(a), "zed.example.com") {
		t.Errorf("routes not sorted by service name: %s", a)
	}
}

func TestBuildCaddyConfig_RouteWithoutPort_Errors(t *testing.T) {
	_, err := BuildCaddyConfig(CaddyConfigInput{
		Namespace: "default",
		Routes:    []CaddyRoute{{Service: "web", Port: 0, Domains: []string{"www.nvoi.to"}}},
	})
	if err == nil {
		t.Error("expected error for route with port 0")
	}
}
