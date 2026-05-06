package workload

import (
	"testing"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/internal/kube"
	"github.com/getnvoi/core/pkg/runtime"
)

func TestBuildStatefulSet_Shape(t *testing.T) {
	rt := &runtime.Runtime{
		Cfg:        &config.Config{App: "hello", Env: "dev"},
		DeployHash: "20260430-120000",
	}
	ss := buildStatefulSet(rt, "postgres", config.ServiceSpec{
		Image:   "postgres:16",
		Port:    5432,
		Storage: &config.StorageSpec{Size: "10Gi", MountPath: "/var/lib/postgresql/data"},
	})

	if ss.Name != "postgres" || ss.Namespace != "default" {
		t.Errorf("name/ns: %s/%s", ss.Name, ss.Namespace)
	}
	if ss.Spec.ServiceName != "postgres" {
		t.Errorf("serviceName: got %q want %q (must reference the headless Service of the same name)", ss.Spec.ServiceName, "postgres")
	}
	if got := *ss.Spec.Replicas; got != 1 {
		t.Errorf("default replicas: got %d want 1", got)
	}
	// Selector contains labelService only. NOT deploy-hash (orphans
	// pods on every roll). NOT owner (selector immutability + owner
	// taxonomy evolution = destroy required for every taxonomy change).
	sel := ss.Spec.Selector.MatchLabels
	if sel[labelService] != "postgres" {
		t.Errorf("selector: got %v want %s=postgres", sel, labelService)
	}
	if _, ok := sel[labelDeployHash]; ok {
		t.Error("selector must NOT contain deploy-hash")
	}
	if _, ok := sel[kube.LabelOwner]; ok {
		t.Error("selector must NOT contain nvoi/owner")
	}
}

func TestBuildStatefulSet_VolumeClaimTemplate(t *testing.T) {
	rt := &runtime.Runtime{Cfg: &config.Config{}, DeployHash: "h"}
	ss := buildStatefulSet(rt, "postgres", config.ServiceSpec{
		Image:   "postgres:16",
		Port:    5432,
		Storage: &config.StorageSpec{Size: "20Gi", MountPath: "/data"},
	})

	if len(ss.Spec.VolumeClaimTemplates) != 1 {
		t.Fatalf("PVC templates: got %d want 1", len(ss.Spec.VolumeClaimTemplates))
	}
	pvc := ss.Spec.VolumeClaimTemplates[0]
	if pvc.Name != "postgres-data" {
		t.Errorf("PVC name: got %q want %q", pvc.Name, "postgres-data")
	}
	if got := pvc.Spec.Resources.Requests.Storage().String(); got != "20Gi" {
		t.Errorf("PVC size: got %q want 20Gi", got)
	}
	if pvc.Spec.StorageClassName != nil {
		t.Errorf("storageClassName must be nil so k3s default (local-path) wins, got %v", pvc.Spec.StorageClassName)
	}
}

func TestBuildStatefulSet_VolumeMountWired(t *testing.T) {
	rt := &runtime.Runtime{Cfg: &config.Config{}, DeployHash: "h"}
	ss := buildStatefulSet(rt, "postgres", config.ServiceSpec{
		Image:   "postgres:16",
		Port:    5432,
		Storage: &config.StorageSpec{Size: "10Gi", MountPath: "/var/lib/postgresql/data"},
	})

	mounts := ss.Spec.Template.Spec.Containers[0].VolumeMounts
	if len(mounts) != 1 {
		t.Fatalf("volume mounts: got %d want 1", len(mounts))
	}
	if mounts[0].Name != "postgres-data" || mounts[0].MountPath != "/var/lib/postgresql/data" {
		t.Errorf("mount: %+v", mounts[0])
	}
}

func TestBuildStatefulSet_SecretEnvInjected(t *testing.T) {
	rt := &runtime.Runtime{Cfg: &config.Config{}, DeployHash: "h"}
	ss := buildStatefulSet(rt, "postgres", config.ServiceSpec{
		Image:   "postgres:16",
		Port:    5432,
		Storage: &config.StorageSpec{Size: "1Gi", MountPath: "/data"},
		Secrets: []string{"POSTGRES_PASSWORD", "POSTGRES_USER"},
	})
	env := ss.Spec.Template.Spec.Containers[0].Env
	if len(env) != 2 {
		t.Fatalf("env count: got %d want 2", len(env))
	}
	// Sorted: POSTGRES_PASSWORD < POSTGRES_USER
	if env[0].Name != "POSTGRES_PASSWORD" || env[1].Name != "POSTGRES_USER" {
		t.Errorf("env not sorted: %+v", env)
	}
	if env[0].ValueFrom.SecretKeyRef.Name != appSecretName {
		t.Errorf("secretKeyRef.Name: %q", env[0].ValueFrom.SecretKeyRef.Name)
	}
}

func TestBuildStatefulSet_DefaultPlacementIsMaster(t *testing.T) {
	rt := &runtime.Runtime{Cfg: &config.Config{}, DeployHash: "h"}
	ss := buildStatefulSet(rt, "postgres", config.ServiceSpec{
		Image:   "postgres:16",
		Port:    5432,
		Storage: &config.StorageSpec{Size: "1Gi", MountPath: "/data"},
		// No Servers set → defaults to ["master"]
	})
	if got := ss.Spec.Template.Spec.NodeSelector[LabelNvoiRole]; got != "master" {
		t.Errorf("default placement should pin to master, got %v", ss.Spec.Template.Spec.NodeSelector)
	}
}
