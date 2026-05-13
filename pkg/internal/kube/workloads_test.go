package kube_test

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/getnvoi/core/pkg/internal/kube"
)

func TestGetStatefulSet_Exists(t *testing.T) {
	cs := fake.NewSimpleClientset(&appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "pg", Namespace: "ns"},
		Spec: appsv1.StatefulSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					NodeSelector: map[string]string{"nvoi-role": "db-worker"},
				},
			},
		},
	})
	c := kube.NewForTest(cs)
	got, err := c.GetStatefulSet(context.Background(), "ns", "pg")
	if err != nil {
		t.Fatalf("GetStatefulSet: %v", err)
	}
	if got == nil {
		t.Fatalf("got nil, want StatefulSet")
	}
	if got.Spec.Template.Spec.NodeSelector["nvoi-role"] != "db-worker" {
		t.Errorf("nodeSelector lost on round-trip")
	}
}

func TestGetStatefulSet_Missing_ReturnsNilNil(t *testing.T) {
	c := kube.NewForTest(fake.NewSimpleClientset())
	got, err := c.GetStatefulSet(context.Background(), "ns", "missing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("got %v, want nil for absent StatefulSet", got)
	}
}

func TestDeleteByName_DeploymentAndService(t *testing.T) {
	cs := fake.NewSimpleClientset(
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns"}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns"}},
	)
	c := kube.NewForTest(cs)
	if err := c.DeleteByName(context.Background(), "ns", "api"); err != nil {
		t.Fatalf("DeleteByName: %v", err)
	}
	if _, err := cs.AppsV1().Deployments("ns").Get(context.Background(), "api", metav1.GetOptions{}); err == nil {
		t.Errorf("deployment still present after delete")
	}
	if _, err := cs.CoreV1().Services("ns").Get(context.Background(), "api", metav1.GetOptions{}); err == nil {
		t.Errorf("service still present after delete")
	}
}

func TestDeleteByName_StatefulSet(t *testing.T) {
	cs := fake.NewSimpleClientset(
		&appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "pg", Namespace: "ns"}},
	)
	c := kube.NewForTest(cs)
	if err := c.DeleteByName(context.Background(), "ns", "pg"); err != nil {
		t.Fatalf("DeleteByName: %v", err)
	}
	if _, err := cs.AppsV1().StatefulSets("ns").Get(context.Background(), "pg", metav1.GetOptions{}); err == nil {
		t.Errorf("statefulset still present after delete")
	}
}

func TestDeleteByName_AllAbsent_Idempotent(t *testing.T) {
	c := kube.NewForTest(fake.NewSimpleClientset())
	if err := c.DeleteByName(context.Background(), "ns", "missing"); err != nil {
		t.Errorf("expected idempotent NotFound, got %v", err)
	}
}

func TestDeletePVC_Idempotent(t *testing.T) {
	cs := fake.NewSimpleClientset(
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pg-data", Namespace: "ns"}},
	)
	c := kube.NewForTest(cs)
	if err := c.DeletePVC(context.Background(), "ns", "pg-data"); err != nil {
		t.Fatalf("DeletePVC: %v", err)
	}
	// Second delete on the now-absent PVC must succeed.
	if err := c.DeletePVC(context.Background(), "ns", "pg-data"); err != nil {
		t.Errorf("idempotent re-delete failed: %v", err)
	}
}
