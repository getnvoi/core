// Package workload turns config.ServiceSpec / RegistryDef into typed
// Kubernetes manifests (Deployment, StatefulSet, Service, Secret).
// Pure transforms: no apiserver calls, no I/O. The kube package's
// Apply* methods do the actual writes.
//
// One file per manifest kind. Each Build* function takes the runtime
// (for App/Env/DeployHash) and the spec, returns a fully-formed
// pointer ready to hand to kc.ApplyDeployment / etc.
package workload

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/core/internal/config"
	"github.com/getnvoi/core/internal/runtime"
)

// imageRef computes the image string the Deployment's PodSpec gets.
// If the service has build:, we append the deploy hash so the
// PodSpec.image differs per deploy → rolling update fires.
// For pre-built images we leave the user's tag alone (operator
// controls when their tag changes; we don't second-guess).
func imageRef(rt *runtime.Runtime, svc config.ServiceSpec) string {
	if !svc.HasBuild() {
		return svc.Image
	}
	return svc.Image + ":" + rt.DeployHash
}

// containerFor builds the single PodSpec container shared by both
// Deployment and StatefulSet builders. Centralizes image resolution,
// port wiring, resource defaults, and secretKeyRef env injection so
// the two workload kinds stay in lockstep.
func containerFor(rt *runtime.Runtime, name string, svc config.ServiceSpec) corev1.Container {
	c := corev1.Container{
		Name:  name,
		Image: imageRef(rt, svc),
		Ports: []corev1.ContainerPort{{
			Name:          "http",
			ContainerPort: int32(svc.Port),
		}},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("64Mi"),
			},
		},
		Env: secretEnvVars(svc.Secrets),
	}
	if svc.Storage != nil {
		c.VolumeMounts = []corev1.VolumeMount{{
			Name:      name + "-data",
			MountPath: svc.Storage.MountPath,
		}}
	}
	return c
}

// podTemplateFor produces the pod template both workload kinds reuse.
// Carries the full label set (incl. deploy hash → rolling update
// trigger), the node placement, and imagePullSecrets when registry: is
// declared.
func podTemplateFor(rt *runtime.Runtime, name string, svc config.ServiceSpec) corev1.PodTemplateSpec {
	labels := map[string]string{
		LabelOwner:      "nvoi",
		LabelService:    name,
		LabelDeployHash: rt.DeployHash,
		LabelAppName:    name,
	}
	pod := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: labels},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{containerFor(rt, name, svc)},
		},
	}
	applyNodePlacement(&pod.Spec, name, svc.Servers)
	if len(rt.Cfg.Registry) > 0 {
		pod.Spec.ImagePullSecrets = []corev1.LocalObjectReference{
			{Name: registrySecretName},
		}
	}
	return pod
}

// BuildDeployment turns a service spec into a typed Deployment.
// Replicas: explicit ServiceSpec.Replicas wins; nil → default 1.
// Selector matches the pod-template via LabelOwner+LabelService —
// stable across deploys (no LabelDeployHash on selectors, that would
// orphan pods every roll).
func BuildDeployment(rt *runtime.Runtime, name string, svc config.ServiceSpec) *appsv1.Deployment {
	replicas := int32(1)
	if svc.Replicas != nil {
		replicas = int32(*svc.Replicas)
	}
	objLabels := map[string]string{
		LabelOwner:      "nvoi",
		LabelService:    name,
		LabelDeployHash: rt.DeployHash,
	}
	selector := map[string]string{LabelOwner: "nvoi", LabelService: name}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    objLabels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: selector},
			Template: podTemplateFor(rt, name, svc),
		},
	}
}
