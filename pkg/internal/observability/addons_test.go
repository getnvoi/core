package observability_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/getnvoi/core/pkg/internal/kube"
	"github.com/getnvoi/core/pkg/internal/observability"
	"github.com/getnvoi/core/pkg/internal/testutil/sshfake"
	"github.com/getnvoi/core/pkg/log"
)

// newTestLogger returns a log.Log writing JSONL to a buffer the test
// can grep. Real text-vs-JSONL projection is covered in pkg/log; here
// we only care about presence of specific Warn / Info / Step calls.
func newTestLogger(t *testing.T) (log.Log, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	return log.NewWith(true, &buf), &buf
}

func TestApplyMetricsServer_HappyPath(t *testing.T) {
	sh := &sshfake.Shell{} // every command succeeds with empty stdout
	lg, _ := newTestLogger(t)

	if err := observability.ApplyMetricsServer(context.Background(), sh, lg); err != nil {
		t.Fatalf("ApplyMetricsServer: %v", err)
	}

	// Expected command sequence in order: apply → wait → 4 label patches.
	if len(sh.Calls) != 6 {
		t.Fatalf("expected 6 ssh calls, got %d: %v", len(sh.Calls), sh.Calls)
	}

	// 1: apply -f <url>
	if !strings.Contains(sh.Calls[0], "kubectl apply -f "+observability.MetricsServerURL) {
		t.Errorf("call[0] not the apply: %q", sh.Calls[0])
	}
	// 2: wait --for=condition=Available deployment/metrics-server
	if !strings.Contains(sh.Calls[1], "wait --for=condition=Available") ||
		!strings.Contains(sh.Calls[1], "deployment/metrics-server") {
		t.Errorf("call[1] not the wait: %q", sh.Calls[1])
	}
	// 3-6: label patches — order matches metricsServerOwned.
	wantLabelTargets := []string{
		"deployment metrics-server",
		"service metrics-server",
		"serviceaccount metrics-server",
		"apiservice v1beta1.metrics.k8s.io",
	}
	for i, want := range wantLabelTargets {
		call := sh.Calls[2+i]
		if !strings.Contains(call, "label "+want) {
			t.Errorf("call[%d] not the expected label patch (want target %q): %q", 2+i, want, call)
		}
		if !strings.Contains(call, kube.LabelOwner+"="+kube.OwnerAddons) {
			t.Errorf("call[%d] missing owner label %s=%s: %q", 2+i, kube.LabelOwner, kube.OwnerAddons, call)
		}
		if !strings.Contains(call, "--overwrite") {
			t.Errorf("call[%d] missing --overwrite: %q", 2+i, call)
		}
	}
}

func TestApplyMetricsServer_ApplyFails(t *testing.T) {
	sh := &sshfake.Shell{
		Matchers: []sshfake.Match{
			{Contains: "apply -f", Resp: sshfake.Response{
				Stdout: []byte("connection refused"),
				Err:    errors.New("exit status 1"),
			}},
		},
	}
	lg, _ := newTestLogger(t)

	err := observability.ApplyMetricsServer(context.Background(), sh, lg)
	if err == nil {
		t.Fatal("expected error from failing apply")
	}
	if !strings.Contains(err.Error(), "apply metrics-server") {
		t.Errorf("error not wrapped: %v", err)
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error missing stdout context: %v", err)
	}
	// Only the apply call ran; wait + label patches should be skipped.
	if len(sh.Calls) != 1 {
		t.Errorf("expected fail-fast at 1 call, got %d: %v", len(sh.Calls), sh.Calls)
	}
}

func TestApplyMetricsServer_WaitTimeout(t *testing.T) {
	sh := &sshfake.Shell{
		Matchers: []sshfake.Match{
			{Contains: "wait --for=condition=Available", Resp: sshfake.Response{
				Stdout: []byte("timed out waiting for the condition"),
				Err:    errors.New("exit status 1"),
			}},
		},
	}
	lg, _ := newTestLogger(t)

	err := observability.ApplyMetricsServer(context.Background(), sh, lg)
	if err == nil {
		t.Fatal("expected error from wait timeout")
	}
	if !strings.Contains(err.Error(), "wait metrics-server") {
		t.Errorf("error not wrapped: %v", err)
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error missing stdout context: %v", err)
	}
	if len(sh.Calls) != 2 {
		t.Errorf("expected stop at 2 calls (apply + wait), got %d", len(sh.Calls))
	}
}

func TestApplyMetricsServer_DeploymentLabelFailure_IsFatal(t *testing.T) {
	// Deployment label is mandatory — ListOwned filters by it.
	sh := &sshfake.Shell{
		Matchers: []sshfake.Match{
			{Contains: "label deployment metrics-server", Resp: sshfake.Response{
				Stdout: []byte("not found"),
				Err:    errors.New("exit status 1"),
			}},
		},
	}
	lg, _ := newTestLogger(t)

	err := observability.ApplyMetricsServer(context.Background(), sh, lg)
	if err == nil {
		t.Fatal("expected error when deployment label patch fails")
	}
	if !strings.Contains(err.Error(), "label deployment/metrics-server") {
		t.Errorf("error not the deployment-label path: %v", err)
	}
}

func TestApplyMetricsServer_AuxiliaryLabelFailure_IsWarn(t *testing.T) {
	// Service / SA / apiservice labels are best-effort. Failure on one
	// must not abort the install — Deployment label is what matters.
	sh := &sshfake.Shell{
		Matchers: []sshfake.Match{
			{Contains: "label apiservice", Resp: sshfake.Response{
				Stdout: []byte("not found"),
				Err:    errors.New("exit status 1"),
			}},
		},
	}
	lg, buf := newTestLogger(t)

	if err := observability.ApplyMetricsServer(context.Background(), sh, lg); err != nil {
		t.Fatalf("auxiliary label failure should not be fatal: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"level":"warn"`)) {
		t.Errorf("expected a warn event in the log for auxiliary failure; got:\n%s", buf.String())
	}
	if !bytes.Contains(buf.Bytes(), []byte("apiservice/v1beta1.metrics.k8s.io")) {
		t.Errorf("warn message missing the failing target; got:\n%s", buf.String())
	}
}
