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

func TestFollowPrintsADeploymentsLinesOnceAndStopsWhenLive(t *testing.T) {
	linesEvery = time.Millisecond
	var asked []string
	pages := []string{
		`{"deployment":{"status":"waiting"},"lines":[]}`,
		`{"deployment":{"status":"building"},"lines":[{"id":"a","step":"build","output":"#1 DONE"},{"id":"b","step":"import","output":"master downloads"}]}`,
		`{"deployment":{"status":"live"},"lines":[{"id":"c","step":"rollout","output":"live"}]}`,
	}
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/deployments/d1/logs" {
			t.Errorf("path %s", r.URL.Path)
		}
		asked = append(asked, r.URL.Query().Get("since"))
		w.Write([]byte(pages[min(n, len(pages)-1)]))
		n++
	}))
	defer srv.Close()

	var out bytes.Buffer
	if err := followLines(client.New(srv.URL, "tok"), deploymentLog, "d1", &out); err != nil {
		t.Fatal(err)
	}
	want := "waiting for a person to allow it: nvoi deployment allow d1\nbuild    #1 DONE\nimport   master downloads\nrollout  live\nlive\n"
	if out.String() != want {
		t.Fatalf("out %q", out.String())
	}
	if strings.Join(asked, ",") != ",,b" {
		t.Fatalf("since %q", asked)
	}
}

func TestFollowAnImageThatFailsSaysWhy(t *testing.T) {
	linesEvery = time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/images/i1/logs" {
			t.Errorf("path %s", r.URL.Path)
		}
		w.Write([]byte(`{"image":{"status":"failed","error":"exit 1: no Dockerfile"},"lines":[{"id":"a","step":"failed","output":"exit 1: no Dockerfile"}]}`))
	}))
	defer srv.Close()

	var out bytes.Buffer
	err := followLines(client.New(srv.URL, "tok"), imageLog, "i1", &out)
	if err == nil || err.Error() != "the image failed: exit 1: no Dockerfile" {
		t.Fatalf("err %v", err)
	}
}

func TestFollowADatabaseUntilItIsReady(t *testing.T) {
	linesEvery = time.Millisecond
	pages := []string{
		`{"database":{"status":"creating"},"lines":[{"id":"a","step":"provision","output":"making nvoi-prod-acme-worker-production at neon: postgres 17"}]}`,
		`{"database":{"status":"ready"},"lines":[{"id":"b","step":"provision","output":"made nvoi-prod-acme-worker-production (p-1)"}]}`,
	}
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/environments/e1/database/logs" {
			t.Errorf("path %s", r.URL.Path)
		}
		w.Write([]byte(pages[n]))
		n++
	}))
	defer srv.Close()

	var out bytes.Buffer
	if err := followLines(client.New(srv.URL, "tok"), databaseLog, "e1", &out); err != nil {
		t.Fatal(err)
	}
	want := "provision making nvoi-prod-acme-worker-production at neon: postgres 17\nprovision made nvoi-prod-acme-worker-production (p-1)\nready\n"
	if out.String() != want {
		t.Fatalf("got %q", out.String())
	}
}
