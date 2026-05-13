package utils

import (
	"context"
	"errors"
	"time"
)

// ErrTimeout is returned by Poll when the deadline elapses before fn
// returns true. Callers that want to attach diagnostics on timeout
// errors.Is against this sentinel and fan out into a richer error.
var ErrTimeout = errors.New("poll: timeout exceeded")

// Poll calls fn every interval until it returns true or the timeout
// elapses. Respects ctx cancellation between intervals. Returns
// ErrTimeout on deadline, ctx.Err() on cancellation, and any error
// fn returns immediately.
//
// Used by wait-for-job and wait-for-rollout paths in pkg/internal/kube
// — kept here (utility) rather than in kube so unrelated polling
// callers (registry digest backoff, ssh dial retry) can reuse it.
func Poll(ctx context.Context, interval, timeout time.Duration, fn func() (bool, error)) error {
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return ErrTimeout
		}
		if err := ctx.Err(); err != nil {
			return context.Cause(ctx)
		}
		done, err := fn()
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-time.After(interval):
		}
	}
}
