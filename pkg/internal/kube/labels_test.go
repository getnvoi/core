package kube_test

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/getnvoi/core/pkg/internal/kube"
)

func TestLabelNode_AppliesLabel(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "nvoi-hello-dev-master",
			Labels: map[string]string{"existing": "preserved"},
		},
	}
	cs := fake.NewSimpleClientset(node)
	kc := kube.NewForTest(cs)

	if err := kc.LabelNode(context.Background(), node.Name, "nvoi-role", "master"); err != nil {
		t.Fatalf("LabelNode: %v", err)
	}

	got, err := cs.CoreV1().Nodes().Get(context.Background(), node.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Labels["nvoi-role"] != "master" {
		t.Errorf("nvoi-role: got %q want master", got.Labels["nvoi-role"])
	}
	if got.Labels["existing"] != "preserved" {
		t.Errorf("strategic-merge-patch must preserve existing labels; got %v", got.Labels)
	}
}

func TestLabelNode_Overwrites(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "nvoi-hello-dev-master",
			Labels: map[string]string{"nvoi-role": "stale"},
		},
	}
	cs := fake.NewSimpleClientset(node)
	kc := kube.NewForTest(cs)

	if err := kc.LabelNode(context.Background(), node.Name, "nvoi-role", "master"); err != nil {
		t.Fatalf("LabelNode: %v", err)
	}

	got, _ := cs.CoreV1().Nodes().Get(context.Background(), node.Name, metav1.GetOptions{})
	if got.Labels["nvoi-role"] != "master" {
		t.Errorf("overwrite failed: got %q want master", got.Labels["nvoi-role"])
	}
}

func TestLabelNode_MissingNode_ReturnsError(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewForTest(cs)

	err := kc.LabelNode(context.Background(), "ghost-node", "nvoi-role", "master")
	if err == nil {
		t.Error("expected error patching nonexistent node")
	}
}
