package client

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJSONSendsBearerAndBody(t *testing.T) {
	var got *http.Request
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("content-type", "application/json")
		w.Write([]byte(`{"skill":{"name":"x"}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "tok")
	c.Workspace = "acme"
	status, raw, err := c.JSON("POST", "/api/v1/skills", url.Values{"q": {"a b"}}, map[string]any{"name": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if status != 200 || !strings.Contains(string(raw), `"skill"`) {
		t.Fatalf("status %d raw %s", status, raw)
	}
	if got.Header.Get("Authorization") != "Bearer tok" {
		t.Fatalf("authorization %q", got.Header.Get("Authorization"))
	}
	if got.Header.Get("Nvoi-Workspace") != "acme" {
		t.Fatalf("workspace %q", got.Header.Get("Nvoi-Workspace"))
	}
	if got.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("content-type %q", got.Header.Get("Content-Type"))
	}
	if got.URL.Path != "/api/v1/skills" || got.URL.Query().Get("q") != "a b" {
		t.Fatalf("url %s", got.URL)
	}
	var sent map[string]any
	json.Unmarshal(body, &sent)
	if sent["name"] != "x" {
		t.Fatalf("body %s", body)
	}
}

func TestFailures(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   string
	}{
		{404, `{"error":"That does not exist.","code":"not_found"}`, "That does not exist."},
		{404, `{"code":"not_found"}`, "not_found"},
		{401, `{"error":"This token is expired: nvoi login.","code":"unauthenticated"}`, "This token is expired: nvoi login."},
		{401, `{"code":"unauthenticated"}`, "run nvoi login"},
		{422, `{"errors":{"name":["can't be blank"],"branch":["is invalid"]}}`, "branch: is invalid; name: can't be blank"},
		{500, `oops`, "HTTP 500"},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			w.Write([]byte(tc.body))
		}))
		_, _, err := New(srv.URL, "tok").JSON("GET", "/x", nil, nil)
		srv.Close()
		if err == nil || err.Error() != tc.want {
			t.Fatalf("%d %s: got %v, want %q", tc.status, tc.body, err, tc.want)
		}
	}
}

func TestFailureKeepsTheCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`{"error":"Owners only.","code":"not_an_owner"}`))
	}))
	defer srv.Close()
	_, _, err := New(srv.URL, "tok").JSON("GET", "/x", nil, nil)
	f, ok := err.(*Failure)
	if !ok || f.Code != "not_an_owner" || f.Status != 403 {
		t.Fatalf("err %#v", err)
	}
}

func TestMultipart(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "notes.md")
	os.WriteFile(file, []byte("hello"), 0o600)

	var fields, files map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseMultipartForm(1 << 20)
		fields = map[string]string{"path": r.FormValue("path")}
		f, hdr, _ := r.FormFile("file")
		data, _ := io.ReadAll(f)
		files = map[string]string{hdr.Filename: string(data)}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	_, _, err := New(srv.URL, "tok").Multipart("POST", "/api/v1/skills/1/files", url.Values{"path": {"docs/notes.md"}}, map[string][]string{"file": {file}})
	if err != nil {
		t.Fatal(err)
	}
	if fields["path"] != "docs/notes.md" || files["notes.md"] != "hello" {
		t.Fatalf("fields %v files %v", fields, files)
	}
}
