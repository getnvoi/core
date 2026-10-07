package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/getnvoi/nvoi/packages/clients/cli/internal/client"
)

func TestFollowPrintsEventsOnceAndStopsOnIdle(t *testing.T) {
	followEvery = time.Millisecond
	pages := []string{
		`{"session":{"status":"running"},"turns":[{"id":"t1","events":[{"id":"1","kind":"text","content":"hi"}]}]}`,
		`{"session":{"status":"running"},"turns":[{"id":"t1","events":[{"id":"1","kind":"text","content":"hi"},{"id":"2","kind":"tool_use","content":"{}","tool":"Read"},{"id":"3","kind":"result","content":"done"}]}]}`,
		`{"session":{"status":"idle"},"turns":[{"id":"t1","events":[{"id":"3","kind":"result","content":"done"},{"id":"4","kind":"result","content":""}]}]}`,
	}
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/sessions/s1/messages" {
			t.Errorf("path %s", r.URL.Path)
		}
		page := pages[min(n, len(pages)-1)]
		n++
		w.Write([]byte(page))
	}))
	defer srv.Close()

	var out bytes.Buffer
	if err := follow(client.New(srv.URL, "tok"), "s1", &out); err != nil {
		t.Fatal(err)
	}
	want := "text: hi\ntool: Read\nresult: done\n"
	if out.String() != want {
		t.Fatalf("out %q", out.String())
	}
	if n != 3 {
		t.Fatalf("%d requests", n)
	}
}

func TestFollowStopsOnWaiting(t *testing.T) {
	followEvery = time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"session":{"status":"waiting"},"turns":[]}`))
	}))
	defer srv.Close()
	var out bytes.Buffer
	if err := follow(client.New(srv.URL, "tok"), "s1", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "waiting on an approval") {
		t.Fatalf("out %q", out.String())
	}
}

func TestFollowKeepsPollingWhileIdleBeforeTheTurnStarts(t *testing.T) {
	followEvery = time.Millisecond
	pages := []string{
		`{"session":{"status":"idle"},"turns":[]}`,
		`{"session":{"status":"running"},"turns":[]}`,
		`{"session":{"status":"idle"},"turns":[]}`,
	}
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(pages[min(n, len(pages)-1)]))
		n++
	}))
	defer srv.Close()
	var out bytes.Buffer
	if err := follow(client.New(srv.URL, "tok"), "s1", &out); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("%d requests", n)
	}
}
