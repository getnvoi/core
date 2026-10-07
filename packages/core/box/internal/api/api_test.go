package api

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
	"time"

	"github.com/getnvoi/nvoi/packages/core/box/internal/proc"
)

const token = "t0ken"

func serve(t *testing.T) http.Handler {
	t.Helper()

	// The reaper writes a status file beside the log after a command exits, and
	// `t.TempDir()` removes that directory when the test ends. Cleanups run
	// last-registered-first, so this one finishes before the removal.
	tbl := proc.New(t.TempDir())
	t.Cleanup(tbl.Close)

	return New(tbl, token)
}

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("X-Box-Token", token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// Every JSON route must say it is JSON. net/http sniffs an unlabelled body and calls JSON
// "text/plain", so a client declined to decode it and used the whole document as a command id.
func TestJSONRoutesDeclareTheirContentType(t *testing.T) {
	h := serve(t)

	spawn := do(t, h, "POST", "/commands", `{"argv":["true"],"dir":"/tmp"}`)
	if got := spawn.Header().Get("content-type"); got != "application/json" {
		t.Errorf("POST /commands content-type = %q, want application/json", got)
	}

	var spawned struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(spawn.Body.Bytes(), &spawned); err != nil || spawned.ID == "" {
		t.Fatalf("POST /commands body = %q, want a decodable {id}", spawn.Body.String())
	}

	health := do(t, h, "GET", "/health", "")
	if got := health.Header().Get("content-type"); got != "application/json" {
		t.Errorf("GET /health content-type = %q, want application/json", got)
	}

	status := do(t, h, "GET", "/commands/"+spawned.ID, "")
	if got := status.Header().Get("content-type"); got != "application/json" {
		t.Errorf("GET /commands/{id} content-type = %q, want application/json", got)
	}
}

// The log stream is not json — it is a byte range of a file that happens to contain NDJSON,
// and mislabelling it would invite a client to decode what this agent never interprets.
func TestLogsAreNDJSON(t *testing.T) {
	h := serve(t)

	spawn := do(t, h, "POST", "/commands", `{"argv":["true"],"dir":"/tmp"}`)
	var spawned struct {
		ID string `json:"id"`
	}
	json.Unmarshal(spawn.Body.Bytes(), &spawned)

	logs := do(t, h, "GET", "/commands/"+spawned.ID+"/logs?skip=0", "")
	if got := logs.Header().Get("content-type"); got != "application/x-ndjson" {
		t.Errorf("GET logs content-type = %q, want application/x-ndjson", got)
	}
}

// /health is the readiness probe, and readiness is asked before a token has been agreed.
// Requiring auth would make a starting sandbox indistinguishable from a misconfigured one.
func TestHealthNeedsNoToken(t *testing.T) {
	h := serve(t)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/health", nil))

	if w.Code != http.StatusOK {
		t.Errorf("unauthenticated GET /health = %d, want 200", w.Code)
	}
}

// Everything else must not be reachable without the token: the agent's whole
// authority is "run anything, as root, in this workspace".
func TestOtherRoutesRequireTheToken(t *testing.T) {
	h := serve(t)

	for _, route := range []struct{ method, path string }{
		{"POST", "/commands"},
		{"GET", "/commands/anything"},
		{"GET", "/commands/anything/logs"},
		{"POST", "/commands/anything/stdin"},
		{"DELETE", "/commands/anything"},
		{"GET", "/file?path=/etc/passwd"},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(route.method, route.path, nil))

		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a token = %d, want 401", route.method, route.path, w.Code)
		}
	}
}

// PUT /file is how Phoenix puts bytes INTO a box — seed files, `.dev.vars`,
// skills, and the Agent SDK transcript restored before a turn resumes.
func TestPutFileWritesThroughNestedDirectories(t *testing.T) {
	h := serve(t)
	dir := t.TempDir()
	// Nested on purpose: every real caller writes somewhere like `.claude/projects/<hash>/`, and
	// requiring callers to pre-create the tree would move that chore into each of them.
	path := filepath.Join(dir, "a", "b", "c.txt")

	if w := do(t, h, "PUT", "/file?path="+url.QueryEscape(path), "hello"); w.Code != http.StatusNoContent {
		t.Fatalf("put: got %d, want 204: %s", w.Code, w.Body)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("got %q, want %q", got, "hello")
	}
}

// boxd runs as root and what it writes is read by the vendor cli as `nvoi`: a
// skill, an artifact, a document to render. So a file is 0644 and its parents
// 0755 — readable by everyone in the box, writable by root alone. `CreateTemp`'s
// 0600 left every staged skill unreadable to the model.
func TestPutFileIsReadableByEveryoneAndWritableByRootOnly(t *testing.T) {
	h := serve(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "skills", "probe", "SKILL.md")

	if w := do(t, h, "PUT", "/file?path="+url.QueryEscape(path), "# probe"); w.Code != http.StatusNoContent {
		t.Fatalf("put: got %d, want 204: %s", w.Code, w.Body)
	}

	file, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := file.Mode().Perm(); mode != 0o644 {
		t.Fatalf("file mode %o, want 644", mode)
	}

	parent, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if mode := parent.Mode().Perm(); mode != 0o755 {
		t.Fatalf("dir mode %o, want 755", mode)
	}
}

func TestPutFileRoundTripsThroughGetFile(t *testing.T) {
	h := serve(t)
	path := filepath.Join(t.TempDir(), "transcript.jsonl")

	do(t, h, "PUT", "/file?path="+url.QueryEscape(path), `{"type":"user"}`)
	w := do(t, h, "GET", "/file?path="+url.QueryEscape(path), "")

	if w.Body.String() != `{"type":"user"}` {
		t.Fatalf("round trip lost content: %q", w.Body.String())
	}
}

func TestPutFileOverwritesRatherThanAppending(t *testing.T) {
	h := serve(t)
	path := filepath.Join(t.TempDir(), "f")

	do(t, h, "PUT", "/file?path="+url.QueryEscape(path), "first-and-longer")
	do(t, h, "PUT", "/file?path="+url.QueryEscape(path), "second")

	got, _ := os.ReadFile(path)
	// A rename replaces; an append or a short write would leave the tail of the first value
	// behind, and a transcript half of one turn and half of another fails much later.
	if string(got) != "second" {
		t.Fatalf("got %q, want %q", got, "second")
	}
}

func TestPutFileRequiresAPath(t *testing.T) {
	if w := do(t, serve(t), "PUT", "/file", "x"); w.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", w.Code)
	}
}

func TestPutFileLeavesNoTemporaryFileBehind(t *testing.T) {
	h := serve(t)
	dir := t.TempDir()

	do(t, h, "PUT", "/file?path="+url.QueryEscape(filepath.Join(dir, "f")), "x")

	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		// The write-then-rename dance must not litter: a box that gains a
		// `.nvoi-put-*` per turn eventually fills its disk.
		t.Fatalf("expected only the written file, got %d entries", len(entries))
	}
}

// A spawned command must not inherit this process's whole environment: the agent holds NVOI_TOKEN,
// and `os.Environ()` would put it in every bash call the model makes, where `env` prints it.
func TestChildEnvDoesNotLeakTheAgentsOwnSecrets(t *testing.T) {
	t.Setenv("NVOI_TOKEN", "the-box-credential")
	t.Setenv("CLOUDFLARE_API_TOKEN", "should-never-be-here")
	t.Setenv("PATH", "/usr/bin")

	for _, entry := range ChildEnv(nil) {
		if strings.HasPrefix(entry, "NVOI_TOKEN=") || strings.HasPrefix(entry, "CLOUDFLARE_API_TOKEN=") {
			t.Fatalf("a secret reached a child process: %q", entry)
		}
	}
}

func TestChildEnvKeepsWhatTheDriverNeeds(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("HOME", "/home/agent")
	// CLAUDE_CONFIG_DIR is what keeps the SDK transcript OUT of the user's
	// checkout. A child that does not see it writes `.claude/` into /workspace.
	t.Setenv("CLAUDE_CONFIG_DIR", "/home/agent/.claude")

	got := strings.Join(ChildEnv(nil), "\n")

	for _, want := range []string{"PATH=/usr/bin", "HOME=/home/agent", "CLAUDE_CONFIG_DIR=/home/agent/.claude"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q from:\n%s", want, got)
		}
	}
}

func TestChildEnvLetsTheCallerOverride(t *testing.T) {
	t.Setenv("HOME", "/home/agent")

	env := ChildEnv(map[string]string{"HOME": "/tmp/elsewhere", "ANTHROPIC_API_KEY": "sk-x"})

	// The caller's entries come last, so exec resolves them over the inherited
	// ones — that is how a turn passes the model credential in.
	var home string
	for _, entry := range env {
		if strings.HasPrefix(entry, "HOME=") {
			home = entry
		}
	}
	if home != "HOME=/tmp/elsewhere" {
		t.Fatalf("caller did not win: %q", home)
	}
	if !strings.Contains(strings.Join(env, "\n"), "ANTHROPIC_API_KEY=sk-x") {
		t.Fatal("the caller's own variable did not reach the child")
	}
}

func TestChildEnvCarriesNothingItWasNotAskedFor(t *testing.T) {
	// The list is what a spawned command may see, and every name on it is one
	// somebody chose. A variable set on the box that nobody put here does not
	// reach a child — which is the whole reason `os.Environ()` is not used.
	t.Setenv("NVOI_LOG_DIR", "/var/lib/boxd/logs")
	t.Setenv("SOME_FRAMEWORK_SETTING", "wss")

	got := strings.Join(ChildEnv(nil), "\n")

	if !strings.Contains(got, "NVOI_LOG_DIR=/var/lib/boxd/logs") {
		t.Fatalf("an inherited name did not reach the child:\n%s", got)
	}
	if strings.Contains(got, "SOME_FRAMEWORK_SETTING") {
		t.Fatalf("a name nobody listed reached the child:\n%s", got)
	}
}

// GET /commands — what is running, asked of the table that spawned it.
func TestTheListIsWhatTheBoxIsRunning(t *testing.T) {
	h := serve(t)

	spawned := do(t, h, "POST", "/commands", `{"argv":["sleep","30"],"name":"app"}`)
	if spawned.Code != 200 {
		t.Fatalf("spawn answered %d", spawned.Code)
	}

	var command struct {
		ID string `json:"id"`
	}
	json.Unmarshal(spawned.Body.Bytes(), &command)

	defer do(t, h, "DELETE", "/commands/"+command.ID, "")

	var listed struct {
		Commands []struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Pid   int    `json:"pid"`
			Port  int    `json:"port"`
			Ready any    `json:"ready"`
		} `json:"commands"`
	}

	answer := do(t, h, "GET", "/commands", "")
	if err := json.Unmarshal(answer.Body.Bytes(), &listed); err != nil {
		t.Fatalf("list said %q", answer.Body.String())
	}

	if len(listed.Commands) != 1 {
		t.Fatalf("listed %d commands, want 1: %s", len(listed.Commands), answer.Body.String())
	}

	got := listed.Commands[0]

	if got.ID != command.ID || got.Name != "app" || got.Pid == 0 {
		t.Errorf("listed %+v, want the spawned command with a pid", got)
	}

	// A command that declared no port has not failed to answer on one.
	if got.Ready != nil {
		t.Errorf("ready = %v for a command with no port, want nothing", got.Ready)
	}
}

// And a command that has ended is history. The rows keep that; this is about
// what is running now, which is the question a row cannot answer.
func TestTheListLeavesOutWhatHasEnded(t *testing.T) {
	h := serve(t)

	spawned := do(t, h, "POST", "/commands", `{"argv":["true"],"name":"gone","wait":5}`)
	if spawned.Code != 200 {
		t.Fatalf("spawn answered %d", spawned.Code)
	}

	var listed struct {
		Commands []map[string]any `json:"commands"`
	}

	json.Unmarshal(do(t, h, "GET", "/commands", "").Body.Bytes(), &listed)

	if len(listed.Commands) != 0 {
		t.Fatalf("listed %v, want nothing", listed.Commands)
	}
}

// The named lookup is the same route and still answers 404 for a name nobody has: a stop that
// matched a finished command would report success over a process still running.
func TestANameNobodyAnswersToIsStillA404(t *testing.T) {
	h := serve(t)

	if code := do(t, h, "GET", "/commands?name=nobody", "").Code; code != 404 {
		t.Errorf("status = %d, want 404", code)
	}
}

// What Ctrl-C is, over http. A command that asked a daemon for something takes it down on an
// interrupt and leaves it running on a kill, because SIGKILL cannot be answered.
func TestAnInterruptLetsACommandStopItself(t *testing.T) {
	h := serve(t)

	// A shell that traps the interrupt and says so before leaving is the smallest thing that can
	// tell the two apart. It announces itself first, because a signal arriving before the trap is
	// installed takes the default action — the very thing this distinguishes.
	spawned := do(t, h, "POST", "/commands",
		`{"argv":["sh","-c","trap 'echo stopped cleanly; exit 0' INT; echo trapped; while :; do sleep 0.2; done"],"name":"app"}`)

	var command struct {
		ID string `json:"id"`
	}
	json.Unmarshal(spawned.Body.Bytes(), &command)

	waitFor(t, h, command.ID, "trapped")

	if code := do(t, h, "POST", "/commands/"+command.ID+"/signal", `{"signal":"INT"}`).Code; code != 204 {
		t.Fatalf("signal answered %d", code)
	}

	// Its own shutdown, which takes a moment. The trap runs after the signal is
	// delivered, and the reaper records the ending after that.
	waitFor(t, h, command.ID, "stopped cleanly")
	waitExited(t, h, command.ID)

	var status struct {
		Exited   bool `json:"exited"`
		ExitCode int  `json:"exit_code"`
	}
	json.Unmarshal(do(t, h, "GET", "/commands/"+command.ID, "").Body.Bytes(), &status)

	// Zero, not a signal. A command that answered the interrupt ended on its own
	// terms; one that was killed reports -1 and leaves whatever it started.
	if !status.Exited || status.ExitCode != 0 {
		t.Fatalf("status = %+v, want a clean exit of its own", status)
	}

}

func TestAnUnknownSignalIsRefused(t *testing.T) {
	h := serve(t)

	spawned := do(t, h, "POST", "/commands", `{"argv":["sleep","5"],"name":"app"}`)

	var command struct {
		ID string `json:"id"`
	}
	json.Unmarshal(spawned.Body.Bytes(), &command)

	defer func() {
		do(t, h, "DELETE", "/commands/"+command.ID, "")
		waitExited(t, h, command.ID)
	}()

	if code := do(t, h, "POST", "/commands/"+command.ID+"/signal", `{"signal":"NOPE"}`).Code; code != 400 {
		t.Errorf("status = %d, want 400", code)
	}
}

// A command is only ready for a signal once it says so — spawning returns as
// soon as the process exists, which is before its shell has read a line.
func waitFor(t *testing.T, h http.Handler, id, said string) {
	t.Helper()

	for range 100 {
		if strings.Contains(do(t, h, "GET", "/commands/"+id+"/logs", "").Body.String(), said) {
			return
		}

		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("%s never said %q", id, said)
}

func TestOnlyOurOwnHeaderAuthenticates(t *testing.T) {
	// `authorization` on a request that reached this box carries the SUBSTRATE's
	// preview token. Accepting it puts somebody else's credential up against the
	// only lock a sandbox has, and no caller of ours ever sends one.
	h := serve(t)

	r := httptest.NewRequest("GET", "/commands", nil)
	r.Header.Set("authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("bearer token accepted: %d", w.Code)
	}

	if got := do(t, h, "GET", "/commands", "").Code; got != http.StatusOK {
		t.Fatalf("X-Box-Token refused: %d", got)
	}
}

func TestAStdinBodyIsWrittenWholeOrNotAtAll(t *testing.T) {
	// The pipe on the other side is line-framed. Half a json document leaves the
	// turn waiting on a line that can never complete.
	h := serve(t)

	id := spawn(t, h, `{"argv":["sh","-c","cat > /dev/null"]}`)
	defer do(t, h, "DELETE", "/commands/"+id, "")

	r := httptest.NewRequest("POST", "/commands/"+id+"/stdin", failingBody{})
	r.Header.Set("X-Box-Token", token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("a truncated body answered %d, want 400", w.Code)
	}
}

type failingBody struct{}

func (failingBody) Read(p []byte) (int, error) {
	n := copy(p, []byte(`{"half":`))
	return n, io.ErrUnexpectedEOF
}

func TestAMalformedSkipIsRefusedRatherThanReadAsZero(t *testing.T) {
	// The log is append-only, so an offset is a resume point. Answering from 0
	// replays a turn the caller already has.
	h := serve(t)

	id := spawn(t, h, `{"argv":["sh","-c","echo hi"]}`)

	if got := do(t, h, "GET", "/commands/"+id+"/logs?skip=banana", "").Code; got != http.StatusBadRequest {
		t.Fatalf("skip=banana answered %d, want 400", got)
	}
	if got := do(t, h, "GET", "/commands/"+id+"/logs?skip=-4", "").Code; got != http.StatusBadRequest {
		t.Fatalf("skip=-4 answered %d, want 400", got)
	}
	if got := do(t, h, "GET", "/commands/"+id+"/logs", "").Code; got != http.StatusOK {
		t.Fatalf("no skip at all answered %d, want 200", got)
	}
}

// The id of a freshly spawned command, which every route below is addressed to.
func spawn(t *testing.T, h http.Handler, body string) string {
	t.Helper()

	answered := do(t, h, "POST", "/commands", body)
	if answered.Code != http.StatusOK {
		t.Fatalf("spawn answered %d: %s", answered.Code, answered.Body.String())
	}

	var command struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(answered.Body.Bytes(), &command); err != nil {
		t.Fatal(err)
	}

	return command.ID
}

func TestADirectoryIsNotAnEmptyFile(t *testing.T) {
	// A path one segment short opens perfectly well and copies as nothing, so an
	// archive that is not there would answer exactly like one that is empty.
	h := serve(t)
	dir := t.TempDir()

	answered := do(t, h, "GET", "/file?path="+dir, "")
	if answered.Code != http.StatusBadRequest {
		t.Fatalf("a directory answered %d with %q, want 400", answered.Code, answered.Body.String())
	}
}

func TestASignalBodyThatWillNotParseIsRefused(t *testing.T) {
	// Defaulting it sends INT to a caller that asked for KILL, which the command
	// then survives — while the answer says the signal was delivered.
	h := serve(t)

	id := spawn(t, h, `{"argv":["sleep","30"]}`)
	defer do(t, h, "DELETE", "/commands/"+id, "")

	if got := do(t, h, "POST", "/commands/"+id+"/signal", `{"signal":`).Code; got != http.StatusBadRequest {
		t.Fatalf("malformed signal body answered %d, want 400", got)
	}

	// An absent body is still the interrupt a person means by `^C`.
	if got := do(t, h, "POST", "/commands/"+id+"/signal", "").Code; got != http.StatusNoContent {
		t.Fatalf("no body at all answered %d, want 204", got)
	}
}

func TestEverythingElseStillNeedsTheToken(t *testing.T) {
	h := serve(t)

	r := httptest.NewRequest("GET", "/commands", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != 401 {
		t.Fatalf("commands without a token: got %d, want 401", w.Code)
	}
}
