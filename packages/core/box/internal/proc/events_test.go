package proc

import (
	"fmt"
	"net"
	"testing"
	"time"
)

// Readiness is what the port does, said by the only process placed to say it: a dial from
// inside the container, on the process that owns it.

func readiness(t *testing.T, c *Command, want Readiness, within time.Duration) {
	t.Helper()

	deadline := time.After(within)

	for {
		state, changed := c.Readiness()
		if state == want {
			return
		}

		select {
		case <-changed:
		case <-deadline:
			t.Fatalf("readiness is %v after %s, want %v", state, within, want)
		}
	}
}

func TestACommandWithNoPortMakesNoClaim(t *testing.T) {
	// `jobs` and `css` bind nothing, so being alive is the whole of what they could prove — and
	// "ready" about a process that answers nothing is a word a reader cannot use.
	tbl := New(t.TempDir())

	c, err := tbl.Spawn(Spec{Argv: []string{"sleep", "5"}, Name: "css"})
	if err != nil {
		t.Fatal(err)
	}
	defer ended(t, c)

	time.Sleep(200 * time.Millisecond)

	if state, _ := c.Readiness(); state != Unknown {
		t.Fatalf("readiness is %v, want Unknown", state)
	}
}

func TestAPortedCommandIsReadyOnlyWhenThePortAnswers(t *testing.T) {
	// Alive is not answering: `go run` and `cargo run` are alive while they compile, so reporting
	// a spawn as ready points a preview at a port with nothing behind it.
	port := freePort(t)

	tbl := New(t.TempDir())

	c, err := tbl.Spawn(Spec{
		Argv: []string{"sh", "-c", fmt.Sprintf("sleep 0.7; nc -l %d", port)},
		Name: "app",
		Port: port,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ended(t, c)

	if state, _ := c.Readiness(); state != Unknown {
		t.Fatalf("readiness is %v before anything was listening, want Unknown", state)
	}

	readiness(t, c, Ready, 10*time.Second)
}

func TestAPortThatNeverAnswersSaysSoAtTheDeadline(t *testing.T) {
	// Saying nothing at the deadline makes a port that never bound indistinguishable from one
	// still being waited for, and a person watches a step that can no longer change.
	tbl := New(t.TempDir())

	c, err := tbl.Spawn(Spec{Argv: []string{"sleep", "30"}, Name: "app"})
	if err != nil {
		t.Fatal(err)
	}
	defer ended(t, c)

	go tbl.watchPort(c, freePort(t), 300*time.Millisecond)

	readiness(t, c, Unready, 5*time.Second)
}

func TestAPortThatAnswersAfterTheDeadlineIsStillReady(t *testing.T) {
	// The deadline governs what is said, never whether we keep looking. A first build can
	// legitimately run for many minutes — two go seeds take fifteen — so a watch that returned
	// at the deadline would hold a port that binds at minute eight at `unready` for ever, and
	// the app would never be reported up.
	//
	// Spawned without a port so `Spawn` starts no watch of its own; this drives the one under
	// test.
	port := freePort(t)

	tbl := New(t.TempDir())

	c, err := tbl.Spawn(Spec{Argv: []string{"sleep", "30"}, Name: "app"})
	if err != nil {
		t.Fatal(err)
	}
	defer ended(t, c)

	go tbl.watchPort(c, port, 200*time.Millisecond)

	readiness(t, c, Unready, 5*time.Second)

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	readiness(t, c, Ready, 15*time.Second)
}

func TestAServiceThatComesBackIsReadyAgain(t *testing.T) {
	// One refusal is a dev server restarting on a config change, so three of them take `ready`
	// back rather than ending the watch — a restart must not leave it unready for ever.
	port := freePort(t)

	tbl := New(t.TempDir())

	c, err := tbl.Spawn(Spec{Argv: []string{"sleep", "60"}, Name: "app"})
	if err != nil {
		t.Fatal(err)
	}
	defer ended(t, c)

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}

	go tbl.watchPort(c, port, time.Minute)

	readiness(t, c, Ready, 10*time.Second)

	listener.Close()
	readiness(t, c, Unready, 20*time.Second)

	again, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()

	readiness(t, c, Ready, 20*time.Second)
}

func TestAPortedCommandThatExitsSaysSo(t *testing.T) {
	// A crash while waiting is an answer, and the last one this watch can give.
	tbl := New(t.TempDir())

	c, err := tbl.Spawn(Spec{Argv: []string{"sh", "-c", "exit 1"}, Name: "app"})
	if err != nil {
		t.Fatal(err)
	}

	go tbl.watchPort(c, freePort(t), time.Minute)

	readiness(t, c, Unready, 5*time.Second)
}

func TestAServiceThatStopsAnsweringIsNoLongerReady(t *testing.T) {
	// `ready` can be taken back. A watch that says it once and cannot retract it leaves the switch
	// green over an app a browser can no longer reach.
	port := freePort(t)

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}

	tbl := New(t.TempDir())

	c, err := tbl.Spawn(Spec{Argv: []string{"sleep", "60"}, Name: "app", Port: port})
	if err != nil {
		t.Fatal(err)
	}
	defer ended(t, c)

	readiness(t, c, Ready, 10*time.Second)

	listener.Close()

	readiness(t, c, Unready, 20*time.Second)
}

func TestStoppingDoesNotSucceedWhileThePortStillAnswers(t *testing.T) {
	// Signalling the group is not always stopping the app: `docker compose up` is a client and its
	// containers are in nobody's group, so a stop reporting success on the signal alone would
	// leave the port serving.
	port := freePort(t)

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	tbl := New(t.TempDir())

	c, err := tbl.Spawn(Spec{Argv: []string{"sleep", "60"}, Name: "app", Port: port})
	if err != nil {
		t.Fatal(err)
	}

	if err := c.Stop(2 * time.Second); err == nil {
		t.Fatal("stopping reported success while something was still answering on the port")
	}

	if _, exited := c.ExitCode(); !exited {
		t.Fatal("the command outlived a stop that failed on the port")
	}

	waitExited(t, c)
}

func TestStoppingSucceedsOnceThePortIsSilent(t *testing.T) {
	port := freePort(t)

	tbl := New(t.TempDir())

	c, err := tbl.Spawn(Spec{
		Argv: []string{"sh", "-c", fmt.Sprintf("nc -l %d", port)},
		Name: "app",
		Port: port,
	})
	if err != nil {
		t.Fatal(err)
	}

	readiness(t, c, Ready, 10*time.Second)

	if err := c.Stop(10 * time.Second); err != nil {
		t.Fatalf("stop: %v", err)
	}

	readiness(t, c, Unready, time.Second)
	waitExited(t, c)
}

func TestAnExitedCommandStopsWithoutWaitingOnAnything(t *testing.T) {
	// Already gone is success — a person pressing stop always races a command
	// ending on its own, and that race must not raise.
	tbl := New(t.TempDir())

	c, err := tbl.Spawn(Spec{Argv: []string{"true"}, Name: "app"})
	if err != nil {
		t.Fatal(err)
	}

	waitExited(t, c)

	if err := c.Stop(time.Second); err != nil {
		t.Fatalf("stop: %v", err)
	}
}

// Killing is not reaping: the process dies immediately and the reaper closes the log after it,
// so a `t.TempDir` removed in between finds a file still open.
func ended(t *testing.T, c *Command) {
	t.Helper()

	c.Kill()
	waitExited(t, c)
}

func waitExited(t *testing.T, c *Command) {
	t.Helper()

	for range 200 {
		if _, exited := c.ExitCode(); exited {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("%s never exited", c.ID)
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
