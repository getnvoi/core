package workload

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/core/pkg/internal/kube"
	"github.com/getnvoi/core/pkg/runtime"
)

// buildAppSecret renders the single Opaque Secret holding resolved
// values for every entry in cfg.Secrets. Per-service `secrets:`
// whitelists pull from this object via secretKeyRef.
//
// Returns nil when cfg.Secrets is empty — callers skip Apply in that
// case.
//
// Resolution: takes ALREADY-RESOLVED values from rt.SecretValues. The
// cmd/cli boundary (resolveSecrets) reads os.Getenv before
// runtime.Build runs, so we trust every key here has a non-empty
// literal.
func buildAppSecret(rt *runtime.Runtime) *corev1.Secret {
	if len(rt.SecretValues) == 0 {
		return nil
	}
	data := make(map[string][]byte, len(rt.SecretValues))
	for k, v := range rt.SecretValues {
		data[k] = []byte(v)
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      appSecretName,
			Namespace: namespace,
			Labels: map[string]string{
				kube.LabelOwner: kube.OwnerAppSecrets,
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: data,
	}
}
