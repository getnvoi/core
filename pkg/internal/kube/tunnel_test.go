package kube_test

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/getnvoi/core/pkg/internal/kube"
	"github.com/getnvoi/core/pkg/log"
)

func bufLog(t *testing.T) log.Log {
	t.Helper()
	return log.New(false)
}

func TestApplyTunnel_CreatesNamespace_Secret_Deployment(t *testing.T) {
	cs := fake.NewSimpleClientset()
	c := kube.NewForTest(cs)

	err := c.ApplyTunnel(context.Background(), bufLog(t), kube.TunnelSpec{
		Token:    "eyJabcTOKENBLOB",
		Replicas: 2,
	})
	if err != nil {
		t.Fatalf("ApplyTunnel: %v", err)
	}

	// Namespace materialized.
	if _, err := cs.CoreV1().Namespaces().Get(context.Background(), kube.TunnelNamespace, metav1.GetOptions{}); err != nil {
		t.Fatalf("namespace %s not created: %v", kube.TunnelNamespace, err)
	}

	// Secret carries the token in StringData (apiserver materializes
	// to Data on real clusters; fake clientset stores what we set).
	sec, err := cs.CoreV1().Secrets(kube.TunnelNamespace).Get(context.Background(), kube.TunnelSecretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("secret %s not created: %v", kube.TunnelSecretName, err)
	}
	if sec.StringData["token"] != "eyJabcTOKENBLOB" {
		t.Errorf("secret token mismatch: %q", sec.StringData["token"])
	}
	if sec.Labels[kube.LabelOwner] != kube.OwnerTunnel {
		t.Errorf("secret missing owner label: %v", sec.Labels)
	}

	// Deployment replicas + container image + env-from-secret.
	dep, err := cs.AppsV1().Deployments(kube.TunnelNamespace).Get(context.Background(), kube.TunnelDeploymentName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("deployment %s not created: %v", kube.TunnelDeploymentName, err)
	}
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 2 {
		t.Errorf("replicas: got %v want 2", dep.Spec.Replicas)
	}
	if dep.Labels[kube.LabelOwner] != kube.OwnerTunnel {
		t.Errorf("deployment missing owner label: %v", dep.Labels)
	}
	if len(dep.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf("want 1 container, got %d", len(dep.Spec.Template.Spec.Containers))
	}
	cnt := dep.Spec.Template.Spec.Containers[0]
	if !strings.HasPrefix(cnt.Image, "cloudflare/cloudflared:") {
		t.Errorf("unexpected image: %q", cnt.Image)
	}
	// Token flows through env via SecretKeyRef pointing at our Secret.
	var found bool
	for _, e := range cnt.Env {
		if e.Name == "TUNNEL_TOKEN" && e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil &&
			e.ValueFrom.SecretKeyRef.LocalObjectReference.Name == kube.TunnelSecretName &&
			e.ValueFrom.SecretKeyRef.Key == "token" {
			found = true
		}
	}
	if !found {
		t.Errorf("TUNNEL_TOKEN env missing secretKeyRef → %s/token: %+v", kube.TunnelSecretName, cnt.Env)
	}

	// --token $(TUNNEL_TOKEN) is the contract for token-baked auth.
	args := strings.Join(cnt.Args, " ")
	if !strings.Contains(args, "--token $(TUNNEL_TOKEN)") {
		t.Errorf("args missing --token $(TUNNEL_TOKEN): %v", cnt.Args)
	}
}

func TestApplyTunnel_DefaultsReplicasToTwo(t *testing.T) {
	cs := fake.NewSimpleClientset()
	c := kube.NewForTest(cs)

	err := c.ApplyTunnel(context.Background(), bufLog(t), kube.TunnelSpec{Token: "tok"})
	if err != nil {
		t.Fatalf("ApplyTunnel: %v", err)
	}
	dep, _ := cs.AppsV1().Deployments(kube.TunnelNamespace).Get(context.Background(), kube.TunnelDeploymentName, metav1.GetOptions{})
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 2 {
		t.Errorf("replicas default: got %v want 2", dep.Spec.Replicas)
	}
}

func TestApplyTunnel_EmptyToken_Errors(t *testing.T) {
	cs := fake.NewSimpleClientset()
	c := kube.NewForTest(cs)

	err := c.ApplyTunnel(context.Background(), bufLog(t), kube.TunnelSpec{Token: "", Replicas: 1})
	if err == nil || !strings.Contains(err.Error(), "empty token") {
		t.Errorf("expected empty-token error, got %v", err)
	}
}

func TestApplyTunnel_Idempotent_ReapplyIsNoOp(t *testing.T) {
	cs := fake.NewSimpleClientset()
	c := kube.NewForTest(cs)

	if err := c.ApplyTunnel(context.Background(), bufLog(t), kube.TunnelSpec{Token: "tok-1", Replicas: 2}); err != nil {
		t.Fatal(err)
	}
	if err := c.ApplyTunnel(context.Background(), bufLog(t), kube.TunnelSpec{Token: "tok-2", Replicas: 3}); err != nil {
		t.Fatal(err)
	}
	// Second apply rolls the token + replicas — the Apply path is
	// Get-then-Update, so observed state mirrors the latest call.
	sec, _ := cs.CoreV1().Secrets(kube.TunnelNamespace).Get(context.Background(), kube.TunnelSecretName, metav1.GetOptions{})
	if sec.StringData["token"] != "tok-2" {
		t.Errorf("token after re-apply: got %q want tok-2", sec.StringData["token"])
	}
	dep, _ := cs.AppsV1().Deployments(kube.TunnelNamespace).Get(context.Background(), kube.TunnelDeploymentName, metav1.GetOptions{})
	if *dep.Spec.Replicas != 3 {
		t.Errorf("replicas after re-apply: got %d want 3", *dep.Spec.Replicas)
	}
}

func TestSweepTunnel_RemovesDeploymentAndSecret(t *testing.T) {
	// Seed cluster with cloudflared objects + an unrelated workload
	// to guard against the sweep crossing owner boundaries.
	cs := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: kube.TunnelNamespace}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
			Name: kube.TunnelDeploymentName, Namespace: kube.TunnelNamespace,
			Labels: map[string]string{kube.LabelOwner: kube.OwnerTunnel},
		}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Name: kube.TunnelSecretName, Namespace: kube.TunnelNamespace,
			Labels: map[string]string{kube.LabelOwner: kube.OwnerTunnel},
		}},
		// Sibling app in the same namespace, NOT owned by tunnel —
		// must survive the sweep.
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
			Name: "external", Namespace: kube.TunnelNamespace,
		}},
	)
	c := kube.NewForTest(cs)

	if err := c.SweepTunnel(context.Background(), bufLog(t)); err != nil {
		t.Fatalf("SweepTunnel: %v", err)
	}
	if _, err := cs.AppsV1().Deployments(kube.TunnelNamespace).Get(context.Background(), kube.TunnelDeploymentName, metav1.GetOptions{}); err == nil {
		t.Error("cloudflared Deployment should have been swept")
	}
	if _, err := cs.CoreV1().Secrets(kube.TunnelNamespace).Get(context.Background(), kube.TunnelSecretName, metav1.GetOptions{}); err == nil {
		t.Error("cloudflared-token Secret should have been swept")
	}
	if _, err := cs.AppsV1().Deployments(kube.TunnelNamespace).Get(context.Background(), "external", metav1.GetOptions{}); err != nil {
		t.Errorf("non-tunnel-owned Deployment in same namespace must survive: %v", err)
	}
}

func TestSweepTunnel_NoOpWhenAbsent(t *testing.T) {
	// Cluster has no tunnel objects (post-fresh-deploy / first-time).
	cs := fake.NewSimpleClientset()
	c := kube.NewForTest(cs)
	if err := c.SweepTunnel(context.Background(), bufLog(t)); err != nil {
		t.Errorf("SweepTunnel on empty cluster should be a no-op, got %v", err)
	}
}
