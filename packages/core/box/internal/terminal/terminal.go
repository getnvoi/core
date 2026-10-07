// Package terminal is boxd's shell server, on its own port inside a worktree's machine. It keeps shells on ptys that
// outlive the browser tab that opened them: a shell goes on running when its socket drops, and a client that
// attaches later is sent its scrollback first. Every request but /health carries the machine's key in
// KeyHeader; the preview worker adds it, so the browser never holds it.
package terminal

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"
)

const KeyHeader = "X-Nvoi-Terminal"

const (
	scrollback = 256 << 10
	// The close code sent when the shell could not be started; a client should not attach again.
	failed websocket.StatusCode = 4000
	// Sent while a socket is quiet: the proxies in front close a socket that says nothing for 100s.
	ping = 25 * time.Second
)

var id = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// A systemd unit a follow socket may read the journal of.
var unit = regexp.MustCompile(`^[A-Za-z0-9_.@-]{1,128}$`)

// Lines of a log a follow socket starts with.
const backlog = "300"

type Server struct {
	// The file holding the key, read on every request: a fork is given its own key by rewriting the file,
	// with no restart.
	KeyFile string
	// Where a shell starts; / when it is not there.
	Dir   string
	Shell string
	// Who a shell runs as (nil: boxd's own user) and its environment (proc.As).
	Credential *syscall.Credential
	Env        []string
	ptys       sync.Map
}

// A shell on a pty that outlives its clients: output goes to every attached client and into the scrollback a
// client attaching later is sent first.
type term struct {
	id, command string
	started     time.Time
	cmd         *exec.Cmd
	f           *os.File
	mu          sync.Mutex
	ring        []byte
	clients     map[*client]struct{}
	dead        []byte // the exit message, once the shell has ended
	// A log followed for a socket rather than a shell: it ends with its last client.
	follow bool
}

type client struct {
	out chan frame
}

type frame struct {
	typ  websocket.MessageType
	data []byte
}

// A shell as GET /pty lists it.
type Shell struct {
	ID      string `json:"id"`
	Command string `json:"command"`
	Started string `json:"started"`
	Clients int    `json:"clients"`
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /pty", s.list)
	mux.HandleFunc("GET /pty/{id}", checked(s.attach))
	mux.HandleFunc("DELETE /pty/{id}", checked(s.kill))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" && !s.allowed(r.Header.Get(KeyHeader)) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// A key shorter than 32 characters is no key: a machine whose file is missing or empty refuses everyone.
func (s *Server) allowed(presented string) bool {
	b, err := os.ReadFile(s.KeyFile)
	key := strings.TrimSpace(string(b))
	if err != nil || len(key) < 32 {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(presented), []byte(key)) == 1
}

func checked(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !id.MatchString(r.PathValue("id")) {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		h(w, r)
	}
}

func (s *Server) list(w http.ResponseWriter, _ *http.Request) {
	out := []Shell{}
	s.ptys.Range(func(_, v any) bool {
		t := v.(*term)
		t.mu.Lock()
		if t.dead == nil && !t.follow {
			out = append(out, Shell{t.id, t.command, t.started.UTC().Format(time.RFC3339), len(t.clients)})
		}
		t.mu.Unlock()
		return true
	})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (s *Server) open(id string, cols, rows int) (*term, error) {
	if v, ok := s.ptys.Load(id); ok {
		return v.(*term), nil
	}
	shell := s.Shell
	if shell == "" {
		shell = "bash"
	}
	if _, err := exec.LookPath(shell); err != nil {
		shell = "sh"
	}
	if _, err := exec.LookPath(shell); err != nil {
		return nil, errors.New("no shell on this machine")
	}
	dir := s.Dir
	if info, err := os.Stat(dir); dir == "" || err != nil || !info.IsDir() {
		dir = "/"
	}
	cmd := exec.Command(shell, "-l")
	cmd.Dir = dir
	cmd.Env = append(append([]string(nil), s.Env...), "TERM=xterm-256color", "COLORTERM=truecolor")
	f, err := pty.StartWithAttrs(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)},
		&syscall.SysProcAttr{Setsid: true, Setctty: true, Credential: s.Credential})
	if err != nil {
		return nil, err
	}
	t := &term{id: id, command: shell, started: time.Now(), cmd: cmd, f: f, clients: map[*client]struct{}{}}
	if v, loaded := s.ptys.LoadOrStore(id, t); loaded {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		f.Close()
		cmd.Wait()
		return v.(*term), nil
	}
	s.run(id, t)
	return t, nil
}

// Pumps the output to the clients, and once the command ends says so and forgets it.
func (s *Server) run(id string, t *term) {
	cmd, f := t.cmd, t.f
	pumped := make(chan struct{})
	go t.pump(pumped)
	go func() {
		code := exitCode(cmd.Wait())
		// Output still in the pty is read out before the exit is announced; a child holding the tty cannot
		// hold this up for more than a second.
		select {
		case <-pumped:
		case <-time.After(time.Second):
		}
		f.Close()
		s.ptys.CompareAndDelete(id, t)
		t.exit(code)
	}()
}

func (t *term) pump(done chan struct{}) {
	defer close(done)
	buf := make([]byte, 32<<10)
	for {
		n, err := t.f.Read(buf)
		if n > 0 {
			b := append([]byte(nil), buf[:n]...)
			t.mu.Lock()
			if t.ring = append(t.ring, b...); len(t.ring) > 2*scrollback {
				t.ring = append([]byte(nil), t.ring[len(t.ring)-scrollback:]...)
			}
			for c := range t.clients {
				t.send(c, frame{websocket.MessageBinary, b})
			}
			t.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// Queues f for c; a client that cannot keep up is detached. Called with t.mu held.
func (t *term) send(c *client, f frame) {
	select {
	case c.out <- f:
	default:
		t.drop(c)
	}
}

// Called with t.mu held.
func (t *term) drop(c *client) {
	if _, ok := t.clients[c]; ok {
		delete(t.clients, c)
		close(c.out)
	}
}

func (t *term) exit(code int) {
	msg, _ := json.Marshal(map[string]any{"type": "exit", "code": code})
	t.mu.Lock()
	defer t.mu.Unlock()
	t.dead = msg
	for c := range t.clients {
		t.send(c, frame{websocket.MessageText, msg})
		t.drop(c)
	}
}

// GET /pty/{id}?cols=&rows=: a websocket onto the shell, started when it is not running. With
// follow=unit:<name> or follow=file:<path> it is a log instead (Follow): its last lines, then each line as it is
// written, and it stops when its last client leaves. Binary frames are
// keys; a text frame {"type":"resize","cols":,"rows":} resizes. The server sends output as binary frames,
// the scrollback first, and {"type":"exit","code":} as text when the shell ends.
func (s *Server) attach(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	cols, _ := strconv.Atoi(q.Get("cols"))
	rows, _ := strconv.Atoi(q.Get("rows"))
	if cols <= 0 || rows <= 0 || cols > 1000 || rows > 1000 {
		cols, rows = 120, 32
	}
	// The browser's origin is the app's and this host is the vendor's: the key above is the check.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	conn.SetReadLimit(1 << 20)
	var t *term
	if source := q.Get("follow"); source != "" {
		t, err = s.follow(r.PathValue("id"), source)
	} else {
		t, err = s.open(r.PathValue("id"), cols, rows)
	}
	if err != nil {
		conn.Close(failed, reason(err))
		return
	}
	c := &client{out: make(chan frame, 1024)}
	t.mu.Lock()
	// A shell that ended after it was looked up takes no clients: this one is told it has exited.
	if dead := t.dead; dead != nil {
		t.mu.Unlock()
		conn.Write(r.Context(), websocket.MessageText, dead)
		conn.Close(websocket.StatusNormalClosure, "")
		return
	}
	c.out <- frame{websocket.MessageBinary, append([]byte(nil), t.ring...)}
	t.clients[c] = struct{}{}
	t.mu.Unlock()
	ctx := r.Context()
	written := make(chan struct{})
	go func() {
		defer close(written)
		tick := time.NewTicker(ping)
		defer tick.Stop()
		for {
			select {
			case f, ok := <-c.out:
				if !ok {
					conn.Close(websocket.StatusNormalClosure, "")
					return
				}
				if conn.Write(ctx, f.typ, f.data) != nil {
					return
				}
			case <-tick.C:
				if conn.Ping(ctx) != nil {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			break
		}
		if t.follow {
			continue
		}
		if typ == websocket.MessageBinary {
			t.f.Write(data)
			continue
		}
		var m struct {
			Type       string
			Cols, Rows int
		}
		if json.Unmarshal(data, &m) == nil && m.Type == "resize" && m.Cols > 0 && m.Rows > 0 {
			pty.Setsize(t.f, &pty.Winsize{Cols: uint16(m.Cols), Rows: uint16(m.Rows)})
		}
	}
	t.mu.Lock()
	t.drop(c)
	last := t.follow && len(t.clients) == 0
	t.mu.Unlock()
	if last {
		s.ptys.CompareAndDelete(r.PathValue("id"), t)
		syscall.Kill(-t.cmd.Process.Pid, syscall.SIGTERM)
	}
	<-written
}

// The command that follows a log: a unit's journal, or a file by its absolute path, from its last lines on.
func Follow(source string) ([]string, error) {
	kind, where, _ := strings.Cut(source, ":")
	switch {
	case kind == "unit" && unit.MatchString(where):
		return elevated("journalctl", "-f", "-n", backlog, "--no-pager", "-o", "short-iso", "-u", where), nil
	case kind == "file" && filepath.IsAbs(where) && filepath.Clean(where) == where:
		return elevated("tail", "-n", backlog, "-F", where), nil
	}
	return nil, errors.New("follow takes unit:<name> or file:<absolute path>")
}

// Under sudo when it lets this account in without a password: the journal and some logs are root's to read.
// Anyone holding the key has a shell as this account already, so this opens nothing more.
func elevated(argv ...string) []string {
	if _, err := exec.LookPath("sudo"); err == nil && exec.Command("sudo", "-n", "true").Run() == nil {
		return append([]string{"sudo", "-n"}, argv...)
	}
	return argv
}

// A log followed under id, its output on a pipe rather than a pty: no terminal, no line discipline.
func (s *Server) follow(id, source string) (*term, error) {
	if v, ok := s.ptys.Load(id); ok {
		return v.(*term), nil
	}
	argv, err := Follow(source)
	if err != nil {
		return nil, err
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = w, w
	cmd.Env = append(append([]string(nil), s.Env...), "SYSTEMD_PAGER=", "SYSTEMD_COLORS=0")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Credential: s.Credential}
	if err := cmd.Start(); err != nil {
		r.Close()
		w.Close()
		return nil, err
	}
	w.Close()
	t := &term{id: id, command: source, started: time.Now(), cmd: cmd, f: r, clients: map[*client]struct{}{}, follow: true}
	if v, loaded := s.ptys.LoadOrStore(id, t); loaded {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		r.Close()
		cmd.Wait()
		return v.(*term), nil
	}
	s.run(id, t)
	return t, nil
}

// A close reason fits in 123 bytes.
func reason(err error) string {
	msg := "terminal did not start: " + err.Error()
	if len(msg) > 123 {
		msg = strings.ToValidUTF8(msg[:123], "")
	}
	return msg
}

// Hangs up the shell's process group, then kills what is left of it after 2s.
func (s *Server) kill(w http.ResponseWriter, r *http.Request) {
	v, ok := s.ptys.LoadAndDelete(r.PathValue("id"))
	if !ok {
		http.Error(w, "no such shell", http.StatusNotFound)
		return
	}
	t := v.(*term)
	if t.cmd == nil || t.cmd.Process == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	pid := t.cmd.Process.Pid
	syscall.Kill(-pid, syscall.SIGHUP)
	time.AfterFunc(2*time.Second, func() { syscall.Kill(-pid, syscall.SIGKILL) })
	w.WriteHeader(http.StatusNoContent)
}

func exitCode(err error) int {
	var e *exec.ExitError
	switch {
	case err == nil:
		return 0
	case !errors.As(err, &e):
		return 255
	}
	if ws, ok := e.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return e.ExitCode()
}
