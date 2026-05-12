package observability

// alerts.go wraps operator-supplied Grafana alert-rule YAML files
// (resolved by internal/cli/monitor.go from monitor.alert_rules
// globs) into a single ConfigMap mounted at
// /etc/grafana/provisioning/alerting/. One ConfigMap with multiple
// data keys — Grafana provisioning loader reads every *.yaml in
// the directory, so each operator file becomes its own file in
// the mount.
//
// nvoi does NOT generate alert rules. Operators bring their own —
// examples/alerts/ ships a reference rule set.

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/core/pkg/internal/kube"
	"github.com/getnvoi/core/pkg/runtime"
)

const alertRulesConfigMapName = "alert-rules"

// BuildAlertRules renders the single alert-rules ConfigMap from
// every operator-supplied YAML file. Returns nil when no files are
// supplied — the projected alerting volume's `optional: true` on
// alert-rules lets Grafana boot cleanly.
//
// Data keys are the file basenames so the mounted directory mirrors
// the operator's source tree.
func BuildAlertRules(rt *runtime.Runtime) *corev1.ConfigMap {
	if rt == nil || rt.Monitor == nil || len(rt.Monitor.AlertRules) == 0 {
		return nil
	}
	data := make(map[string]string, len(rt.Monitor.AlertRules))
	for _, f := range rt.Monitor.AlertRules {
		data[f.Name] = string(f.Content)
	}
	labels := objectLabels(grafanaComponent)
	labels[kube.LabelOwner] = kube.OwnerObservability
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      alertRulesConfigMapName,
			Namespace: Namespace,
			Labels:    labels,
		},
		Data: data,
	}
}
