package workload

import (
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/core/pkg/config"
)

// defaultPlacementKeys returns the YAML keys to schedule a workload on
// when its ServiceSpec.Servers field is empty.
//
// Policy: workers if any are declared, else masters. Mirrors the
// k8s convention that control-plane nodes (masters) run cluster
// management; application workloads land on workers. In single-master
// or master-only clusters there's no choice — falls back to masters.
//
// Returns the actual YAML keys from cfg.Servers (sorted) — not the
// literal strings "worker" / "master" — because nvoi-role labels stamp
// the YAML key, not the role. Single-master configs that name their
// server `master:` accidentally worked under the old hardcoded
// `["master"]` default; HA configs with `master-1/2/3:` keys did not.
func defaultPlacementKeys(cfg *config.Config) []string {
	var workers, masters []string
	for name, s := range cfg.Servers {
		switch s.Role {
		case "worker":
			workers = append(workers, name)
		case "master":
			masters = append(masters, name)
		}
	}
	if len(workers) > 0 {
		sort.Strings(workers)
		return workers
	}
	sort.Strings(masters)
	return masters
}

// applyNodePlacement configures pod scheduling based on the supplied
// server-key list:
//
//	len(servers) == 1  → nodeSelector on nvoi-role=<key>
//	len(servers) >= 2  → nodeAffinity (Required, In) +
//	                     topologySpreadConstraints (MaxSkew=1, ScheduleAnyway)
//
// Empty `servers` is a programming error — caller must resolve the
// default (workers-if-any-else-masters) before calling. The default
// resolver lives in defaultPlacementKeys so the policy stays in one
// place; applyNodePlacement is mechanical given the keys.
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
func applyNodePlacement(podSpec *corev1.PodSpec, name string, servers []string) error {
	if len(servers) == 0 {
		// Caller is responsible for resolving via defaultPlacementKeys
		// before calling. Empty here means the cluster has no servers
		// at all — config.Validate enforces at least one master, so
		// in practice this is unreachable. Return error rather than
		// silent fall-through so a future validator regression
		// surfaces with a clear message instead of pods landing on
		// arbitrary nodes.
		return fmt.Errorf("placement for %q: no servers available (cluster must declare at least one master)", name)
	}
	if len(servers) == 1 {
		podSpec.NodeSelector = map[string]string{LabelNvoiRole: servers[0]}
		return nil
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
	return nil
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
