package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/getnvoi/nvoi/packages/clients/cli/internal/client"
)

type request struct {
	method, path, query, contentType string
	body                             []byte
}

func server(t *testing.T, status int, answer string) (*httptest.Server, *request) {
	t.Helper()
	c := &request{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.method, c.path, c.query, c.contentType = r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Content-Type")
		c.body, _ = io.ReadAll(r.Body)
		w.WriteHeader(status)
		w.Write([]byte(answer))
	}))
	t.Cleanup(srv.Close)
	return srv, c
}

func run(t *testing.T, srv *httptest.Server, args ...string) (error, string) {
	t.Helper()
	ops, err := Operations([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	root := &cobra.Command{Use: "nvoi", SilenceErrors: true}
	Tree(root, generated(ops), func() (*client.Client, error) { return client.New(srv.URL, "tok"), nil }, "")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(io.Discard)
	root.SetArgs(args)
	return root.Execute(), out.String()
}

func TestCreatePostsBodyFlags(t *testing.T) {
	srv, c := server(t, 201, `{"skill":{"id":"1","name":"x"}}`)
	err, out := run(t, srv, "skill", "create", "--name", "x", "--description", "y")
	if err != nil {
		t.Fatal(err)
	}
	var sent map[string]any
	json.Unmarshal(c.body, &sent)
	if c.method != "POST" || c.path != "/api/v1/skills" || sent["name"] != "x" || sent["description"] != "y" {
		t.Fatalf("%s %s %s", c.method, c.path, c.body)
	}
	if _, ok := sent["slug"]; ok {
		t.Fatalf("an unset flag was sent: %s", c.body)
	}
	if !strings.Contains(out, `"name": "x"`) {
		t.Fatalf("out %q", out)
	}
}

func TestShowTakesAPositional(t *testing.T) {
	srv, c := server(t, 200, `{"agents":[]}`)
	if err, _ := run(t, srv, "skill-file", "create", "abc"); err != nil {
		t.Fatal(err)
	}
	if c.method != "POST" || c.path != "/api/v1/skills/abc/files" {
		t.Fatalf("%s %s", c.method, c.path)
	}
}

func TestQueryFlags(t *testing.T) {
	srv, c := server(t, 200, `{"agents":[]}`)
	run(t, srv, "agent", "list", "--q", "foo")
	if c.query != "q=foo" {
		t.Fatalf("query %q", c.query)
	}
}

func TestMissingRequiredFlagSendsNothing(t *testing.T) {
	srv, c := server(t, 200, `{}`)
	err, _ := run(t, srv, "skill", "create", "--name", "x")
	if err == nil || c.method != "" || !strings.Contains(err.Error(), "description") {
		t.Fatalf("err %v, request %q", err, c.method)
	}
}

func TestMissingPositionalSendsNothing(t *testing.T) {
	srv, c := server(t, 200, `{}`)
	if err, _ := run(t, srv, "skill-file", "create"); err == nil || c.method != "" {
		t.Fatalf("err %v, request %q", err, c.method)
	}
}

func TestMultipartSendsTheFileAndOneFieldPerArrayValue(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a.md")
	os.WriteFile(file, []byte("hi"), 0o600)
	srv, c := server(t, 200, `{"skill":{}}`)
	if err, _ := run(t, srv, "skill-file", "create", "1", "--file", file, "--path", "docs/a.md", "--document_ids", "a,b"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(c.contentType, "multipart/form-data") {
		t.Fatalf("content-type %q", c.contentType)
	}
	if !bytes.Contains(c.body, []byte(`filename="a.md"`)) || !bytes.Contains(c.body, []byte("docs/a.md")) {
		t.Fatalf("body %s", c.body)
	}
	if bytes.Count(c.body, []byte(`name="document_ids[]"`)) != 2 {
		t.Fatalf("body %s", c.body)
	}
}

func TestFilesRepeatedOrCommaSeparatedSendOnePartEach(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for _, n := range []string{"a.txt", "b.txt", "c.txt"} {
		p := filepath.Join(dir, n)
		os.WriteFile(p, []byte(n), 0o600)
		paths = append(paths, p)
	}
	srv, c := server(t, 201, `{"skill":{}}`)
	if err, _ := run(t, srv, "skill-file", "create", "1", "--path", "docs", "--files", paths[0], "--files", paths[1]+","+paths[2]); err != nil {
		t.Fatal(err)
	}
	if bytes.Count(c.body, []byte(`name="files[]"`)) != 3 {
		t.Fatalf("body %s", c.body)
	}
}

func TestTable(t *testing.T) {
	srv, _ := server(t, 200, `{"agents":[{"id":"1","name":"a","nested":{"x":1}},{"id":"2","name":"b"}]}`)
	_, out := run(t, srv, "agent", "list", "--table")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "id") || !strings.Contains(lines[0], "name") || strings.Contains(lines[0], "nested") {
		t.Fatalf("out %q", out)
	}
}

func TestTableOfARecordListsItsFieldsThenItsLists(t *testing.T) {
	raw := `{"environment":{"id":"e","name":"dev","preview_port":3000,"commands":[{"id":"c","name":"db","kind":"preview","port":5432,"before":["a","b"]}],"vars":[]}}`
	var out bytes.Buffer
	if err := printTable(json.RawMessage(raw), nil, &out); err != nil {
		t.Fatal(err)
	}
	want := "id            e\nname          dev\npreview_port  3000\n\ncommands\nid  name  before  kind     port\nc   db    a; b    preview  5432\n"
	if out.String() != want {
		t.Fatalf("out %q", out.String())
	}
}

func TestEmptyIntegerIsNull(t *testing.T) {
	if v, err := value("integer", ""); v != nil || err != nil {
		t.Fatalf("v %v err %v", v, err)
	}
}

func TestFailureIsTheMessage(t *testing.T) {
	srv, _ := server(t, 404, `{"error":"That does not exist.","code":"not_found"}`)
	err, _ := run(t, srv, "skill-file", "create", "nope")
	if err == nil || err.Error() != "That does not exist." {
		t.Fatalf("err %v", err)
	}
}

func TestFieldErrorsAreListed(t *testing.T) {
	srv, _ := server(t, 422, `{"errors":{"name":["has already been taken"]}}`)
	err, _ := run(t, srv, "skill", "create", "--name", "x", "--description", "y")
	if err == nil || err.Error() != "name: has already been taken" {
		t.Fatalf("err %v", err)
	}
}

func TestNoContentPrintsNothing(t *testing.T) {
	srv, _ := server(t, 204, ``)
	err, out := run(t, srv, "skill-file", "create", "1")
	if err != nil || out != "" {
		t.Fatalf("err %v out %q", err, out)
	}
}

// A declared answer fixes the columns: a field the document names is a column whatever the rows hold, and one it
// declares as an object never is, null or not.
func TestTableColumnsFollowTheDeclaredAnswer(t *testing.T) {
	var answer schema
	json.Unmarshal([]byte(`{"type":"object","properties":{"runs":{"type":"array","items":{"type":"object","properties":{
		"turn_id":{"type":"string"},"session":{"type":["string","null"]},"waiting_on":{"type":["string","null"]},
		"pull_request":{"type":["object","null"]},"lines":{"type":"array","items":{"type":"string"}}}}}}}`), &answer)
	print := func(raw string) string {
		var out bytes.Buffer
		if err := printTable(json.RawMessage(raw), &answer, &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	header := func(out string) string { return strings.Join(strings.Fields(strings.SplitN(out, "\n", 2)[0]), " ") }

	one := print(`{"runs":[{"turn_id":"t","session":"Fix","waiting_on":null,"pull_request":null,"lines":[]}]}`)
	two := print(`{"runs":[{"turn_id":"t","session":"Fix","pull_request":{"url":"u"}}]}`)
	none := print(`{"runs":[]}`)
	if header(one) != header(two) || header(one) != header(none) {
		t.Fatalf("columns moved:\n%s\n%s\n%s", one, two, none)
	}
	if want := "lines  session  turn_id  waiting_on\n"; none != want {
		t.Fatalf("none %q", none)
	}
	if strings.Contains(one, "pull_request") {
		t.Fatalf("an object became a column: %q", one)
	}
}

// A declared record keeps its scalar fields when a value is absent, then prints its lists under their names.
func TestTableOfADeclaredRecord(t *testing.T) {
	var answer schema
	json.Unmarshal([]byte(`{"type":"object","properties":{"status":{"type":"object","properties":{
		"sessions":{"type":"integer"},"turns_waiting":{"type":"integer"},
		"machines":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},"idle_since":{"type":["string","null"]}}}}}}}}`), &answer)
	var out bytes.Buffer
	if err := printTable(json.RawMessage(`{"status":{"sessions":2,"machines":[{"name":"app"}]}}`), &answer, &out); err != nil {
		t.Fatal(err)
	}
	want := "sessions       2\nturns_waiting  \n\nmachines\nname  idle_since\napp   \n"
	if out.String() != want {
		t.Fatalf("out %q", out.String())
	}
}

func TestNamedSaysWhichIdAnArgumentIs(t *testing.T) {
	for _, c := range [][3]string{
		{"/sessions/{id}/machine", "id", "session-id"},
		{"/repos/{id}/start", "id", "repo-id"},
		{"/worktrees/{worktree_id}/ship", "worktree_id", "worktree-id"},
		{"/environments/{id}", "id", "environment-id"},
	} {
		if got := Named(c[0], c[1]); got != c[2] {
			t.Errorf("%s %s: %s, want %s", c[0], c[1], got, c[2])
		}
	}
}
