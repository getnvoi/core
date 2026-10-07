// boxd runs inside a sandbox and owns claude processes and their pipes. No model calls, no tool
// execution, no permission logic, no knowledge of sessions or agents: it starts processes,
// moves bytes, and kills things.
package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/getnvoi/nvoi/packages/core/box/internal/api"
	"github.com/getnvoi/nvoi/packages/core/box/internal/proc"
	"github.com/getnvoi/nvoi/packages/core/box/internal/terminal"
)

func main() {
	token := os.Getenv("NVOI_TOKEN")
	if token == "" {
		log.Fatal("NVOI_TOKEN is required — an unauthenticated sandbox is not a sandbox")
	}

	workspace := envOr("NVOI_WORKSPACE", "/workspace")

	// Never inside the workspace. Everything of ours lives in the agent's home, because anything
	// under the checkout reaches somebody's pull request — a model transcript and a 6MB binary is
	// what that costs.

	logDir := envOr("NVOI_LOG_DIR", filepath.Join(home(), ".nvoi", "logs"))
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		log.Fatalf("log dir: %v", err)
	}

	// A signal is an instruction, not a kill. This process is the container's CMD, and go's default
	// disposition would end it where it stands, severing whatever is mid-write.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	addr := envOr("NVOI_LISTEN", ":8080")

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("listen %s: %v", addr, err)
	}

	log.Printf("boxd listening on %s · workspace %s", addr, workspace)

	table := proc.New(logDir)

	// What the last boot left: the processes died with the container, but their output and their
	// endings are on the checkout's disk, so a restarted box can still answer for them rather
	// than 404ing beside a log that is plainly there.
	if restored := table.Rehydrate(); restored > 0 {
		log.Printf("boxd restored %d command(s) from %s", restored, logDir)
	}

	go shells(ctx, workspace)

	if err := serve(ctx, listener, api.New(table, token)); err != nil {
		log.Fatalf("serve: %v", err)
	}

	// Exit zero, and it carries information. A killed process reports the same non-zero code
	// whether it was resting or crashing, which is why the edge has to announce `cause: "idle"`.
	// This makes the exit code a second signal, so a non-zero code means something went wrong.
	log.Print("boxd stopped cleanly")
}

// The Terminal view's shells, on a port of their own behind the machine's terminal key (the preview worker adds it),
// as the agent's user. A machine whose user is missing serves none; boxd's commands are not held up by it.
func shells(ctx context.Context, workspace string) {
	addr := envOr("NVOI_TERMINAL_LISTEN", ":7777")
	cred, env, err := proc.As(os.Getenv("NVOI_TERMINAL_USER"), api.ChildEnv(nil))
	if err != nil {
		log.Printf("shells: %v", err)
		return
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("shells: listen %s: %v", addr, err)
		return
	}
	s := &terminal.Server{
		KeyFile:    envOr("NVOI_TERMINAL_KEY", "/etc/nvoi/terminal.key"),
		Dir:        workspace,
		Shell:      "bash",
		Credential: cred,
		Env:        env,
	}
	log.Printf("shells on %s", addr)
	if err := serve(ctx, listener, s.Handler()); err != nil {
		log.Printf("shells: %v", err)
	}
}

// How long a drain may take: long enough for a response already being written to finish, short
// enough that it is over before the runtime gives up on a container that was asked to stop.
const drainTimeout = 10 * time.Second

// serve runs until the context is done, then lets in-flight requests finish. Split out of
// `main` because a shutdown is the one behaviour here worth a test and `main` cannot be called
// from one. It takes a listener rather than an address for the same reason.
func serve(ctx context.Context, listener net.Listener, handler http.Handler) error {
	server := &http.Server{Handler: handler}

	failed := make(chan error, 1)

	go func() {
		err := server.Serve(listener)
		// A requested close is not a failure. Distinguishing them is what keeps
		// a container that can never bind from looking like one that rested.
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		failed <- err
	}()

	select {
	case err := <-failed:
		return err
	case <-ctx.Done():
	}

	log.Print("boxd draining")

	// Not the cancelled context: shutting down with one that is already done cancels the drain in
	// the same instant it is asked for, which is the severing this exists to stop.
	drain, cancel := context.WithTimeout(context.Background(), drainTimeout)
	defer cancel()

	return server.Shutdown(drain)
}

// The agent's own home, which is where everything of ours belongs. Falls back to /tmp rather
// than to the workspace: a missing HOME must not put our files back in the repository.
func home() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return "/tmp/nvoi"
}

func envOr(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
