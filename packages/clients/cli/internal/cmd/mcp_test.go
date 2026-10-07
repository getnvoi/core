package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/packages/clients/cli/internal/config"
)

const madeToken = `{"token":{"id":"t1","kind":"personal","name":"Cursor (mac)","expires_at":"2027-01-05T00:00:00Z","secret":"s3cret"}}`

// A server that makes tokens and revokes them, recording each request.
type tokenServer struct {
	*httptest.Server
	body       []byte
	auth       string
	workspace  string
	repository string
	deleted    string
}

func newTokenServer(t *testing.T) *tokenServer {
	t.Helper()
	s := &tokenServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/v1/tokens":
			s.body, _ = io.ReadAll(r.Body)
			s.auth = r.Header.Get("Authorization")
			s.workspace = r.Header.Get("Nvoi-Workspace")
			s.repository = r.Header.Get("Nvoi-Repository")
			w.WriteHeader(201)
			w.Write([]byte(madeToken))
		case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/api/v1/tokens/"):
			s.deleted = strings.TrimPrefix(r.URL.Path, "/api/v1/tokens/")
			w.WriteHeader(204)
		default:
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

// A signed-in context on srv, in a home of its own.
func signedIn(t *testing.T, endpoint string) string {
	t.Helper()
	home(t)
	file := contexts(t)
	file.Set("dev", config.Context{Endpoint: endpoint, Token: "jwt-here", Workspace: "acme"})
	if err := file.Save(); err != nil {
		t.Fatal(err)
	}
	dir, _ := os.UserHomeDir()
	return dir
}

type invocation []string

// A machine whose programs are recorded, never run.
func fakeMachine(home string, claude bool, run func(args []string) ([]byte, error)) (*Machine, *[]invocation) {
	calls := &[]invocation{}
	return &Machine{
		Run: func(name string, args ...string) ([]byte, error) {
			*calls = append(*calls, append([]string{name}, args...))
			if run != nil {
				return run(args)
			}
			return nil, nil
		},
		LookPath: func(name string) (string, error) {
			if claude {
				return "/bin/claude", nil
			}
			return "", errors.New("not found")
		},
		Home:     func() (string, error) { return home, nil },
		Hostname: func() (string, error) { return "mac.local", nil },
	}, calls
}

func TestMCPURL(t *testing.T) {
	for endpoint, want := range map[string]string{
		"https://nvoi.to":        "https://mcp.nvoi.to",
		"https://nvoi.to/":       "https://mcp.nvoi.to",
		"https://dev.nvoi.to":    "https://mcp-dev.nvoi.to",
		"http://127.0.0.1:4797/": "http://mcp.localhost:4797",
	} {
		if got := MCPURL(endpoint); got != want {
			t.Errorf("%s: %s, want %s", endpoint, got, want)
		}
	}
}

func TestTokenRequest(t *testing.T) {
	got := TokenRequest(TokenName("Cursor", "mac.local"), "acme", 90)
	want := map[string]any{"name": "Cursor (mac)", "workspace": "acme", "expires_in_days": 90}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	if got := TokenName("Cursor", "bens-air.home"); got != "Cursor (bens-air)" {
		t.Fatalf("name %q", got)
	}
	if _, found := TokenRequest("x", "", 90)["workspace"]; found {
		t.Fatal("an empty workspace is sent")
	}
}

func TestClaudeArgs(t *testing.T) {
	got := ClaudeArgs("https://mcp.nvoi.to", "s3cret", "user", "acme")
	want := []string{"mcp", "add", "--transport", "http", "--scope", "user", "nvoi", "https://mcp.nvoi.to",
		"--header", "Authorization: Bearer s3cret", "--header", "Nvoi-Workspace: acme"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%q", got)
	}
	if got := ClaudeArgs("https://mcp.nvoi.to", "s3cret", "user", ""); len(got) != 10 {
		t.Fatalf("a token of no known workspace names one: %q", got)
	}
}

func TestAddCursorPostsTheTokenAndKeepsOtherServers(t *testing.T) {
	srv := newTokenServer(t)
	dir := signedIn(t, srv.URL)
	path := CursorPath(dir)
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte(`{"mcpServers":{"other":{"url":"https://other.dev"}},"theme":"dark"}`), 0o644)
	m, _ := fakeMachine(dir, false, nil)

	err, out := execute(MCP(&Resolver{}, m), "add", "cursor", "--expires-in-days", "30")
	if err != nil {
		t.Fatal(err)
	}
	var sent map[string]any
	json.Unmarshal(srv.body, &sent)
	if sent["name"] != "Cursor (mac)" || sent["workspace"] != "acme" || sent["expires_in_days"] != 30.0 ||
		srv.auth != "Bearer jwt-here" || srv.workspace != "acme" {
		t.Fatalf("body %s, auth %s, workspace %s", srv.body, srv.auth, srv.workspace)
	}

	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}
	var file struct {
		Servers map[string]struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
		Theme string `json:"theme"`
	}
	data, _ := os.ReadFile(path)
	json.Unmarshal(data, &file)
	nvoi := file.Servers["nvoi"]
	if nvoi.URL != MCPURL(srv.URL) || nvoi.Headers["Authorization"] != "Bearer s3cret" ||
		nvoi.Headers["Nvoi-Workspace"] != "acme" {
		t.Fatalf("file %s", data)
	}
	if file.Servers["other"].URL != "https://other.dev" || file.Theme != "dark" {
		t.Fatalf("lost an entry: %s", data)
	}
	if strings.Contains(out, "s3cret") || !strings.Contains(out, "nvoi token delete t1") ||
		!strings.Contains(out, path) || !strings.Contains(out, "every Cursor project on this machine now acts in acme") {
		t.Fatalf("out %q", out)
	}
}

func TestAddCursorRefusesAnExistingServerWithoutForce(t *testing.T) {
	srv := newTokenServer(t)
	dir := signedIn(t, srv.URL)
	path := CursorPath(dir)
	os.MkdirAll(filepath.Dir(path), 0o755)
	before := `{"mcpServers":{"nvoi":{"url":"https://old"}}}`
	os.WriteFile(path, []byte(before), 0o600)
	m, _ := fakeMachine(dir, false, nil)

	err, _ := execute(MCP(&Resolver{}, m), "add", "cursor")
	if !errors.Is(err, ErrServerExists) {
		t.Fatalf("err %v", err)
	}
	if srv.body != nil {
		t.Fatal("a token was made")
	}
	if data, _ := os.ReadFile(path); string(data) != before {
		t.Fatalf("file %s", data)
	}

	if err, _ := execute(MCP(&Resolver{}, m), "add", "cursor", "--force"); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), "s3cret") {
		t.Fatalf("file %s", data)
	}
}

func TestAddClaudeRunsTheCommand(t *testing.T) {
	srv := newTokenServer(t)
	dir := signedIn(t, srv.URL)
	// `claude mcp get nvoi` fails: no server yet.
	m, calls := fakeMachine(dir, true, func(args []string) ([]byte, error) {
		if args[1] == "get" {
			return nil, errors.New("exit status 1")
		}
		return nil, nil
	})

	err, out := execute(MCP(&Resolver{}, m), "add", "claude", "--scope", "user", "--url", "https://mcp.example")
	if err != nil {
		t.Fatal(err)
	}
	want := []invocation{
		{"claude", "mcp", "get", "nvoi"},
		append(invocation{"claude"}, ClaudeArgs("https://mcp.example", "s3cret", "user", "acme")...),
	}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("calls %q", *calls)
	}
	if strings.Contains(out, "s3cret") {
		t.Fatalf("out %q", out)
	}
}

func TestAddClaudeRefusesAnExistingServerWithoutForce(t *testing.T) {
	srv := newTokenServer(t)
	dir := signedIn(t, srv.URL)
	m, calls := fakeMachine(dir, true, nil)

	err, _ := execute(MCP(&Resolver{}, m), "add", "claude")
	if !errors.Is(err, ErrServerExists) || srv.body != nil || len(*calls) != 1 {
		t.Fatalf("err %v, body %s, calls %q", err, srv.body, *calls)
	}

	*calls = nil
	if err, _ := execute(MCP(&Resolver{}, m), "add", "claude", "--force"); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 3 || (*calls)[1][2] != "remove" || (*calls)[2][2] != "add" {
		t.Fatalf("calls %q", *calls)
	}
}

func TestAddClaudeRevokesTheTokenWhenClaudeFails(t *testing.T) {
	srv := newTokenServer(t)
	dir := signedIn(t, srv.URL)
	m, _ := fakeMachine(dir, true, func(args []string) ([]byte, error) {
		return []byte("no"), errors.New("exit status 1")
	})

	err, _ := execute(MCP(&Resolver{}, m), "add", "claude")
	if err == nil || srv.deleted != "t1" {
		t.Fatalf("err %v, deleted %q", err, srv.deleted)
	}
}

func TestAddClaudeWithoutClaudePrintsTheCommand(t *testing.T) {
	srv := newTokenServer(t)
	dir := signedIn(t, srv.URL)
	m, calls := fakeMachine(dir, false, nil)

	err, out := execute(MCP(&Resolver{}, m), "add", "claude")
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 || !strings.Contains(out, "claude is not on PATH") ||
		!strings.Contains(out, "'Authorization: Bearer s3cret'") {
		t.Fatalf("calls %q, out %q", *calls, out)
	}
}

func TestAddPrintWritesNothing(t *testing.T) {
	srv := newTokenServer(t)
	dir := signedIn(t, srv.URL)
	m, calls := fakeMachine(dir, true, nil)

	err, out := execute(MCP(&Resolver{}, m), "add", "--print")
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Fatalf("calls %q", *calls)
	}
	if _, err := os.Stat(CursorPath(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the Cursor file was written")
	}
	if !strings.Contains(out, "secret: s3cret") || !strings.Contains(out, "claude mcp add") ||
		!strings.Contains(out, `"mcpServers"`) {
		t.Fatalf("out %q", out)
	}
}

func TestAddNeedsAClient(t *testing.T) {
	signedIn(t, "http://unused")
	m, _ := fakeMachine(t.TempDir(), false, nil)
	for _, args := range [][]string{{"add"}, {"add", "vscode"}} {
		if err, _ := execute(MCP(&Resolver{}, m), args...); err == nil {
			t.Errorf("%q passed", args)
		}
	}
}

func TestAddNeedsASignIn(t *testing.T) {
	home(t)
	m, _ := fakeMachine(t.TempDir(), false, nil)
	if err, _ := execute(MCP(&Resolver{}, m), "add", "cursor"); !errors.Is(err, config.ErrNoContext) {
		t.Fatalf("err %v", err)
	}
}

// Project scope commits the token in .mcp.json: whoever clones acts as the person who added it.
func TestAddClaudeRefusesTheProjectScope(t *testing.T) {
	srv := newTokenServer(t)
	dir := signedIn(t, srv.URL)
	m, calls := fakeMachine(dir, true, nil)
	for _, args := range [][]string{{"add", "claude", "--scope", "project"}, {"add", "--print", "--scope", "project"}} {
		err, _ := execute(MCP(&Resolver{}, m), args...)
		if !errors.Is(err, ErrSharedScope) || srv.body != nil || len(*calls) != 0 {
			t.Fatalf("%q: err %v, body %s, calls %q", args, err, srv.body, *calls)
		}
	}
}

// Inside a repository nvoi hosts, the token is asked for with that repository named, so nvoi refuses a context of
// another workspace before anything is written.
func TestAddNamesTheRepositoryItRunsIn(t *testing.T) {
	srv := newTokenServer(t)
	dir := signedIn(t, srv.URL)
	before := Repository
	Repository = func(string) string { return srv.URL + "/git/other/app.git" }
	t.Cleanup(func() { Repository = before })
	m, _ := fakeMachine(dir, false, nil)

	if err, _ := execute(MCP(&Resolver{}, m), "add", "--print"); err != nil {
		t.Fatal(err)
	}
	if srv.repository != srv.URL+"/git/other/app.git" || srv.workspace != "acme" {
		t.Fatalf("repository %q, workspace %q", srv.repository, srv.workspace)
	}
}

// Several contexts: mcp add names one, like every command.
func TestAddNeedsTheContextAmongSeveral(t *testing.T) {
	srv := newTokenServer(t)
	dir := signedIn(t, srv.URL)
	file := contexts(t)
	file.Set("prod", config.Context{Endpoint: srv.URL, Token: "other-jwt", Workspace: "other"})
	file.Save()
	m, _ := fakeMachine(dir, false, nil)

	if err, _ := execute(MCP(&Resolver{}, m), "add", "--print"); err == nil || !strings.Contains(err.Error(), "--context") {
		t.Fatalf("err %v", err)
	}
	if srv.body != nil {
		t.Fatal("a token was made")
	}
	if err, _ := execute(MCP(&Resolver{Name: "prod"}, m), "add", "--print"); err != nil {
		t.Fatal(err)
	}
	if srv.auth != "Bearer other-jwt" || srv.workspace != "other" {
		t.Fatalf("auth %q, workspace %q", srv.auth, srv.workspace)
	}
}
