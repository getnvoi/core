package kube_test

import (
	"context"
	"errors"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtimeobj "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	testingk8s "k8s.io/client-go/testing"

	"github.com/getnvoi/core/pkg/internal/kube"
	"github.com/getnvoi/core/pkg/internal/utils"
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

// TestWaitStatefulSetReady_AlreadyReady locks the success path:
// when the StatefulSet's ReadyReplicas already matches Spec.Replicas
// and ObservedGeneration >= Generation, the wait returns on the
// first poll (no sleep, no timeout). Exercised by deployDatabases
// to gate workload.ApplyAll on the postgres database actually
// serving — without this, `services.X.databases: [...]` consumers
// CrashLoop on first deploy until k8s backoff converges.
func TestWaitStatefulSetReady_AlreadyReady(t *testing.T) {
	replicas := int32(1)
	cs := fake.NewSimpleClientset(&appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "pg",
			Namespace:  "ns",
			Generation: 1,
		},
		Spec: appsv1.StatefulSetSpec{Replicas: &replicas},
		Status: appsv1.StatefulSetStatus{
			ReadyReplicas:      1,
			ObservedGeneration: 1,
		},
	})
	c := kube.NewForTest(cs)
	if err := c.WaitStatefulSetReady(context.Background(), "ns", "pg"); err != nil {
		t.Fatalf("WaitStatefulSetReady: %v", err)
	}
}

// TestWaitStatefulSetReady_ContextCanceledReturnsError locks the
// error path: when ctx expires before ReadyReplicas catches up, the
// wait returns an error that wraps ctx.Err(). Uses an
// already-canceled context so the wait returns on the FIRST tick
// after the (failed) initial poll, without depending on the
// internal 5-minute timeout. Skipped in -short.
func TestWaitStatefulSetReady_ContextCanceledReturnsError(t *testing.T) {
	if testing.Short() {
		t.Skip("polling wait — skipped under -short")
	}
	replicas := int32(1)
	cs := fake.NewSimpleClientset(&appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "pg", Namespace: "ns", Generation: 1},
		Spec:       appsv1.StatefulSetSpec{Replicas: &replicas},
		Status: appsv1.StatefulSetStatus{
			ReadyReplicas:      0, // not ready
			ObservedGeneration: 1,
		},
	})
	c := kube.NewForTest(cs)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-canceled so the wait fails on the first tick rather than after 5min
	err := c.WaitStatefulSetReady(ctx, "ns", "pg")
	if err == nil {
		t.Fatalf("expected error from canceled context, got nil")
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

// TestDeletePVC_BlocksUntilGone locks the contract that broke
// rollback in production: DeletePVC must NOT return until the API
// server confirms the PVC is gone. The fake clientset's Delete is
// synchronous (no finalizers in play), so this test exercises the
// happy path: Delete → Get NotFound → return nil.
//
// The pathological case (PVC stuck Terminating) is structurally
// covered by TestDeletePVC_TimeoutWhenStuck below.
func TestDeletePVC_BlocksUntilGone(t *testing.T) {
	cs := fake.NewSimpleClientset(
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pg-data", Namespace: "ns"}},
	)
	c := kube.NewForTest(cs)
	if err := c.DeletePVC(context.Background(), "ns", "pg-data"); err != nil {
		t.Fatalf("DeletePVC: %v", err)
	}
	_, err := cs.CoreV1().PersistentVolumeClaims("ns").Get(context.Background(), "pg-data", metav1.GetOptions{})
	if err == nil {
		t.Fatalf("PVC still present after DeletePVC returned — wait was a no-op")
	}
}

// TestDeletePVC_TimeoutWhenStuck locks the timeout behavior: a PVC
// that never finalizes (Get keeps returning the object after Delete)
// makes DeletePVC return ErrTimeout rather than hang forever.
// Reproduces the failure mode rollback hit in production — except
// this time it surfaces as a clean error instead of a silent zombie
// PVC.
//
// The fake clientset's Delete normally removes the object outright
// (no finalizer-respecting semantics), so we install a "delete"
// reactor that intercepts the Delete and returns success without
// actually removing the object. After that, subsequent Get calls
// keep returning the PVC — exactly the Terminating state real
// Kubernetes leaves behind with active finalizers.
//
// Short timing knobs (20ms / 200ms) so the test runs in under a
// second.
func TestDeletePVC_TimeoutWhenStuck(t *testing.T) {
	prevPoll, prevMax := kube.PVCDeletionTimingForTest()
	kube.SetPVCDeletionTiming(20*time.Millisecond, 200*time.Millisecond)
	defer kube.SetPVCDeletionTiming(prevPoll, prevMax)

	cs := fake.NewSimpleClientset(
		&corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "stuck",
				Namespace:  "ns",
				Finalizers: []string{"example.com/stuck"},
			},
		},
	)
	// Intercept Delete so the object stays in the tracker — mimics
	// real k8s behavior where Delete on an object with finalizers
	// only sets deletionTimestamp.
	cs.PrependReactor("delete", "persistentvolumeclaims", func(action testingk8s.Action) (bool, runtimeobj.Object, error) {
		return true, nil, nil
	})
	c := kube.NewForTest(cs)
	err := c.DeletePVC(context.Background(), "ns", "stuck")
	if err == nil {
		t.Fatalf("expected ErrTimeout on stuck PVC, got nil")
	}
	if !errors.Is(err, utils.ErrTimeout) {
		t.Errorf("expected ErrTimeout, got %v", err)
	}
}
