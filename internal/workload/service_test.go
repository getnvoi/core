package workload

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/getnvoi/core/internal/config"
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
	if svc.Labels[LabelOwner] != "nvoi" || svc.Labels[LabelService] != "api" {
		t.Errorf("labels: %v", svc.Labels)
	}
	// Selector matches Deployment's pod template labels (NOT
	// LabelDeployHash — selector must be stable across deploys, or
	// the Service drops connections every roll).
	want := map[string]string{LabelOwner: "nvoi", LabelService: "api"}
	for k, v := range want {
		if svc.Spec.Selector[k] != v {
			t.Errorf("selector[%s]: got %q want %q", k, svc.Spec.Selector[k], v)
		}
	}
	if _, hashOnSelector := svc.Spec.Selector[LabelDeployHash]; hashOnSelector {
		t.Error("selector must NOT include deploy-hash (would break stable routing across rollouts)")
	}
	if len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].Port != 8080 {
		t.Errorf("ports: %+v", svc.Spec.Ports)
	}
	if svc.Spec.Ports[0].TargetPort.StrVal != "http" {
		t.Errorf("targetPort: got %v want \"http\"", svc.Spec.Ports[0].TargetPort)
	}
}
