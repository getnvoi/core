package proc

import (
	"sync"
	"testing"
	"time"
)

// `Write` reads `c.stdin` while `CloseStdin` sets it to nil.
//
// These are the two ends of a park: an approval writes the control_response the
// cli is blocked on, and a turn that has finished closes the pipe. Both come
// from different processes over HTTP, so they genuinely overlap.
//
// Taking the lock, releasing it, and THEN reading the field is a data race on
// `c.stdin` — the check is answered about a pipe that may be gone by the time it
// is used. `go test -race` is what proves it; without the race detector the read
// is simply wrong and usually gets away with it, which is the worst kind of
// wrong.
//
// It is NOT about interleaved bytes: `*os.File.Write` takes its own per-fd lock
// inside the runtime poller, so two goroutines writing one file cannot split
// each other's writes however large they are. Holding this mutex across the
// write costs nothing and keeps the field read where the check is.
func TestWriteRacesCloseStdin(t *testing.T) {
	dir := t.TempDir()
	tbl := New(dir)
	// A reaper writes its status file beside the log AFTER the process is gone,
	// so the temp directory cannot be torn down until it has. `Close` kills every
	// live command and waits for the last of them.
	t.Cleanup(tbl.Close)

	c, err := tbl.Spawn(Spec{Argv: []string{"cat"}})
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup

	wg.Add(2)

	go func() {
		defer wg.Done()

		for i := 0; i < 200; i++ {
			// ErrExited is the expected answer once the pipe is closed. What must
			// not happen is reading the field outside the lock that guards it.
			_ = c.Write("{}\n")
		}
	}()

	go func() {
		defer wg.Done()

		for i := 0; i < 200; i++ {
			_ = c.CloseStdin()
		}
	}()

	wg.Wait()

	// Killed AND waited for: the reaper writes a status file beside the log when
	// the process ends, and a test that returns first races t.TempDir()'s
	// cleanup.
	if err := c.Kill(); err != nil {
		t.Fatal(err)
	}

	if !c.AwaitExit(10*time.Second, nil) {
		t.Fatal("it never exited")
	}
}

// A child that stops draining its stdin blocks the writer, and the writer must
// not be holding the table's mutex while it does.
//
// A pipe holds 64 KiB. Past that `Write` blocks until the child reads, and a
// child that never will — `sleep`, or a cli wedged mid-turn — blocks it for as
// long as it lives. With the lock held across the write, `Kill`, `Signal`,
// `CloseStdin`, `ExitCode`, every socket reader's `logSize` and the reaper's own
// `cmd.Wait()` all queue behind it: the box cannot kill or reap the command that
// is stuck.
func TestABlockedWriteDoesNotWedgeKill(t *testing.T) {
	dir := t.TempDir()
	tbl := New(dir)
	t.Cleanup(tbl.Close)

	// Never reads stdin, so the pipe fills and stays full.
	c, err := tbl.Spawn(Spec{Argv: []string{"sleep", "60"}})
	if err != nil {
		t.Fatal(err)
	}

	blocked := make(chan struct{})

	go func() {
		defer close(blocked)
		// Well past the 64 KiB a pipe holds.
		_ = c.Write(string(make([]byte, 256*1024)))
	}()

	// Long enough for the writer to fill the pipe and block in the write.
	time.Sleep(250 * time.Millisecond)

	killed := make(chan error, 1)
	go func() { killed <- c.Kill() }()

	select {
	case err := <-killed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Kill waited on a blocked write")
	}

	// Killing the process closes the read end, so the blocked write ends with an
	// error.
	<-blocked

	if !c.AwaitExit(10*time.Second, nil) {
		t.Fatal("it never exited")
	}
}
