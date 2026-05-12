package observability

// dashboards.go wraps operator-supplied Grafana dashboard JSON files
// (resolved by internal/cli/monitor.go from monitor.dashboards
// globs) into ConfigMaps the kiwigrid sidecar picks up and drops
// into /etc/grafana/provisioning/dashboards/.
//
// nvoi does NOT generate dashboard JSON. Operators bring their own —
// examples/dashboards/ ships a reference dashboard, grafana.com has
// thousands more, and any kubectl-edit-friendly JSON is one
// `monitor.dashboards: [path]` away. Same shape `domains:` uses for
// hostnames: declarative, file-based, deterministic.

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/core/pkg/runtime"
)

const (
	dashboardLabel      = "grafana_dashboard"
	dashboardLabelValue = "1"
)

// BuildDashboards renders one ConfigMap per operator-supplied
// dashboard file. Returns nil when no files were supplied; the
// sidecar still runs but writes nothing.
//
// ConfigMap name: dashboard-<basename-without-ext>.
// Data key:       <filename> (sidecar writes it verbatim to disk).
func BuildDashboards(rt *runtime.Runtime) ([]*corev1.ConfigMap, []string) {
	if rt == nil || rt.Monitor == nil || len(rt.Monitor.Dashboards) == 0 {
		return nil, nil
	}
	out := make([]*corev1.ConfigMap, 0, len(rt.Monitor.Dashboards))
	names := make([]string, 0, len(rt.Monitor.Dashboards))
	for _, f := range rt.Monitor.Dashboards {
		cm := dashboardConfigMap(f.Name, f.Content)
		out = append(out, cm)
		names = append(names, cm.Name)
	}
	return out, names
}

func dashboardConfigMap(filename string, content []byte) *corev1.ConfigMap {
	labels := objectLabels(grafanaComponent)
	labels[dashboardLabel] = dashboardLabelValue
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dashboard-" + stripExt(filename),
			Namespace: Namespace,
			Labels:    labels,
		},
		Data: map[string]string{filename: string(content)},
	}
}

// stripExt drops the file extension from a basename. "nvoi.json"
// -> "nvoi". Used to derive a DNS-1123-safe ConfigMap name from the
// dashboard filename without colliding with the data key.
func stripExt(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			return name[:i]
		}
		if name[i] == '/' {
			break
		}
	}
	return name
}
