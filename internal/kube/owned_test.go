package kube_test

import (
	"context"
	"sort"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/getnvoi/core/internal/kube"
)

// labeled returns the standard owner-label set ApplyOwned would stamp.
// Used to seed the fake clientset for SweepOwned / ListOwned tests
// without going through ApplyOwned itself.
func labeled(owner string) map[string]string {
	return map[string]string{kube.LabelOwner: owner}
}

// ── ApplyOwned ────────────────────────────────────────────────────

func TestApplyOwned_StampsOwnerLabel_OnCreate(t *testing.T) {
	cs := fake.NewSimpleClientset()
	c := kube.NewForTest(cs)

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api"},
		Spec:       appsv1.DeploymentSpec{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"k": "v"}}},
	}
	if err := c.ApplyOwned(context.Background(), kube.Scope{Namespace: "ns", Owner: kube.OwnerServices}, dep); err != nil {
		t.Fatalf("ApplyOwned: %v", err)
	}
	got, err := cs.AppsV1().Deployments("ns").Get(context.Background(), "api", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Labels[kube.LabelOwner] != kube.OwnerServices {
		t.Errorf("owner label: got %v want %s=%s", got.Labels, kube.LabelOwner, kube.OwnerServices)
	}
}

func TestApplyOwned_PreservesExistingLabels(t *testing.T) {
	cs := fake.NewSimpleClientset()
	c := kube.NewForTest(cs)

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "api",
			Labels: map[string]string{"app": "external", "version": "v1"},
		},
		Spec: appsv1.DeploymentSpec{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"k": "v"}}},
	}
	if err := c.ApplyOwned(context.Background(), kube.Scope{Namespace: "ns", Owner: kube.OwnerServices}, dep); err != nil {
		t.Fatalf("ApplyOwned: %v", err)
	}
	got, _ := cs.AppsV1().Deployments("ns").Get(context.Background(), "api", metav1.GetOptions{})
	if got.Labels["app"] != "external" || got.Labels["version"] != "v1" {
		t.Errorf("existing labels not preserved: %v", got.Labels)
	}
	if got.Labels[kube.LabelOwner] != kube.OwnerServices {
		t.Errorf("owner not stamped: %v", got.Labels)
	}
}

func TestApplyOwned_EmptyOwner_Errors(t *testing.T) {
	cs := fake.NewSimpleClientset()
	c := kube.NewForTest(cs)
	err := c.ApplyOwned(context.Background(), kube.Scope{Namespace: "ns"}, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "x"}})
	if err == nil {
		t.Error("expected error for empty owner")
	}
}

func TestApplyOwned_UnsupportedKind_Errors(t *testing.T) {
	cs := fake.NewSimpleClientset()
	c := kube.NewForTest(cs)
	err := c.ApplyOwned(context.Background(), kube.Scope{Namespace: "ns", Owner: kube.OwnerServices}, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "x"}})
	if err == nil {
		t.Error("expected error for unsupported kind (Pod)")
	}
}

// ── SweepOwned ────────────────────────────────────────────────────

func TestSweepOwned_DeletesOrphan_KeepsDesired(t *testing.T) {
	cs := fake.NewSimpleClientset(
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "keep", Namespace: "ns", Labels: labeled(kube.OwnerServices)}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "stale", Namespace: "ns", Labels: labeled(kube.OwnerServices)}},
	)
	c := kube.NewForTest(cs)

	if err := c.SweepOwned(context.Background(), kube.Scope{Namespace: "ns", Owner: kube.OwnerServices}, kube.KindDeployment, []string{"keep"}); err != nil {
		t.Fatalf("SweepOwned: %v", err)
	}
	if _, err := cs.AppsV1().Deployments("ns").Get(context.Background(), "keep", metav1.GetOptions{}); err != nil {
		t.Errorf("keep should survive: %v", err)
	}
	if _, err := cs.AppsV1().Deployments("ns").Get(context.Background(), "stale", metav1.GetOptions{}); err == nil {
		t.Error("stale should have been deleted")
	}
}

// Migration cleanup: SweepOwned with desired=nil purges every resource
// of the owner+kind. This is the cross-mode primitive for the
// caddy ↔ tunnel transition (operator flips providers.tunnel; old
// mode's footprint sweeps in one call).
func TestSweepOwned_NilDesired_PurgesAll(t *testing.T) {
	cs := fake.NewSimpleClientset(
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "caddy", Namespace: "kube-system", Labels: labeled(kube.OwnerCaddy)}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "caddy", Namespace: "kube-system", Labels: labeled(kube.OwnerCaddy)}},
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "caddy-data", Namespace: "kube-system", Labels: labeled(kube.OwnerCaddy)}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "caddy-config", Namespace: "kube-system", Labels: labeled(kube.OwnerCaddy)}},
	)
	c := kube.NewForTest(cs)

	for _, kind := range []kube.Kind{kube.KindDeployment, kube.KindService, kube.KindPVC, kube.KindConfigMap} {
		if err := c.SweepOwned(context.Background(), kube.Scope{Namespace: "kube-system", Owner: kube.OwnerCaddy}, kind, nil); err != nil {
			t.Fatalf("SweepOwned %s: %v", kind, err)
		}
	}

	// Everything caddy-owned is gone.
	if _, err := cs.AppsV1().Deployments("kube-system").Get(context.Background(), "caddy", metav1.GetOptions{}); err == nil {
		t.Error("caddy Deployment should have been purged")
	}
	if _, err := cs.CoreV1().Services("kube-system").Get(context.Background(), "caddy", metav1.GetOptions{}); err == nil {
		t.Error("caddy Service should have been purged")
	}
	if _, err := cs.CoreV1().PersistentVolumeClaims("kube-system").Get(context.Background(), "caddy-data", metav1.GetOptions{}); err == nil {
		t.Error("caddy PVC should have been purged")
	}
}

// Owner-scoping: SweepOwned NEVER touches resources owned by another
// step. The label discriminator is the discipline.
func TestSweepOwned_NeverCrossesOwnerBoundaries(t *testing.T) {
	cs := fake.NewSimpleClientset(
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "ns", Labels: labeled(kube.OwnerServices)}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "caddy", Namespace: "ns", Labels: labeled(kube.OwnerCaddy)}},
		// Unmanaged — no owner label.
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "external", Namespace: "ns"}},
	)
	c := kube.NewForTest(cs)

	// Sweep all services-owned. Caddy Deployment + external Deployment
	// must remain untouched.
	if err := c.SweepOwned(context.Background(), kube.Scope{Namespace: "ns", Owner: kube.OwnerServices}, kube.KindDeployment, nil); err != nil {
		t.Fatalf("SweepOwned: %v", err)
	}
	if _, err := cs.AppsV1().Deployments("ns").Get(context.Background(), "caddy", metav1.GetOptions{}); err != nil {
		t.Errorf("caddy-owned Deployment must survive a services sweep: %v", err)
	}
	if _, err := cs.AppsV1().Deployments("ns").Get(context.Background(), "external", metav1.GetOptions{}); err != nil {
		t.Errorf("unmanaged Deployment must survive a services sweep: %v", err)
	}
	if _, err := cs.AppsV1().Deployments("ns").Get(context.Background(), "web", metav1.GetOptions{}); err == nil {
		t.Error("services-owned Deployment should have been swept")
	}
}

func TestSweepOwned_EmptyOwner_Errors(t *testing.T) {
	cs := fake.NewSimpleClientset()
	c := kube.NewForTest(cs)
	if err := c.SweepOwned(context.Background(), kube.Scope{Namespace: "ns"}, kube.KindDeployment, nil); err == nil {
		t.Error("expected error for empty owner")
	}
}

func TestSweepOwned_UnsupportedKind_Errors(t *testing.T) {
	cs := fake.NewSimpleClientset()
	c := kube.NewForTest(cs)
	if err := c.SweepOwned(context.Background(), kube.Scope{Namespace: "ns", Owner: kube.OwnerServices}, kube.Kind("Pod"), nil); err == nil {
		t.Error("expected error for unsupported kind")
	}
}

// ── ListOwned ─────────────────────────────────────────────────────

func TestListOwned_OnlyMatchingOwner(t *testing.T) {
	cs := fake.NewSimpleClientset(
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "ns", Labels: labeled(kube.OwnerServices)}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns", Labels: labeled(kube.OwnerServices)}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "caddy", Namespace: "ns", Labels: labeled(kube.OwnerCaddy)}},
	)
	c := kube.NewForTest(cs)

	got, err := c.ListOwned(context.Background(), kube.Scope{Namespace: "ns", Owner: kube.OwnerServices}, kube.KindService)
	if err != nil {
		t.Fatalf("ListOwned: %v", err)
	}
	sort.Strings(got)
	if len(got) != 2 || got[0] != "api" || got[1] != "web" {
		t.Errorf("got %v want [api web]", got)
	}
}

func TestListOwned_AcrossKinds(t *testing.T) {
	cs := fake.NewSimpleClientset(
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "d", Namespace: "ns", Labels: labeled(kube.OwnerServices)}},
		&appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "ns", Labels: labeled(kube.OwnerServices)}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns", Labels: labeled(kube.OwnerServices)}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "sec", Namespace: "ns", Labels: labeled(kube.OwnerServices)}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm", Namespace: "ns", Labels: labeled(kube.OwnerServices)}},
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pvc", Namespace: "ns", Labels: labeled(kube.OwnerServices)}},
	)
	c := kube.NewForTest(cs)

	for _, tc := range []struct {
		kind kube.Kind
		want string
	}{
		{kube.KindDeployment, "d"},
		{kube.KindStatefulSet, "s"},
		{kube.KindService, "svc"},
		{kube.KindSecret, "sec"},
		{kube.KindConfigMap, "cm"},
		{kube.KindPVC, "pvc"},
	} {
		got, err := c.ListOwned(context.Background(), kube.Scope{Namespace: "ns", Owner: kube.OwnerServices}, tc.kind)
		if err != nil {
			t.Errorf("ListOwned %s: %v", tc.kind, err)
			continue
		}
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("ListOwned %s: got %v want [%s]", tc.kind, got, tc.want)
		}
	}
}
