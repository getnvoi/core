package grafana

// datasource.go renders the Grafana datasource provisioning YAML
// pointing at the in-cluster Thanos Querier (PromQL) + Loki (LogQL).
// Static content — no per-deploy variation — but kept as a builder
// so future datasources (Tempo, Mimir, …) drop in without touching
// the deploy phase.

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/core/pkg/internal/kube"
)

// DatasourceConfigMapName is the singleton ConfigMap mounted at
// /etc/grafana/provisioning/datasources/. PR 6's Grafana volume
// reference uses this constant.
const DatasourceConfigMapName = "grafana-datasources"

// datasourcesYAML is the Grafana provisioning v1 datasource list.
// Prometheus UID is "prometheus" (matches alerts.go's
// datasourceUid). Loki UID is "loki" (referenced by per-service
// dashboards' logs panel).
//
// Default datasource = Prometheus. Operators querying without a
// selector land on metrics first.
// Loki datasource intentionally sets jsonData.manageAlerts=false.
// Grafana's Alerting UI otherwise probes every datasource's ruler
// endpoint; Loki's single-binary mode doesn't run a ruler we expose,
// so the probe fails and surfaces as "Errors loading rules" in the
// Alerting tab. All alerts in nvoi come from Prometheus via
// provisioning — Loki is read-only for log queries.
const datasourcesYAML = `apiVersion: 1
datasources:
  - name: Prometheus
    type: prometheus
    uid: prometheus
    access: proxy
    url: http://thanos-querier.nvoi-observability.svc.cluster.local:9090
    isDefault: true
    jsonData:
      timeInterval: 30s
  - name: Loki
    type: loki
    uid: loki
    access: proxy
    url: http://loki.nvoi-observability.svc.cluster.local:3100
    jsonData:
      manageAlerts: false
`

// BuildDatasourceConfigMap returns the typed ConfigMap. Caller
// (stack.BuildStack) pre-pends it to the apply order so Grafana
// finds datasources at startup. Owner label gets stamped by
// kc.ApplyOwned at apply time.
func BuildDatasourceConfigMap() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      DatasourceConfigMapName,
			Namespace: "nvoi-observability",
			Labels: map[string]string{
				"app.kubernetes.io/name": "grafana",
				kube.LabelOwner:          kube.OwnerObservability,
			},
		},
		Data: map[string]string{"datasources.yaml": datasourcesYAML},
	}
}
