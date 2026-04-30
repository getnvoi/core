package workload

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/core/internal/config"
	"github.com/getnvoi/core/internal/runtime"
)

// BuildStatefulSet produces the typed StatefulSet for any service
// declaring `storage:`. Mirrors BuildDeployment with two additions:
//
//   - serviceName references the headless Service (BuildService
//     emits ClusterIP=None when storage is set), giving each pod
//     stable DNS via the Service's per-pod records;
//   - VolumeClaimTemplates declares the PVC the pod's container
//     mounts at svc.Storage.MountPath. StorageClassName left nil →
//     k8s picks the cluster-default class. On k3s that's
//     `local-path`, which provisions a hostPath volume on whichever
//     node the pod first schedules to (PVC binding then pins the pod
//     to that node).
//
// Replicas: explicit ServiceSpec.Replicas wins; nil → default 1.
// Selector matches the pod-template via LabelOwner+LabelService —
// stable across deploys (no LabelDeployHash on selectors).
func BuildStatefulSet(rt *runtime.Runtime, name string, svc config.ServiceSpec) *appsv1.StatefulSet {
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

	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    objLabels,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &replicas,
			ServiceName: name, // pairs with the headless Service of the same name
			Selector:    &metav1.LabelSelector{MatchLabels: selector},
			Template:    podTemplateFor(rt, name, svc),
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{{
				ObjectMeta: metav1.ObjectMeta{
					Name:   name + "-data",
					Labels: map[string]string{LabelOwner: "nvoi", LabelService: name},
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse(svc.Storage.Size),
						},
					},
				},
			}},
		},
	}
}
