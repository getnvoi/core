package workload

import (
	"reflect"
	"testing"

	appsv1 "k8s.io/api/apps/v1"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/internal/kube"
	"github.com/getnvoi/core/pkg/runtime"
)

// mustBuildDeployment wraps buildDeployment for tests that don't care
// about the placement-resolution error path. The standard rt() helper
// seeds a master so defaultPlacementKeys always returns non-empty;
// any err here is a real bug.
func mustBuildDeployment(t *testing.T, rt *runtime.Runtime, name string, svc config.ServiceSpec) *appsv1.Deployment {
	t.Helper()
	d, err := buildDeployment(rt, name, svc)
	if err != nil {
		t.Fatalf("buildDeployment %s: %v", name, err)
	}
	return d
}

func rt(reg map[string]config.RegistryDef, deployHash string) *runtime.Runtime {
	return &runtime.Runtime{
		Cfg: &config.Config{
			App: "hello",
			Env: "dev",
			Servers: map[string]config.ServerSpec{
				"master": {Role: "master"},
			},
			Registry: reg,
		},
		RegistryCreds: reg,
		DeployHash:    deployHash,
	}
}

// rtServers builds a runtime with the given server set — used by tests
// that need to exercise default placement against richer cluster
// shapes (HA, mixed master+worker).
func rtServers(servers map[string]config.ServerSpec) *runtime.Runtime {
	return &runtime.Runtime{
		Cfg:        &config.Config{App: "hello", Env: "dev", Servers: servers},
		DeployHash: "h",
	}
}

func ptrInt(i int) *int { return &i }

func TestBuildDeployment_Defaults(t *testing.T) {
	d := mustBuildDeployment(t,rt(nil, "20260430-120000"), "api", config.ServiceSpec{
		Image: "nginx:alpine",
		Port:  80,
	})

	if d.Name != "api" || d.Namespace != "default" {
		t.Errorf("name/namespace: got %s/%s", d.Name, d.Namespace)
	}
	if got := *d.Spec.Replicas; got != 1 {
		t.Errorf("default replicas: got %d want 1", got)
	}
	if d.Labels[kube.LabelOwner] != kube.OwnerServices || d.Labels[labelService] != "api" {
		t.Errorf("labels: %v", d.Labels)
	}
	if d.Labels[labelDeployHash] != "20260430-120000" {
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
	d := mustBuildDeployment(t,rt(reg, "20260430-120000"), "api", config.ServiceSpec{
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
	d := mustBuildDeployment(t,rt(reg, "h"), "api", config.ServiceSpec{Image: "ghcr.io/x/y", Port: 80})

	pull := d.Spec.Template.Spec.ImagePullSecrets
	if len(pull) != 1 || pull[0].Name != "registry-auth" {
		t.Errorf("imagePullSecrets: %v want [{registry-auth}]", pull)
	}
}

func TestBuildDeployment_NoImagePullSecretsWhenNoRegistry(t *testing.T) {
	d := mustBuildDeployment(t,rt(nil, "h"), "api", config.ServiceSpec{Image: "nginx", Port: 80})
	if len(d.Spec.Template.Spec.ImagePullSecrets) != 0 {
		t.Errorf("no registry block → no imagePullSecrets, got %v", d.Spec.Template.Spec.ImagePullSecrets)
	}
}

func TestBuildDeployment_ExplicitReplicas(t *testing.T) {
	d := mustBuildDeployment(t,rt(nil, "h"), "api", config.ServiceSpec{
		Image:    "nginx",
		Port:     80,
		Replicas: ptrInt(3),
	})
	if got := *d.Spec.Replicas; got != 3 {
		t.Errorf("replicas: got %d want 3", got)
	}
}

func TestBuildDeployment_SecretEnvInjected(t *testing.T) {
	d := mustBuildDeployment(t,rt(nil, "h"), "web", config.ServiceSpec{
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
	if ref.Name != appSecretName || ref.Key != "DATABASE_URL" {
		t.Errorf("secretKeyRef: %+v", ref)
	}
}

// In a single-master cluster (the rt() helper's shape), default
// placement resolves to that single master. NodeSelector matches the
// `nvoi-role=master` label stamped at deploy time.
func TestBuildDeployment_DefaultPlacementSingleMaster(t *testing.T) {
	d := mustBuildDeployment(t,rt(nil, "h"), "web", config.ServiceSpec{Image: "nginx", Port: 80})
	if got := d.Spec.Template.Spec.NodeSelector[LabelNvoiRole]; got != "master" {
		t.Errorf("default placement: got %v want %s=master", d.Spec.Template.Spec.NodeSelector, LabelNvoiRole)
	}
}

// In an HA cluster with workers, default placement lands on workers
// (control plane stays on masters). Multi-replica path → nodeAffinity
// + topologySpread, no nodeSelector.
func TestBuildDeployment_DefaultPlacementPrefersWorkers(t *testing.T) {
	r := rtServers(map[string]config.ServerSpec{
		"master-1": {Role: "master"},
		"master-2": {Role: "master"},
		"worker-1": {Role: "worker"},
		"worker-2": {Role: "worker"},
	})
	d := mustBuildDeployment(t,r, "web", config.ServiceSpec{Image: "nginx", Port: 80})
	if d.Spec.Template.Spec.NodeSelector != nil {
		t.Errorf("multi-server default must not use nodeSelector; got %v", d.Spec.Template.Spec.NodeSelector)
	}
	aff := d.Spec.Template.Spec.Affinity
	if aff == nil || aff.NodeAffinity == nil {
		t.Fatal("multi-server default must set nodeAffinity")
	}
	expr := aff.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions[0]
	if expr.Key != LabelNvoiRole {
		t.Errorf("affinity key: got %s want %s", expr.Key, LabelNvoiRole)
	}
	// Must list the worker keys, NOT masters.
	for _, v := range expr.Values {
		if v == "master-1" || v == "master-2" {
			t.Errorf("default placement leaked masters into affinity values: %v", expr.Values)
		}
	}
	if len(expr.Values) != 2 || expr.Values[0] != "worker-1" || expr.Values[1] != "worker-2" {
		t.Errorf("expected worker keys in affinity, got %v", expr.Values)
	}
}

// HA cluster with NO workers (e.g. compute-only HA test): default
// falls back to masters. Multi-master → affinity over master keys.
func TestBuildDeployment_DefaultPlacementHAMastersOnlyFallback(t *testing.T) {
	r := rtServers(map[string]config.ServerSpec{
		"master-1": {Role: "master"},
		"master-2": {Role: "master"},
		"master-3": {Role: "master"},
	})
	d := mustBuildDeployment(t,r, "web", config.ServiceSpec{Image: "nginx", Port: 80})
	aff := d.Spec.Template.Spec.Affinity
	if aff == nil {
		t.Fatal("ha-no-workers default must set nodeAffinity")
	}
	got := aff.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions[0].Values
	want := []string{"master-1", "master-2", "master-3"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ha-fallback placement: got %v want %v", got, want)
	}
}

func TestBuildDeployment_ServersPin(t *testing.T) {
	d := mustBuildDeployment(t,rt(nil, "h"), "web", config.ServiceSpec{
		Image:   "nginx",
		Port:    80,
		Servers: []string{"worker-1"},
	})
	if got := d.Spec.Template.Spec.NodeSelector[LabelNvoiRole]; got != "worker-1" {
		t.Errorf("placement: got %v want %s=worker-1", d.Spec.Template.Spec.NodeSelector, LabelNvoiRole)
	}
}
