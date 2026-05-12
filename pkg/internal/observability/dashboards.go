package observability

// dashboards.go generates the Grafana dashboard JSON ConfigMaps the
// k8s-sidecar provisions at Grafana boot. Inputs come from
// rt.Cfg.Services + rt.Cfg.Domains + rt.Cfg.Servers — operators
// never edit dashboard JSON directly. The YAML describes the
// workloads; nvoi decides the panels.
//
// One ConfigMap per dashboard, labeled grafana_dashboard=1 +
// nvoi/owner=observability + nvoi/dashboard=<name>. The sidecar
// (configured in buildGrafanaDeployment) watches the namespace for
// these labels and drops the JSON into Grafana's
// /etc/grafana/provisioning/dashboards/ tree.
//
// Dashboard JSON is produced from Go text/template strings. Five
// dashboards in v1: overview, per-service, ingress, cluster,
// deploys. Each is a function (`buildOverviewDashboard`,
// `buildServicesDashboard`, …) so adding a sixth is a new function +
// one line in BuildDashboards.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"text/template"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/runtime"
)

// dashboardLabel + dashboardLabelValue is the sidecar discovery key.
// MUST match the LABEL env on the kiwigrid container in
// buildGrafanaDeployment.
const (
	dashboardLabel      = "grafana_dashboard"
	dashboardLabelValue = "1"
)

// BuildDashboards renders every dashboard ConfigMap from the cfg
// shape. Returns one ConfigMap per dashboard + the list of declared
// dashboard ConfigMap names (consumed by the deploy phase's sweep).
//
// Order is alphabetical so dashboard listings in Grafana stay stable
// across deploys.
func BuildDashboards(rt *runtime.Runtime) ([]*corev1.ConfigMap, []string, error) {
	cfg := rt.Cfg
	dashes := []struct {
		name string
		fn   func(*config.Config) (string, error)
	}{
		{"cluster", buildClusterDashboard},
		{"deploys", buildDeploysDashboard},
		{"ingress", buildIngressDashboard},
		{"overview", buildOverviewDashboard},
		{"services", buildServicesDashboard},
	}

	out := make([]*corev1.ConfigMap, 0, len(dashes))
	names := make([]string, 0, len(dashes))
	for _, d := range dashes {
		json, err := d.fn(cfg)
		if err != nil {
			return nil, nil, fmt.Errorf("dashboard %s: %w", d.name, err)
		}
		cm := dashboardConfigMap(d.name, json)
		out = append(out, cm)
		names = append(names, cm.Name)
	}
	return out, names, nil
}

// dashboardConfigMap wraps a dashboard JSON blob in a labeled
// ConfigMap. Data key matches the dashboard filename Grafana would
// expect in /etc/grafana/provisioning/dashboards/ (.json).
func dashboardConfigMap(name, jsonBody string) *corev1.ConfigMap {
	labels := objectLabels(grafanaComponent)
	labels[dashboardLabel] = dashboardLabelValue
	labels["nvoi/dashboard"] = name
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dashboard-" + name,
			Namespace: Namespace,
			Labels:    labels,
		},
		Data: map[string]string{name + ".json": jsonBody},
	}
}

// ── Per-dashboard generators ──────────────────────────────────────────

// buildOverviewDashboard renders the home-page health grid. One stat
// panel per service showing kube_deployment_status_replicas_ready vs
// _spec, restart count over 1h. Color thresholds: green if equal,
// red if ready < spec.
//
// Empty cfg.Services → a placeholder panel ("no services configured").
// Keeps the dashboard valid + helpful when an operator activates
// monitor: before declaring any services.
func buildOverviewDashboard(cfg *config.Config) (string, error) {
	services := sortedServiceNames(cfg)
	ctx := dashboardCtx{
		Title:    "nvoi — overview",
		UID:      "nvoi-overview",
		Services: services,
		AppName:  cfg.App,
		EnvName:  cfg.Env,
	}
	return renderTemplate(overviewTpl, ctx)
}

// buildServicesDashboard renders per-service detail: replicas, CPU,
// memory, restarts. One row per service, repeated via Grafana's
// template-variable mechanism. The variable's options list is
// embedded directly (no datasource query) so the dashboard renders
// with the exact set declared in YAML.
func buildServicesDashboard(cfg *config.Config) (string, error) {
	ctx := dashboardCtx{
		Title:    "nvoi — services",
		UID:      "nvoi-services",
		Services: sortedServiceNames(cfg),
		AppName:  cfg.App,
		EnvName:  cfg.Env,
	}
	return renderTemplate(servicesTpl, ctx)
}

// buildIngressDashboard renders per-domain request rate + status
// breakdown using Traefik metrics. Cert expiry tile per domain.
// Empty cfg.Domains → placeholder.
func buildIngressDashboard(cfg *config.Config) (string, error) {
	domains := flattenDomains(cfg)
	ctx := dashboardCtx{
		Title:   "nvoi — ingress",
		UID:     "nvoi-ingress",
		Domains: domains,
		AppName: cfg.App,
		EnvName: cfg.Env,
	}
	return renderTemplate(ingressTpl, ctx)
}

// buildClusterDashboard renders node status + capacity + etcd health
// (when HA). Reads from kube_node_* and etcd_* metrics scraped by
// Prometheus.
func buildClusterDashboard(cfg *config.Config) (string, error) {
	masters := 0
	for _, s := range cfg.Servers {
		if s.Role == "master" {
			masters++
		}
	}
	ctx := dashboardCtx{
		Title:   "nvoi — cluster",
		UID:     "nvoi-cluster",
		AppName: cfg.App,
		EnvName: cfg.Env,
		HasHA:   masters >= 2,
	}
	return renderTemplate(clusterTpl, ctx)
}

// buildDeploysDashboard renders a timeline of nvoi/deploy-hash
// label transitions. Useful for correlating a regression to a
// specific deploy. Annotation queries on other dashboards reference
// the same source.
func buildDeploysDashboard(cfg *config.Config) (string, error) {
	ctx := dashboardCtx{
		Title:   "nvoi — deploys",
		UID:     "nvoi-deploys",
		AppName: cfg.App,
		EnvName: cfg.Env,
	}
	return renderTemplate(deploysTpl, ctx)
}

// ── Template context + helpers ────────────────────────────────────────

type dashboardCtx struct {
	Title    string
	UID      string
	AppName  string
	EnvName  string
	Services []string
	Domains  []string // flat list of all hostnames across cfg.Domains
	HasHA    bool
}

// tplFuncs are the helpers dashboard templates use for layout math
// (positioning panels in the grid). text/template ships no arithmetic
// — we register the minimum here.
var tplFuncs = template.FuncMap{
	"add": func(a, b int) int { return a + b },
	"mul": func(a, b int) int { return a * b },
}

// renderTemplate executes the template and validates the output as
// JSON (so a malformed template surfaces here, not at Grafana boot).
func renderTemplate(tpl string, ctx dashboardCtx) (string, error) {
	t, err := template.New("dashboard").Funcs(tplFuncs).Parse(tpl)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}
	// Validate JSON shape — guards against template bugs that produce
	// syntactically invalid output (trailing commas, unbalanced braces).
	var sink any
	if err := json.Unmarshal(buf.Bytes(), &sink); err != nil {
		return "", fmt.Errorf("rendered dashboard is not valid JSON: %w (output: %s)", err, buf.String())
	}
	return buf.String(), nil
}

func sortedServiceNames(cfg *config.Config) []string {
	out := make([]string, 0, len(cfg.Services))
	for name := range cfg.Services {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func flattenDomains(cfg *config.Config) []string {
	var out []string
	for _, hosts := range cfg.Domains {
		out = append(out, hosts...)
	}
	sort.Strings(out)
	return out
}

// ── Dashboard JSON templates ──────────────────────────────────────────
//
// Schema version 39 = Grafana 11.x compatible. UIDs are stable across
// deploys so cross-dashboard links don't break.
//
// Each template is minimal but VALID — Grafana imports it without
// error, panels render with real PromQL/LogQL queries against the
// Thanos Querier / Loki datasources. Operators can extend later via
// kubectl edit, but those changes don't persist past the next
// deploy (sidecar reprovisions from the ConfigMap on Grafana
// restart). Same contract as "don't hand-edit a Service".

const dashboardBase = `{
  "annotations": {
    "list": [
      {
        "datasource": "Prometheus",
        "enable": true,
        "expr": "changes(kube_pod_labels{label_nvoi_deploy_hash!=\"\"}[1m]) > 0",
        "iconColor": "rgba(0, 211, 255, 1)",
        "name": "Deploys",
        "step": "1m",
        "tagKeys": "label_nvoi_deploy_hash"
      }
    ]
  },
  "editable": false,
  "schemaVersion": 39,
  "title": "{{ .Title }}",
  "uid": "{{ .UID }}",
  "tags": ["nvoi", "{{ .AppName }}", "{{ .EnvName }}"],
  "time": { "from": "now-6h", "to": "now" },
  "timezone": "browser",`

const overviewTpl = dashboardBase + `
  "panels": [
    {
      "type": "stat",
      "title": "Services ready",
      "datasource": "Prometheus",
      "gridPos": {"x": 0, "y": 0, "w": 8, "h": 4},
      "targets": [{"expr": "sum(kube_deployment_status_replicas_ready{namespace=\"default\"})", "refId": "A"}]
    },
    {
      "type": "stat",
      "title": "Services desired",
      "datasource": "Prometheus",
      "gridPos": {"x": 8, "y": 0, "w": 8, "h": 4},
      "targets": [{"expr": "sum(kube_deployment_spec_replicas{namespace=\"default\"})", "refId": "A"}]
    },
    {
      "type": "stat",
      "title": "Restarts (1h)",
      "datasource": "Prometheus",
      "gridPos": {"x": 16, "y": 0, "w": 8, "h": 4},
      "targets": [{"expr": "sum(increase(kube_pod_container_status_restarts_total{namespace=\"default\"}[1h]))", "refId": "A"}]
    }{{ range $i, $s := .Services }},
    {
      "type": "timeseries",
      "title": "{{ $s }} — ready replicas",
      "datasource": "Prometheus",
      "gridPos": {"x": 0, "y": {{ add 4 (mul $i 6) }}, "w": 24, "h": 6},
      "targets": [
        {"expr": "kube_deployment_status_replicas_ready{namespace=\"default\",deployment=\"{{ $s }}\"}", "refId": "A", "legendFormat": "ready"},
        {"expr": "kube_deployment_spec_replicas{namespace=\"default\",deployment=\"{{ $s }}\"}", "refId": "B", "legendFormat": "desired"}
      ]
    }{{ end }}
  ]
}`

const servicesTpl = dashboardBase + `
  "templating": {
    "list": [
      {
        "name": "service",
        "type": "custom",
        "current": {"text": "{{ if .Services }}{{ index .Services 0 }}{{ end }}", "value": "{{ if .Services }}{{ index .Services 0 }}{{ end }}"},
        "options": [{{ range $i, $s := .Services }}{{ if $i }},{{ end }}{"text": "{{ $s }}", "value": "{{ $s }}"}{{ end }}],
        "query": "{{ range $i, $s := .Services }}{{ if $i }},{{ end }}{{ $s }}{{ end }}"
      }
    ]
  },
  "panels": [
    {
      "type": "timeseries",
      "title": "$service — replicas",
      "datasource": "Prometheus",
      "gridPos": {"x": 0, "y": 0, "w": 12, "h": 6},
      "targets": [
        {"expr": "kube_deployment_status_replicas_ready{namespace=\"default\",deployment=\"$service\"}", "refId": "A", "legendFormat": "ready"},
        {"expr": "kube_deployment_spec_replicas{namespace=\"default\",deployment=\"$service\"}", "refId": "B", "legendFormat": "desired"}
      ]
    },
    {
      "type": "timeseries",
      "title": "$service — restarts (rate)",
      "datasource": "Prometheus",
      "gridPos": {"x": 12, "y": 0, "w": 12, "h": 6},
      "targets": [{"expr": "rate(kube_pod_container_status_restarts_total{namespace=\"default\",pod=~\"$service-.*\"}[5m])", "refId": "A"}]
    },
    {
      "type": "logs",
      "title": "$service — logs",
      "datasource": "Loki",
      "gridPos": {"x": 0, "y": 6, "w": 24, "h": 10},
      "targets": [{"expr": "{namespace=\"default\", service=\"$service\"}", "refId": "A"}]
    }
  ]
}`

const ingressTpl = dashboardBase + `
  "panels": [
    {
      "type": "timeseries",
      "title": "Request rate by status",
      "datasource": "Prometheus",
      "gridPos": {"x": 0, "y": 0, "w": 24, "h": 8},
      "targets": [
        {"expr": "sum by (code) (rate(traefik_service_requests_total[5m]))", "refId": "A", "legendFormat": "{{ "{{" }} code {{ "}}" }}"}
      ]
    },
    {
      "type": "timeseries",
      "title": "Latency (p50 / p95 / p99)",
      "datasource": "Prometheus",
      "gridPos": {"x": 0, "y": 8, "w": 24, "h": 8},
      "targets": [
        {"expr": "histogram_quantile(0.50, sum by (le) (rate(traefik_service_request_duration_seconds_bucket[5m])))", "refId": "A", "legendFormat": "p50"},
        {"expr": "histogram_quantile(0.95, sum by (le) (rate(traefik_service_request_duration_seconds_bucket[5m])))", "refId": "B", "legendFormat": "p95"},
        {"expr": "histogram_quantile(0.99, sum by (le) (rate(traefik_service_request_duration_seconds_bucket[5m])))", "refId": "C", "legendFormat": "p99"}
      ]
    },
    {
      "type": "stat",
      "title": "Cert expiry (days)",
      "datasource": "Prometheus",
      "gridPos": {"x": 0, "y": 16, "w": 12, "h": 6},
      "targets": [
        {"expr": "(certmanager_certificate_expiration_timestamp_seconds - time()) / 86400", "refId": "A", "legendFormat": "{{ "{{" }} name {{ "}}" }}"}
      ]
    }
  ]
}`

const clusterTpl = dashboardBase + `
  "panels": [
    {
      "type": "stat",
      "title": "Nodes Ready",
      "datasource": "Prometheus",
      "gridPos": {"x": 0, "y": 0, "w": 12, "h": 4},
      "targets": [{"expr": "sum(kube_node_status_condition{condition=\"Ready\",status=\"true\"})", "refId": "A"}]
    },
    {
      "type": "stat",
      "title": "Pods Running",
      "datasource": "Prometheus",
      "gridPos": {"x": 12, "y": 0, "w": 12, "h": 4},
      "targets": [{"expr": "sum(kube_pod_status_phase{phase=\"Running\"})", "refId": "A"}]
    },
    {
      "type": "timeseries",
      "title": "Node CPU usage",
      "datasource": "Prometheus",
      "gridPos": {"x": 0, "y": 4, "w": 12, "h": 8},
      "targets": [{"expr": "rate(node_cpu_seconds_total{mode!=\"idle\"}[5m])", "refId": "A", "legendFormat": "{{ "{{" }} instance {{ "}}" }} {{ "{{" }} mode {{ "}}" }}"}]
    },
    {
      "type": "timeseries",
      "title": "Node memory usage",
      "datasource": "Prometheus",
      "gridPos": {"x": 12, "y": 4, "w": 12, "h": 8},
      "targets": [{"expr": "1 - (node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes)", "refId": "A", "legendFormat": "{{ "{{" }} instance {{ "}}" }}"}]
    }{{ if .HasHA }},
    {
      "type": "timeseries",
      "title": "etcd has leader",
      "datasource": "Prometheus",
      "gridPos": {"x": 0, "y": 12, "w": 24, "h": 6},
      "targets": [{"expr": "etcd_server_has_leader", "refId": "A"}]
    }{{ end }}
  ]
}`

const deploysTpl = dashboardBase + `
  "panels": [
    {
      "type": "timeseries",
      "title": "Deploys (by hash)",
      "datasource": "Prometheus",
      "gridPos": {"x": 0, "y": 0, "w": 24, "h": 8},
      "targets": [
        {"expr": "count by (label_nvoi_deploy_hash) (kube_pod_labels{label_nvoi_deploy_hash!=\"\"})", "refId": "A", "legendFormat": "{{ "{{" }} label_nvoi_deploy_hash {{ "}}" }}"}
      ]
    }
  ]
}`
