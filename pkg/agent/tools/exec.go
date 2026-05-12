package tools

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
)

// Runner abstracts how a tool spawns nvoi. Production uses OSRunner —
// real exec.Command-backed subprocess. Tests inject a fakeRunner that
// records args/cwd/stdin and returns canned output without spawning
// anything. The active runner is read from context; absent → OSRunner.
type Runner interface {
	Run(ctx context.Context, nvoiBinary, cwd string, args []string, stdin []byte, stdoutCap, stderrCap int) (runResult, error)
}

// OSRunner is the production Runner. exec.Command-backed; passes the
// parent's env to the child (operator-set creds flow through). Stdout
// and stderr are captured with cappedWriter to bound memory + the
// model-facing tool_result size.
type OSRunner struct{}

func (OSRunner) Run(ctx context.Context, nvoiBinary, cwd string, args []string, stdin []byte, stdoutCap, stderrCap int) (runResult, error) {
	cmd := exec.CommandContext(ctx, nvoiBinary, args...)
	cmd.Dir = cwd
	var so, se bytes.Buffer
	cmd.Stdout = &cappedWriter{buf: &so, cap: stdoutCap}
	cmd.Stderr = &cappedWriter{buf: &se, cap: stderrCap}

	if len(stdin) == 0 {
		err := cmd.Run()
		return finalize(so.Bytes(), se.Bytes(), err)
	}

	wp, err := cmd.StdinPipe()
	if err != nil {
		return runResult{}, fmt.Errorf("stdin pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return runResult{}, fmt.Errorf("start: %w", err)
	}
	go func() {
		defer wp.Close()
		_, _ = io.Copy(wp, bytes.NewReader(stdin))
	}()
	return finalize(so.Bytes(), se.Bytes(), cmd.Wait())
}

func finalize(stdout, stderr []byte, err error) (runResult, error) {
	res := runResult{Stdout: stdout, Stderr: stderr}
	if ee, ok := err.(*exec.ExitError); ok {
		res.ExitCode = ee.ExitCode()
		return res, nil
	}
	if err != nil {
		return res, fmt.Errorf("exec: %w", err)
	}
	return res, nil
}

// runnerKey is the ctx slot for the active Runner. Tests set this via
// WithRunner; production callers leave it unset, falling back to
// OSRunner.
type runnerCtxKey struct{}

// WithRunner returns a derived context using r as the Runner. Tests
// call this to inject a fakeRunner before invoking a Tool.Execute.
func WithRunner(ctx context.Context, r Runner) context.Context {
	return context.WithValue(ctx, runnerCtxKey{}, r)
}

func runnerFromCtx(ctx context.Context) Runner {
	if r, ok := ctx.Value(runnerCtxKey{}).(Runner); ok && r != nil {
		return r
	}
	return OSRunner{}
}

// runResult is the captured outcome of a child nvoi invocation.
// ExitCode is the OS exit code (0 on success; non-zero from the verb
// or from exec itself). Stdout/Stderr are capped at the values
// runNvoi was called with.
type runResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// runNvoi delegates to the Runner in ctx (OSRunner by default). args[0]
// is the verb (e.g. "deploy"). The -c flag, --database, --project,
// and --json are NOT auto-injected — callers compose argv via
// buildNvoiArgs so per-tool decisions are visible at the call site.
//
// Cwd is set to env.Cwd (the agent's working directory, threaded via
// tools.WithEnv). The child inherits the parent's env — operator-set
// creds (HCLOUD_TOKEN, ANTHROPIC_API_KEY, …) flow through.
//
// stdoutCap / stderrCap bound captured output via cappedWriter. The
// FULL output still flows through the verb itself (e.g. runs JSONL
// to disk in store mode), so truncation is a UX choice not data loss.
//
// Non-zero exit is NOT a Go-level error: callers decide how to
// surface it (some tools want the captured output even on failure to
// feed back to the model). exec-level failures (binary missing,
// permission denied) ARE returned as errors.
func runNvoi(ctx context.Context, args []string, stdoutCap, stderrCap int) (runResult, error) {
	env, err := envFromCtx(ctx)
	if err != nil {
		return runResult{}, err
	}
	return runnerFromCtx(ctx).Run(ctx, env.NvoiBinary, env.Cwd, args, nil, stdoutCap, stderrCap)
}

// runNvoiStdin is the variant for verbs that read JSON from stdin
// (config replace today).
func runNvoiStdin(ctx context.Context, args []string, stdin []byte, stdoutCap, stderrCap int) (runResult, error) {
	env, err := envFromCtx(ctx)
	if err != nil {
		return runResult{}, err
	}
	return runnerFromCtx(ctx).Run(ctx, env.NvoiBinary, env.Cwd, args, stdin, stdoutCap, stderrCap)
}

// cappedWriter buffers at most `cap` bytes of incoming data, then
// appends a "...[truncated N bytes]\n" marker noting the overflow.
// The marker itself is overhead beyond cap — cap bounds the captured
// data, not the buffer's total size. Idempotent across multiple
// overflowing Writes: the marker is rewritten in place each time.
type cappedWriter struct {
	buf       *bytes.Buffer
	cap       int
	truncated int
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	fits := w.cap - w.dataLen()
	if fits < 0 {
		fits = 0
	}
	if fits >= len(p) {
		return w.buf.Write(p)
	}
	if fits > 0 {
		w.buf.Write(p[:fits])
	}
	w.truncated += len(p) - fits
	w.stampMarker()
	return len(p), nil
}

// dataLen returns the buffer's content length excluding any marker
// already written. Used to compute remaining cap correctly across
// repeated overflows.
func (w *cappedWriter) dataLen() int {
	if i := bytes.LastIndex(w.buf.Bytes(), []byte("...[truncated ")); i >= 0 {
		return i
	}
	return w.buf.Len()
}

// stampMarker writes/rewrites the truncation marker. Removes any
// previous marker first so the count stays accurate across overflows.
func (w *cappedWriter) stampMarker() {
	if w.truncated == 0 {
		return
	}
	if i := bytes.LastIndex(w.buf.Bytes(), []byte("...[truncated ")); i >= 0 {
		w.buf.Truncate(i)
	}
	fmt.Fprintf(w.buf, "...[truncated %d bytes]\n", w.truncated)
}
