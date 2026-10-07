package proc

import (
	stdlog "log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// An ending outlives the process that had it: exit codes live in memory, which a container restart
// does not. So the code is written beside the log, and the table is rebuilt from the directory.
const statusSuffix = ".status"

func statusPath(logPath string) string {
	return strings.TrimSuffix(logPath, ".log") + statusSuffix
}

// Write then rename. A crash midway through leaves a truncated file that still
// parses as a number — an ending that is not the ending.
func writeStatus(logPath string, code int) error {
	dir := filepath.Dir(logPath)

	tmp, err := os.CreateTemp(dir, ".boxd-status-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.WriteString(strconv.Itoa(code)); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	return os.Rename(tmp.Name(), statusPath(logPath))
}

func readStatus(logPath string) (int, bool) {
	raw, err := os.ReadFile(statusPath(logPath))
	if err != nil {
		return 0, false
	}

	code, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0, false
	}

	return code, true
}

// Rehydrate rebuilds the table from disk at boot. Every rehydrated command has ended whatever its
// status file says; a log with no status is `exited` with no code, where a zero would be a lie.
func (t *Table) Rehydrate() int {
	entries, err := os.ReadDir(t.logDir)
	if err != nil {
		// `main` creates this directory before calling, so a failure here is the
		// disk or the permissions, never a first boot. Answering 0 in silence
		// boots a box that has never heard of the commands Phoenix holds ids for.
		stdlog.Printf("boxd: cannot read %s, so no command was restored: %v", t.logDir, err)
		return 0
	}

	restored := 0

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".log") {
			continue
		}

		id := strings.TrimSuffix(name, ".log")
		logPath := filepath.Join(t.logDir, name)

		code, known := readStatus(logPath)

		c := &Command{
			ID:       id,
			LogPath:  logPath,
			exited:   true,
			exitCode: code,
			coded:    known,
			// Nothing will ever change it, and a channel nobody closes is what a
			// reader of a finished command should wait on for ever.
			readyChanged: make(chan struct{}),
		}

		t.mu.Lock()
		if _, taken := t.cmds[id]; !taken {
			t.cmds[id] = c
			restored++
		}
		t.mu.Unlock()
	}

	return restored
}
