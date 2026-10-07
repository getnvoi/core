package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// The agent is the container's CMD, so stopping the container is a signal to this process. We own
// the pipes of every command we started, so sleeping quietly severed whatever was mid-write.

func listen(t *testing.T) net.Listener {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	return ln
}

func TestServeReturnsNilWhenTheContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ln := listen(t)

	done := make(chan error, 1)
	go func() { done <- serve(ctx, ln, http.NewServeMux()) }()

	cancel()

	select {
	case err := <-done:
		// Exit zero. A signal we asked for is not a failure, and reporting one
		// would put every ordinary sleep in the logs as a fault.
		if err != nil {
			t.Fatalf("a requested shutdown reported an error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return after its context was cancelled")
	}
}

func TestServeDrainsAnInFlightRequest(t *testing.T) {
	// The whole point: a turn's output is written through this process, so a stop that severs a
	// response loses bytes that exist nowhere else, and the box is gone before anybody notices.
	ctx, cancel := context.WithCancel(context.Background())
	ln := listen(t)

	started := make(chan struct{})
	release := make(chan struct{})

	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = w.Write([]byte("finished"))
	})

	done := make(chan error, 1)
	go func() { done <- serve(ctx, ln, mux) }()

	type result struct {
		body string
		err  error
	}

	got := make(chan result, 1)

	go func() {
		response, err := http.Get("http://" + ln.Addr().String() + "/slow")
		if err != nil {
			got <- result{err: err}
			return
		}
		defer func() { _ = response.Body.Close() }()

		body, err := io.ReadAll(response.Body)
		got <- result{body: string(body), err: err}
	}()

	<-started
	cancel()

	// The stop is now in progress WHILE a handler is still writing. Before the
	// drain, this is where the process died.
	release <- struct{}{}

	select {
	case r := <-got:
		if r.err != nil {
			t.Fatalf("the in-flight request was severed: %v", r.err)
		}
		if r.body != "finished" {
			t.Fatalf("the in-flight response was truncated: %q", r.body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the in-flight request never completed")
	}

	if err := <-done; err != nil {
		t.Fatalf("serve reported an error draining cleanly: %v", err)
	}
}

func TestServeReportsAListenerThatDies(t *testing.T) {
	// Not every ending is a shutdown. A listener that fails on its own is a real failure and must
	// not be reported as a clean stop, or a container that can never bind :8080 would look like
	// one that rested.
	ln := listen(t)
	_ = ln.Close()

	err := serve(context.Background(), ln, http.NewServeMux())

	if err == nil {
		t.Fatal("a dead listener was reported as a clean shutdown")
	}
	if errors.Is(err, http.ErrServerClosed) {
		t.Fatal("a dead listener was reported as a requested close")
	}
}
