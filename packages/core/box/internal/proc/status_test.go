package proc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAnEndingSurvivesTheProcessThatHadIt(t *testing.T) {
	dir := t.TempDir()
	tbl := New(dir)

	c, err := tbl.Spawn(Spec{Argv: []string{"sh", "-c", "echo hello; exit 7"}})
	if err != nil {
		t.Fatal(err)
	}
	waitExited(t, c)

	// A new table over the same disk is what a restarted container is.
	restored := New(dir)
	if n := restored.Rehydrate(); n != 1 {
		t.Fatalf("restored %d, want 1", n)
	}

	back, ok := restored.Get(c.ID)
	if !ok {
		t.Fatalf("%s was not restored", c.ID)
	}

	code, exited := back.ExitCode()
	if !exited || code != 7 || !back.Coded() {
		t.Fatalf("restored as exited=%v code=%d coded=%v, want true/7/true", exited, code, back.Coded())
	}

	body, err := readAll(back, 0)
	if err != nil || body != "hello\n" {
		t.Fatalf("restored log = %q (%v), want %q", body, err, "hello\n")
	}
}

func TestALogWithNoStatusIsEndedWithNoCode(t *testing.T) {
	// The container died mid-command: the log landed, the ending never did.
	// Reporting 0 here would be a lie about something that was probably killed.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cmd_abc.log"), []byte("half a line"), 0o600); err != nil {
		t.Fatal(err)
	}

	tbl := New(dir)
	if n := tbl.Rehydrate(); n != 1 {
		t.Fatalf("restored %d, want 1", n)
	}

	c, _ := tbl.Get("cmd_abc")
	if _, exited := c.ExitCode(); !exited {
		t.Fatal("a command whose container is gone has ended")
	}
	if c.Coded() {
		t.Fatal("there is no code to report, so `coded` must say so")
	}
}

func TestARestoredCommandRefusesStdinAndSurvivesAKill(t *testing.T) {
	// It has no process. Reaching for one would panic on exactly the rows a
	// restart restores.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cmd_gone.log"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}

	tbl := New(dir)
	tbl.Rehydrate()
	c, _ := tbl.Get("cmd_gone")

	if err := c.Write("{}\n"); err != ErrExited {
		t.Fatalf("write to a restored command = %v, want ErrExited", err)
	}
	if err := c.Kill(); err != nil {
		t.Fatalf("killing something already gone is success, got %v", err)
	}
}

func TestRehydrateDoesNotDisplaceALiveCommand(t *testing.T) {
	dir := t.TempDir()
	tbl := New(dir)

	c, err := tbl.Spawn(Spec{Argv: []string{"sleep", "30"}})
	if err != nil {
		t.Fatal(err)
	}
	// Killed AND waited for: the reaper writes a status file beside the log when
	// it ends, and a test that returns first races t.TempDir()'s cleanup.
	defer func() {
		c.Kill()
		waitExited(t, c)
	}()

	if n := tbl.Rehydrate(); n != 0 {
		t.Fatalf("restored %d over a live table, want 0", n)
	}

	back, _ := tbl.Get(c.ID)
	if _, exited := back.ExitCode(); exited {
		t.Fatal("a running command was overwritten by its own log file")
	}
}

func readAll(c *Command, skip int64) (string, error) {
	rc, err := c.ReadFrom(skip)
	if err != nil {
		return "", err
	}
	defer rc.Close()

	b, err := os.ReadFile(c.LogPath)
	if err != nil {
		return "", err
	}
	return string(b[skip:]), nil
}
