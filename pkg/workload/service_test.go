package workload

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/internal/kube"
	"github.com/getnvoi/core/pkg/runtime"
)

func TestBuildService_Shape(t *testing.T) {
	svc := buildService(&runtime.Runtime{}, "api", config.ServiceSpec{Port: 8080})

	if svc.Name != "api" || svc.Namespace != "default" {
		t.Errorf("name/ns: %s/%s", svc.Name, svc.Namespace)
	}
	if svc.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Errorf("type: got %s want ClusterIP", svc.Spec.Type)
	}
	if svc.Labels[kube.LabelOwner] != kube.OwnerServices || svc.Labels[labelService] != "api" {
		t.Errorf("labels: %v", svc.Labels)
	}
	// Selector strictly contains labelService. NOT labelDeployHash
	// (selector mutations orphan pods every roll) and NOT
	// kube.LabelOwner (owner is a sweep concern; selectors are
	// immutable post-Create so putting owner there means destroy +
	// recreate for any taxonomy change).
	if svc.Spec.Selector[labelService] != "api" {
		t.Errorf("selector[%s]: got %q want api", labelService, svc.Spec.Selector[labelService])
	}
	if _, hashOnSelector := svc.Spec.Selector[labelDeployHash]; hashOnSelector {
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
	svc := buildService(&runtime.Runtime{}, "postgres", config.ServiceSpec{
		Port:    5432,
		Storage: &config.StorageSpec{Size: "1Gi", MountPath: "/data"},
	})
	if svc.Spec.ClusterIP != "None" {
		t.Errorf("stateful service must be headless (ClusterIP=None), got %q", svc.Spec.ClusterIP)
	}
}

func TestBuildService_StatelessIsClusterIP(t *testing.T) {
	svc := buildService(&runtime.Runtime{}, "web", config.ServiceSpec{Port: 8080})
	if svc.Spec.ClusterIP == "None" {
		t.Error("stateless service must NOT be headless")
	}
	if svc.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Errorf("type: got %s want ClusterIP", svc.Spec.Type)
	}
}
