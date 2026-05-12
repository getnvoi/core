package deploy

import (
	"context"
	"strings"
	"testing"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/log"
	"github.com/getnvoi/core/pkg/runtime"
)

// Monitor's pre-flight check: missing monitor: block errors with a
// clear operator-facing message before any tunnel attempt.
// The full happy path is untested-by-design (kube tunnel + SPDY
// port-forward require a real apiserver — same exclusion list as
// kube.New per CLAUDE.md).
func TestMonitor_ErrorsWhenMonitorBlockMissing(t *testing.T) {
	rt := &runtime.Runtime{
		Cfg: &config.Config{App: "x", Env: "y"}, // no Monitor
		Log: log.NewWith(true, &nopWriter{}),
	}
	err := Monitor(context.Background(), rt, 3000)
	if err == nil {
		t.Fatal("expected error when monitor: not configured")
	}
	if !strings.Contains(err.Error(), "monitor:") {
		t.Errorf("error should mention monitor: block, got %v", err)
	}
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }
