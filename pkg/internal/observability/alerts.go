package observability

// alerts.go generates Grafana Unified Alerting provisioning YAML
// from cfg.Services + cfg.Domains + cfg.Servers. Output is a single
// ConfigMap that PR 6 will mount into Grafana's
// /etc/grafana/provisioning/alerting/ directory.
//
// Alert taxonomy (v1):
//
//   per-service:
//     replicas-not-ready   ready < spec for 5m → warning
//     crashloop            container restart rate > 0 for 10m → warning
//     pvc-full             stateful only: used/cap > 85% for 10m → warning
//
//   per-domain:
//     5xx-rate             5xx fraction > 1% for 5m → critical
//     cert-expiring        < 7 days remaining → warning
//
//   cluster:
//     node-not-ready       any node Ready==false for 5m → critical
//     etcd-no-leader       HA only: max(has_leader)==0 for 1m → critical
//
// All rules route through the default notification policy (PR 6).

import (
	"bytes"
	"fmt"
	"sort"
	"text/template"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/internal/kube"
	"github.com/getnvoi/core/pkg/runtime"
)

// alertLabel / alertLabelValue match the kiwigrid sidecar discovery
// pattern Grafana's alerting provisioner consumes when watched.
// PR 6 mounts this ConfigMap directly into the alerting provisioning
// dir; the label is declared upfront so future watch-based reloads
// are a one-line wiring change.
const (
	alertLabel      = "grafana_alert"
	alertLabelValue = "1"
)

// alertRulesConfigMapName is the singleton ConfigMap that PR 6's
// Grafana Deployment volume-mounts at
// /etc/grafana/provisioning/alerting/. Singleton because Grafana's
// provisioning loader concatenates all YAML in the directory at
// boot — splitting per-rule-group buys nothing and adds sweep noise.
const alertRulesConfigMapName = "alert-rules"

// BuildAlertRules renders the Grafana Unified Alerting provisioning
// YAML covering every alert nvoi declares for this config. Returns
// the ConfigMap + its declared name (for the deploy phase's sweep).
//
// Generator produces ONE rule group ("nvoi") containing all rules.
// Splitting into per-category groups (services / ingress / cluster)
// is a UX choice deferred to operators who customize via the
// Grafana UI — out of scope for v1.
func BuildAlertRules(rt *runtime.Runtime) (*corev1.ConfigMap, string, error) {
	cfg := rt.Cfg
	ctx := alertCtx{
		Services:    sortedServices(cfg),
		Domains:     flattenDomainsForAlerts(cfg),
		Stateful:    sortedStatefulServices(cfg),
		IsHA:        masterCount(cfg) >= 2,
	}

	t, err := template.New("alerts").Parse(alertRulesTpl)
	if err != nil {
		return nil, "", fmt.Errorf("parse alerts template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, ctx); err != nil {
		return nil, "", fmt.Errorf("execute alerts template: %w", err)
	}

	labels := objectLabels(grafanaComponent)
	labels[alertLabel] = alertLabelValue
	labels[kube.LabelOwner] = kube.OwnerObservability // pre-stamp for self-description

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      alertRulesConfigMapName,
			Namespace: Namespace,
			Labels:    labels,
		},
		Data: map[string]string{"alerts.yaml": buf.String()},
	}
	return cm, cm.Name, nil
}

// ── Template context ──────────────────────────────────────────────────

type alertCtx struct {
	Services []string // every cfg.Services key (sorted)
	Domains  []alertDomain
	Stateful []string // services with Storage set (subset of Services)
	IsHA     bool
}

// alertDomain pairs a hostname with the service it points at. The
// 5xx-rate rule needs both: the hostname for the alert name + the
// service for the metric label.
type alertDomain struct {
	Host    string
	Service string
}

func sortedServices(cfg *config.Config) []string {
	out := make([]string, 0, len(cfg.Services))
	for name := range cfg.Services {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func sortedStatefulServices(cfg *config.Config) []string {
	var out []string
	for name, svc := range cfg.Services {
		if svc.IsStateful() {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func flattenDomainsForAlerts(cfg *config.Config) []alertDomain {
	var out []alertDomain
	for svc, hosts := range cfg.Domains {
		for _, h := range hosts {
			out = append(out, alertDomain{Host: h, Service: svc})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out
}

func masterCount(cfg *config.Config) int {
	n := 0
	for _, s := range cfg.Servers {
		if s.Role == "master" {
			n++
		}
	}
	return n
}

// ── Provisioning YAML template ────────────────────────────────────────
//
// Grafana Unified Alerting provisioning format v1. apiVersion: 1.
// Each rule references a `condition` (refId of the data target whose
// boolean result fires the alert). datasourceUid is "prometheus" —
// matches the Datasource ConfigMap name PR 6 produces.

const alertRulesTpl = `apiVersion: 1
groups:
  - orgId: 1
    name: nvoi
    folder: nvoi
    interval: 1m
    rules:
{{- range .Services }}
      - uid: nvoi-{{ . }}-replicas-not-ready
        title: "{{ . }}: ready replicas below desired"
        condition: A
        data:
          - refId: A
            relativeTimeRange: {from: 600, to: 0}
            datasourceUid: prometheus
            model:
              expr: 'kube_deployment_status_replicas_ready{namespace="default",deployment="{{ . }}"} < kube_deployment_spec_replicas{namespace="default",deployment="{{ . }}"}'
              intervalMs: 60000
              maxDataPoints: 43200
              refId: A
        for: 5m
        labels:
          severity: warning
          service: {{ . }}
        annotations:
          summary: "{{ . }} has fewer ready replicas than desired"
      - uid: nvoi-{{ . }}-crashloop
        title: "{{ . }}: container restart rate"
        condition: A
        data:
          - refId: A
            relativeTimeRange: {from: 600, to: 0}
            datasourceUid: prometheus
            model:
              expr: 'rate(kube_pod_container_status_restarts_total{namespace="default",pod=~"{{ . }}-.*"}[5m]) > 0'
              intervalMs: 60000
              maxDataPoints: 43200
              refId: A
        for: 10m
        labels:
          severity: warning
          service: {{ . }}
        annotations:
          summary: "{{ . }} is restarting"
{{- end }}
{{- range .Stateful }}
      - uid: nvoi-{{ . }}-pvc-full
        title: "{{ . }}: PVC > 85% full"
        condition: A
        data:
          - refId: A
            relativeTimeRange: {from: 600, to: 0}
            datasourceUid: prometheus
            model:
              expr: 'kubelet_volume_stats_used_bytes{persistentvolumeclaim=~"{{ . }}-.*"} / kubelet_volume_stats_capacity_bytes{persistentvolumeclaim=~"{{ . }}-.*"} > 0.85'
              intervalMs: 60000
              maxDataPoints: 43200
              refId: A
        for: 10m
        labels:
          severity: warning
          service: {{ . }}
        annotations:
          summary: "{{ . }} PVC over 85% capacity"
{{- end }}
{{- range .Domains }}
      - uid: nvoi-{{ .Host }}-5xx
        title: "{{ .Host }}: 5xx rate > 1%"
        condition: A
        data:
          - refId: A
            relativeTimeRange: {from: 600, to: 0}
            datasourceUid: prometheus
            model:
              expr: 'sum(rate(traefik_service_requests_total{code=~"5..",service=~"{{ .Service }}.*"}[5m])) / sum(rate(traefik_service_requests_total{service=~"{{ .Service }}.*"}[5m])) > 0.01'
              intervalMs: 60000
              maxDataPoints: 43200
              refId: A
        for: 5m
        labels:
          severity: critical
          service: {{ .Service }}
          host: {{ .Host }}
        annotations:
          summary: "{{ .Host }} 5xx rate above 1%"
      - uid: nvoi-{{ .Host }}-cert-expiring
        title: "{{ .Host }}: cert expiring soon"
        condition: A
        data:
          - refId: A
            relativeTimeRange: {from: 600, to: 0}
            datasourceUid: prometheus
            model:
              expr: '(certmanager_certificate_expiration_timestamp_seconds - time()) / 86400 < 7'
              intervalMs: 60000
              maxDataPoints: 43200
              refId: A
        for: 1h
        labels:
          severity: warning
          host: {{ .Host }}
        annotations:
          summary: "TLS cert for {{ .Host }} expires in under 7 days"
{{- end }}
      - uid: nvoi-node-not-ready
        title: "Node not Ready"
        condition: A
        data:
          - refId: A
            relativeTimeRange: {from: 600, to: 0}
            datasourceUid: prometheus
            model:
              expr: 'kube_node_status_condition{condition="Ready",status="true"} == 0'
              intervalMs: 60000
              maxDataPoints: 43200
              refId: A
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "A cluster node is not Ready"
{{- if .IsHA }}
      - uid: nvoi-etcd-no-leader
        title: "etcd has no leader"
        condition: A
        data:
          - refId: A
            relativeTimeRange: {from: 60, to: 0}
            datasourceUid: prometheus
            model:
              expr: 'max(etcd_server_has_leader) == 0'
              intervalMs: 60000
              maxDataPoints: 43200
              refId: A
        for: 1m
        labels:
          severity: critical
        annotations:
          summary: "etcd cluster has no leader"
{{- end }}
`
