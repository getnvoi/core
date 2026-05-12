package observability_test

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/getnvoi/core/pkg/internal/kube"
	"github.com/getnvoi/core/pkg/internal/observability"
)

// EnsureNamespace contract: idempotent owner-labeled namespace
// creation. Lives in pkg/internal/observability as a helper because
// it's the observability package's gateway to its own scope. The
// underlying typed apply lives on kube.Client.
func TestNamespaceConst(t *testing.T) {
	if observability.Namespace != "nvoi-observability" {
		t.Errorf("Namespace const drift: %q", observability.Namespace)
	}
}

// Smoke: ApplyOwned on a Namespace with OwnerObservability stamps the
// label and lands the object. The Namespace const + ApplyOwned path
// are the two halves of "ensure the observability namespace".
func TestEnsureNamespaceViaApplyOwned(t *testing.T) {
	cs := fake.NewSimpleClientset()
	c := kube.NewForTest(cs)

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: observability.Namespace}}
	if err := c.ApplyOwned(context.Background(), kube.Scope{Owner: kube.OwnerObservability}, ns); err != nil {
		t.Fatalf("ApplyOwned Namespace: %v", err)
	}
	got, err := cs.CoreV1().Namespaces().Get(context.Background(), observability.Namespace, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Labels[kube.LabelOwner] != kube.OwnerObservability {
		t.Errorf("owner label missing: %v", got.Labels)
	}
}
