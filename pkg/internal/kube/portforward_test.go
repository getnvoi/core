package kube_test

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/getnvoi/core/pkg/internal/kube"
)

// FirstReadyPodFor is the public-effect of PortForward's pod-resolution
// step. Tested via the fake clientset; the actual port-forward dial
// requires a real apiserver and lives behind the same
// "untested-by-design" rubric as kube.New (per CLAUDE.md). Resolution
// is what surfaces the operator-facing error messages, so it's worth
// covering.
//
// We exercise it via the exported PortForward call path that errors
// in resolution before reaching the SPDY dial — PortForward itself
// is unexported tests in this package access via a thin wrapper if
// the resolution helper is package-internal.

// To keep firstReadyPodFor testable without exporting it, this test
// covers PortForward through its first-failure path (no Ready pod →
// returns error mentioning the service).
func TestPortForward_ResolutionFails_NoService(t *testing.T) {
	cs := fake.NewSimpleClientset()
	c := kube.NewForTest(cs)
	// nil ssh is fine — error surfaces from kube resolution before
	// the SSH dial is reached.
	err := c.PortForward(context.Background(), nil, "obs", "grafana", 3000, 3000)
	if err == nil {
		t.Fatal("expected error (no Service exists)")
	}
}

func TestPortForward_ResolutionFails_NoReadyPodWithIP(t *testing.T) {
	cs := fake.NewSimpleClientset(
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "grafana", Namespace: "obs"},
			Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "grafana"}},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "grafana-not-ready", Namespace: "obs", Labels: map[string]string{"app": "grafana"}},
			Status: corev1.PodStatus{
				PodIP:      "10.42.0.7",
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse}},
			},
		},
	)
	c := kube.NewForTest(cs)
	err := c.PortForward(context.Background(), nil, "obs", "grafana", 3000, 3000)
	if err == nil {
		t.Fatal("expected error (pod not Ready)")
	}
}
