package workload

import (
	"testing"

	"github.com/getnvoi/core/internal/config"
	"github.com/getnvoi/core/internal/runtime"
)

func rt(reg map[string]config.RegistryDef, deployHash string) *runtime.Runtime {
	return &runtime.Runtime{
		Cfg:        &config.Config{App: "hello", Env: "dev", Registry: reg},
		DeployHash: deployHash,
	}
}

func ptrInt(i int) *int { return &i }

func TestBuildDeployment_Defaults(t *testing.T) {
	d := BuildDeployment(rt(nil, "20260430-120000"), "api", config.ServiceSpec{
		Image: "nginx:alpine",
		Port:  80,
	})

	if d.Name != "api" || d.Namespace != "default" {
		t.Errorf("name/namespace: got %s/%s", d.Name, d.Namespace)
	}
	if got := *d.Spec.Replicas; got != 1 {
		t.Errorf("default replicas: got %d want 1", got)
	}
	if d.Labels[LabelOwner] != "nvoi" || d.Labels[LabelService] != "api" {
		t.Errorf("labels: %v", d.Labels)
	}
	if d.Labels[LabelDeployHash] != "20260430-120000" {
		t.Errorf("deploy-hash label: %v", d.Labels)
	}
	c := d.Spec.Template.Spec.Containers[0]
	if c.Image != "nginx:alpine" {
		t.Errorf("pre-built image must NOT have hash appended: got %q", c.Image)
	}
	if c.Ports[0].ContainerPort != 80 || c.Ports[0].Name != "http" {
		t.Errorf("ports: %+v", c.Ports)
	}
}

func TestBuildDeployment_BuildAppendsDeployHashToImage(t *testing.T) {
	reg := map[string]config.RegistryDef{"ghcr.io": {Username: "u", Password: "p"}}
	d := BuildDeployment(rt(reg, "20260430-120000"), "api", config.ServiceSpec{
		Image: "ghcr.io/myorg/api",
		Port:  80,
		Build: &config.BuildSpec{Context: ".", Dockerfile: "Dockerfile"},
	})

	got := d.Spec.Template.Spec.Containers[0].Image
	want := "ghcr.io/myorg/api:20260430-120000"
	if got != want {
		t.Errorf("built image: got %q want %q", got, want)
	}
}

func TestBuildDeployment_ImagePullSecretsWhenRegistrySet(t *testing.T) {
	reg := map[string]config.RegistryDef{"ghcr.io": {Username: "u", Password: "p"}}
	d := BuildDeployment(rt(reg, "h"), "api", config.ServiceSpec{Image: "ghcr.io/x/y", Port: 80})

	pull := d.Spec.Template.Spec.ImagePullSecrets
	if len(pull) != 1 || pull[0].Name != "registry-auth" {
		t.Errorf("imagePullSecrets: %v want [{registry-auth}]", pull)
	}
}

func TestBuildDeployment_NoImagePullSecretsWhenNoRegistry(t *testing.T) {
	d := BuildDeployment(rt(nil, "h"), "api", config.ServiceSpec{Image: "nginx", Port: 80})
	if len(d.Spec.Template.Spec.ImagePullSecrets) != 0 {
		t.Errorf("no registry block → no imagePullSecrets, got %v", d.Spec.Template.Spec.ImagePullSecrets)
	}
}

func TestBuildDeployment_ExplicitReplicas(t *testing.T) {
	d := BuildDeployment(rt(nil, "h"), "api", config.ServiceSpec{
		Image:    "nginx",
		Port:     80,
		Replicas: ptrInt(3),
	})
	if got := *d.Spec.Replicas; got != 3 {
		t.Errorf("replicas: got %d want 3", got)
	}
}

func TestBuildDeployment_SecretEnvInjected(t *testing.T) {
	d := BuildDeployment(rt(nil, "h"), "web", config.ServiceSpec{
		Image:   "nvoi/web",
		Port:    8080,
		Secrets: []string{"DATABASE_URL"},
	})
	env := d.Spec.Template.Spec.Containers[0].Env
	if len(env) != 1 {
		t.Fatalf("env count: got %d want 1", len(env))
	}
	if env[0].Name != "DATABASE_URL" {
		t.Errorf("env name: got %q want DATABASE_URL", env[0].Name)
	}
	ref := env[0].ValueFrom.SecretKeyRef
	if ref.Name != AppSecretName || ref.Key != "DATABASE_URL" {
		t.Errorf("secretKeyRef: %+v", ref)
	}
}

func TestBuildDeployment_DefaultPlacementIsMaster(t *testing.T) {
	d := BuildDeployment(rt(nil, "h"), "web", config.ServiceSpec{Image: "nginx", Port: 80})
	if got := d.Spec.Template.Spec.NodeSelector[LabelNvoiRole]; got != "master" {
		t.Errorf("default placement: got %v want %s=master", d.Spec.Template.Spec.NodeSelector, LabelNvoiRole)
	}
}

func TestBuildDeployment_ServersPin(t *testing.T) {
	d := BuildDeployment(rt(nil, "h"), "web", config.ServiceSpec{
		Image:   "nginx",
		Port:    80,
		Servers: []string{"worker-1"},
	})
	if got := d.Spec.Template.Spec.NodeSelector[LabelNvoiRole]; got != "worker-1" {
		t.Errorf("placement: got %v want %s=worker-1", d.Spec.Template.Spec.NodeSelector, LabelNvoiRole)
	}
}
