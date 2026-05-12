package observability

// versions.go pins every upstream image the observability stack uses.
// Bumping any of these is a one-line change; review the release notes
// before doing so. Matches the convention in
// pkg/internal/kube/certmanager.go (CertManagerVersion).
const (
	PrometheusImage = "prom/prometheus:v2.55.1"
	ThanosImage     = "quay.io/thanos/thanos:v0.36.1"
	LokiImage       = "grafana/loki:3.2.1"
	PromtailImage   = "grafana/promtail:3.2.1"
	GrafanaImage    = "grafana/grafana:11.3.0"
	GrafanaSidecar  = "kiwigrid/k8s-sidecar:1.27.5"
)
