package observability

// labels.go centralizes the small set of labels every observability
// manifest carries. Owner is stamped by kc.ApplyOwned at apply time,
// so builders don't set it themselves — but the per-component label
// (`app.kubernetes.io/name=<component>`) IS the builder's
// responsibility, because pod-template selectors reference it.

import "github.com/getnvoi/core/pkg/internal/kube"

// componentLabel is the canonical key for the per-component
// identifier the observability stack uses in Service selectors,
// pod-template labels, and dashboard variable options.
const componentLabel = "app.kubernetes.io/name"

// objectLabels are the labels stamped on the workload metadata
// (Deployment / StatefulSet / DaemonSet / Service / ConfigMap / etc.).
// kc.ApplyOwned adds nvoi/owner=observability on top.
func objectLabels(component string) map[string]string {
	return map[string]string{
		componentLabel:   component,
		kube.LabelOwner:  kube.OwnerObservability, // also stamped by ApplyOwned; self-describing manifests
	}
}

// podLabels are the labels on every pod in the StatefulSet /
// Deployment / DaemonSet template. Same as objectLabels for v1 —
// future deploy-hash-style labels can be layered here.
func podLabels(component string) map[string]string {
	return objectLabels(component)
}

// selectorFor is the matchLabels selector a Service uses to target
// its workload's pods. MUST be a strict subset of podLabels and MUST
// be stable across deploys (selector mutations orphan pods).
func selectorFor(component string) map[string]string {
	return map[string]string{componentLabel: component}
}
