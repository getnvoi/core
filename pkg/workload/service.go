package workload

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/internal/kube"
	"github.com/getnvoi/core/pkg/naming"
	"github.com/getnvoi/core/pkg/runtime"
)

// buildService turns a ServiceSpec into a typed Service. ClusterIP
// for stateless workloads (Deployment), headless (ClusterIP="None")
// when the service is stateful (storage set → StatefulSet). Headless
// is what gives each StatefulSet pod its stable DNS — required for
// the pod-identity guarantees the StatefulSet contract makes.
//
// Selector matches the pod template via LabelOwner+labelService
// (stable across deploys). Port name "http" matches the container
// port; downstream consumers (Traefik ingress / cloudflared / sibling
// services) can target it by name.
func buildService(_ *runtime.Runtime, name string, svc config.ServiceSpec) *corev1.Service {
	labels := map[string]string{
		kube.LabelOwner: kube.OwnerServices,
		labelService:    name,
	}
	spec := corev1.ServiceSpec{
		Type:     corev1.ServiceTypeClusterIP,
		Selector: serviceSelector(name),
		Ports: []corev1.ServicePort{{
			Name:       "http",
			Port:       int32(svc.Port),
			TargetPort: intstr.FromString("http"),
			Protocol:   corev1.ProtocolTCP,
		}},
	}
	if svc.IsStateful() {
		// "None" is the documented sentinel for headless. No typed
		// constant in corev1; the literal is part of the apiserver
		// contract.
		spec.ClusterIP = "None"
	}
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: naming.Namespace,
			Labels:    labels,
		},
		Spec: spec,
	}
}
