package workload

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/core/internal/config"
	"github.com/getnvoi/core/internal/runtime"
)

const registrySecretName = "registry-auth"

// dockerConfig is the JSON shape kubelet expects in a
// kubernetes.io/dockerconfigjson Secret. Per-host auths blob.
type dockerConfig struct {
	Auths map[string]dockerAuthEntry `json:"auths"`
}

type dockerAuthEntry struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Auth     string `json:"auth"` // base64(username:password)
}

// BuildRegistrySecret renders the kubernetes.io/dockerconfigjson
// Secret holding pull credentials for every registry host declared
// in YAML. Returns nil when no registry: block — caller skips Apply
// in that case.
//
// Resolution: this function takes ALREADY-RESOLVED creds (literal
// strings, not $VAR refs). The cmd/cli boundary expands env-var
// references before handing the runtime down.
func BuildRegistrySecret(rt *runtime.Runtime) (*corev1.Secret, error) {
	if len(rt.Cfg.Registry) == 0 {
		return nil, nil
	}
	auths := make(map[string]dockerAuthEntry, len(rt.Cfg.Registry))
	for host, reg := range rt.Cfg.Registry {
		auths[host] = dockerAuthEntry{
			Username: reg.Username,
			Password: reg.Password,
			Auth:     base64.StdEncoding.EncodeToString([]byte(reg.Username + ":" + reg.Password)),
		}
	}
	dc := dockerConfig{Auths: auths}
	raw, err := json.Marshal(dc)
	if err != nil {
		return nil, fmt.Errorf("encode dockerconfigjson: %w", err)
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      registrySecretName,
			Namespace: namespace,
			Labels: map[string]string{
				LabelOwner: "nvoi",
			},
		},
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: raw,
		},
	}, nil
}

// ResolveRegistryCreds expands $VAR references in registry creds
// against the operator's environment. Called at the cmd/ boundary
// once, before runtime.Build, so internal packages always see literal
// strings.
//
// Lives here (workload package) because the workload-side Secret is
// the one consumer that needs the resolved values. Kept as an
// exported helper so cmd/cli can call it.
func ResolveRegistryCreds(in map[string]config.RegistryDef, getenv func(string) string) map[string]config.RegistryDef {
	out := make(map[string]config.RegistryDef, len(in))
	for host, reg := range in {
		out[host] = config.RegistryDef{
			Username: resolveVar(reg.Username, getenv),
			Password: resolveVar(reg.Password, getenv),
		}
	}
	return out
}

// resolveVar: "$FOO" → getenv("FOO"), otherwise literal.
func resolveVar(s string, getenv func(string) string) string {
	if len(s) > 1 && s[0] == '$' {
		return getenv(s[1:])
	}
	return s
}
