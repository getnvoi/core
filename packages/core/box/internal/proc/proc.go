// Package proc owns running processes and their pipes. It exists because the vendor cli has no
// detach: a parked turn is a cli suspended on stdin, so whatever holds that pipe must outlive every
// external connection. It never decodes a byte — protocol lives in the control plane.
package proc

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	stdlog "log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"
)

var ErrExited = errors.New("command has exited")

type Spec struct {
	Argv []string
	Dir  string
	Env  []string

	// What this process is called, and where it listens. Both optional: a one-off `test -d` has
	// neither, and a command that declares no port makes no claim about answering.
	Name string
	Port int

	// Who it runs as. Empty is root, which is the default because dockerd needs it and the file
	// writes are root's. See user.go for why only the vendor cli drops.
	As string

	// Whether a pipe is held open on its stdin for `Write`. False reads EOF at once, so a
	// command that prompts fails instead of blocking on a pipe nothing writes.
	Stdin bool
}

type Command struct {
	ID      string
	Name    string
	LogPath string

	// Nil on a command restored from disk: its writer died with the container,
	// and there is nothing left to wait on.
	log *logFile

	stdin io.WriteCloser

	mu       sync.Mutex
	cmd      *exec.Cmd
	exited   bool
	exitCode int
	// Whether the code is known, which is not the same as it being zero: a command restored from
	// disk with no status file has ended and there is nothing true to say about how, so reporting
	// 0 would be a lie about something that may have been killed.
	coded bool

	// Where it listens, and what it has proved about that. See events.go: a port that has not
	// answered is not a service, and one that stopped answering is not one any more.
	port         int
	ready        Readiness
	readyChanged chan struct{}
}

type Table struct {
	logDir string
	mu     sync.RWMutex
	cmds   map[string]*Command
	// Reapers write a status file beside the log AFTER the process is gone, so
	// anything that tears the log directory down has to be able to wait for them.
	reaping sync.WaitGroup
}

func New(logDir string) *Table {
	return &Table{logDir: logDir, cmds: map[string]*Command{}}
}

// Named finds the live command a caller gave this name to: a turn is started by a worker and
// stopped by a web request that never saw the id. Live only — names are reused.
func (t *Table) Named(name string) (*Command, bool) {
	if name == "" {
		return nil, false
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	for _, c := range t.cmds {
		if c.Name != name {
			continue
		}
		if _, exited := c.ExitCode(); exited {
			continue
		}
		return c, true
	}

	return nil, false
}

// What is running, asked of the thing that spawned it. A row in the control plane saying
// `running` is a claim about a live process; this is the process table. Live only — a finished
// command is history, and the rows already keep that.
func (t *Table) Live() []*Command {
	t.mu.RLock()
	defer t.mu.RUnlock()

	live := make([]*Command, 0, len(t.cmds))

	for _, c := range t.cmds {
		if _, exited := c.ExitCode(); exited {
			continue
		}

		live = append(live, c)
	}

	sort.Slice(live, func(i, j int) bool { return live[i].ID < live[j].ID })

	return live
}

func (t *Table) Get(id string) (*Command, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	c, ok := t.cmds[id]
	return c, ok
}

// Spawn starts a detached command whose stdout and stderr append to a log file. The log is
// append-only and never rewritten, which is what makes a byte offset mean the same thing forever
// — a reader that died resumes exactly where it stopped instead of replaying.
func (t *Table) Spawn(spec Spec) (*Command, error) {
	c, _, err := t.spawn(spec, newID())
	return c, err
}

// A spawn that happens once per key, whoever asks and however often. The key
// is the id, so the log on disk carries it and a boxd restarted between the
// asks still answers with the process that ran. `spawned` is false for an ask
// that found it.
func (t *Table) SpawnKeyed(spec Spec, key string) (c *Command, spawned bool, err error) {
	if !validKey(key) {
		return nil, false, ErrBadKey
	}

	t.mu.RLock()
	found, ok := t.cmds[key]
	t.mu.RUnlock()

	if ok {
		return found, false, nil
	}

	return t.spawn(spec, key)
}

// A key is the log's filename, so it is one: letters, digits, `.`, `_`, `-`.
var ErrBadKey = errors.New("key must be a filename: letters, digits, . _ -")

func validKey(key string) bool {
	if key == "" || key == "." || key == ".." {
		return false
	}

	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
		default:
			return false
		}
	}

	return true
}

func (t *Table) spawn(spec Spec, id string) (*Command, bool, error) {
	if len(spec.Argv) == 0 {
		return nil, false, errors.New("argv is empty")
	}

	logPath := filepath.Join(t.logDir, id+".log")

	// Two asks with one key at once: the log is created exclusively, so the
	// loser finds the winner's rather than a second process.
	log, err := openNewLog(logPath)
	if errors.Is(err, os.ErrExist) {
		found := t.awaitEntry(id)
		return found, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...)
	cmd.Dir = spec.Dir
	cmd.Stdout = log
	cmd.Stderr = log
	// Its own process group, so a kill takes the children with it. Without this a
	// build outlives the turn that started it.
	credential, account, err := credentialFor(spec.As)
	if err != nil {
		log.Close()
		os.Remove(logPath)
		return nil, false, err
	}

	cmd.Env = envFor(spec.Env, account)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Credential: credential}

	var stdin io.WriteCloser

	if spec.Stdin {
		pipe, err := cmd.StdinPipe()
		if err != nil {
			log.Close()
			os.Remove(logPath)
			return nil, false, err
		}

		stdin = pipe
	}

	if err := cmd.Start(); err != nil {
		log.Close()
		os.Remove(logPath)
		return nil, false, err
	}

	c := &Command{
		ID: id, Name: spec.Name, LogPath: logPath, log: log, stdin: stdin, cmd: cmd,
		port: spec.Port, readyChanged: make(chan struct{}),
	}

	t.mu.Lock()
	t.cmds[id] = c
	t.mu.Unlock()

	// Readiness is decided in here, and only for a command that declared a port. A command with
	// none makes no claim: `jobs` and `css` bind nothing, so saying "ready" about them would tell
	// a reader something it cannot use.
	if spec.Port > 0 {
		go t.watchPort(c, spec.Port, PortWithin)
	}

	// The instant it happens, which is why polling was always the wrong shape: this goroutine has
	// known every exit since the table was written, and nothing asked it.
	t.reaping.Add(1)

	go func() {
		defer t.reaping.Done()

		err := cmd.Wait()

		// An ending is known two ways and no third: `Wait` returned nil, so the
		// program exited 0, or it returned the program's own non-zero code. Any
		// other error is `Wait` itself failing — the pipes, the kernel — and the
		// program's answer is then something nobody has. `coded` says which, and
		// a zero stamped over that failure is a clean success nobody observed.
		var ee *exec.ExitError

		c.mu.Lock()
		c.exited = true

		switch {
		case err == nil:
			c.exitCode, c.coded = 0, true
		case errors.As(err, &ee):
			c.exitCode, c.coded = ee.ExitCode(), true
		default:
			c.coded = false
		}

		code, coded := c.exitCode, c.coded
		c.mu.Unlock()

		// Beside the log, because memory does not survive the container. See status.go — this is
		// what lets a restarted box still answer how a command ended. An uncoded
		// ending writes nothing: the absent file is what `Rehydrate` reads as
		// "ended, code unknown", which is exactly what happened.
		if coded {
			if err := writeStatus(c.LogPath, code); err != nil {
				stdlog.Printf("boxd: %s ended %d and the status file was not written: %v", c.ID, code, err)
			}
		}

		// Last. Closing is what wakes every waiter, and each reads the ending
		// the moment it wakes, so the ending is complete before the close.
		log.Close()
	}()

	return c, true, nil
}

// The entry for an id whose log another spawn created a moment ago. The
// creator inserts it right after opening the log, so this waits out that
// moment rather than reporting a process that exists as absent.
func (t *Table) awaitEntry(id string) *Command {
	for {
		t.mu.RLock()
		c, ok := t.cmds[id]
		t.mu.RUnlock()

		if ok {
			return c
		}

		time.Sleep(5 * time.Millisecond)
	}
}

// Close kills everything this table started and waits for the last status file
// to be written. Nothing in a container calls it — the process exits and the
// disk keeps what it has — but a caller that OWNS the log directory must not
// tear it down while a reaper is still writing into it.
//
// Killing first is the whole of it: a reaper is blocked in `cmd.Wait()`, so
// waiting without killing waits for the service to end on its own.
func (t *Table) Close() {
	for _, c := range t.Live() {
		_ = c.Kill()
	}

	t.reaping.Wait()
}

func (c *Command) ExitCode() (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.exitCode, c.exited
}

// Pid is the process the kernel knows about, or 0 once it has been reaped. The entrypoint, and the
// reason a reader never has to infer "is it running" from a word somebody set.
func (c *Command) Pid() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.exited || c.cmd == nil || c.cmd.Process == nil {
		return 0
	}

	return c.cmd.Process.Pid
}

// Coded is whether the exit code means anything. False on a command restored
// from disk whose ending was never written down.
func (c *Command) Coded() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.coded
}

// ReadFrom opens the log positioned at skip bytes.
func (c *Command) ReadFrom(skip int64) (io.ReadCloser, error) {
	f, err := os.Open(c.LogPath)
	if err != nil {
		return nil, err
	}
	if _, err := f.Seek(skip, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// WaitPast blocks until there is something past `offset` to read, or the command has ended. False
// means "nothing more is coming" — see logFile. A restored command never waits: its bytes are all
// on disk and its writer is gone, so no signal could ever arrive.
func (c *Command) WaitPast(offset int64, stop <-chan struct{}) bool {
	c.mu.Lock()
	log := c.log
	c.mu.Unlock()

	if log == nil {
		return false
	}

	return log.WaitPast(offset, stop)
}

// AwaitExit blocks until the command ends, the deadline passes, or the caller gives up. No polling:
// the reaper closes the log when it records the exit, and closing wakes everything waiting on it.
func (c *Command) AwaitExit(within time.Duration, stop <-chan struct{}) bool {
	deadline := time.After(within)
	gone := make(chan struct{})
	defer close(gone)

	waiting := make(chan struct{})

	go func() {
		// `WaitPast` returns false when the log is closed, which is what the
		// reaper does the instant a command ends.
		for c.WaitPast(c.logSize(), gone) {
		}
		close(waiting)
	}()

	select {
	case <-waiting:
	case <-deadline:
	case <-stop:
	}

	_, exited := c.ExitCode()

	return exited
}

func (c *Command) logSize() int64 {
	c.mu.Lock()
	log := c.log
	c.mu.Unlock()

	if log == nil {
		return 0
	}

	return log.Size()
}

// Write sends one line to a running process's stdin. This is what resolves a
// park: a suspended CLI is waiting on exactly this. The pipe is opened at spawn
// and stays open for as long as the process lives.
//
// The handle is copied under the mutex and written to outside it. Reading
// `c.stdin` outside the lock is a data race with `CloseStdin`, which sets it to
// nil; holding the lock across the write is worse, because a pipe holds 64 KiB
// and a child that stops draining it blocks the writer for as long as it lives —
// `Kill`, `Signal`, `CloseStdin`, `ExitCode`, every socket reader's `logSize`
// and the reaper's own `cmd.Wait()` then queue behind it, so the box cannot kill
// or reap the command that is stuck. Closing the pipe under a blocked write ends
// it with an error, which is the right answer.
//
// Interleaving is not what the lock was for: `*os.File.Write` takes its own
// per-fd lock inside the runtime poller, so two writers cannot split each
// other's lines however large they are.
func (c *Command) Write(line string) error {
	c.mu.Lock()
	exited, stdin := c.exited, c.stdin
	c.mu.Unlock()

	if exited || stdin == nil {
		return ErrExited
	}

	_, err := io.WriteString(stdin, line)

	return err
}

// CloseStdin sends EOF to a running command, which otherwise never ends if it reads to EOF: the
// pipe is held for the process's whole life so a parked turn can be answered. Idempotent.
func (c *Command) CloseStdin() error {
	c.mu.Lock()
	stdin := c.stdin
	c.stdin = nil
	c.mu.Unlock()

	if stdin == nil {
		return nil
	}

	return stdin.Close()
}

// Ask it to stop, which is what `^C` is: the line discipline signals the foreground group and the
// program runs its own shutdown — `docker compose up` interrupted takes its containers with it.
func (c *Command) Signal(sig syscall.Signal) error {
	c.mu.Lock()
	exited := c.exited || c.cmd == nil || c.cmd.Process == nil
	pid := 0
	if !exited {
		pid = c.cmd.Process.Pid
	}
	c.mu.Unlock()

	// Already gone is success: a person pressing stop always races a command
	// ending on its own.
	if exited {
		return nil
	}

	if err := syscall.Kill(-pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}

	return nil
}

// Kill terminates the process group. Idempotent by contract: a deadline reaper always races a
// normal exit, and that race must not raise.
func (c *Command) Kill() error {
	c.mu.Lock()
	// A rehydrated command has no process. Reading `c.cmd.Process` before the
	// exited check would panic on exactly the rows a restart restores.
	exited := c.exited || c.cmd == nil || c.cmd.Process == nil
	pid := 0
	if !exited {
		pid = c.cmd.Process.Pid
	}
	c.mu.Unlock()
	if exited {
		return nil
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

func newID() string {
	b := make([]byte, 8)
	// `crypto/rand.Read` cannot fail on go 1.24 and later — it panics rather than
	// returning an error — so there is nothing here to handle. Left unchecked
	// deliberately: an id of eight zero bytes would collide every command in the
	// table onto one row.
	rand.Read(b)
	return "cmd_" + hex.EncodeToString(b)
}
