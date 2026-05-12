package observability

// loki.go builds Loki (single-binary mode) + Promtail (DaemonSet
// pod-log shipper) + their wiring (ConfigMaps, Service, RBAC).
//
// Loki uses the S3-compatible logs bucket as the chunk store. No
// PVC — chunks ship to object storage continuously; the BoltDB
// shipper index lives in emptyDir and re-syncs from the bucket on
// restart.
//
// Promtail tails /var/log/containers on every node, applies pipeline
// stages to extract per-pod labels (namespace, pod, nvoi/service),
// and pushes batches to Loki. One DaemonSet pod per node.

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	lokiComponent     = "loki"
	promtailComponent = "promtail"

	lokiConfigName     = "loki-config"
	promtailConfigName = "promtail-config"
)

// buildLokiConfigMap renders Loki's config.yaml. Single-binary mode
// (all targets in one process) using S3 chunk storage + BoltDB
// shipper index. Replication factor 1 (single replica). Retention
// is bucket-side (lifecycle rules) — Loki itself doesn't expire.
func buildLokiConfigMap(creds BucketCreds) *corev1.ConfigMap {
	host := stripScheme(creds.Endpoint)
	cfg := fmt.Sprintf(`auth_enabled: false

server:
  http_listen_port: 3100
  grpc_listen_port: 9095

common:
  path_prefix: /loki
  replication_factor: 1
  ring:
    kvstore:
      store: inmemory

schema_config:
  configs:
    - from: 2024-01-01
      store: boltdb-shipper
      object_store: s3
      schema: v13
      index:
        prefix: loki_index_
        period: 24h

storage_config:
  boltdb_shipper:
    active_index_directory: /loki/index
    cache_location: /loki/index_cache
  aws:
    s3: s3://%s:%s@%s/%s
    region: %s
    s3forcepathstyle: true
    insecure: false

compactor:
  working_directory: /loki/compactor
  delete_request_store: s3

ingester:
  chunk_idle_period: 30m
  chunk_target_size: 1572864
  max_chunk_age: 1h

limits_config:
  reject_old_samples: true
  reject_old_samples_max_age: 168h
`, creds.AccessKey, creds.SecretKey, host, creds.LogsBucket, creds.Region)

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      lokiConfigName,
			Namespace: Namespace,
			Labels:    objectLabels(lokiComponent),
		},
		Data: map[string]string{"config.yaml": cfg},
	}
}

// buildLoki renders the Loki StatefulSet + ClusterIP Service.
// StatefulSet for stable pod name (loki-0) — Grafana points at the
// Service, but the BoltDB shipper benefits from a deterministic
// hostname for chunk handoff identification.
func buildLoki() (*appsv1.StatefulSet, *corev1.Service) {
	replicas := int32(1)
	ss := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      lokiComponent,
			Namespace: Namespace,
			Labels:    objectLabels(lokiComponent),
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &replicas,
			ServiceName: lokiComponent + "-headless",
			Selector:    &metav1.LabelSelector{MatchLabels: selectorFor(lokiComponent)},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: podLabels(lokiComponent)},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "loki",
						Image: LokiImage,
						Args:  []string{"-config.file=/etc/loki/config.yaml", "-target=all"},
						Ports: []corev1.ContainerPort{
							{Name: "http", ContainerPort: 3100},
							{Name: "grpc", ContainerPort: 9095},
						},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "config", MountPath: "/etc/loki"},
							{Name: "data", MountPath: "/loki"},
						},
						Resources: stdRequests("250m", "256Mi"),
					}},
					Volumes: []corev1.Volume{
						{Name: "config", VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: lokiConfigName}},
						}},
						{Name: "data", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
					},
				},
			},
		},
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      lokiComponent,
			Namespace: Namespace,
			Labels:    objectLabels(lokiComponent),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: selectorFor(lokiComponent),
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 3100, TargetPort: intstr.FromString("http")},
			},
		},
	}
	return ss, svc
}

// buildPromtailConfigMap renders Promtail's config.yaml. Scrapes
// /var/log/pods/*/*/*.log (the canonical k8s container log path),
// extracts namespace + pod + container + service labels from the
// kubernetes_sd_configs metadata, and pushes to Loki via its HTTP
// API.
//
// Why kubernetes_sd_configs and not the older docker_sd / cri /
// journal sources: the SD model gives Promtail typed access to pod
// labels — we need nvoi/service propagated to Loki streams so
// dashboards can filter by service name (matches the pod label
// workload.LabelService stamps).
func buildPromtailConfigMap() *corev1.ConfigMap {
	cfg := `server:
  http_listen_port: 9080

positions:
  filename: /run/promtail/positions.yaml

clients:
  - url: http://loki.nvoi-observability.svc.cluster.local:3100/loki/api/v1/push

scrape_configs:
  - job_name: kubernetes-pods
    kubernetes_sd_configs:
      - role: pod
    relabel_configs:
      - source_labels: [__meta_kubernetes_pod_node_name]
        target_label: node
      - source_labels: [__meta_kubernetes_namespace]
        target_label: namespace
      - source_labels: [__meta_kubernetes_pod_name]
        target_label: pod
      - source_labels: [__meta_kubernetes_pod_container_name]
        target_label: container
      - source_labels: [__meta_kubernetes_pod_label_nvoi_service]
        target_label: service
      - source_labels: [__meta_kubernetes_pod_uid, __meta_kubernetes_pod_container_name]
        separator: /
        target_label: __path__
        replacement: /var/log/pods/*$1/$2/*.log
    pipeline_stages:
      - cri: {}
`
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      promtailConfigName,
			Namespace: Namespace,
			Labels:    objectLabels(promtailComponent),
		},
		Data: map[string]string{"config.yaml": cfg},
	}
}

// buildPromtailRBAC returns the ServiceAccount + ClusterRole +
// ClusterRoleBinding Promtail needs to do kubernetes_sd_configs
// discovery. Read-only on pods + nodes + namespaces — strict minimum.
func buildPromtailRBAC() (*corev1.ServiceAccount, *rbacv1.ClusterRole, *rbacv1.ClusterRoleBinding) {
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      promtailComponent,
			Namespace: Namespace,
			Labels:    objectLabels(promtailComponent),
		},
	}
	cr := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "nvoi-" + promtailComponent,
			Labels: objectLabels(promtailComponent),
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"nodes", "nodes/proxy", "services", "endpoints", "pods", "namespaces"},
				Verbs:     []string{"get", "list", "watch"},
			},
		},
	}
	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "nvoi-" + promtailComponent,
			Labels: objectLabels(promtailComponent),
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "nvoi-" + promtailComponent,
		},
		Subjects: []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      promtailComponent,
			Namespace: Namespace,
		}},
	}
	return sa, cr, crb
}

// buildPromtailDaemonSet renders the per-node Promtail pod. Mounts
// host /var/log + /var/lib/docker/containers (the canonical paths
// k8s + container runtimes use). DaemonSet so every node ships its
// own pod logs — no cross-node log transport.
func buildPromtailDaemonSet() *appsv1.DaemonSet {
	hostPathDir := corev1.HostPathDirectory
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      promtailComponent,
			Namespace: Namespace,
			Labels:    objectLabels(promtailComponent),
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: selectorFor(promtailComponent)},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: podLabels(promtailComponent)},
				Spec: corev1.PodSpec{
					ServiceAccountName: promtailComponent,
					// Promtail needs to schedule on every node including
					// the master (so master-pinned services have their
					// logs shipped). Tolerate the standard control-plane
					// taint.
					Tolerations: []corev1.Toleration{
						{Operator: corev1.TolerationOpExists},
					},
					Containers: []corev1.Container{{
						Name:  "promtail",
						Image: PromtailImage,
						Args:  []string{"-config.file=/etc/promtail/config.yaml"},
						Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: 9080}},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "config", MountPath: "/etc/promtail"},
							{Name: "varlog", MountPath: "/var/log", ReadOnly: true},
							{Name: "varlibdockercontainers", MountPath: "/var/lib/docker/containers", ReadOnly: true},
							{Name: "run", MountPath: "/run/promtail"},
						},
						Resources: stdRequests("100m", "64Mi"),
					}},
					Volumes: []corev1.Volume{
						{Name: "config", VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: promtailConfigName}},
						}},
						{Name: "varlog", VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{Path: "/var/log", Type: &hostPathDir},
						}},
						{Name: "varlibdockercontainers", VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{Path: "/var/lib/docker/containers", Type: &hostPathDir},
						}},
						{Name: "run", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
					},
				},
			},
		},
	}
}
