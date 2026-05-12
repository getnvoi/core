package observability

// nodeexporter.go builds the node-exporter DaemonSet + Service.
// Required for the cluster dashboard's node CPU/mem panels —
// `node_cpu_seconds_total`, `node_memory_*` come from this exporter.
// k3s doesn't bundle it; we run it ourselves under
// OwnerObservability scope.
//
// One pod per node (incl. master via Operator=Exists toleration).
// hostNetwork: true + hostPID: true so the exporter sees host-level
// metrics; mounts /proc + /sys + / read-only for the same reason.

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	nodeExporterComponent = "node-exporter"
	NodeExporterImage     = "quay.io/prometheus/node-exporter:v1.8.2"
)

// buildNodeExporterDaemonSet renders the per-node exporter pod.
// hostNetwork+hostPID gives kernel-level metric visibility; the
// security boundary is the node, same as Promtail's /var/log mount.
func buildNodeExporterDaemonSet() *appsv1.DaemonSet {
	hostPathDir := corev1.HostPathDirectory
	hostPathRoot := corev1.HostPathDirectory
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      nodeExporterComponent,
			Namespace: Namespace,
			Labels:    objectLabels(nodeExporterComponent),
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: selectorFor(nodeExporterComponent)},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: podLabels(nodeExporterComponent)},
				Spec: corev1.PodSpec{
					HostNetwork: true,
					HostPID:     true,
					Tolerations: []corev1.Toleration{
						{Operator: corev1.TolerationOpExists},
					},
					Containers: []corev1.Container{{
						Name:  "node-exporter",
						Image: NodeExporterImage,
						Args: []string{
							"--path.procfs=/host/proc",
							"--path.sysfs=/host/sys",
							"--path.rootfs=/host/root",
							"--web.listen-address=0.0.0.0:9100",
							`--collector.filesystem.mount-points-exclude=^/(dev|proc|sys|var/lib/docker/.+|var/lib/kubelet/.+)($|/)`,
						},
						Ports: []corev1.ContainerPort{
							{Name: "metrics", ContainerPort: 9100, HostPort: 9100, Protocol: corev1.ProtocolTCP},
						},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "proc", MountPath: "/host/proc", ReadOnly: true},
							{Name: "sys", MountPath: "/host/sys", ReadOnly: true},
							{Name: "root", MountPath: "/host/root", ReadOnly: true, MountPropagation: ptrMP(corev1.MountPropagationHostToContainer)},
						},
						Resources: stdRequests("50m", "32Mi"),
					}},
					Volumes: []corev1.Volume{
						{Name: "proc", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/proc", Type: &hostPathDir}}},
						{Name: "sys", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/sys", Type: &hostPathDir}}},
						{Name: "root", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/", Type: &hostPathRoot}}},
					},
				},
			},
		},
	}
}

// buildNodeExporterService renders the headless Service Prometheus
// targets via kubernetes_sd_configs role=endpoints. Clusters with
// node-exporter on hostNetwork still benefit from the Service —
// SD picks up the endpoint list automatically.
func buildNodeExporterService() *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      nodeExporterComponent,
			Namespace: Namespace,
			Labels:    objectLabels(nodeExporterComponent),
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: corev1.ClusterIPNone,
			Selector:  selectorFor(nodeExporterComponent),
			Ports: []corev1.ServicePort{
				{Name: "metrics", Port: 9100, Protocol: corev1.ProtocolTCP},
			},
		},
	}
}

func ptrMP(m corev1.MountPropagationMode) *corev1.MountPropagationMode {
	return &m
}
