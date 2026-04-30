package workload

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/getnvoi/core/internal/config"
	"github.com/getnvoi/core/internal/kube"
	"github.com/getnvoi/core/internal/runtime"
)

func TestBuildService_Shape(t *testing.T) {
	svc := BuildService(&runtime.Runtime{}, "api", config.ServiceSpec{Port: 8080})

	if svc.Name != "api" || svc.Namespace != "default" {
		t.Errorf("name/ns: %s/%s", svc.Name, svc.Namespace)
	}
	if svc.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Errorf("type: got %s want ClusterIP", svc.Spec.Type)
	}
	if svc.Labels[kube.LabelOwner] != kube.OwnerServices || svc.Labels[LabelService] != "api" {
		t.Errorf("labels: %v", svc.Labels)
	}
	// Selector strictly contains LabelService. NOT LabelDeployHash
	// (selector mutations orphan pods every roll) and NOT
	// kube.LabelOwner (owner is a sweep concern; selectors are
	// immutable post-Create so putting owner there means destroy +
	// recreate for any taxonomy change).
	if svc.Spec.Selector[LabelService] != "api" {
		t.Errorf("selector[%s]: got %q want api", LabelService, svc.Spec.Selector[LabelService])
	}
	if _, hashOnSelector := svc.Spec.Selector[LabelDeployHash]; hashOnSelector {
		t.Error("selector must NOT include deploy-hash")
	}
	if _, ownerOnSelector := svc.Spec.Selector[kube.LabelOwner]; ownerOnSelector {
		t.Error("selector must NOT include nvoi/owner (immutable selector + evolvable owner taxonomy = destroy required on every taxonomy change)")
	}
	if len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].Port != 8080 {
		t.Errorf("ports: %+v", svc.Spec.Ports)
	}
	if svc.Spec.Ports[0].TargetPort.StrVal != "http" {
		t.Errorf("targetPort: got %v want \"http\"", svc.Spec.Ports[0].TargetPort)
	}
}

func TestBuildService_StatefulIsHeadless(t *testing.T) {
	svc := BuildService(&runtime.Runtime{}, "postgres", config.ServiceSpec{
		Port:    5432,
		Storage: &config.StorageSpec{Size: "1Gi", MountPath: "/data"},
	})
	if svc.Spec.ClusterIP != "None" {
		t.Errorf("stateful service must be headless (ClusterIP=None), got %q", svc.Spec.ClusterIP)
	}
}

func TestBuildService_StatelessIsClusterIP(t *testing.T) {
	svc := BuildService(&runtime.Runtime{}, "web", config.ServiceSpec{Port: 8080})
	if svc.Spec.ClusterIP == "None" {
		t.Error("stateless service must NOT be headless")
	}
	if svc.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Errorf("type: got %s want ClusterIP", svc.Spec.Type)
	}
}
