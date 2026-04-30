package workload

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/core/internal/kube"
	"github.com/getnvoi/core/internal/runtime"
)

// BuildAppSecret renders the single Opaque Secret holding resolved
// values for every entry in cfg.Secrets. Per-service `secrets:`
// whitelists pull from this object via secretKeyRef.
//
// Returns nil when cfg.Secrets is empty — callers skip Apply in that
// case.
//
// Resolution: takes ALREADY-RESOLVED values from rt.Secrets. The
// cmd/cli boundary (resolveSecrets) reads os.Getenv before
// runtime.Build runs, so we trust every key here has a non-empty
// literal.
func BuildAppSecret(rt *runtime.Runtime) *corev1.Secret {
	if len(rt.Secrets) == 0 {
		return nil
	}
	data := make(map[string][]byte, len(rt.Secrets))
	for k, v := range rt.Secrets {
		data[k] = []byte(v)
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      AppSecretName,
			Namespace: namespace,
			Labels: map[string]string{
				kube.LabelOwner: kube.OwnerAppSecrets,
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: data,
	}
}
