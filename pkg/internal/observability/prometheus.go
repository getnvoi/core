package observability

// prometheus.go builds the Prometheus + Thanos sidecar StatefulSet
// (single pod, two containers), the Thanos Querier Deployment, the
// Thanos Store Deployment, their Services, and the shared
// objstore.yml Secret that lets sidecar + store talk to the
// -metrics bucket.
//
// Architecture: Prometheus writes 2h blocks to its local TSDB
// (emptyDir). The Thanos sidecar in the same pod watches that
// directory and ships each completed block to the metrics bucket.
// Thanos Store reads historical blocks from the bucket. Thanos
// Querier fans out PromQL across the sidecar (recent / in-WAL) AND
// the Store (older / in-bucket) and presents a unified view —
// that's what Grafana points at as the Prometheus datasource.
//
// Why StatefulSet for the prom + sidecar pod: gives us a stable name
// (prometheus-0) the thanos querier can target via the headless
// Service for gRPC discovery. Replicas = 1; HA Prometheus is a future
// concern.

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// objstoreSecretName is the shared Secret holding the Thanos
// objstore.yml. Consumed by both the sidecar (writes blocks) and the
// store (reads blocks).
const objstoreSecretName = "thanos-objstore"

// promConfigName is the ConfigMap holding the prometheus.yml scrape
// configs.
const promConfigName = "prometheus-config"

// Component names — also the Service names + StatefulSet/Deployment
// names. Operators reading `kubectl get all -n nvoi-observability`
// see these.
const (
	prometheusComponent    = "prometheus"
	thanosQuerierComponent = "thanos-querier"
	thanosStoreComponent   = "thanos-store"
)

// buildThanosObjstoreSecret renders the objstore.yml Secret Thanos
// (both sidecar + store) consumes. S3 wire format — works for R2,
// AWS S3, MinIO, etc. all the same. Endpoint differs per provider;
// signature_version=4 is mandatory for R2.
//
// stringData (not binary) so YAML stays diff-able for an operator
// who pulls it for debugging.
func buildThanosObjstoreSecret(creds BucketCreds) *corev1.Secret {
	// Strip scheme + trailing slash from Endpoint — Thanos's s3
	// config takes host:port, not https://host.
	host := stripScheme(creds.Endpoint)

	objstore := fmt.Sprintf(`type: S3
config:
  bucket: %s
  endpoint: %s
  region: %s
  access_key: %s
  secret_key: %s
  insecure: false
  signature_version2: false
`, creds.MetricsBucket, host, creds.Region, creds.AccessKey, creds.SecretKey)

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      objstoreSecretName,
			Namespace: Namespace,
			Labels:    objectLabels("thanos"),
		},
		Type:       corev1.SecretTypeOpaque,
		StringData: map[string]string{"objstore.yml": objstore},
	}
}

// buildPrometheusConfigMap renders the prometheus.yml the Prometheus
// container loads at start. Minimal scrape list for v1:
//   - prometheus self-scrape
//   - kubernetes-pods (any pod with prometheus.io/scrape=true annotation)
//   - kubelet (node metrics — cAdvisor lives here too)
//   - traefik (k3s ships it; default metrics endpoint :9100)
//
// Block boundaries pinned to 2h via the Prometheus container args
// (not the config) — that's where the Thanos sidecar contract lives.
func buildPrometheusConfigMap() *corev1.ConfigMap {
	cfg := `global:
  scrape_interval: 30s
  external_labels:
    cluster: nvoi
    replica: prometheus-0

scrape_configs:
  - job_name: prometheus
    static_configs:
      - targets: ['localhost:9090']

  - job_name: kubernetes-pods
    kubernetes_sd_configs:
      - role: pod
    relabel_configs:
      - source_labels: [__meta_kubernetes_pod_annotation_prometheus_io_scrape]
        action: keep
        regex: true
      - source_labels: [__meta_kubernetes_pod_annotation_prometheus_io_path]
        action: replace
        target_label: __metrics_path__
        regex: (.+)
      - source_labels: [__address__, __meta_kubernetes_pod_annotation_prometheus_io_port]
        action: replace
        regex: ([^:]+)(?::\d+)?;(\d+)
        replacement: $1:$2
        target_label: __address__
      - source_labels: [__meta_kubernetes_namespace]
        target_label: namespace
      - source_labels: [__meta_kubernetes_pod_name]
        target_label: pod
      - source_labels: [__meta_kubernetes_pod_label_nvoi_service]
        target_label: service

  - job_name: kubelet
    kubernetes_sd_configs:
      - role: node
    scheme: https
    tls_config:
      ca_file: /var/run/secrets/kubernetes.io/serviceaccount/ca.crt
      insecure_skip_verify: true
    bearer_token_file: /var/run/secrets/kubernetes.io/serviceaccount/token
    relabel_configs:
      - action: labelmap
        regex: __meta_kubernetes_node_label_(.+)
      - target_label: __address__
        replacement: kubernetes.default.svc:443
      - source_labels: [__meta_kubernetes_node_name]
        regex: (.+)
        target_label: __metrics_path__
        replacement: /api/v1/nodes/$1/proxy/metrics
`
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      promConfigName,
			Namespace: Namespace,
			Labels:    objectLabels(prometheusComponent),
		},
		Data: map[string]string{"prometheus.yml": cfg},
	}
}

// buildPrometheusStatefulSet renders the prom + thanos-sidecar pod.
// Single replica, 2h block boundaries (Thanos sidecar requirement),
// shared emptyDir between containers so the sidecar can scan
// /prometheus/ for completed blocks.
//
// emptyDir not PVC: blocks ship to object storage every 2h. Loss of
// the in-pod WAL means at most 2h of unindexed data; the historical
// view (via Thanos Store) is intact.
func buildPrometheusStatefulSet() *appsv1.StatefulSet {
	replicas := int32(1)
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      prometheusComponent,
			Namespace: Namespace,
			Labels:    objectLabels(prometheusComponent),
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &replicas,
			ServiceName: prometheusComponent + "-headless",
			Selector:    &metav1.LabelSelector{MatchLabels: selectorFor(prometheusComponent)},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: podLabels(prometheusComponent)},
				Spec: corev1.PodSpec{
					ServiceAccountName: "default",
					Containers: []corev1.Container{
						{
							Name:  "prometheus",
							Image: PrometheusImage,
							Args: []string{
								"--config.file=/etc/prometheus/prometheus.yml",
								"--storage.tsdb.path=/prometheus",
								"--storage.tsdb.min-block-duration=2h",
								"--storage.tsdb.max-block-duration=2h",
								"--storage.tsdb.retention.time=6h",
								"--web.enable-lifecycle",
							},
							Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: 9090}},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "config", MountPath: "/etc/prometheus"},
								{Name: "tsdb", MountPath: "/prometheus"},
							},
							Resources: stdRequests("250m", "256Mi"),
						},
						{
							Name:  "thanos-sidecar",
							Image: ThanosImage,
							Args: []string{
								"sidecar",
								"--prometheus.url=http://localhost:9090",
								"--tsdb.path=/prometheus",
								"--objstore.config-file=/etc/thanos/objstore.yml",
								"--grpc-address=0.0.0.0:10901",
								"--http-address=0.0.0.0:10902",
							},
							// Port name "http" would collide with the
							// prometheus container's :9090 in the same
							// pod (k8s requires unique port names
							// per-pod). The sidecar's HTTP port isn't
							// targeted by any Service or probe by name,
							// so we drop the name field — k8s allows
							// nameless ContainerPort entries.
							Ports: []corev1.ContainerPort{
								{Name: "grpc", ContainerPort: 10901},
								{ContainerPort: 10902},
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "tsdb", MountPath: "/prometheus"},
								{Name: "objstore", MountPath: "/etc/thanos"},
							},
							Resources: stdRequests("100m", "128Mi"),
						},
					},
					Volumes: []corev1.Volume{
						{Name: "config", VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: promConfigName}},
						}},
						{Name: "tsdb", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
						{Name: "objstore", VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: objstoreSecretName},
						}},
					},
				},
			},
		},
	}
}

// buildPrometheusServices returns:
//   - the headless Service the StatefulSet uses for stable pod DNS
//     (prometheus-0.prometheus-headless...).
//   - the ClusterIP Service exposing :9090 (prom HTTP) + :10901
//     (thanos sidecar gRPC) for in-cluster consumers.
//
// The Thanos Querier targets the gRPC port on this Service via DNS
// resolution.
func buildPrometheusServices() []*corev1.Service {
	headless := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      prometheusComponent + "-headless",
			Namespace: Namespace,
			Labels:    objectLabels(prometheusComponent),
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: corev1.ClusterIPNone,
			Selector:  selectorFor(prometheusComponent),
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 9090, TargetPort: intstr.FromString("http")},
				{Name: "grpc", Port: 10901, TargetPort: intstr.FromString("grpc")},
			},
		},
	}
	clusterIP := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      prometheusComponent,
			Namespace: Namespace,
			Labels:    objectLabels(prometheusComponent),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: selectorFor(prometheusComponent),
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 9090, TargetPort: intstr.FromString("http")},
				{Name: "grpc", Port: 10901, TargetPort: intstr.FromString("grpc")},
			},
		},
	}
	return []*corev1.Service{headless, clusterIP}
}

// buildThanosQuerier renders the Thanos Querier Deployment + Service.
// Targets the Prometheus headless Service (for the sidecar's gRPC)
// + the Thanos Store Service. Grafana points at this :9090.
func buildThanosQuerier() (*appsv1.Deployment, *corev1.Service) {
	replicas := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      thanosQuerierComponent,
			Namespace: Namespace,
			Labels:    objectLabels(thanosQuerierComponent),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: selectorFor(thanosQuerierComponent)},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: podLabels(thanosQuerierComponent)},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "querier",
						Image: ThanosImage,
						Args: []string{
							"query",
							"--http-address=0.0.0.0:9090",
							"--grpc-address=0.0.0.0:10901",
							"--store=dnssrv+_grpc._tcp." + prometheusComponent + "-headless." + Namespace + ".svc.cluster.local",
							"--store=" + thanosStoreComponent + "." + Namespace + ".svc.cluster.local:10901",
							"--query.replica-label=replica",
						},
						Ports: []corev1.ContainerPort{
							{Name: "http", ContainerPort: 9090},
							{Name: "grpc", ContainerPort: 10901},
						},
						Resources: stdRequests("100m", "128Mi"),
					}},
				},
			},
		},
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      thanosQuerierComponent,
			Namespace: Namespace,
			Labels:    objectLabels(thanosQuerierComponent),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: selectorFor(thanosQuerierComponent),
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 9090, TargetPort: intstr.FromString("http")},
				{Name: "grpc", Port: 10901, TargetPort: intstr.FromString("grpc")},
			},
		},
	}
	return dep, svc
}

// buildThanosStore renders the Thanos Store Deployment + Service.
// Reads historical blocks from the metrics bucket and serves them
// over gRPC to the querier. Local cache in emptyDir (block index
// cache — re-fetched after restart, OK to be ephemeral).
func buildThanosStore() (*appsv1.Deployment, *corev1.Service) {
	replicas := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      thanosStoreComponent,
			Namespace: Namespace,
			Labels:    objectLabels(thanosStoreComponent),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: selectorFor(thanosStoreComponent)},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: podLabels(thanosStoreComponent)},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "store",
						Image: ThanosImage,
						Args: []string{
							"store",
							"--data-dir=/data",
							"--objstore.config-file=/etc/thanos/objstore.yml",
							"--grpc-address=0.0.0.0:10901",
							"--http-address=0.0.0.0:10902",
						},
						Ports: []corev1.ContainerPort{
							{Name: "grpc", ContainerPort: 10901},
							{Name: "http", ContainerPort: 10902},
						},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "data", MountPath: "/data"},
							{Name: "objstore", MountPath: "/etc/thanos"},
						},
						Resources: stdRequests("100m", "128Mi"),
					}},
					Volumes: []corev1.Volume{
						{Name: "data", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
						{Name: "objstore", VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: objstoreSecretName},
						}},
					},
				},
			},
		},
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      thanosStoreComponent,
			Namespace: Namespace,
			Labels:    objectLabels(thanosStoreComponent),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: selectorFor(thanosStoreComponent),
			Ports: []corev1.ServicePort{
				{Name: "grpc", Port: 10901, TargetPort: intstr.FromString("grpc")},
				{Name: "http", Port: 10902, TargetPort: intstr.FromString("http")},
			},
		},
	}
	return dep, svc
}

// stdRequests is the sizing helper. Requests-only (no limits) so
// noisy-neighbor protection is left to the operator's cluster-level
// policy; the budget in 00-overview.md targets ~1 GB resident for
// the whole stack on a cax21-class worker.
func stdRequests(cpu, mem string) corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(cpu),
			corev1.ResourceMemory: resource.MustParse(mem),
		},
	}
}

// stripScheme strips https:// or http:// + any trailing slash from
// the endpoint URL so Thanos's s3 config gets a bare host:port.
func stripScheme(s string) string {
	for _, prefix := range []string{"https://", "http://"} {
		if len(s) > len(prefix) && s[:len(prefix)] == prefix {
			s = s[len(prefix):]
			break
		}
	}
	if n := len(s); n > 0 && s[n-1] == '/' {
		s = s[:n-1]
	}
	return s
}

