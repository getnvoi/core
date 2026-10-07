package proc

import (
	"testing"
	"time"
)

// `AwaitExit` wakes when the reaper closes the log and then reads `ExitCode`.
// The ending must be recorded before the close, or a waiter woken by it reads
// a command that has not exited. Repeated, because the window is microseconds.
func TestAnEndingIsRecordedBeforeTheLogCloses(t *testing.T) {
	dir := t.TempDir()
	tbl := New(dir)
	t.Cleanup(tbl.Close)

	for i := 0; i < 200; i++ {
		c, err := tbl.Spawn(Spec{Argv: []string{"true"}})
		if err != nil {
			t.Fatal(err)
		}

		if !c.AwaitExit(5*time.Second, nil) {
			t.Fatalf("spawn %d: woken by the log closing, and not exited", i)
		}

		if _, ok := readStatus(c.LogPath); !ok {
			t.Fatalf("spawn %d: the status file was not written before the log closed", i)
		}
	}
}
