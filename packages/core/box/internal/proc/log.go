package proc

import (
	"io"
	"os"
	"sync"
)

// The log, and whoever is waiting on it. The child could be handed the file directly, but then
// nothing here knows when a byte arrives and a follower has to stat and sleep — an interval
// somebody picked, under every token delta. The file is still the truth: append-only, never rewritten.
type logFile struct {
	file *os.File

	mu     sync.Mutex
	cond   *sync.Cond
	size   int64
	closed bool
}

// A log that must not exist yet: two spawns with one key race on this open,
// and the loser gets `os.ErrExist`.
func openNewLog(path string) (*logFile, error) {
	return openLogWith(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND)
}

func openLog(path string) (*logFile, error) {
	return openLogWith(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND)
}

func openLogWith(path string, flag int) (*logFile, error) {
	f, err := os.OpenFile(path, flag, 0o600)
	if err != nil {
		return nil, err
	}

	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}

	l := &logFile{file: f, size: info.Size()}
	l.cond = sync.NewCond(&l.mu)

	return l, nil
}

// To disk first, then the signal. A follower woken before the write landed
// would read a size that is not there yet.
func (l *logFile) Write(p []byte) (int, error) {
	n, err := l.file.Write(p)

	l.mu.Lock()
	l.size += int64(n)
	l.mu.Unlock()
	l.cond.Broadcast()

	return n, err
}

func (l *logFile) Close() error {
	l.mu.Lock()
	l.closed = true
	l.mu.Unlock()
	// Every waiter, not one. A command ending must release all its followers, or
	// a reader hangs until its own deadline on a stream that is over.
	l.cond.Broadcast()

	return l.file.Close()
}

// WaitPast blocks until the log holds more than `offset` bytes or the command has ended, and says
// whether there is anything new. `stop` is how a follower that gave up leaves before then.
func (l *logFile) WaitPast(offset int64, stop <-chan struct{}) bool {
	// A condition variable cannot be selected on, so the wakeup has to come from
	// somewhere. One goroutine per waiter, ending when the wait does.
	done := make(chan struct{})
	defer close(done)

	go func() {
		select {
		case <-stop:
			l.cond.Broadcast()
		case <-done:
		}
	}()

	l.mu.Lock()
	defer l.mu.Unlock()

	for {
		if l.size > offset {
			return true
		}
		if l.closed {
			return false
		}

		select {
		case <-stop:
			return false
		default:
		}

		l.cond.Wait()
	}
}

func (l *logFile) Size() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.size
}

var _ io.WriteCloser = (*logFile)(nil)
