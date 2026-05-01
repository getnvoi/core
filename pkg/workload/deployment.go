// Package workload turns config.ServiceSpec / RegistryDef into typed
// Kubernetes manifests (Deployment, StatefulSet, Service, Secret).
// Pure transforms: no apiserver calls, no I/O. The kube package's
// ApplyOwned does the actual writes.
//
// Each Build* function returns a self-describing object — its labels
// match what kube.ApplyOwned would stamp, so the manifest is
// inspectable at build time without an apply pass.
package workload

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/kube"
	"github.com/getnvoi/core/pkg/runtime"
)

// imageRef computes the image string the Deployment's PodSpec gets.
// If the service has build:, we append the deploy hash so the
// PodSpec.image differs per deploy → rolling update fires. For
// pre-built images we leave the user's tag alone.
func imageRef(rt *runtime.Runtime, svc config.ServiceSpec) string {
	if !svc.HasBuild() {
		return svc.Image
	}
	return svc.Image + ":" + rt.DeployHash
}

// containerFor builds the single PodSpec container shared by both
// Deployment and StatefulSet builders. Centralizes image resolution,
// port wiring, resource defaults, secretKeyRef env injection, and
// (for stateful services) the volume mount the StatefulSet's PVC
// template fronts.
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
// Carries the full label set (owner + service + deploy-hash + app-name
// for topology-spread), node placement, and imagePullSecrets when
// `registry:` is declared.
func podTemplateFor(rt *runtime.Runtime, name string, svc config.ServiceSpec) corev1.PodTemplateSpec {
	pod := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: podLabels(rt, name)},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{containerFor(rt, name, svc)},
		},
	}
	applyNodePlacement(&pod.Spec, name, svc.Servers)
	if len(rt.RegistryCreds) > 0 {
		pod.Spec.ImagePullSecrets = []corev1.LocalObjectReference{
			{Name: registrySecretName},
		}
	}
	return pod
}

// objectLabels are the labels on the Deployment / StatefulSet itself
// (not the pod template). Carries the deploy hash so
// `kubectl get deploy -L nvoi/deploy-hash` answers
// "what hash is currently rolled out."
func objectLabels(rt *runtime.Runtime, name string) map[string]string {
	return map[string]string{
		kube.LabelOwner: kube.OwnerServices,
		LabelService:    name,
		LabelDeployHash: rt.DeployHash,
	}
}

// podLabels are the labels on every pod in the Deployment/StatefulSet's
// template. Includes LabelAppName (for topologySpread grouping) on top
// of objectLabels.
func podLabels(rt *runtime.Runtime, name string) map[string]string {
	out := objectLabels(rt, name)
	out[LabelAppName] = name
	return out
}

// serviceSelector is the stable selector both the Deployment /
// StatefulSet and the Service share. Strict architectural rule:
//
//   - INCLUDES   LabelService (per-service identifier; unique by
//     construction).
//   - EXCLUDES   LabelDeployHash (selector mutations orphan pods on
//     every roll).
//   - EXCLUDES   kube.LabelOwner (owner is a sweep-scope concern, not
//     a selection concern). StatefulSet/Deployment
//     selectors are immutable post-Create — putting the
//     owner taxonomy in the selector means any future
//     taxonomy change requires destroy+recreate. Keep
//     owner on object metadata for ListOwned/SweepOwned;
//     keep it OUT of selectors so the architecture stays
//     evolvable.
//
// LabelService alone is unique enough to discriminate pods — no
// other system writes pods with `nvoi/service=<our-name>` keys.
func serviceSelector(name string) map[string]string {
	return map[string]string{LabelService: name}
}

// BuildDeployment turns a service spec into a typed Deployment.
// Replicas: explicit ServiceSpec.Replicas wins; nil → default 1.
// Selector matches the pod-template via LabelOwner+LabelService —
// stable across deploys.
func BuildDeployment(rt *runtime.Runtime, name string, svc config.ServiceSpec) *appsv1.Deployment {
	replicas := int32(1)
	if svc.Replicas != nil {
		replicas = int32(*svc.Replicas)
	}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    objectLabels(rt, name),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: serviceSelector(name)},
			Template: podTemplateFor(rt, name, svc),
		},
	}
}
