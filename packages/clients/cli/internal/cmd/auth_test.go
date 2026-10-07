package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/getnvoi/nvoi/packages/clients/cli/internal/config"
)

func execute(c *cobra.Command, args ...string) (error, string) {
	var out strings.Builder
	c.SetOut(&out)
	c.SetErr(io.Discard)
	c.SetArgs(args)
	return c.Execute(), out.String()
}

// A home of its own, and no environment override: the tests are about the file.
func home(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NVOI_API_ENDPOINT", "")
	t.Setenv("NVOI_TOKEN", "")
}

func contexts(t *testing.T) *config.File {
	t.Helper()
	file, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	return file
}

const loggedIn = `{"token":"jwt-here","user":{"id":"u1","email":"a@b.co"},"workspace":{"id":"w1","name":"Dev","slug":"dev"}}`

func loginServer(t *testing.T, body *[]byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if body != nil {
			*body, _ = io.ReadAll(r.Body)
		}
		if r.URL.Path != "/api/v1/login" || r.Header.Get("Authorization") != "" {
			t.Errorf("%s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.WriteHeader(201)
		w.Write([]byte(loggedIn))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestLoginKeepsTheTokenAsAContextNamedAfterTheWorkspace(t *testing.T) {
	home(t)
	var body []byte
	srv := loginServer(t, &body)

	err, out := execute(Login(&Resolver{}), "--email", "a@b.co", "--password", "pw",
		"--workspace", "dev", "--endpoint", srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	var sent map[string]string
	json.Unmarshal(body, &sent)
	if sent["email"] != "a@b.co" || sent["password"] != "pw" || sent["workspace"] != "dev" {
		t.Fatalf("body %s", body)
	}
	file := contexts(t)
	context := file.Contexts["dev"]
	if context.Token != "jwt-here" || context.Endpoint != srv.URL || context.Workspace != "dev" {
		t.Fatalf("context %v", context)
	}
	if out != "logged in to dev as context dev\n" {
		t.Fatalf("out %q", out)
	}
}

func TestLoginNamesTheContextWhenAsked(t *testing.T) {
	home(t)
	srv := loginServer(t, nil)

	err, _ := execute(Login(&Resolver{Name: "staging"}), "--email", "a@b.co", "--password", "pw",
		"--endpoint", srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	file := contexts(t)
	if file.Contexts["staging"].Token != "jwt-here" {
		t.Fatalf("%v", file.Contexts)
	}
}

// Logging in again with no --endpoint stays on the server the context already names.
func TestLoginWithoutAnEndpointKeepsTheContextsServer(t *testing.T) {
	home(t)
	srv := loginServer(t, nil)
	file := contexts(t)
	file.Set("dev", config.Context{Endpoint: srv.URL, Token: "stale"})
	if err := file.Save(); err != nil {
		t.Fatal(err)
	}

	if err, _ := execute(Login(&Resolver{}), "--email", "a@b.co", "--password", "pw"); err != nil {
		t.Fatal(err)
	}
	if got := contexts(t).Contexts["dev"].Token; got != "jwt-here" {
		t.Fatalf("token %q", got)
	}
}

func TestLoginWithoutAWorkspaceSendsNone(t *testing.T) {
	home(t)
	var body []byte
	srv := loginServer(t, &body)

	if err, _ := execute(Login(&Resolver{}), "--email", "a@b.co", "--password", "pw",
		"--endpoint", srv.URL); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "workspace") {
		t.Fatalf("body %s", body)
	}
}

func TestLoginRefusedWritesNoContext(t *testing.T) {
	home(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`{"error":"other is not one of this account's workspaces.","code":"not_a_member"}`))
	}))
	defer srv.Close()

	err, _ := execute(Login(&Resolver{}), "--email", "a@b.co", "--password", "pw",
		"--workspace", "other", "--endpoint", srv.URL)
	if err == nil || !strings.Contains(err.Error(), "other is not one of this account's workspaces") {
		t.Fatalf("err %v", err)
	}
	if len(contexts(t).Contexts) != 0 {
		t.Fatal("a context was written")
	}
}

func TestLogoutRevokesAndForgetsTheContext(t *testing.T) {
	home(t)
	var method, path, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path, auth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		w.WriteHeader(204)
	}))
	defer srv.Close()
	file := contexts(t)
	file.Set("prod", config.Context{Endpoint: "https://nvoi.to", Token: "other"})
	file.Set("dev", config.Context{Endpoint: srv.URL, Token: "jwt-here"})
	if err := file.Save(); err != nil {
		t.Fatal(err)
	}

	if err, _ := execute(Logout(&Resolver{Name: "dev"})); err != nil {
		t.Fatal(err)
	}
	if method != "DELETE" || path != "/api/v1/logout" || auth != "Bearer jwt-here" {
		t.Fatalf("%s %s %s", method, path, auth)
	}
	file = contexts(t)
	if _, found := file.Contexts["dev"]; found {
		t.Fatal("the context is still there")
	}
	// The other context stays.
	if len(file.Contexts) != 1 {
		t.Fatalf("%v", file.Contexts)
	}
}

func TestContextListShowAndRemove(t *testing.T) {
	home(t)
	file := contexts(t)
	file.Set("prod", config.Context{Endpoint: "https://nvoi.to", Token: "t1", Workspace: "ben"})
	file.Set("dev", config.Context{Endpoint: "http://localhost:3000", Token: "t2", Workspace: "ben"})
	if err := file.Save(); err != nil {
		t.Fatal(err)
	}

	err, out := execute(Context(&Resolver{}), "list")
	if err != nil || !strings.Contains(out, "dev ") || !strings.Contains(out, "prod ") {
		t.Fatalf("%v %q", err, out)
	}
	if err, _ := execute(Context(&Resolver{}), "show"); err == nil || !strings.Contains(err.Error(), "--context") {
		t.Fatalf("several contexts and no name: %v", err)
	}
	err, out = execute(Context(&Resolver{Name: "prod"}), "show")
	if err != nil || !strings.HasPrefix(out, "prod\thttps://nvoi.to\tben") {
		t.Fatalf("%v %q", err, out)
	}
	if err, _ := execute(Context(&Resolver{}), "remove", "prod"); err != nil {
		t.Fatal(err)
	}
	if _, found := contexts(t).Contexts["prod"]; found {
		t.Fatal("prod is still there")
	}
}

// --context names the context for one command.
func TestTheContextFlagIsAOneOff(t *testing.T) {
	home(t)
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.Header.Get("Authorization")
		w.Write([]byte(`{"user":{"email":"a@b.co"}}`))
	}))
	defer srv.Close()
	file := contexts(t)
	file.Set("other", config.Context{Endpoint: srv.URL, Token: "other-jwt"})
	file.Set("dev", config.Context{Endpoint: srv.URL, Token: "dev-jwt"})
	if err := file.Save(); err != nil {
		t.Fatal(err)
	}

	root, err := Root(&Resolver{})
	if err != nil {
		t.Fatal(err)
	}
	if err, _ := execute(root, "whoami", "--context", "other"); err != nil {
		t.Fatal(err)
	}
	if asked != "Bearer other-jwt" {
		t.Fatalf("asked %q", asked)
	}
}

// Named after the workspace, a login does not replace the same slug's context on another server.
func TestLoginKeepsAContextOfAnotherServer(t *testing.T) {
	home(t)
	srv := loginServer(t, nil)
	file := contexts(t)
	file.Set("dev", config.Context{Endpoint: "https://nvoi.to", Token: "prod-jwt", Workspace: "dev"})
	if err := file.Save(); err != nil {
		t.Fatal(err)
	}

	err, _ := execute(Login(&Resolver{}), "--email", "a@b.co", "--password", "pw", "--endpoint", srv.URL)
	if err == nil || !strings.Contains(err.Error(), "--context") {
		t.Fatalf("err %v", err)
	}
	if got := contexts(t).Contexts["dev"].Token; got != "prod-jwt" {
		t.Fatalf("token %q", got)
	}
	if err, _ := execute(Login(&Resolver{Name: "local"}), "--email", "a@b.co", "--password", "pw",
		"--endpoint", srv.URL); err != nil {
		t.Fatal(err)
	}
}

// With NVOI_TOKEN, logout revokes that token and forgets no context of the file.
func TestLogoutWithTheEnvironmentsTokenForgetsNoContext(t *testing.T) {
	home(t)
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.WriteHeader(204)
	}))
	t.Cleanup(srv.Close)
	file := contexts(t)
	file.Set("dev", config.Context{Endpoint: srv.URL, Token: "file-jwt", Workspace: "dev"})
	file.Save()
	t.Setenv("NVOI_API_ENDPOINT", srv.URL)
	t.Setenv("NVOI_TOKEN", "env-jwt")

	if err, _ := execute(Logout(&Resolver{})); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer env-jwt" || contexts(t).Contexts["dev"].Token != "file-jwt" {
		t.Fatalf("auth %q, contexts %v", auth, contexts(t).Contexts)
	}
}
