package workload

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/getnvoi/core/pkg/kube"
	"github.com/getnvoi/core/pkg/runtime"
)

func TestBuildAppSecret_Empty_ReturnsNil(t *testing.T) {
	rt := &runtime.Runtime{SecretValues: nil}
	if got := BuildAppSecret(rt); got != nil {
		t.Errorf("expected nil when no secrets, got %+v", got)
	}
}

func TestBuildAppSecret_Shape(t *testing.T) {
	rt := &runtime.Runtime{
		SecretValues: map[string]string{
			"DATABASE_URL":      "postgres://u:p@db:5432/x",
			"POSTGRES_USER":     "alice",
			"POSTGRES_PASSWORD": "ghp_xxx",
		},
	}
	s := BuildAppSecret(rt)
	if s == nil {
		t.Fatal("Secret nil")
	}
	if s.Name != AppSecretName || s.Namespace != "default" {
		t.Errorf("name/ns: %s/%s", s.Name, s.Namespace)
	}
	if s.Type != corev1.SecretTypeOpaque {
		t.Errorf("type: got %s want Opaque", s.Type)
	}
	if s.Labels[kube.LabelOwner] != kube.OwnerAppSecrets {
		t.Errorf("owner label: got %v want %s=%s", s.Labels, kube.LabelOwner, kube.OwnerAppSecrets)
	}
	if got := string(s.Data["DATABASE_URL"]); got != "postgres://u:p@db:5432/x" {
		t.Errorf("DATABASE_URL: got %q", got)
	}
	if got := string(s.Data["POSTGRES_USER"]); got != "alice" {
		t.Errorf("POSTGRES_USER: got %q", got)
	}
}
