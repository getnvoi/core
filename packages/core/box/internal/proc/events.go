package proc

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Readiness: whether the thing a command started is answering. Alive is not answering — `go run`
// is alive while it compiles — so a spawn proves nothing and a preview pointed at one reaches a
// dead port. State and not an event: a reader that attached a second late still learns the port
// answered.
type Readiness int

const (
	// Nothing to say: the port has not answered yet, or there is no port and
	// being alive is the whole of what this command can prove.
	Unknown Readiness = iota
	Ready
	Unready
)

const (
	// How long a declared port has to answer. A first `docker compose build`
	// legitimately runs for minutes; past this it is not slow, it is not coming.
	PortWithin = 5 * time.Minute

	dialWithin = time.Second
	// While waiting for a first answer. A boot is what somebody is watching.
	dialEvery = 250 * time.Millisecond
	// Once it has answered. Nobody is waiting on this one, and a dial per second
	// is enough to notice a service that went away.
	watchEvery = time.Second
	// How many refusals in a row mean it has stopped answering. One is a dev server restarting
	// itself on a config change; three across three seconds is a service that is gone.
	silentAfter = 3
	// How long a stop waits for the port to go quiet before saying it did not.
	StopWithin = 15 * time.Second
	// How long the signalled group has to release the port on its own.
	stopGrace = time.Second
)

// Readiness is what is known now, and the channel that closes when that changes.
func (c *Command) Readiness() (Readiness, <-chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.ready, c.readyChanged
}

func (c *Command) Port() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.port
}

func (c *Command) settle(state Readiness) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.ready == state {
		return
	}

	c.ready = state
	close(c.readyChanged)
	c.readyChanged = make(chan struct{})
}

// watchPort dials the service's own port from inside the container and keeps dialing for as long
// as the process lives. Every way out says something, and `ready` can be taken back.
//
// The deadline governs what is SAID, never whether we keep looking. A first build legitimately
// runs for minutes, so a port that binds at minute eight is reported up rather than held at
// `unready` for ever. Three refusals are read the same way: a dev server restarting, not a
// service that can never come back.
//
// Only the process ending ends the watch, because only then is the question closed.
func (t *Table) watchPort(c *Command, port int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)

	answered := false
	misses := 0

	for {
		if _, exited := c.ExitCode(); exited {
			c.settle(Unready)
			return
		}

		switch {
		case answers(port):
			c.settle(Ready)
			answered = true
			misses = 0

		// One refusal is a dev server restarting on a config change; three across
		// three seconds is a service that is gone.
		case answered:
			if misses++; misses >= silentAfter {
				c.settle(Unready)
				answered = false
				misses = 0
			}

		// Nothing has answered yet. Saying so is only honest once it is late.
		case !time.Now().Before(deadline):
			c.settle(Unready)
		}

		// A boot is what somebody is watching; once it has answered, or given up
		// answering, nobody is waiting on this one.
		if answered || !time.Now().Before(deadline) {
			time.Sleep(watchEvery)
		} else {
			time.Sleep(dialEvery)
		}
	}
}

func answers(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), dialWithin)
	if err != nil {
		return false
	}

	conn.Close()

	return true
}

// Stop ends the command and, for one that declared a port, waits until that port stops answering:
// `docker compose up` is a client, so its containers survive a signal to its own group.
func (c *Command) Stop(within time.Duration) error {
	if err := c.Kill(); err != nil {
		return err
	}

	port := c.Port()
	if port == 0 {
		return nil
	}

	deadline := time.Now().Add(within)
	grace := time.Now().Add(stopGrace)
	signalled := false

	for {
		if !answers(port) {
			c.settle(Unready)
			return nil
		}

		// Once, and only after the group has had a moment to go on its own: a
		// server closing its own listener does not need killing twice.
		if !signalled && time.Now().After(grace) {
			killHolders(port)
			signalled = true
		}

		if !time.Now().Before(deadline) {
			return fmt.Errorf("port %d still answers %s after it was signalled", port, within)
		}

		time.Sleep(dialEvery)
	}
}

// Who is listening on this port, from /proc rather than from a tool the image may not carry:
// the listening socket's inode is in /proc/net/tcp, and the process holding it is whichever one
// has that inode as an open descriptor.
func killHolders(port int) {
	inodes := listeningInodes(port)
	if len(inodes) == 0 {
		return
	}

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return
	}

	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}

		if holds(pid, inodes) {
			syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}

func listeningInodes(port int) map[string]struct{} {
	inodes := map[string]struct{}{}
	// LISTEN, in the hex the kernel writes.
	const listening = "0A"

	for _, table := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		raw, err := os.ReadFile(table)
		if err != nil {
			continue
		}

		for _, line := range strings.Split(string(raw), "\n")[1:] {
			fields := strings.Fields(line)
			if len(fields) < 10 || fields[3] != listening {
				continue
			}

			_, hex, found := strings.Cut(fields[1], ":")
			if !found {
				continue
			}

			bound, err := strconv.ParseUint(hex, 16, 32)
			if err != nil || int(bound) != port {
				continue
			}

			inodes[fields[9]] = struct{}{}
		}
	}

	return inodes
}

func holds(pid int, inodes map[string]struct{}) bool {
	fds, err := os.ReadDir(filepath.Join("/proc", strconv.Itoa(pid), "fd"))
	if err != nil {
		return false
	}

	for _, fd := range fds {
		target, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "fd", fd.Name()))
		if err != nil {
			continue
		}

		inode := strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")
		if _, ok := inodes[inode]; ok {
			return true
		}
	}

	return false
}
