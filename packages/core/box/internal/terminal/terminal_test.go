package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

const key = "0123456789abcdef0123456789abcdef"

func server(t *testing.T) (*Server, *httptest.Server) {
	file := filepath.Join(t.TempDir(), "key")
	os.WriteFile(file, []byte(key+"\n"), 0o600)
	s := &Server{KeyFile: file, Dir: t.TempDir(), Shell: "sh"}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return s, srv
}

func dial(t *testing.T, ctx context.Context, srv *httptest.Server, path string) *websocket.Conn {
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+path,
		&websocket.DialOptions{HTTPHeader: http.Header{KeyHeader: {key}}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Reads until the output so far contains want, or an exit message arrives.
func until(t *testing.T, ctx context.Context, c *websocket.Conn, want string) (string, int) {
	var out strings.Builder
	for !strings.Contains(out.String(), want) {
		typ, b, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("waiting for %q, got %q: %v", want, out.String(), err)
		}
		if typ == websocket.MessageText {
			var m struct {
				Type string
				Code int
			}
			json.Unmarshal(b, &m)
			if m.Type == "exit" {
				return out.String(), m.Code
			}
			continue
		}
		out.Write(b)
	}
	return out.String(), -1
}

func do(t *testing.T, method, url, presented string) *http.Response {
	req, _ := http.NewRequest(method, url, nil)
	if presented != "" {
		req.Header.Set(KeyHeader, presented)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestKey(t *testing.T) {
	s, srv := server(t)
	if res := do(t, "GET", srv.URL+"/health", ""); res.StatusCode != 200 {
		t.Fatalf("health without a key: %d", res.StatusCode)
	}
	for _, presented := range []string{"", "wrong", key + "x"} {
		if res := do(t, "GET", srv.URL+"/pty", presented); res.StatusCode != 401 {
			t.Fatalf("key %q: %d", presented, res.StatusCode)
		}
	}
	if res := do(t, "GET", srv.URL+"/pty", key); res.StatusCode != 200 {
		t.Fatalf("right key: %d", res.StatusCode)
	}
	// A new key in the file is the key from the next request on; the old one stops working.
	os.WriteFile(s.KeyFile, []byte("fedcba9876543210fedcba9876543210"), 0o600)
	if res := do(t, "GET", srv.URL+"/pty", key); res.StatusCode != 401 {
		t.Fatalf("old key after a rewrite: %d", res.StatusCode)
	}
	// A short or missing key file opens nothing, not even to an empty header.
	os.WriteFile(s.KeyFile, []byte("short"), 0o600)
	if res := do(t, "GET", srv.URL+"/pty", "short"); res.StatusCode != 401 {
		t.Fatalf("short key: %d", res.StatusCode)
	}
	os.Remove(s.KeyFile)
	if res := do(t, "GET", srv.URL+"/pty", ""); res.StatusCode != 401 {
		t.Fatalf("no key file: %d", res.StatusCode)
	}
}

func TestShell(t *testing.T) {
	s, srv := server(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	a := dial(t, ctx, srv, "/pty/one?cols=80&rows=24")
	a.Write(ctx, websocket.MessageBinary, []byte("echo h''i\n"))
	until(t, ctx, a, "hi")
	if err := a.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","cols":100,"rows":40}`)); err != nil {
		t.Fatal(err)
	}
	a.Write(ctx, websocket.MessageBinary, []byte("stty size\n"))
	until(t, ctx, a, "40 100")
	a.Close(websocket.StatusNormalClosure, "")

	res := do(t, "GET", srv.URL+"/pty", key)
	var list []Shell
	json.NewDecoder(res.Body).Decode(&list)
	if len(list) != 1 || list[0].ID != "one" || list[0].Command != "sh" {
		t.Fatalf("list after detach: %+v", list)
	}

	b := dial(t, ctx, srv, "/pty/one")
	if _, b1, err := b.Read(ctx); err != nil || !strings.Contains(string(b1), "hi") {
		t.Fatalf("scrollback %q %v", b1, err)
	}
	b.Write(ctx, websocket.MessageBinary, []byte("exit 3\n"))
	if _, code := until(t, ctx, b, "\x00never"); code != 3 {
		t.Fatalf("exit code %d", code)
	}
	if _, _, err := b.Read(ctx); websocket.CloseStatus(err) != websocket.StatusNormalClosure {
		t.Fatalf("close: %v", err)
	}
	if _, ok := s.ptys.Load("one"); ok {
		t.Fatal("shell kept after it exited")
	}
}

func TestKill(t *testing.T) {
	_, srv := server(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c := dial(t, ctx, srv, "/pty/two")
	c.Write(ctx, websocket.MessageBinary, []byte("echo re''ady\n"))
	until(t, ctx, c, "ready")
	if res := do(t, "DELETE", srv.URL+"/pty/two", key); res.StatusCode != 204 {
		t.Fatalf("delete: %d", res.StatusCode)
	}
	if _, code := until(t, ctx, c, "\x00never"); code == -1 {
		t.Fatal("no exit message after delete")
	}
	if res := do(t, "DELETE", srv.URL+"/pty/two", key); res.StatusCode != 404 {
		t.Fatalf("second delete: %d", res.StatusCode)
	}
	if res := do(t, "DELETE", srv.URL+"/pty/..", key); res.StatusCode == 204 {
		t.Fatal("a bad id was deleted")
	}
}

func TestDead(t *testing.T) {
	s, srv := server(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// A shell still listed whose process has ended, as an attach racing the exit finds it.
	d := &term{id: "gone", clients: map[*client]struct{}{}}
	d.exit(5)
	s.ptys.Store("gone", d)
	c := dial(t, ctx, srv, "/pty/gone")
	if _, code := until(t, ctx, c, "\x00never"); code != 5 {
		t.Fatalf("exit code %d", code)
	}
	if _, _, err := c.Read(ctx); websocket.CloseStatus(err) != websocket.StatusNormalClosure {
		t.Fatalf("close: %v", err)
	}
}

func TestFailed(t *testing.T) {
	t.Setenv("PATH", "")
	_, srv := server(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := dial(t, ctx, srv, "/pty/none")
	_, _, err := c.Read(ctx)
	var ce websocket.CloseError
	if !errors.As(err, &ce) || ce.Code != failed || !strings.HasPrefix(ce.Reason, "terminal did not start") {
		t.Fatalf("close: %v", err)
	}
}

func TestFollow(t *testing.T) {
	s, srv := server(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	log := filepath.Join(t.TempDir(), "preview.log")
	os.WriteFile(log, []byte("booting\nlistening on 3000\n"), 0o600)

	c := dial(t, ctx, srv, "/pty/log-1?follow=file:"+log)
	until(t, ctx, c, "listening on 3000")
	f, _ := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("GET /up 200\n")
	f.Close()
	until(t, ctx, c, "GET /up 200")

	// A followed log is not a shell: the Terminal view never lists it.
	res := do(t, "GET", srv.URL+"/pty", key)
	var list []Shell
	json.NewDecoder(res.Body).Decode(&list)
	if len(list) != 0 {
		t.Fatalf("a follow listed as a shell: %+v", list)
	}

	// Its last client gone, the follow stops.
	c.Close(websocket.StatusNormalClosure, "")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := s.ptys.Load("log-1"); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("follow kept after its client left")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestFollowSource(t *testing.T) {
	for _, bad := range []string{"", "unit:", "unit:a b", "unit:x;rm", "file:relative.log", "file:/tmp/../etc/passwd", "shell:bash"} {
		if _, err := Follow(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
	argv, err := Follow("unit:nvoi-preview-web")
	if err != nil || argv[len(argv)-1] != "nvoi-preview-web" || !strings.Contains(strings.Join(argv, " "), "journalctl -f -n 300") {
		t.Fatalf("unit: %v %v", argv, err)
	}
	argv, err = Follow("file:/mnt/data/state/preview-web.log")
	if err != nil || !strings.Contains(strings.Join(argv, " "), "tail -n 300 -F /mnt/data/state/preview-web.log") {
		t.Fatalf("file: %v %v", argv, err)
	}

	_, srv := server(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := dial(t, ctx, srv, "/pty/log-2?follow=shell:bash")
	if _, _, err := c.Read(ctx); websocket.CloseStatus(err) != failed {
		t.Fatalf("a bad source: %v", err)
	}
}
