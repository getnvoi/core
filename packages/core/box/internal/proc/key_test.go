package proc

import (
	"os"
	"path/filepath"
	"testing"
)

// Two nodes asking for one turn ask with one key. The second finds the first's
// process rather than starting another, whichever node asked first.
func TestAKeyedSpawnAskedTwiceIsOneProcess(t *testing.T) {
	tbl := New(t.TempDir())

	first, spawned, err := tbl.SpawnKeyed(Spec{Argv: []string{"sleep", "30"}}, "evt_1")
	if err != nil || !spawned {
		t.Fatalf("first spawn: %v, spawned=%v", err, spawned)
	}
	defer func() { first.Kill(); waitExited(t, first) }()

	second, spawned, err := tbl.SpawnKeyed(Spec{Argv: []string{"sleep", "30"}}, "evt_1")
	if err != nil {
		t.Fatal(err)
	}
	if spawned || second.ID != first.ID {
		t.Fatalf("second spawn: spawned=%v id=%s; want the first, %s", spawned, second.ID, first.ID)
	}
	if first.ID != "evt_1" {
		t.Fatalf("the key is the id; got %s", first.ID)
	}
}

// A key names something that happened: asked again after the process ended, it
// is still that process, not a fresh one.
func TestAKeyedSpawnAskedAfterTheEndIsStillThatProcess(t *testing.T) {
	tbl := New(t.TempDir())

	done, spawned, err := tbl.SpawnKeyed(Spec{Argv: []string{"true"}}, "evt_2")
	if err != nil || !spawned {
		t.Fatal(err)
	}
	waitExited(t, done)

	again, spawned, err := tbl.SpawnKeyed(Spec{Argv: []string{"sleep", "30"}}, "evt_2")
	if err != nil {
		t.Fatal(err)
	}
	if spawned || again.ID != done.ID {
		t.Fatalf("spawned=%v id=%s; want %s untouched", spawned, again.ID, done.ID)
	}
	if code, exited := again.ExitCode(); !exited || code != 0 {
		t.Fatalf("the ended process answers: exited=%v code=%d", exited, code)
	}
}

// The log outlives boxd, so a key asked of a restarted boxd finds the ended
// process on disk rather than running it again.
func TestAKeyedSpawnSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	tbl := New(dir)

	done, _, err := tbl.SpawnKeyed(Spec{Argv: []string{"true"}}, "evt_3")
	if err != nil {
		t.Fatal(err)
	}
	waitExited(t, done)
	tbl.Close()

	if _, err := os.Stat(filepath.Join(dir, "evt_3.log")); err != nil {
		t.Fatalf("the log is named by the key: %v", err)
	}

	restarted := New(dir)
	restarted.Rehydrate()

	again, spawned, err := restarted.SpawnKeyed(Spec{Argv: []string{"sleep", "30"}}, "evt_3")
	if err != nil {
		t.Fatal(err)
	}
	if spawned || again.ID != "evt_3" {
		t.Fatalf("spawned=%v id=%s; want the restored evt_3", spawned, again.ID)
	}
}

func TestAKeyIsAFilename(t *testing.T) {
	tbl := New(t.TempDir())

	for _, bad := range []string{"../x", "a/b", "", "a b"} {
		if _, _, err := tbl.SpawnKeyed(Spec{Argv: []string{"true"}}, bad); err == nil {
			t.Fatalf("key %q was accepted", bad)
		}
	}
}
