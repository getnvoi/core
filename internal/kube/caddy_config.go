package kube

import (
	"encoding/json"
	"fmt"
	"sort"
)

// CaddyRoute is one service-to-domains mapping. The reconcile layer
// builds these from cfg.Domains plus the resolved Service port for
// each entry.
type CaddyRoute struct {
	Service string
	Port    int
	Domains []string
}

// CaddyConfigInput is everything BuildCaddyConfig needs from the
// reconcile layer: the namespace where backend Services live, the
// resolved routes, and the ACME contact email.
type CaddyConfigInput struct {
	Namespace string
	Routes    []CaddyRoute

	// ACMEEmail, when non-empty, is used as the ACME contact email
	// for every domain. Empty → deterministic fallback
	// (acme@<first-sorted-domain>) is generated.
	ACMEEmail string
}

// caddyConfig models the subset of Caddy's native JSON config we
// render. Only the fields nvoi needs — the rest of Caddy's giant
// schema stays out.
type caddyConfig struct {
	Admin caddyAdmin `json:"admin"`
	Apps  caddyApps  `json:"apps"`
}

type caddyAdmin struct {
	Listen string `json:"listen"`
}

type caddyApps struct {
	HTTP caddyHTTPApp `json:"http"`
	TLS  caddyTLSApp  `json:"tls,omitempty"`
}

type caddyHTTPApp struct {
	Servers map[string]caddyServer `json:"servers"`
}

type caddyServer struct {
	Listen []string     `json:"listen"`
	Routes []caddyRoute `json:"routes"`
}

type caddyRoute struct {
	Match  []caddyMatch   `json:"match"`
	Handle []caddyHandler `json:"handle"`
}

type caddyMatch struct {
	Host []string `json:"host"`
}

type caddyHandler struct {
	Handler   string          `json:"handler"`
	Upstreams []caddyUpstream `json:"upstreams"`
}

type caddyUpstream struct {
	Dial string `json:"dial"`
}

type caddyTLSApp struct {
	Automation caddyAutomation `json:"automation"`
}

type caddyAutomation struct {
	Policies []caddyAutomationPolicy `json:"policies"`
}

type caddyAutomationPolicy struct {
	Subjects []string      `json:"subjects"`
	Issuers  []caddyIssuer `json:"issuers"`
}

type caddyIssuer struct {
	Module string `json:"module"`
	Email  string `json:"email"`
}

// BuildCaddyConfig produces Caddy's native JSON config from the
// resolved input. Output is deterministic — services are sorted by
// name and routes appear in that order so re-running with identical
// input produces byte-identical bytes (matters for "is the config
// actually different from what's loaded" comparisons and for
// reproducible tests).
//
// Empty Routes → admin-only config (admin listener + an HTTP server
// with no routes and an empty TLS automation block). Caddy accepts
// this; no certs are requested. We still list :80/:443 so removing
// the last domain doesn't unbind them — the next deploy with a
// domain just installs a route, no port re-bind.
func BuildCaddyConfig(input CaddyConfigInput) ([]byte, error) {
	if input.Namespace == "" {
		return nil, fmt.Errorf("BuildCaddyConfig: namespace required")
	}

	out := caddyConfig{
		Admin: caddyAdmin{Listen: CaddyAdminListen},
		Apps: caddyApps{
			HTTP: caddyHTTPApp{
				Servers: map[string]caddyServer{
					"main": {
						Listen: []string{":80", ":443"},
						Routes: []caddyRoute{},
					},
				},
			},
			TLS: caddyTLSApp{
				Automation: caddyAutomation{Policies: []caddyAutomationPolicy{}},
			},
		},
	}

	// Sort routes by Service name. Domains within each route preserve
	// the caller's order (matches the YAML the operator wrote).
	sortedRoutes := append([]CaddyRoute(nil), input.Routes...)
	sort.SliceStable(sortedRoutes, func(i, j int) bool {
		return sortedRoutes[i].Service < sortedRoutes[j].Service
	})

	main := out.Apps.HTTP.Servers["main"]
	var allSubjects []string
	for _, r := range sortedRoutes {
		if r.Service == "" {
			return nil, fmt.Errorf("BuildCaddyConfig: route with empty service")
		}
		if len(r.Domains) == 0 {
			continue
		}
		if r.Port == 0 {
			return nil, fmt.Errorf("BuildCaddyConfig: no port resolved for service %q", r.Service)
		}
		dial := fmt.Sprintf("%s.%s.svc.cluster.local:%d", r.Service, input.Namespace, r.Port)

		main.Routes = append(main.Routes, caddyRoute{
			Match: []caddyMatch{{Host: append([]string(nil), r.Domains...)}},
			Handle: []caddyHandler{{
				Handler:   "reverse_proxy",
				Upstreams: []caddyUpstream{{Dial: dial}},
			}},
		})
		allSubjects = append(allSubjects, r.Domains...)
	}
	out.Apps.HTTP.Servers["main"] = main

	if len(allSubjects) > 0 {
		email := input.ACMEEmail
		if email == "" {
			// Deterministic fallback — the first subject in sort
			// order so this stays stable across runs.
			email = "acme@" + allSubjects[0]
		}
		out.Apps.TLS.Automation.Policies = append(out.Apps.TLS.Automation.Policies, caddyAutomationPolicy{
			Subjects: allSubjects,
			Issuers:  []caddyIssuer{{Module: "acme", Email: email}},
		})
	}

	return json.Marshal(out)
}
