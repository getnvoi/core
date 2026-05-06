package workload

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/runtime"
)

func TestBuildRegistrySecret_NoRegistry_ReturnsNil(t *testing.T) {
	rt := &runtime.Runtime{Cfg: &config.Config{}}
	got, err := buildRegistrySecret(rt)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil Secret when registry: empty, got %+v", got)
	}
}

func TestBuildRegistrySecret_PerHostAuthsEntry(t *testing.T) {
	rt := &runtime.Runtime{
		Cfg: &config.Config{Registry: map[string]config.RegistryDef{
			"ghcr.io":               {Username: "alice", Password: "ghp_token"},
			"registry.example:5000": {Username: "bob", Password: "secret"},
		}},
		RegistryCreds: map[string]config.RegistryDef{
			"ghcr.io":               {Username: "alice", Password: "ghp_token"},
			"registry.example:5000": {Username: "bob", Password: "secret"},
		},
	}

	s, err := buildRegistrySecret(rt)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if s == nil {
		t.Fatal("Secret nil; expected populated")
	}
	if s.Name != registrySecretName || s.Namespace != "default" {
		t.Errorf("name/ns: %s/%s", s.Name, s.Namespace)
	}
	if s.Type != corev1.SecretTypeDockerConfigJson {
		t.Errorf("type: got %s want %s", s.Type, corev1.SecretTypeDockerConfigJson)
	}

	raw := s.Data[corev1.DockerConfigJsonKey]
	var dc dockerConfig
	if err := json.Unmarshal(raw, &dc); err != nil {
		t.Fatalf("dockerconfigjson invalid: %v\n%s", err, raw)
	}
	if len(dc.Auths) != 2 {
		t.Fatalf("got %d auths want 2", len(dc.Auths))
	}

	// Verify "auth" field is base64(username:password) per dockerconfigjson spec.
	gh := dc.Auths["ghcr.io"]
	if gh.Username != "alice" || gh.Password != "ghp_token" {
		t.Errorf("ghcr auths: %+v", gh)
	}
	wantAuth := base64.StdEncoding.EncodeToString([]byte("alice:ghp_token"))
	if gh.Auth != wantAuth {
		t.Errorf("ghcr auth: got %q want %q", gh.Auth, wantAuth)
	}
}

func TestResolveRegistryCreds_LiteralsAndEnvRefs(t *testing.T) {
	env := map[string]string{
		"GH_USER":  "alice",
		"GH_TOKEN": "ghp_xxx",
	}
	getenv := func(k string) string { return env[k] }

	in := map[string]config.RegistryDef{
		"ghcr.io":   {Username: "$GH_USER", Password: "$GH_TOKEN"},
		"docker.io": {Username: "literal-user", Password: "literal-pass"},
	}
	out := ResolveRegistryCreds(in, getenv)

	if got := out["ghcr.io"]; got.Username != "alice" || got.Password != "ghp_xxx" {
		t.Errorf("ghcr resolved: %+v", got)
	}
	if got := out["docker.io"]; got.Username != "literal-user" || got.Password != "literal-pass" {
		t.Errorf("docker.io literal: %+v", got)
	}
}
