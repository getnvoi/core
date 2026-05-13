package workload

import (
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/internal/kube"
	"github.com/getnvoi/core/pkg/naming"
)

const ingressClassName = "traefik"

// buildIngress renders the per-service Ingress consumed by k3s's
// built-in Traefik controller. One Ingress per service-with-domains.
//
// No TLS section: Cloudflare terminates TLS at the edge. cloudflared
// dials Traefik in-cluster over plain HTTP; Traefik routes by Host
// header to the backend Service, which gives per-request L7 load
// balancing across pods via the EndpointSlice watch — the whole
// reason we don't bypass Traefik with cloudflared upstreams pointing
// at the Service ClusterIP directly.
//
// We deliberately use vanilla networking.k8s.io/v1.Ingress (not
// Traefik's IngressRoute CRD) so the architecture stays portable
// across ingress controllers. Swap Traefik for nginx-ingress later
// → no Ingress shape changes.
func buildIngress(name string, svc config.ServiceSpec, domains []string) *networkingv1.Ingress {
	rules := make([]networkingv1.IngressRule, 0, len(domains))
	for _, host := range domains {
		rules = append(rules, networkingv1.IngressRule{
			Host: host,
			IngressRuleValue: networkingv1.IngressRuleValue{
				HTTP: &networkingv1.HTTPIngressRuleValue{
					Paths: []networkingv1.HTTPIngressPath{{
						Path:     "/",
						PathType: ptr.To(networkingv1.PathTypePrefix),
						Backend: networkingv1.IngressBackend{
							Service: &networkingv1.IngressServiceBackend{
								Name: name,
								Port: networkingv1.ServiceBackendPort{Number: int32(svc.Port)},
							},
						},
					}},
				},
			},
		})
	}

	return &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: naming.Namespace,
			Labels: map[string]string{
				kube.LabelOwner: kube.OwnerIngress,
			},
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: ptr.To(ingressClassName),
			Rules:            rules,
		},
	}
}
