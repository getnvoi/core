// Package api exposes the eleven routes the control plane needs to drive a claude process,
// and nothing else. It moves bytes and never inspects them: every protocol decision — what a
// line means, whether a tool may run, when a turn is over — belongs to the control plane.
package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/getnvoi/nvoi/packages/core/box/internal/proc"
)

func New(tbl *proc.Table, token string) http.Handler {
	s := &server{tbl: tbl, token: token}
	mux := http.NewServeMux()
	// Unauthenticated, and it says everything a caller waits on. Always 200: a box whose daemon has
	// not come up is answering correctly, and a 503 would look like a box that cannot be reached.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "docker": dockerUp()})
	})
	mux.HandleFunc("POST /commands", s.spawn)
	mux.HandleFunc("GET /commands", s.named)
	mux.HandleFunc("GET /commands/{id}/logs", s.logs)
	mux.HandleFunc("GET /commands/{id}/socket", s.socket)
	mux.HandleFunc("POST /commands/{id}/stdin", s.stdin)
	mux.HandleFunc("DELETE /commands/{id}/stdin", s.closeStdin)
	mux.HandleFunc("GET /commands/{id}", s.status)
	mux.HandleFunc("POST /commands/{id}/signal", s.signal)
	mux.HandleFunc("DELETE /commands/{id}", s.kill)
	mux.HandleFunc("GET /file", s.file)
	mux.HandleFunc("PUT /file", s.putFile)
	return s.authenticated(mux)
}

type server struct {
	tbl   *proc.Table
	token string
}

// Our own header, not `Authorization`: the substrate's edge claims that one, and sharing the
// slot would mean it consuming the agent's own token.
const tokenHeader = "X-Box-Token"

// Constant-time compare: this token is the only thing between a sandbox and
// anything that can reach its address.
func (s *server) authenticated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The substrate asks /health and cannot hold a token. Nothing else here is
		// unauthenticated.
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}

		// This header and no other. `authorization` on a request that reached here
		// carries the SUBSTRATE's preview token, not ours — accepting it would put
		// somebody else's credential up against this box's only lock.
		got := r.Header.Get(tokenHeader)

		if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) spawn(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Argv []string          `json:"argv"`
		Dir  string            `json:"dir"`
		Env  map[string]string `json:"env"`
		// What it is and where it listens. Both optional — a one-off `test -d` has neither, and a
		// command with no port makes no claim about answering.
		Name string `json:"name"`
		Port int    `json:"port"`
		// Who it runs as. Absent is root — see internal/proc/user.go.
		As string `json:"as"`
		// Whether its stdin is held open for `POST /commands/{id}/stdin`. Absent reads EOF.
		Stdin bool `json:"stdin"`
		// How long to hold this request for the command's own ending, in seconds; absent means do not
		// wait. Not a second way to run one — the same spawn, held until what it started is over.
		Wait int `json:"wait"`
		// What caused this spawn. With a key the spawn happens once: a second ask
		// with the same key, from any node and after any restart, answers the
		// process the first one started, and `spawned` false.
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Argv) == 0 {
		http.Error(w, "argv required", http.StatusBadRequest)
		return
	}

	spec := proc.Spec{
		Argv:  body.Argv,
		Dir:   body.Dir,
		Env:   ChildEnv(body.Env),
		Name:  body.Name,
		Port:  body.Port,
		As:    body.As,
		Stdin: body.Stdin,
	}

	var cmd *proc.Command
	var err error
	spawned := true

	if body.Key == "" {
		cmd, err = s.tbl.Spawn(spec)
	} else {
		cmd, spawned, err = s.tbl.SpawnKeyed(spec, body.Key)
		if errors.Is(err, proc.ErrBadKey) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if body.Wait <= 0 {
		writeJSON(w, map[string]any{"id": cmd.ID, "spawned": spawned})
		return
	}

	// Timed out is not failed, and the answer says which: `exited` false means
	// the command is still going and the caller still holds its id.
	waited := cmd.AwaitExit(time.Duration(body.Wait)*time.Second, r.Context().Done())

	output, err := readAll(cmd)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	code, exited := cmd.ExitCode()

	writeJSON(w, map[string]any{
		"id": cmd.ID, "spawned": spawned, "exited": exited && waited, "exit_code": code,
		"coded": cmd.Coded(), "output": string(output),
	})
}

// Everything it said, for a caller that asked to wait. Bounded by the log file
// itself rather than by a cap: a command that printed a gigabyte is a caller
// that should have streamed.
func readAll(cmd *proc.Command) ([]byte, error) {
	rc, err := cmd.ReadFrom(0)
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	return io.ReadAll(rc)
}

// Inherited by name, never wholesale: `os.Environ()` would put this process's NVOI_TOKEN in every
// bash call, where `env` prints it. A list rather than a deletion, so the next one is safe too.
var inherited = []string{
	"PATH",
	"HOME",
	"USER",
	"LANG",
	"TERM",
	"NVOI_WORKSPACE",
	"NVOI_LOG_DIR",
	"CLAUDE_CONFIG_DIR",
}

// ChildEnv is the environment a process boxd starts gets: the inherited names, then extra.
func ChildEnv(extra map[string]string) []string {
	env := make([]string, 0, len(inherited)+len(extra))

	for _, name := range inherited {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}

	// The caller's own, last, so it can override an inherited one on purpose.
	for k, v := range extra {
		env = append(env, k+"="+v)
	}

	return env
}

// The content-type is explicit because net/http otherwise labels json as "text/plain", and a
// client that declines to decode it takes the whole document for the scalar it asked for — a
// command id, in the one place that matters.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("content-type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// logs returns what exists from the offset; with `follow` it keeps returning what
// arrives until `timeout` seconds have passed. Bounded, because a worker parked in a
// longer read misses its heartbeat — and a reconnect loses nothing, the log being
// append-only. A capped default, so a caller that forgets is held for thirty seconds.
func (s *server) logs(w http.ResponseWriter, r *http.Request) {
	cmd, ok := s.tbl.Get(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	// Absent means from the start. Present and unreadable is refused rather than
	// read as zero: this log is append-only so an offset is a resume point, and
	// silently answering from 0 replays a whole turn the caller already has.
	skip := int64(0)
	if raw := r.URL.Query().Get("skip"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			http.Error(w, "skip must be a byte offset", http.StatusBadRequest)
			return
		}
		skip = parsed
	}

	rc, err := cmd.ReadFrom(skip)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rc.Close()

	w.Header().Set("content-type", "application/x-ndjson")
	// The proxy in front of this must not buffer a stream into a response.
	w.Header().Set("x-accel-buffering", "no")

	if r.URL.Query().Get("follow") == "" {
		io.Copy(w, rc)
		return
	}

	follow(w, r, cmd, rc, followFor(r))
}

const (
	// How long a follow sleeps when the file has nothing new. Short enough that
	// a token delta is not visibly late.
	followIdle = 50 * time.Millisecond
	// The longest any follow may hold, whatever it asked for.
	followCeiling = 60 * time.Second
	followDefault = 30 * time.Second
)

func followFor(r *http.Request) time.Duration {
	seconds, err := strconv.Atoi(r.URL.Query().Get("timeout"))
	if err != nil || seconds <= 0 {
		return followDefault
	}

	held := time.Duration(seconds) * time.Second
	if held > followCeiling {
		return followCeiling
	}

	return held
}

// An exit is not the end of the bytes: the reaper marks a command exited while the last write may
// still be in flight, so a follow that returned the instant it saw `exited` would cut off the final
// line — often the one carrying the result. It drains once more, then returns.
func follow(w http.ResponseWriter, r *http.Request, cmd *proc.Command, rc io.Reader, held time.Duration) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		io.Copy(w, rc)
		return
	}

	deadline := time.Now().Add(held)
	draining := false

	for {
		written, err := io.Copy(w, rc)
		if err != nil {
			return
		}
		if written > 0 {
			flusher.Flush()
		}

		if draining {
			return
		}

		if _, exited := cmd.ExitCode(); exited {
			draining = true
			continue
		}

		if time.Now().After(deadline) {
			return
		}

		select {
		case <-r.Context().Done():
			return
		case <-time.After(followIdle):
		}
	}
}

func (s *server) closeStdin(w http.ResponseWriter, r *http.Request) {
	cmd, ok := s.tbl.Get(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := cmd.CloseStdin(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) stdin(w http.ResponseWriter, r *http.Request) {
	cmd, ok := s.tbl.Get(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	// Read whole or not at all. The pipe on the other side is line-framed, so a
	// body cut short by a dropped connection would hand the cli half a json
	// document and leave the turn waiting on a line that can never complete.
	line, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := cmd.Write(string(line)); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Which live command has this name, or — with no name — every one of them. The box and not the
// rows: a row saying `running` is a claim with no sweeper behind it, so this is the table that
// spawned them and reaps them. 404 for a name nobody answers to; the list is never a 404.
func (s *server) named(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")

	if name == "" {
		writeJSON(w, map[string]any{"commands": live(s.tbl.Live())})
		return
	}

	cmd, ok := s.tbl.Named(name)
	if !ok {
		http.NotFound(w, r)
		return
	}

	writeJSON(w, map[string]any{"id": cmd.ID, "name": cmd.Name, "pid": cmd.Pid()})
}

// What a person looking at a list needs: what it is called, whether the kernel
// still has it, and — for one that declared a port — what that port is doing.
func live(cmds []*proc.Command) []map[string]any {
	out := make([]map[string]any, 0, len(cmds))

	for _, cmd := range cmds {
		state, _ := cmd.Readiness()

		out = append(out, map[string]any{
			"id": cmd.ID, "name": cmd.Name, "pid": cmd.Pid(),
			"port": cmd.Port(), "ready": wordFor(state),
		})
	}

	return out
}

func (s *server) status(w http.ResponseWriter, r *http.Request) {
	cmd, ok := s.tbl.Get(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	code, exited := cmd.ExitCode()

	// Every command in the table has a log — one was created to spawn it, or it
	// was restored from one. A stat that fails is the disk, and answering 0 hands
	// a follower an offset that replays the whole command.
	fi, err := os.Stat(cmd.LogPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	size := fi.Size()
	// `coded` says whether `exit_code` means anything: a command restored from disk after this
	// container restarted has ended and may have no status file beside its log, and reporting 0
	// there would be a lie about something quite possibly killed.
	writeJSON(w, map[string]any{
		"exited": exited, "exit_code": code, "coded": cmd.Coded(), "log_size": size,
	})
}

// What a terminal does, over http: `DELETE` is SIGKILL, and this is the interrupt a person means
// by `^C` — the only way a command that asked a daemon for something takes it down too. The name
// and not the number, so no caller has to know the platform's.
var signals = map[string]syscall.Signal{
	"INT":  syscall.SIGINT,
	"TERM": syscall.SIGTERM,
	"HUP":  syscall.SIGHUP,
	"QUIT": syscall.SIGQUIT,
	"KILL": syscall.SIGKILL,
}

func (s *server) signal(w http.ResponseWriter, r *http.Request) {
	cmd, ok := s.tbl.Get(r.PathValue("id"))
	if !ok {
		// Already gone is success — see proc.Command.Signal.
		w.WriteHeader(http.StatusNoContent)
		return
	}

	var body struct {
		Signal string `json:"signal"`
	}

	// No body at all means the interrupt a person means by `^C`. A body that will
	// not parse means something else entirely, and defaulting it sends INT to a
	// caller that asked for KILL — which the command then survives.
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	sig, known := signals[strings.ToUpper(orDefault(body.Signal, "INT"))]
	if !known {
		http.Error(w, "unknown signal", http.StatusBadRequest)
		return
	}

	if err := cmd.Signal(sig); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *server) kill(w http.ResponseWriter, r *http.Request) {
	cmd, ok := s.tbl.Get(r.PathValue("id"))
	if !ok {
		// Already gone is success — see proc.Command.Kill.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := cmd.Stop(proc.StopWithin); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Artifacts are read off disk rather than passed through a tool call: a 300 KB
// build as a tool argument is roughly 200k tokens of round trip.
func (s *server) file(w http.ResponseWriter, r *http.Request) {
	f, err := os.Open(r.URL.Query().Get("path"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	// A directory opens perfectly well and copies as nothing, so without this a
	// caller reading a path one segment short is handed 200 and an empty body —
	// an archive that is not there, indistinguishable from one that is empty.
	info, err := f.Stat()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !info.Mode().IsRegular() {
		http.Error(w, "not a file", http.StatusBadRequest)
		return
	}

	io.Copy(w, f)
}

// The other direction: how the control plane puts bytes into a box. It belongs to the agent rather
// than the substrate, so there is one file API and not two free to disagree about paths. Streamed,
// never buffered — a 40 MB restore costs a few kilobytes here.
func (s *server) putFile(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}

	// Parents first. Every real caller writes somewhere nested —
	// `.claude/projects/<hash>/`, `.wrangler/state/` — and failing on a missing
	// directory would make every caller pre-create its own tree.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Write then rename. A crash midway through a direct write leaves a truncated file that still
	// opens — a half-written transcript would be resumed as if whole, which fails much later and
	// looks like model misbehaviour rather than a bad write.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".nvoi-put-*")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer os.Remove(tmp.Name())

	if _, err := io.Copy(tmp, r.Body); err != nil {
		tmp.Close()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := tmp.Close(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Read by the vendor cli as `nvoi`, written by root alone. `CreateTemp` is
	// 0600, which is unreadable to the model.
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := os.Rename(tmp.Name(), path); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
