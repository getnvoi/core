package kube_test

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/getnvoi/core/pkg/internal/kube"
)

func TestEnsureSecret_CreatesWhenMissing(t *testing.T) {
	cs := fake.NewSimpleClientset()
	c := kube.NewForTest(cs)
	ctx := context.Background()

	if err := c.EnsureSecret(ctx, "ns", kube.OwnerDatabases, "creds", map[string]string{
		"url":      "postgres://u:p@h:5432/d",
		"host":     "h",
		"port":     "5432",
		"user":     "u",
		"password": "p",
		"database": "d",
		"sslmode":  "disable",
	}); err != nil {
		t.Fatalf("EnsureSecret: %v", err)
	}

	got, err := cs.CoreV1().Secrets("ns").Get(ctx, "creds", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if got.Type != corev1.SecretTypeOpaque {
		t.Errorf("type = %s, want Opaque", got.Type)
	}
	if got.Labels[kube.LabelOwner] != kube.OwnerDatabases {
		t.Errorf("owner label = %q, want %q", got.Labels[kube.LabelOwner], kube.OwnerDatabases)
	}
	if string(got.Data["url"]) != "postgres://u:p@h:5432/d" {
		t.Errorf("url = %q", string(got.Data["url"]))
	}
}

func TestEnsureSecret_MergesIntoExisting(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "ns"},
		Type:       corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"old-key": []byte("stays"),
			"url":     []byte("old-url"),
		},
	})
	c := kube.NewForTest(cs)
	ctx := context.Background()

	if err := c.EnsureSecret(ctx, "ns", kube.OwnerDatabases, "creds", map[string]string{
		"url":  "new-url",
		"host": "new-host",
	}); err != nil {
		t.Fatalf("EnsureSecret: %v", err)
	}

	got, _ := cs.CoreV1().Secrets("ns").Get(ctx, "creds", metav1.GetOptions{})
	if string(got.Data["url"]) != "new-url" {
		t.Errorf("url = %q, want new-url", string(got.Data["url"]))
	}
	if string(got.Data["host"]) != "new-host" {
		t.Errorf("host = %q, want new-host", string(got.Data["host"]))
	}
	if string(got.Data["old-key"]) != "stays" {
		t.Errorf("old-key dropped: got %q", string(got.Data["old-key"]))
	}
	if got.Labels[kube.LabelOwner] != kube.OwnerDatabases {
		t.Errorf("owner label not stamped on update: %q", got.Labels[kube.LabelOwner])
	}
}

func TestEnsureSecret_EmptyOwner_Errors(t *testing.T) {
	c := kube.NewForTest(fake.NewSimpleClientset())
	err := c.EnsureSecret(context.Background(), "ns", "", "creds", map[string]string{"k": "v"})
	if err == nil {
		t.Fatalf("expected error for empty owner")
	}
}

func TestGetSecretValue_NotFound_ErrsClearly(t *testing.T) {
	c := kube.NewForTest(fake.NewSimpleClientset())
	_, err := c.GetSecretValue(context.Background(), "ns", "missing", "url")
	if err == nil {
		t.Fatalf("expected NotFound error")
	}
}

func TestGetSecretValue_KeyAbsent_ErrsClearly(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "ns"},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{"url": []byte("u")},
	})
	c := kube.NewForTest(cs)
	_, err := c.GetSecretValue(context.Background(), "ns", "creds", "missing-key")
	if err == nil {
		t.Fatalf("expected key-absent error")
	}
}

func TestGetSecretValue_Reads(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "ns"},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{"url": []byte("postgres://x")},
	})
	c := kube.NewForTest(cs)
	got, err := c.GetSecretValue(context.Background(), "ns", "creds", "url")
	if err != nil {
		t.Fatalf("GetSecretValue: %v", err)
	}
	if got != "postgres://x" {
		t.Errorf("got %q, want postgres://x", got)
	}
}
