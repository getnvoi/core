package kube_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/getnvoi/core/pkg/internal/kube"
)

// caddyPod returns a stub Pod carrying the labels FirstPod's
// app.kubernetes.io/name selector requires.
func caddyPod(name, app string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: kube.CaddyNamespace,
			Labels:    map[string]string{"app.kubernetes.io/name": app},
		},
	}
}

// fakePodReady seeds a fake clientset with a Caddy Deployment that's
// already Available + a matching pod. EnsureCaddy's wait loop sees
// Ready immediately so tests don't sleep.
func fakePodReady() *fake.Clientset {
	one := int32(1)
	return fake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      kube.CaddyName,
				Namespace: kube.CaddyNamespace,
			},
			Spec: appsv1.DeploymentSpec{Replicas: &one},
			Status: appsv1.DeploymentStatus{
				ReadyReplicas:      1,
				ObservedGeneration: 0, // Generation defaults to 0 — Status.ObservedGeneration >= Generation
			},
		},
	)
}

func TestEnsureCaddy_AppliesAllFour(t *testing.T) {
	defer kube.SetCaddyTimingForTest(time.Millisecond, 50*time.Millisecond)()

	cs := fakePodReady()
	c := kube.NewForTest(cs)

	if err := c.EnsureCaddy(context.Background()); err != nil {
		t.Fatalf("EnsureCaddy: %v", err)
	}

	for _, kind := range []kube.Kind{kube.KindPVC, kube.KindConfigMap, kube.KindService, kube.KindDeployment} {
		names, err := c.ListOwned(context.Background(), kube.Scope{Namespace: kube.CaddyNamespace, Owner: kube.OwnerCaddy}, kind)
		if err != nil {
			t.Errorf("ListOwned %s: %v", kind, err)
			continue
		}
		if len(names) == 0 {
			t.Errorf("EnsureCaddy did not apply any %s with owner=caddy", kind)
		}
	}
}

func TestEnsureCaddy_DeploymentNotReady_TimesOut(t *testing.T) {
	defer kube.SetCaddyTimingForTest(time.Millisecond, 20*time.Millisecond)()

	// Empty clientset — no pre-seeded Deployment, so EnsureCaddy
	// applies it but the fake never reports Ready.
	c := kube.NewForTest(fake.NewSimpleClientset())

	err := c.EnsureCaddy(context.Background())
	if err == nil {
		t.Error("expected timeout error when Deployment never reaches Ready")
	}
}

// fakeExec captures every Exec call + can return canned output.
type fakeExec struct {
	calls    []kube.ExecRequest
	stdinBuf bytes.Buffer
	respond  func(req kube.ExecRequest) error
}

func (f *fakeExec) handle(_ context.Context, req kube.ExecRequest) error {
	f.calls = append(f.calls, req)
	if req.Stdin != nil {
		_, _ = io.Copy(&f.stdinBuf, req.Stdin)
	}
	if f.respond != nil {
		return f.respond(req)
	}
	return nil
}

func TestReloadCaddyConfig_PostsViaExec(t *testing.T) {
	cs := fakePodReady()
	// Add a pod so firstCaddyPod resolves.
	_, _ = cs.CoreV1().Pods(kube.CaddyNamespace).Create(context.Background(),
		caddyPod("caddy-abc", "caddy"), metav1.CreateOptions{})
	c := kube.NewForTest(cs)

	fe := &fakeExec{}
	c.ExecFunc = fe.handle

	configJSON := []byte(`{"admin":{"listen":"localhost:2019"}}`)
	if err := c.ReloadCaddyConfig(context.Background(), configJSON); err != nil {
		t.Fatalf("ReloadCaddyConfig: %v", err)
	}
	if len(fe.calls) != 1 {
		t.Fatalf("exec calls: got %d want 1", len(fe.calls))
	}
	got := fe.calls[0]
	if got.Pod != "caddy-abc" || got.Container != kube.CaddyName {
		t.Errorf("pod/container: %s/%s", got.Pod, got.Container)
	}
	cmdJoined := strings.Join(got.Command, " ")
	if !strings.Contains(cmdJoined, "/load") || !strings.Contains(cmdJoined, "localhost:2019") {
		t.Errorf("expected POST to admin /load, got %v", got.Command)
	}
	if !bytes.Equal(fe.stdinBuf.Bytes(), configJSON) {
		t.Errorf("stdin mismatch: got %q want %q", fe.stdinBuf.Bytes(), configJSON)
	}
}

func TestReloadCaddyConfig_RejectionSurfacedVerbatim(t *testing.T) {
	cs := fakePodReady()
	_, _ = cs.CoreV1().Pods(kube.CaddyNamespace).Create(context.Background(),
		caddyPod("caddy-abc", "caddy"), metav1.CreateOptions{})
	c := kube.NewForTest(cs)

	fe := &fakeExec{
		respond: func(req kube.ExecRequest) error {
			// Caddy's --fail-with-body writes the rejection body to stdout
			// before exiting non-zero.
			_, _ = req.Stdout.Write([]byte("invalid handler module"))
			return errors.New("exit 22")
		},
	}
	c.ExecFunc = fe.handle

	err := c.ReloadCaddyConfig(context.Background(), []byte("{}"))
	if err == nil || !strings.Contains(err.Error(), "invalid handler module") {
		t.Errorf("expected verbatim rejection in error, got %v", err)
	}
}

func TestWaitForCaddyCert_PollsForFile(t *testing.T) {
	defer kube.SetCaddyTimingForTest(time.Millisecond, 20*time.Millisecond)()
	cs := fakePodReady()
	_, _ = cs.CoreV1().Pods(kube.CaddyNamespace).Create(context.Background(),
		caddyPod("caddy-abc", "caddy"), metav1.CreateOptions{})
	c := kube.NewForTest(cs)

	attempts := 0
	c.ExecFunc = func(_ context.Context, req kube.ExecRequest) error {
		attempts++
		if attempts < 3 {
			return errors.New("not yet")
		}
		return nil
	}

	if err := c.WaitForCaddyCert(context.Background(), "www.nvoi.to"); err != nil {
		t.Fatalf("WaitForCaddyCert: %v", err)
	}
	if attempts < 3 {
		t.Errorf("expected ≥3 attempts, got %d", attempts)
	}
}
