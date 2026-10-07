package proc

import (
	"testing"
	"time"
)

// A command spawned without `Stdin` reads EOF at once: a prompt for a password
// fails instead of blocking on a pipe nothing writes. One spawned with it is
// held open for the writer, as a parked turn needs.
func TestStdinIsEOFUnlessAsked(t *testing.T) {
	tbl := New(t.TempDir())
	t.Cleanup(tbl.Close)

	quiet, err := tbl.Spawn(Spec{Argv: []string{"cat"}})
	if err != nil {
		t.Fatal(err)
	}

	if !quiet.AwaitExit(2*time.Second, nil) {
		t.Fatal("cat without stdin did not end on EOF")
	}

	if err := quiet.Write("x\n"); err != ErrExited {
		t.Fatalf("a write to a command with no stdin: %v", err)
	}

	held, err := tbl.Spawn(Spec{Argv: []string{"cat"}, Stdin: true})
	if err != nil {
		t.Fatal(err)
	}

	if held.AwaitExit(200*time.Millisecond, nil) {
		t.Fatal("cat with stdin ended without a close")
	}

	if err := held.CloseStdin(); err != nil {
		t.Fatal(err)
	}

	if !held.AwaitExit(2*time.Second, nil) {
		t.Fatal("cat did not end once stdin was closed")
	}
}
