package proc

import (
	"os"
	"testing"
)

func TestAnExitCodeIsOnlyReportedWhenItWasObserved(t *testing.T) {
	// `Wait` answers three ways: nil, the program's own non-zero code, or an
	// error of its own — a failed copy onto the log, which is a full disk inside
	// the box. Only the first two are the program's answer. Reporting the third
	// as 0 tells Phoenix a `docker compose build` succeeded, which marks setup
	// done over a checkout that never built.
	dir := t.TempDir()
	tbl := New(dir)

	c, err := tbl.Spawn(Spec{Argv: []string{"sh", "-c", "sleep 0.3; echo hi"}})
	if err != nil {
		t.Fatal(err)
	}

	// The write the child is about to make now fails, while the child itself
	// exits perfectly well.
	if err := c.log.file.Close(); err != nil {
		t.Fatal(err)
	}

	waitExited(t, c)

	if c.Coded() {
		code, _ := c.ExitCode()
		t.Fatalf("reported exit %d for an ending nobody observed", code)
	}

	if _, err := os.Stat(statusPath(c.LogPath)); !os.IsNotExist(err) {
		t.Fatalf("wrote a status file for an unobserved ending (%v)", err)
	}

	// And a restart agrees, rather than reading a fabricated success off disk.
	restored := New(dir)
	restored.Rehydrate()

	back, ok := restored.Get(c.ID)
	if !ok {
		t.Fatalf("%s was not restored", c.ID)
	}
	if _, exited := back.ExitCode(); !exited {
		t.Fatal("a command whose container is gone has ended")
	}
	if back.Coded() {
		t.Fatal("restored with a code it never had")
	}
}
