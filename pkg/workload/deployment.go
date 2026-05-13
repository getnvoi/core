// Package workload turns config.ServiceSpec / RegistryDef into typed
// Kubernetes manifests and applies them via the kube package. The
// only public entry points are ApplyAll (full per-deploy reconcile)
// and ResolveRegistryCreds (env-var expansion at the cmd boundary).
// All builders are package-internal.
package workload

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/internal/kube"
	"github.com/getnvoi/core/pkg/naming"
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
	env := secretEnvVars(svc.Secrets)
	env = append(env, databaseEnvVars(rt, svc)...)
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
		Env: env,
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
func podTemplateFor(rt *runtime.Runtime, name string, svc config.ServiceSpec) (corev1.PodTemplateSpec, error) {
	pod := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: podLabels(rt, name)},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{containerFor(rt, name, svc)},
		},
	}
	servers := svc.Servers
	if len(servers) == 0 {
		servers = defaultPlacementKeys(rt.Cfg)
	}
	if err := applyNodePlacement(&pod.Spec, name, servers); err != nil {
		return pod, err
	}
	if len(rt.RegistryCreds) > 0 {
		pod.Spec.ImagePullSecrets = []corev1.LocalObjectReference{
			{Name: registrySecretName},
		}
	}
	return pod, nil
}

// objectLabels are the labels on the Deployment / StatefulSet itself
// (not the pod template). Carries the deploy hash so
// `kubectl get deploy -L nvoi/deploy-hash` answers
// "what hash is currently rolled out."
func objectLabels(rt *runtime.Runtime, name string) map[string]string {
	return map[string]string{
		kube.LabelOwner: kube.OwnerServices,
		labelService:    name,
		labelDeployHash: rt.DeployHash,
	}
}

// podLabels are the labels on every pod in the Deployment/StatefulSet's
// template. Includes labelAppName (for topologySpread grouping) on top
// of objectLabels.
func podLabels(rt *runtime.Runtime, name string) map[string]string {
	out := objectLabels(rt, name)
	out[labelAppName] = name
	return out
}

// serviceSelector is the stable selector both the Deployment /
// StatefulSet and the Service share. Strict architectural rule:
//
//   - INCLUDES   labelService (per-service identifier; unique by
//     construction).
//   - EXCLUDES   labelDeployHash (selector mutations orphan pods on
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
// labelService alone is unique enough to discriminate pods — no
// other system writes pods with `nvoi/service=<our-name>` keys.
func serviceSelector(name string) map[string]string {
	return map[string]string{labelService: name}
}

// buildDeployment turns a service spec into a typed Deployment.
// Replicas: explicit ServiceSpec.Replicas wins; nil → default 1.
// Selector matches the pod-template via LabelOwner+labelService —
// stable across deploys.
func buildDeployment(rt *runtime.Runtime, name string, svc config.ServiceSpec) (*appsv1.Deployment, error) {
	replicas := int32(1)
	if svc.Replicas != nil {
		replicas = int32(*svc.Replicas)
	}
	template, err := podTemplateFor(rt, name, svc)
	if err != nil {
		return nil, err
	}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: naming.Namespace,
			Labels:    objectLabels(rt, name),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: serviceSelector(name)},
			Template: template,
		},
	}, nil
}
