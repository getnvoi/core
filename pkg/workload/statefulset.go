package workload

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/internal/kube"
	"github.com/getnvoi/core/pkg/runtime"
)

// buildStatefulSet produces the typed StatefulSet for any service
// declaring `storage:`. Mirrors buildDeployment with two additions:
//
//   - serviceName references the headless Service of the same name
//     (buildService emits ClusterIP=None when storage is set), giving
//     each pod stable DNS via the Service's per-pod records;
//   - VolumeClaimTemplates declares the PVC the pod's container
//     mounts at svc.Storage.MountPath. StorageClassName left nil → k8s
//     picks the cluster default. On k3s that's `local-path`, which
//     provisions a hostPath volume on whichever node the pod first
//     schedules to (PVC binding then pins the pod to that node).
//
// The PVC is OWNED by the StatefulSet controller — we don't stamp
// nvoi/owner on it via ApplyOwned. The PVC's lifecycle follows the
// StatefulSet's (delete the StatefulSet → PVC is preserved by k8s
// policy until explicitly reclaimed; `nvoi destroy` tears the node
// down which takes the hostPath volume with it).
func buildStatefulSet(rt *runtime.Runtime, name string, svc config.ServiceSpec) (*appsv1.StatefulSet, error) {
	replicas := int32(1)
	if svc.Replicas != nil {
		replicas = int32(*svc.Replicas)
	}
	template, err := podTemplateFor(rt, name, svc)
	if err != nil {
		return nil, err
	}
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    objectLabels(rt, name),
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &replicas,
			ServiceName: name,
			Selector:    &metav1.LabelSelector{MatchLabels: serviceSelector(name)},
			Template:    template,
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{{
				ObjectMeta: metav1.ObjectMeta{
					Name: name + "-data",
					Labels: map[string]string{
						kube.LabelOwner: kube.OwnerServices,
						labelService:    name,
					},
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
	}, nil
}
