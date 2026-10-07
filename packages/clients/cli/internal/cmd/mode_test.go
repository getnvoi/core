package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/packages/clients/cli/internal/config"
)

// The contexts the machine holds, each by name and mode, all on endpoint.
func keepContexts(t *testing.T, endpoint string, modes map[string]string) {
	t.Helper()
	file := contexts(t)
	for name, mode := range modes {
		file.Set(name, config.Context{Endpoint: endpoint, Token: "jwt", Workspace: name, Mode: mode})
	}
	if err := file.Save(); err != nil {
		t.Fatal(err)
	}
}

func root(t *testing.T, r *Resolver, args ...string) (error, string) {
	t.Helper()
	c, err := Root(r)
	if err != nil {
		t.Fatal(err)
	}
	return execute(c, args...)
}

func shows(out, group string) bool { return strings.Contains(out, "\n  "+group+" ") }

func TestLoginKeepsTheServersMode(t *testing.T) {
	home(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		w.Write([]byte(`{"token":"jwt-here","mode":"selfhosted","workspace":{"slug":"dev"}}`))
	}))
	t.Cleanup(srv.Close)

	if err, _ := execute(Login(&Resolver{}), "--email", "a@b.co", "--password", "pw", "--endpoint", srv.URL); err != nil {
		t.Fatal(err)
	}
	if got := contexts(t).Contexts["dev"]; got.Mode != "selfhosted" || got.Endpoint != srv.URL {
		t.Fatalf("context %+v", got)
	}
}

func TestTheDocumentsMarkIsTheOperationsMode(t *testing.T) {
	ops, err := Operations([]byte(`{"paths":{
	  "/api/v1/billing": {"get": {"operationId": "utils_billing_show", "summary": "Show billing", "x-nvoi-mode": "cloud"}},
	  "/api/v1/agents": {"get": {"operationId": "agent_list", "summary": "List agents"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if ops[0].ID != "agent_list" || ops[0].Mode != "" || ops[1].ID != "utils_billing_show" || ops[1].Mode != "cloud" {
		t.Fatalf("ops %+v", ops)
	}
}

// The built-in document marks nvoi.to's own operations cloud: billing, checkout, funnels.
func TestASelfhostedContextHidesWhatOnlyTheCloudServes(t *testing.T) {
	home(t)
	keepContexts(t, "http://127.0.0.1:1", map[string]string{"local": "selfhosted"})

	_, out := root(t, &Resolver{}, "--help")
	if !shows(out, "agent") || !shows(out, "repo") {
		t.Fatalf("the core's groups hidden:\n%s", out)
	}
	for _, group := range []string{"billing", "funnel", "checkout", "portal"} {
		if shows(out, group) {
			t.Fatalf("%s shown:\n%s", group, out)
		}
	}
	_, out = root(t, &Resolver{}, "funnel", "--help")
	if shows(out, "list") || shows(out, "create") {
		t.Fatalf("funnel's commands:\n%s", out)
	}
	_, out = root(t, &Resolver{}, "__complete", "")
	if !strings.Contains(out, "agent\t") || strings.Contains(out, "billing") {
		t.Fatalf("completion:\n%s", out)
	}
}

func TestACloudContextAndOneWithoutAModeShowEveryCommand(t *testing.T) {
	for _, mode := range []string{"cloud", ""} {
		home(t)
		keepContexts(t, "http://127.0.0.1:1", map[string]string{"one": mode})
		if _, out := root(t, &Resolver{}, "--help"); !shows(out, "billing") || !shows(out, "agent") {
			t.Fatalf("mode %q:\n%s", mode, out)
		}
	}
}

// Several contexts: the one --context names decides; without it none resolves, and every command shows.
func TestSeveralContextsFollowTheContextFlag(t *testing.T) {
	home(t)
	keepContexts(t, "http://127.0.0.1:1", map[string]string{"local": "selfhosted", "cloud": "cloud"})

	if _, out := root(t, &Resolver{Name: "local"}, "--context", "local", "--help"); shows(out, "billing") {
		t.Fatalf("billing shown for local:\n%s", out)
	}
	if err, out := root(t, &Resolver{Name: "cloud"}, "--context", "cloud", "--help"); !shows(out, "billing") {
		t.Fatalf("billing hidden for cloud (%v):\n%s", err, out)
	}
	if _, out := root(t, &Resolver{}, "--help"); !shows(out, "billing") {
		t.Fatalf("billing hidden without --context:\n%s", out)
	}
	// Completion reads no flag: main reads --context from the line (ContextFlag) into the resolver.
	if _, out := root(t, &Resolver{Name: "local"}, "__complete", "--context", "local", ""); strings.Contains(out, "billing") {
		t.Fatalf("completion for local:\n%s", out)
	}
	if _, out := root(t, &Resolver{}, "__complete", ""); !strings.Contains(out, "billing") {
		t.Fatalf("completion without --context:\n%s", out)
	}
}

func TestDocsShowEveryCommand(t *testing.T) {
	home(t)
	keepContexts(t, "http://127.0.0.1:1", map[string]string{"local": "selfhosted"})
	if _, out := root(t, &Resolver{All: true}, "--help"); !shows(out, "billing") {
		t.Fatalf("billing hidden from the docs:\n%s", out)
	}
}

// A server without the route answers 404 and no code: the command says the server does not offer it. A record that
// is not there keeps its refusal.
func TestAnOperationTheServerDoesNotServeSaysSo(t *testing.T) {
	home(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		if strings.HasPrefix(r.URL.Path, "/api/v1/funnels/") || strings.HasPrefix(r.URL.Path, "/api/v1/agents/") {
			w.Write([]byte(`{"error":"Not found","code":"not_found"}`))
		}
	}))
	t.Cleanup(srv.Close)

	keepContexts(t, srv.URL, map[string]string{"local": "selfhosted"})
	err, _ := root(t, &Resolver{}, "billing", "show")
	if err == nil || err.Error() != "the server at "+srv.URL+" does not offer nvoi billing show" {
		t.Fatalf("err %v", err)
	}
	if err, _ := root(t, &Resolver{}, "agent", "show", "a1"); err == nil || err.Error() != "Not found" {
		t.Fatalf("err %v", err)
	}

	home(t)
	keepContexts(t, srv.URL, map[string]string{"cloud": "cloud"})
	if err, _ := root(t, &Resolver{}, "funnel", "update", "f1"); err == nil || err.Error() != "Not found" {
		t.Fatalf("err %v", err)
	}
}

func TestContextFlagIsReadBeforeTheTree(t *testing.T) {
	for args, want := range map[string]string{
		"agent list --context local":   "local",
		"--context=prod agent list":    "prod",
		"agent list":                   "",
		"agent list -- --context nope": "",
	} {
		if got := ContextFlag(strings.Fields(args)); got != want {
			t.Errorf("%q: %q", args, got)
		}
	}
}
