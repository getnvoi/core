package workload

import (
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// applyNodePlacement configures pod scheduling based on the supplied
// server-key list. Mirrors upstream nvoi's pkg/kube/generate.go::applyNodePlacement
// verbatim:
//
//	len(servers) == 0  → defaults to ["master"] → nodeSelector
//	len(servers) == 1  → nodeSelector on nvoi-role=<key>
//	len(servers) >= 2  → nodeAffinity (Required, In) +
//	                     topologySpreadConstraints (MaxSkew=1, ScheduleAnyway)
//
// The label key `nvoi-role` is applied to every node by
// kube.LabelNode at deploy time. Servers are referenced by their YAML
// keys — not hostnames — so the YAML stays readable across hostname
// changes (cloud-init / re-provisioning).
//
// `name` is the service name used as the topologySpread label
// selector value (matches the pod's `app.kubernetes.io/name` label).
// Each service spreads its own replicas across its declared servers
// independently of every other service.
func applyNodePlacement(podSpec *corev1.PodSpec, name string, servers []string) {
	if len(servers) == 0 {
		servers = []string{"master"}
	}
	if len(servers) == 1 {
		podSpec.NodeSelector = map[string]string{LabelNvoiRole: servers[0]}
		return
	}
	podSpec.Affinity = &corev1.Affinity{
		NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{{
					MatchExpressions: []corev1.NodeSelectorRequirement{{
						Key:      LabelNvoiRole,
						Operator: corev1.NodeSelectorOpIn,
						Values:   servers,
					}},
				}},
			},
		},
	}
	podSpec.TopologySpreadConstraints = []corev1.TopologySpreadConstraint{{
		MaxSkew:           1,
		TopologyKey:       LabelNvoiRole,
		WhenUnsatisfiable: corev1.ScheduleAnyway,
		LabelSelector:     &metav1.LabelSelector{MatchLabels: map[string]string{labelAppName: name}},
	}}
}

// secretEnvVars produces the secretKeyRef-based env entries for each
// declared name. The shared Secret (appSecretName) holds resolved
// values for every entry in cfg.Secrets; per-service whitelists
// (svc.Secrets) drive which keys land in this PodSpec.
//
// Sorted output → deterministic manifest, stable diff across runs.
func secretEnvVars(names []string) []corev1.EnvVar {
	if len(names) == 0 {
		return nil
	}
	cp := make([]string, len(names))
	copy(cp, names)
	sort.Strings(cp)
	out := make([]corev1.EnvVar, 0, len(cp))
	for _, n := range cp {
		out = append(out, corev1.EnvVar{
			Name: n,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: appSecretName},
					Key:                  n,
				},
			},
		})
	}
	return out
}
