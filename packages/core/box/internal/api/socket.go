package api

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/getnvoi/nvoi/packages/core/box/internal/proc"
	"github.com/gorilla/websocket"
)

// One socket per command, duplex: frames out, stdin in, and the close frame carries the exit.
// `WaitPast` blocks on the condition `logFile.Write` broadcasts, so a byte written is a byte sent,
// where `follow` ends on a timer. One message is one line, carrying the offset after it, and a
// frame with no `kind` is a line.
type frame struct {
	Kind   string `json:"kind"`
	Line   string `json:"line"`
	Offset int64  `json:"offset"`
}

// What the port is doing, and nothing about the log — a line and an offset on
// this would be two fields a reader has to know to ignore.
type verdict struct {
	Kind string `json:"kind"`
}

const (
	kindLine    = "line"
	kindReady   = "ready"
	kindUnready = "unready"
)

// What an exit looks like: a close frame, not a message. A terminal state that arrives as data
// is a state two readers can disagree about, and the protocol already has a way to say this.
const closeExit = 4000

// The edge is the clock, not the command: the substrate hangs up an idle connection while a
// model thinking silently writes nothing. Ping on a ticker.
const (
	pingEvery   = 20 * time.Second
	pongWithin  = 60 * time.Second
	writeWithin = 10 * time.Second
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	// Same origin is not a question here. Every request already carries
	// `X-Box-Token` through `authenticated`, and a browser is never the client.
	CheckOrigin: func(*http.Request) bool { return true },
}

func (s *server) socket(w http.ResponseWriter, r *http.Request) {
	cmd, ok := s.tbl.Get(r.PathValue("id"))
	if !ok {
		http.Error(w, "no such command", http.StatusNotFound)
		return
	}

	from, err := strconv.ParseInt(orDefault(r.URL.Query().Get("from"), "0"), 10, 64)
	if err != nil || from < 0 {
		http.Error(w, "from must be a byte offset", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade has already answered; there is nothing left to say.
		return
	}

	newSocket(conn, cmd, from).serve()
}

// One writer, because the library allows one. The log pump, the ping ticker and the close all
// want to write, and two goroutines writing is a corrupted frame, not an error.
type socket struct {
	conn *websocket.Conn
	cmd  *proc.Command
	from int64

	// One sender, and it is the only thing that closes this: a second goroutine closing a channel
	// under a sender is a panic rather than an error.
	out chan any

	// Readiness takes its own path to the writer: `pump` closes `out` when the log is over, and a
	// second sender racing that close is a panic.
	verdicts chan string

	// The peer went away without a close frame — a killed control plane. The
	// writer has nobody left to write to.
	gone chan struct{}

	stop chan struct{}
}

func newSocket(conn *websocket.Conn, cmd *proc.Command, from int64) *socket {
	return &socket{
		conn:     conn,
		cmd:      cmd,
		from:     from,
		out:      make(chan any, 64),
		verdicts: make(chan string, 4),
		gone:     make(chan struct{}),
		stop:     make(chan struct{}),
	}
}

func (s *socket) serve() {
	defer s.conn.Close()
	defer close(s.stop)

	go s.readStdin()
	go s.watchReadiness()
	go s.pump()

	s.write()
}

// The reader is also the health check: a read deadline is the only thing that notices a peer gone
// without a close frame. Text is a line of stdin; binary closes it, since a sentinel could be data.
//
// A reader letting go is not a command ending. This agent owns the pipes and outlives every
// connection, so a disconnect closes nothing: a turn parked on an approval stops reading and is
// answered later over `POST /commands/{id}/stdin`. Closing stdin from here would end the runner on
// EOF and leave that approval nothing to resume into.
func (s *socket) readStdin() {
	defer close(s.gone)

	s.conn.SetReadDeadline(time.Now().Add(pongWithin))
	s.conn.SetPongHandler(func(string) error {
		return s.conn.SetReadDeadline(time.Now().Add(pongWithin))
	})

	for {
		kind, data, err := s.conn.ReadMessage()
		if err != nil {
			return
		}

		switch kind {
		case websocket.BinaryMessage:
			s.cmd.CloseStdin()
		case websocket.TextMessage:
			if err := s.cmd.Write(string(data)); err != nil {
				return
			}
		}
	}
}

// Follows the log, and an exit is not the end of the bytes: the reaper marks a command exited
// while the last write may still be in flight, and that last line is often the one carrying the
// result — so a false `WaitPast` drains once more before saying so.
func (s *socket) pump() {
	defer close(s.out)

	offset := s.from

	for {
		more := s.drain(&offset)

		select {
		case <-s.stop:
			return
		default:
		}

		if !more {
			s.send(exitOf(s.cmd))
			return
		}

		if !s.cmd.WaitPast(offset, s.stop) {
			s.drain(&offset)
			s.send(exitOf(s.cmd))
			return
		}
	}
}

// Reads whole lines from `offset`, moving it past each one sent. A trailing fragment is left
// where it is: an unterminated line is not a frame yet, and the next write completes it.
func (s *socket) drain(offset *int64) bool {
	rc, err := s.cmd.ReadFrom(*offset)
	if err != nil {
		return false
	}
	defer rc.Close()

	reader := bufio.NewReaderSize(rc, 64*1024)

	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			*offset += int64(len(line))

			if !s.send(frame{Kind: kindLine, Line: string(trimEOL(line)), Offset: *offset}) {
				return false
			}

			continue
		}

		if err == io.EOF {
			return true
		}
		if err != nil {
			return false
		}
	}
}

// What the port is doing, for as long as this socket is open. State rather than an event: the
// spawn happens before anybody can connect, so a reader that attached a second after the port
// answered is told on arrival. The watch ends with the socket.
func (s *socket) watchReadiness() {
	said := proc.Unknown

	for {
		state, changed := s.cmd.Readiness()

		if state != said && state != proc.Unknown {
			select {
			case s.verdicts <- wordFor(state).(string):
				said = state
			case <-s.stop:
				return
			case <-s.gone:
				return
			}
		}

		select {
		case <-changed:
		case <-s.stop:
			return
		case <-s.gone:
			return
		}
	}
}

// Nothing to say is its own answer: a command that declared no port has not failed to answer on
// one, and a list that said `unready` about `bundle install` would be inventing a claim.
func wordFor(state proc.Readiness) any {
	switch state {
	case proc.Ready:
		return kindReady
	case proc.Unready:
		return kindUnready
	default:
		return nil
	}
}

// Never blocks for ever: a writer that has gone home leaves this unread.
func (s *socket) send(message any) bool {
	select {
	case s.out <- message:
		return true
	case <-s.gone:
		return false
	case <-s.stop:
		return false
	}
}

// The one place that touches the connection for writing, ping included.
func (s *socket) write() {
	ticker := time.NewTicker(pingEvery)
	defer ticker.Stop()

	for {
		select {
		case <-s.gone:
			return

		case message, open := <-s.out:
			if !open {
				return
			}

			if code, done := message.(exit); done {
				s.close(code)
				return
			}

			s.conn.SetWriteDeadline(time.Now().Add(writeWithin))
			if err := s.conn.WriteJSON(message); err != nil {
				return
			}

		case kind := <-s.verdicts:
			s.conn.SetWriteDeadline(time.Now().Add(writeWithin))
			if err := s.conn.WriteJSON(verdict{Kind: kind}); err != nil {
				return
			}

		case <-ticker.C:
			s.conn.SetWriteDeadline(time.Now().Add(writeWithin))
			if err := s.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

type exit struct {
	Code   int  `json:"code"`
	Exited bool `json:"exited"`
	// Whether `code` means anything, the same question `GET /commands/{id}`
	// answers. A turn is ended by this frame and by nothing else, so a box that
	// knows the code was never observed and does not say so closes the turn as a
	// clean zero.
	Coded bool `json:"coded"`
}

func exitOf(cmd *proc.Command) exit {
	code, exited := cmd.ExitCode()

	return exit{Code: code, Exited: exited, Coded: cmd.Coded()}
}

// The reason is json so a reader parses one shape whatever ended the socket.
func (s *socket) close(reason exit) {
	said, _ := json.Marshal(reason)

	s.conn.SetWriteDeadline(time.Now().Add(writeWithin))
	s.conn.WriteMessage(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(closeExit, string(said)),
	)
}

func trimEOL(line []byte) []byte {
	line = line[:len(line)-1]
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}

	return line
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}

	return value
}
