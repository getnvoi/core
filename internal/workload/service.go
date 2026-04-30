package workload

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/core/internal/config"
	"github.com/getnvoi/core/internal/runtime"
)

// namespace is where every nvoi-managed workload lands today. Single
// namespace keeps the substrate simple; per-app namespaces lift in
// when we need isolation.
const namespace = "default"

// BuildService turns a ServiceSpec into a typed ClusterIP Service.
// Selector matches the Deployment via LabelOwner + LabelService.
// Port name "http" matches the container port; downstream Caddy /
// ingress can target it by name.
func BuildService(_ *runtime.Runtime, name string, svc config.ServiceSpec) *corev1.Service {
	labels := map[string]string{
		LabelOwner:   "nvoi",
		LabelService: name,
	}
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: labels,
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Port:       int32(svc.Port),
				TargetPort: intstr.FromString("http"),
				Protocol:   corev1.ProtocolTCP,
			}},
		},
	}
}
