package deploy

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/getnvoi/core/pkg/internal/kube"
	"github.com/getnvoi/core/pkg/providers"
)

func TestEnsureDatabaseNodeUnchanged_RejectsServerDrift(t *testing.T) {
	cs := fake.NewSimpleClientset(&appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "nvoi-myapp-prod-db-app", Namespace: "default"},
		Spec: appsv1.StatefulSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					NodeSelector: map[string]string{"nvoi-role": "db-a"},
				},
			},
		},
	})
	kc := kube.NewForTest(cs)
	req := providers.DatabaseRequest{
		Name:      "app",
		FullName:  "nvoi-myapp-prod-db-app",
		Namespace: "default",
		Spec: providers.DatabaseSpec{
			Server: "db-b",
		},
	}

	err := ensureDatabaseNodeUnchanged(context.Background(), kc, req)
	if err == nil {
		t.Fatal("expected drift error, got nil")
	}
	if !strings.Contains(err.Error(), "migrate is required") {
		t.Fatalf("error = %v, want migrate-required message", err)
	}
}

func TestEnsureDatabaseNodeUnchanged_AllowsFirstDeployAndStableNode(t *testing.T) {
	kc := kube.NewForTest(fake.NewSimpleClientset())
	req := providers.DatabaseRequest{
		Name:      "app",
		FullName:  "nvoi-myapp-prod-db-app",
		Namespace: "default",
		Spec: providers.DatabaseSpec{
			Server: "db-a",
		},
	}
	if err := ensureDatabaseNodeUnchanged(context.Background(), kc, req); err != nil {
		t.Fatalf("missing statefulset should be allowed: %v", err)
	}

	cs := fake.NewSimpleClientset(&appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "nvoi-myapp-prod-db-app", Namespace: "default"},
		Spec: appsv1.StatefulSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					NodeSelector: map[string]string{"nvoi-role": "db-a"},
				},
			},
		},
	})
	kc = kube.NewForTest(cs)
	if err := ensureDatabaseNodeUnchanged(context.Background(), kc, req); err != nil {
		t.Fatalf("stable node should be allowed: %v", err)
	}
}
