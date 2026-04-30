package kube

import (
	"crypto/sha256"
	"encoding/hex"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// Caddy in-cluster constants. Single source of truth.
const (
	CaddyNamespace      = "kube-system"
	CaddyName           = "caddy"
	CaddyConfigMapName  = "caddy-config"
	CaddyPVCName        = "caddy-data"
	CaddyImage          = "caddy:2.10-alpine"
	CaddyAdminListen    = "localhost:2019"
	CaddyConfigMountDir = "/etc/caddy"
	CaddyConfigKey      = "caddy.json"
	CaddyDataDir        = "/data"
	CaddyConfigStateDir = "/config"

	caddySeedConfigJSON = `{"admin":{"listen":"localhost:2019"},"apps":{"http":{"servers":{"main":{"listen":[":80"],"routes":[]}}}}}`
)

// caddyLabels are the labels every Caddy resource carries. Includes
// app.kubernetes.io/name=caddy so FirstPod / Service selectors find
// the pod. Owner label gets stamped by ApplyOwned at apply time.
func caddyLabels() map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       CaddyName,
		"app.kubernetes.io/managed-by": "nvoi",
	}
}

// caddySeedChecksum hashes the seed config so the Deployment's pod
// template carries it as an annotation. When nvoi versions change
// the seed bytes, the checksum changes → pod template changes →
// k8s rolls the pod and the new pod boots with the new seed.
// Without this annotation, ConfigMap volume updates land on disk
// but the running Caddy process never re-reads its bootstrap file.
func caddySeedChecksum() string {
	sum := sha256.Sum256([]byte(caddySeedConfigJSON))
	return hex.EncodeToString(sum[:])
}

// buildCaddyPVC: 1Gi PVC at /data for ACME state. Storage class
// unset → k3s default (`local-path`) takes over → hostPath on master.
func buildCaddyPVC() *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      CaddyPVCName,
			Namespace: CaddyNamespace,
			Labels:    caddyLabels(),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
			},
		},
	}
}

// buildCaddyConfigMap: the seed admin-only config. Real config flows
// through the admin API; this exists so the pod's TCP-on-:80
// readiness probe passes from boot before the reconciler reaches
// ReloadCaddyConfig.
func buildCaddyConfigMap() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      CaddyConfigMapName,
			Namespace: CaddyNamespace,
			Labels:    caddyLabels(),
		},
		Data: map[string]string{CaddyConfigKey: caddySeedConfigJSON},
	}
}

// buildCaddyService: ClusterIP for internal probes. Real public
// traffic enters via hostPort 80/443 on the master, NOT through this
// Service. Admin port (2019) intentionally not exposed.
func buildCaddyService() *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      CaddyName,
			Namespace: CaddyNamespace,
			Labels:    caddyLabels(),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: map[string]string{"app.kubernetes.io/name": CaddyName},
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 80, TargetPort: intstr.FromInt(80), Protocol: corev1.ProtocolTCP},
				{Name: "https", Port: 443, TargetPort: intstr.FromInt(443), Protocol: corev1.ProtocolTCP},
			},
		},
	}
}

// buildCaddyDeployment: single replica, Recreate strategy (only one
// pod can hold hostPort :80/:443 at a time), nodeSelector + toleration
// pin to master.
func buildCaddyDeployment() *appsv1.Deployment {
	one := int32(1)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      CaddyName,
			Namespace: CaddyNamespace,
			Labels:    caddyLabels(),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &one,
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType},
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app.kubernetes.io/name": CaddyName},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: caddyLabels(),
					// Seed-config checksum on the pod template — changes
					// to caddySeedConfigJSON force a rolling restart.
					Annotations: map[string]string{
						"nvoi/caddy-seed-checksum": caddySeedChecksum(),
					},
				},
				Spec: corev1.PodSpec{
					// Pin to master via the same nvoi-role label
					// applied by deploy.go's node-labels step.
					NodeSelector: map[string]string{"nvoi-role": "master"},
					// Tolerate control-plane taints so this schedules
					// on a single-master cluster where the master also
					// runs workloads.
					Tolerations: []corev1.Toleration{
						{Key: "node-role.kubernetes.io/control-plane", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
						{Key: "node-role.kubernetes.io/master", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
					},
					Containers: []corev1.Container{{
						Name:    CaddyName,
						Image:   CaddyImage,
						Command: []string{"caddy"},
						// --adapter "" → Caddy reads native JSON, not Caddyfile.
						Args: []string{"run", "--config", CaddyConfigMountDir + "/" + CaddyConfigKey, "--adapter", ""},
						Ports: []corev1.ContainerPort{
							{Name: "http", ContainerPort: 80, HostPort: 80, Protocol: corev1.ProtocolTCP},
							{Name: "https", ContainerPort: 443, HostPort: 443, Protocol: corev1.ProtocolTCP},
						},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "config", MountPath: CaddyConfigMountDir, ReadOnly: true},
							{Name: "data", MountPath: CaddyDataDir},
							{Name: "state", MountPath: CaddyConfigStateDir},
						},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt(80)},
							},
							InitialDelaySeconds: 2,
							PeriodSeconds:       5,
							TimeoutSeconds:      3,
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("50m"),
								corev1.ResourceMemory: resource.MustParse("64Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("500m"),
								corev1.ResourceMemory: resource.MustParse("256Mi"),
							},
						},
					}},
					Volumes: []corev1.Volume{
						{
							Name: "config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: CaddyConfigMapName},
								},
							},
						},
						{
							Name: "data",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: CaddyPVCName},
							},
						},
						{
							Name:         "state",
							VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
						},
					},
				},
			},
		},
	}
}
