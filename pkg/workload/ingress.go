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
// TLS section references the cert-manager-issued Secret per domain
// (Secret name produced by kube.SanitizeDNS1123(domain)+"-tls"; same
// builder cert-manager.go used to render the Certificate). Traefik
// reads the Secret on its own, terminates TLS, and forwards plain
// HTTP to the backend Service.
//
// We deliberately use vanilla networking.k8s.io/v1.Ingress (not
// Traefik's IngressRoute CRD) so the architecture stays portable
// across ingress controllers. Swap Traefik for nginx-ingress later
// → no Ingress shape changes.
func buildIngress(name string, svc config.ServiceSpec, domains []string) *networkingv1.Ingress {
	tlsBlocks := make([]networkingv1.IngressTLS, 0, len(domains))
	rules := make([]networkingv1.IngressRule, 0, len(domains))
	for _, host := range domains {
		secretName := kube.SanitizeDNS1123(host) + "-tls"
		tlsBlocks = append(tlsBlocks, networkingv1.IngressTLS{
			Hosts:      []string{host},
			SecretName: secretName,
		})
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
			TLS:              tlsBlocks,
			Rules:            rules,
		},
	}
}
