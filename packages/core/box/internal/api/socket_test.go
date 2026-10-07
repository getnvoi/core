package api

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// A real server, a real socket, a real `WaitPast` wakeup — the point of the
// route is that none of those involve a clock.
func dial(t *testing.T, from string) (*websocket.Conn, string, func()) {
	t.Helper()

	handler := serve(t)
	server := httptest.NewServer(handler)

	spawned := do(t, handler, "POST", "/commands",
		`{"argv":["sh","-c","cat"],"name":"turn","stdin":true}`)
	if spawned.Code != 200 {
		t.Fatalf("spawn answered %d: %s", spawned.Code, spawned.Body.String())
	}

	var command struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(spawned.Body.Bytes(), &command); err != nil {
		t.Fatalf("spawn said %q", spawned.Body.String())
	}

	url := "ws" + strings.TrimPrefix(server.URL, "http") +
		"/commands/" + command.ID + "/socket?from=" + from

	conn, _, err := websocket.DefaultDialer.Dial(url, map[string][]string{
		"X-Box-Token": {token},
	})
	if err != nil {
		server.Close()
		t.Fatalf("dial: %v", err)
	}

	// The command outlives the test otherwise, and `t.TempDir` cannot remove a directory whose log
	// is still open. Killed rather than waited on: `cat` ends when its input does.
	return conn, command.ID, func() {
		conn.Close()
		do(t, handler, "DELETE", "/commands/"+command.ID, "")
		waitExited(t, handler, command.ID)
		server.Close()
	}
}

// Killing is not reaping. The process dies immediately and the reaper closes
// the log after, so a `t.TempDir` removed in between finds a file still open.
func waitExited(t *testing.T, h http.Handler, id string) {
	t.Helper()

	for range 300 {
		var status struct {
			Exited bool `json:"exited"`
		}

		json.Unmarshal(do(t, h, "GET", "/commands/"+id, "").Body.Bytes(), &status)
		if status.Exited {
			// And the status file is written after the exit is recorded, so a `t.TempDir` removed the
			// instant this returns races a file being created inside it.
			time.Sleep(50 * time.Millisecond)
			return
		}

		time.Sleep(10 * time.Millisecond)
	}
}

func readFrame(t *testing.T, conn *websocket.Conn, within time.Duration) frame {
	t.Helper()

	conn.SetReadDeadline(time.Now().Add(within))

	var got frame
	if err := conn.ReadJSON(&got); err != nil {
		t.Fatalf("no frame inside %s: %v", within, err)
	}

	return got
}

// No floor under a frame. The route this replaces sleeps 50ms between reads, so a line written
// now cannot arrive before then. 40ms is under that sleep and generous against a condition
// variable, so this fails the day somebody puts a poll back.
func TestAFrameArrivesWithoutAPoll(t *testing.T) {
	conn, _, done := dial(t, "0")
	defer done()

	if err := conn.WriteMessage(websocket.TextMessage, []byte("hello\n")); err != nil {
		t.Fatalf("stdin: %v", err)
	}

	got := readFrame(t, conn, 40*time.Millisecond)

	if got.Line != "hello" {
		t.Fatalf("line was %q", got.Line)
	}
	if got.Offset != int64(len("hello\n")) {
		t.Fatalf("offset was %d", got.Offset)
	}
}

// One message is one line, whatever the writes were: two lines in one write must not arrive as
// one message, and a line split across writes must not arrive as two.
func TestWritesAreRegroupedIntoLines(t *testing.T) {
	conn, _, done := dial(t, "0")
	defer done()

	conn.WriteMessage(websocket.TextMessage, []byte("one\ntwo\n"))

	if first := readFrame(t, conn, time.Second); first.Line != "one" {
		t.Fatalf("first line was %q", first.Line)
	}
	if second := readFrame(t, conn, time.Second); second.Line != "two" {
		t.Fatalf("second line was %q", second.Line)
	}
}

// A fragment is not a frame. `cat` echoes whatever it is given, so a write with no newline is
// not a message yet — the newline that follows makes one frame of both halves.
func TestAnUnterminatedLineIsNotSentYet(t *testing.T) {
	conn, _, done := dial(t, "0")
	defer done()

	conn.WriteMessage(websocket.TextMessage, []byte("half"))
	conn.WriteMessage(websocket.TextMessage, []byte(" a line\n"))

	if whole := readFrame(t, conn, time.Second); whole.Line != "half a line" {
		t.Fatalf("the rejoined line was %q", whole.Line)
	}
}

// Joining late reads from the offset, and reads nothing twice. This is the
// re-attach the control plane does after its own process died.
func TestAttachingFromAnOffsetSkipsWhatWasRead(t *testing.T) {
	handler := serve(t)
	server := httptest.NewServer(handler)
	defer server.Close()

	spawned := do(t, handler, "POST", "/commands", `{"argv":["sh","-c","cat"],"name":"turn","stdin":true}`)

	var command struct {
		ID string `json:"id"`
	}
	json.Unmarshal(spawned.Body.Bytes(), &command)

	do(t, handler, "POST", "/commands/"+command.ID+"/stdin", "first\n")
	do(t, handler, "POST", "/commands/"+command.ID+"/stdin", "second\n")

	url := "ws" + strings.TrimPrefix(server.URL, "http") +
		"/commands/" + command.ID + "/socket?from=" + "6"

	conn, _, err := websocket.DefaultDialer.Dial(url, map[string][]string{
		"X-Box-Token": {token},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	got := readFrame(t, conn, time.Second)

	if got.Line != "second" {
		t.Fatalf("attaching at 6 replayed %q", got.Line)
	}
}

// The exit is the close frame. A terminal state that arrives as data is a state
// two readers can disagree about.
func TestTheCloseFrameCarriesTheExit(t *testing.T) {
	handler := serve(t)
	server := httptest.NewServer(handler)
	defer server.Close()

	spawned := do(t, handler, "POST", "/commands", `{"argv":["sh","-c","echo bye; exit 3"],"name":"turn"}`)

	var command struct {
		ID string `json:"id"`
	}
	json.Unmarshal(spawned.Body.Bytes(), &command)

	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/commands/" + command.ID + "/socket?from=0"

	conn, _, err := websocket.DefaultDialer.Dial(url, map[string][]string{
		"X-Box-Token": {token},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if line := readFrame(t, conn, 2*time.Second); line.Line != "bye" {
		t.Fatalf("the last line was %q", line.Line)
	}

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = conn.ReadMessage()

	closed, ok := err.(*websocket.CloseError)
	if !ok {
		t.Fatalf("the socket ended with %v, not a close frame", err)
	}
	if closed.Code != closeExit {
		t.Fatalf("close code was %d", closed.Code)
	}

	var said exit
	if err := json.Unmarshal([]byte(closed.Text), &said); err != nil {
		t.Fatalf("the close reason was %q", closed.Text)
	}
	if !said.Exited || said.Code != 3 {
		t.Fatalf("the close said %+v", said)
	}
	// A turn is ended by this frame and by nothing else, so it carries the same
	// question `GET /commands/{id}` answers: does that code mean anything. Absent,
	// an ending nobody observed closes the turn as a clean zero.
	if !said.Coded {
		t.Fatalf("the close did not say the code was observed: %+v", said)
	}
}

// A binary message closes stdin and nothing else. `cat` ends when its input does, so the close
// frame arriving is the proof stdin was closed — and the socket had to still be readable.
func TestABinaryMessageClosesStdin(t *testing.T) {
	conn, _, done := dial(t, "0")
	defer done()

	conn.WriteMessage(websocket.TextMessage, []byte("last\n"))

	if line := readFrame(t, conn, time.Second); line.Line != "last" {
		t.Fatalf("the line was %q", line.Line)
	}

	conn.WriteMessage(websocket.BinaryMessage, nil)

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err := conn.ReadMessage()

	if _, ok := err.(*websocket.CloseError); !ok {
		t.Fatalf("closing stdin did not end the command: %v", err)
	}
}

// A reader letting go is not a command ending.
//
// boxd owns the pipes and outlives every connection — that is its whole reason
// to exist. A turn that parks on an approval stops reading and answers later
// over `POST /commands/:id/stdin`, so a disconnect that closed stdin made the
// runner exit on EOF and the approval had nothing left to resume into.
//
// Only the binary sentinel closes stdin. Dropping the socket must not.
func TestDroppingTheSocketLeavesTheCommandRunning(t *testing.T) {
	handler := serve(t)
	server := httptest.NewServer(handler)
	defer server.Close()

	spawned := do(t, handler, "POST", "/commands", `{"argv":["sh","-c","cat"],"name":"turn","stdin":true}`)

	var command struct {
		ID string `json:"id"`
	}
	json.Unmarshal(spawned.Body.Bytes(), &command)

	url := "ws" + strings.TrimPrefix(server.URL, "http") +
		"/commands/" + command.ID + "/socket?from=0"

	conn, _, err := websocket.DefaultDialer.Dial(url, map[string][]string{
		"X-Box-Token": {token},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	conn.WriteMessage(websocket.TextMessage, []byte("before\n"))

	if line := readFrame(t, conn, time.Second); line.Line != "before" {
		t.Fatalf("the line was %q", line.Line)
	}

	conn.Close()

	// Long enough for an EOF to have taken it down if one were sent.
	time.Sleep(300 * time.Millisecond)

	var state struct {
		Exited bool `json:"exited"`
	}
	json.Unmarshal(do(t, handler, "GET", "/commands/"+command.ID, "").Body.Bytes(), &state)

	if state.Exited {
		t.Fatal("the command exited when its reader let go")
	}

	// And it is still writable, which is the whole point: an approval is
	// answered by whatever process a person's click lands in.
	if code := do(t, handler, "POST", "/commands/"+command.ID+"/stdin", `{"data":"after\n"}`).Code; code != 204 {
		t.Fatalf("stdin answered %d, want 204", code)
	}

	do(t, handler, "DELETE", "/commands/"+command.ID, "")
	waitExited(t, handler, command.ID)
}

// The handshake is an ordinary request, so the token middleware covers it and
// nothing about the upgrade is exempt.
func TestTheSocketIsAuthenticated(t *testing.T) {
	handler := serve(t)
	server := httptest.NewServer(handler)
	defer server.Close()

	spawned := do(t, handler, "POST", "/commands", `{"argv":["sh","-c","cat"],"name":"turn","stdin":true}`)

	var command struct {
		ID string `json:"id"`
	}
	json.Unmarshal(spawned.Body.Bytes(), &command)

	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/commands/" + command.ID + "/socket"

	if _, _, err := websocket.DefaultDialer.Dial(url, nil); err == nil {
		t.Fatal("an unauthenticated dial was upgraded")
	}
}

// Readiness comes down the socket the log already uses: a second endpoint would be a second
// flavour of receiving, and `GET /events` was exactly that.
func TestTheSocketSaysWhenThePortAnswers(t *testing.T) {
	port := freePort(t)

	handler := serve(t)
	server := httptest.NewServer(handler)
	defer server.Close()

	// `-k`: the readiness probe is a connection, and a listener that exits on
	// its first one closes the socket with the exit before the ready frame.
	body := fmt.Sprintf(`{"argv":["sh","-c","sleep 0.5; nc -lk %d"],"name":"app","port":%d}`, port, port)
	id := spawnedID(t, do(t, handler, "POST", "/commands", body))

	defer func() {
		do(t, handler, "DELETE", "/commands/"+id, "")
		waitExited(t, handler, id)
	}()

	conn := open(t, server, id, "0")
	defer conn.Close()

	if got := readFrame(t, conn, 20*time.Second); got.Kind != "ready" {
		t.Fatalf("frame was %+v, want a ready", got)
	}
}

// A line says it is one. The control plane branches on `kind`, and everything
// it was reading before this existed was a line.
func TestALineSaysItIsALine(t *testing.T) {
	conn, _, done := dial(t, "0")
	defer done()

	conn.WriteMessage(websocket.TextMessage, []byte("hello\n"))

	if got := readFrame(t, conn, time.Second); got.Kind != "line" || got.Line != "hello" {
		t.Fatalf("frame was %+v, want a line saying hello", got)
	}
}

func spawnedID(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()

	if response.Code != 200 {
		t.Fatalf("spawn answered %d: %s", response.Code, response.Body.String())
	}

	var command struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &command); err != nil {
		t.Fatalf("spawn said %q", response.Body.String())
	}

	return command.ID
}

func open(t *testing.T, server *httptest.Server, id, from string) *websocket.Conn {
	t.Helper()

	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/commands/" + id + "/socket?from=" + from

	conn, _, err := websocket.DefaultDialer.Dial(url, map[string][]string{
		"X-Box-Token": {token},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	return conn
}

func freePort(t *testing.T) int {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	return l.Addr().(*net.TCPAddr).Port
}
