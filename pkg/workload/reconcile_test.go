package workload_test

import (
	"context"
	"io"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/internal/kube"
	"github.com/getnvoi/core/pkg/log"
	"github.com/getnvoi/core/pkg/runtime"
	"github.com/getnvoi/core/pkg/workload"
)

func silentLog() log.Log { return log.NewWith(false, io.Discard) }

func makeRuntime(services map[string]config.ServiceSpec, registry map[string]config.RegistryDef) *runtime.Runtime {
	return &runtime.Runtime{
		Cfg: &config.Config{
			App:      "hello",
			Env:      "dev",
			Servers:  map[string]config.ServerSpec{"master": {Role: "master"}},
			Services: services,
			Registry: registry,
		},
		RegistryCreds: registry,
		DeployHash:    "20260430-120000",
	}
}

// orphanDeployment creates an existing nvoi-managed Deployment that
// would be detected as "stale" if not in cfg.Services.
func orphanDeployment(name string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			Labels:    map[string]string{kube.LabelOwner: kube.OwnerServices, "nvoi/service": name},
		},
	}
}

func TestApplyAll_CreatesDeploymentAndService(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewForTest(cs)

	rt := makeRuntime(map[string]config.ServiceSpec{
		"api": {Image: "nginx:alpine", Port: 80},
	}, nil)

	if err := workload.ApplyAll(context.Background(), rt, kc, silentLog()); err != nil {
		t.Fatalf("ApplyAll: %v", err)
	}

	if _, err := cs.AppsV1().Deployments("default").Get(context.Background(), "api", metav1.GetOptions{}); err != nil {
		t.Errorf("Deployment api should exist: %v", err)
	}
	if _, err := cs.CoreV1().Services("default").Get(context.Background(), "api", metav1.GetOptions{}); err != nil {
		t.Errorf("Service api should exist: %v", err)
	}
}

func TestApplyAll_AppliesRegistrySecretWhenRegistrySet(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewForTest(cs)

	rt := makeRuntime(map[string]config.ServiceSpec{
		"api": {Image: "ghcr.io/x/api", Port: 80},
	}, map[string]config.RegistryDef{
		"ghcr.io": {Username: "u", Password: "p"},
	})

	if err := workload.ApplyAll(context.Background(), rt, kc, silentLog()); err != nil {
		t.Fatalf("ApplyAll: %v", err)
	}

	sec, err := cs.CoreV1().Secrets("default").Get(context.Background(), "registry-auth", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("registry-auth Secret should exist: %v", err)
	}
	if sec.Type != corev1.SecretTypeDockerConfigJson {
		t.Errorf("type: got %s want %s", sec.Type, corev1.SecretTypeDockerConfigJson)
	}
}

func TestApplyAll_NoRegistry_NoSecretCreated(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewForTest(cs)

	rt := makeRuntime(map[string]config.ServiceSpec{
		"api": {Image: "nginx", Port: 80},
	}, nil)

	if err := workload.ApplyAll(context.Background(), rt, kc, silentLog()); err != nil {
		t.Fatalf("ApplyAll: %v", err)
	}

	if _, err := cs.CoreV1().Secrets("default").Get(context.Background(), "registry-auth", metav1.GetOptions{}); err == nil {
		t.Errorf("registry-auth Secret should NOT exist when no registry: declared")
	}
}

func TestApplyAll_ReconcilesRemovalOfOrphanedDeployment(t *testing.T) {
	// Pre-state: cluster has a Deployment "old-api" that nvoi
	// previously created. YAML now only declares "new-api".
	cs := fake.NewSimpleClientset(orphanDeployment("old-api"))
	kc := kube.NewForTest(cs)

	rt := makeRuntime(map[string]config.ServiceSpec{
		"new-api": {Image: "nginx:alpine", Port: 80},
	}, nil)

	if err := workload.ApplyAll(context.Background(), rt, kc, silentLog()); err != nil {
		t.Fatalf("ApplyAll: %v", err)
	}

	// new-api created
	if _, err := cs.AppsV1().Deployments("default").Get(context.Background(), "new-api", metav1.GetOptions{}); err != nil {
		t.Errorf("new-api should exist: %v", err)
	}
	// old-api deleted (orphan)
	if _, err := cs.AppsV1().Deployments("default").Get(context.Background(), "old-api", metav1.GetOptions{}); err == nil {
		t.Errorf("old-api should have been deleted (no longer in YAML)")
	}
}

func TestApplyAll_DoesNotTouchUnmanagedDeployments(t *testing.T) {
	// Cluster has an unmanaged Deployment (no nvoi/owner label).
	// Reconcile must leave it alone.
	external := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "operator-installed",
			Namespace: "default",
			Labels:    map[string]string{"app": "external"},
		},
	}
	cs := fake.NewSimpleClientset(external)
	kc := kube.NewForTest(cs)

	rt := makeRuntime(map[string]config.ServiceSpec{
		"api": {Image: "nginx:alpine", Port: 80},
	}, nil)

	if err := workload.ApplyAll(context.Background(), rt, kc, silentLog()); err != nil {
		t.Fatalf("ApplyAll: %v", err)
	}

	if _, err := cs.AppsV1().Deployments("default").Get(context.Background(), "operator-installed", metav1.GetOptions{}); err != nil {
		t.Errorf("unmanaged Deployment must NOT be deleted by reconcile: %v", err)
	}
}

// orphanStatefulSet creates an existing nvoi-managed StatefulSet that
// would be detected as "stale" if not in cfg.Services.
func orphanStatefulSet(name string) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			Labels:    map[string]string{kube.LabelOwner: kube.OwnerServices, "nvoi/service": name},
		},
	}
}

func TestApplyAll_StatefulServiceCreatesStatefulSet(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewForTest(cs)

	rt := makeRuntime(map[string]config.ServiceSpec{
		"postgres": {
			Image:   "postgres:16",
			Port:    5432,
			Storage: &config.StorageSpec{Size: "10Gi", MountPath: "/var/lib/postgresql/data"},
		},
	}, nil)

	if err := workload.ApplyAll(context.Background(), rt, kc, silentLog()); err != nil {
		t.Fatalf("ApplyAll: %v", err)
	}

	if _, err := cs.AppsV1().StatefulSets("default").Get(context.Background(), "postgres", metav1.GetOptions{}); err != nil {
		t.Errorf("StatefulSet postgres should exist: %v", err)
	}
	if _, err := cs.AppsV1().Deployments("default").Get(context.Background(), "postgres", metav1.GetOptions{}); err == nil {
		t.Error("Deployment must NOT exist for stateful service")
	}
	svc, err := cs.CoreV1().Services("default").Get(context.Background(), "postgres", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Service postgres should exist: %v", err)
	}
	if svc.Spec.ClusterIP != "None" {
		t.Errorf("stateful Service must be headless, got ClusterIP=%q", svc.Spec.ClusterIP)
	}
}

func TestApplyAll_AppliesTopLevelSecrets(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewForTest(cs)

	rt := &runtime.Runtime{
		Cfg: &config.Config{
			App:     "hello",
			Env:     "dev",
			Servers: map[string]config.ServerSpec{"master": {Role: "master"}},
			Secrets: []string{"DATABASE_URL"}, // declared in YAML so the Secret survives ReconcileRemoval
			Services: map[string]config.ServiceSpec{
				"web": {Image: "nginx", Port: 80, Secrets: []string{"DATABASE_URL"}},
			},
		},
		DeployHash:   "20260430-120000",
		SecretValues: map[string]string{"DATABASE_URL": "postgres://u:p@db/x"},
	}

	if err := workload.ApplyAll(context.Background(), rt, kc, silentLog()); err != nil {
		t.Fatalf("ApplyAll: %v", err)
	}

	sec, err := cs.CoreV1().Secrets("default").Get(context.Background(), "nvoi-secrets", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("nvoi-secrets should exist: %v", err)
	}
	if got := string(sec.Data["DATABASE_URL"]); got != "postgres://u:p@db/x" {
		t.Errorf("Secret data: got %q", got)
	}
}

func TestApplyAll_ReconcilesRemovalOfOrphanedStatefulSet(t *testing.T) {
	cs := fake.NewSimpleClientset(orphanStatefulSet("old-pg"))
	kc := kube.NewForTest(cs)

	rt := makeRuntime(map[string]config.ServiceSpec{
		"web": {Image: "nginx", Port: 80},
	}, nil)

	if err := workload.ApplyAll(context.Background(), rt, kc, silentLog()); err != nil {
		t.Fatalf("ApplyAll: %v", err)
	}
	if _, err := cs.AppsV1().StatefulSets("default").Get(context.Background(), "old-pg", metav1.GetOptions{}); err == nil {
		t.Error("orphan StatefulSet should have been deleted")
	}
}

func TestApplyAll_KindFlipFromDeploymentToStatefulSet(t *testing.T) {
	// Pre-state: cluster has a Deployment for `pg`. YAML adds storage:
	// to the same name → expected: StatefulSet created, Deployment deleted.
	cs := fake.NewSimpleClientset(orphanDeployment("pg"))
	kc := kube.NewForTest(cs)

	rt := makeRuntime(map[string]config.ServiceSpec{
		"pg": {
			Image:   "postgres:16",
			Port:    5432,
			Storage: &config.StorageSpec{Size: "1Gi", MountPath: "/data"},
		},
	}, nil)

	if err := workload.ApplyAll(context.Background(), rt, kc, silentLog()); err != nil {
		t.Fatalf("ApplyAll: %v", err)
	}
	if _, err := cs.AppsV1().StatefulSets("default").Get(context.Background(), "pg", metav1.GetOptions{}); err != nil {
		t.Errorf("StatefulSet pg should be created: %v", err)
	}
	if _, err := cs.AppsV1().Deployments("default").Get(context.Background(), "pg", metav1.GetOptions{}); err == nil {
		t.Error("orphan Deployment pg should be deleted (replaced by StatefulSet)")
	}
}

func TestApplyAll_Idempotent(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewForTest(cs)

	rt := makeRuntime(map[string]config.ServiceSpec{
		"api": {Image: "nginx:alpine", Port: 80},
	}, nil)

	for i := 0; i < 3; i++ {
		if err := workload.ApplyAll(context.Background(), rt, kc, silentLog()); err != nil {
			t.Fatalf("ApplyAll[%d]: %v", i, err)
		}
	}
	// Single Deployment, single Service — no duplicates from re-runs.
	deps, _ := cs.AppsV1().Deployments("default").List(context.Background(), metav1.ListOptions{})
	if len(deps.Items) != 1 {
		t.Errorf("expected 1 Deployment after 3 applies, got %d", len(deps.Items))
	}
}

// ── ingress-mode branching ───────────────────────────────────────────

// TestApplyAll_TraefikMode_WithDomains_EmitsIngress is the baseline:
// services with domains under Traefik mode produce a per-service
// Ingress object owned by OwnerIngress.
func TestApplyAll_TraefikMode_WithDomains_EmitsIngress(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewForTest(cs)

	rt := makeRuntime(map[string]config.ServiceSpec{
		"web": {Image: "nginx", Port: 80},
	}, nil)
	rt.Cfg.Providers.DNS = "cloudflare" // domains require providers.dns
	rt.Cfg.Domains = config.Domains{"web": {"www.nvoi.to"}}

	if err := workload.ApplyAll(context.Background(), rt, kc, silentLog()); err != nil {
		t.Fatalf("ApplyAll: %v", err)
	}
	ing, err := cs.NetworkingV1().Ingresses("default").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list ingresses: %v", err)
	}
	if len(ing.Items) != 1 || ing.Items[0].Name != "web" {
		t.Errorf("Traefik mode + domains: expected 1 ingress 'web', got %v", ing.Items)
	}
}

// TestApplyAll_TunnelMode_WithDomains_EmitsIngressWithoutTLS: tunnel
// mode emits per-service Ingress (the L7-routing refactor keeps
// Traefik in the path so cloudflared → Traefik → pods gives real
// per-request load balancing). The Ingress carries NO TLS section
// because CF terminates TLS at the edge.
func TestApplyAll_TunnelMode_WithDomains_EmitsIngressWithoutTLS(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewForTest(cs)

	rt := makeRuntime(map[string]config.ServiceSpec{
		"web": {Image: "nginx", Port: 80},
	}, nil)
	rt.Cfg.Providers.DNS = "cloudflare"
	rt.Cfg.Providers.Ingress = config.IngressCloudflare
	rt.Cfg.Domains = config.Domains{"web": {"www.nvoi.to"}}

	if err := workload.ApplyAll(context.Background(), rt, kc, silentLog()); err != nil {
		t.Fatalf("ApplyAll: %v", err)
	}
	ing, err := cs.NetworkingV1().Ingresses("default").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list ingresses: %v", err)
	}
	if len(ing.Items) != 1 || ing.Items[0].Name != "web" {
		t.Errorf("tunnel mode + domains: expected 1 ingress 'web', got %v", ing.Items)
	}
	if got := ing.Items[0].Spec.TLS; len(got) != 0 {
		t.Errorf("tunnel mode: ingress must have NO TLS section (CF terminates at edge), got %+v", got)
	}
	if _, err := cs.AppsV1().Deployments("default").Get(context.Background(), "web", metav1.GetOptions{}); err != nil {
		t.Errorf("Deployment web should exist in tunnel mode: %v", err)
	}
	if _, err := cs.CoreV1().Services("default").Get(context.Background(), "web", metav1.GetOptions{}); err != nil {
		t.Errorf("Service web should exist in tunnel mode: %v", err)
	}
}

// TestApplyAll_FlipTraefikToTunnel_DropsIngressTLS: flipping a cluster
// from Traefik to tunnel mode re-applies the Ingress without the TLS
// section. The same Ingress name stays; only its spec.tls clears.
func TestApplyAll_FlipTraefikToTunnel_DropsIngressTLS(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewForTest(cs)

	rt := makeRuntime(map[string]config.ServiceSpec{
		"web": {Image: "nginx", Port: 80},
	}, nil)
	rt.Cfg.Providers.DNS = "cloudflare"
	rt.Cfg.Domains = config.Domains{"web": {"www.nvoi.to"}}

	// Traefik mode deploy — Ingress has TLS section.
	if err := workload.ApplyAll(context.Background(), rt, kc, silentLog()); err != nil {
		t.Fatalf("ApplyAll (traefik): %v", err)
	}
	got, _ := cs.NetworkingV1().Ingresses("default").List(context.Background(), metav1.ListOptions{})
	if len(got.Items) != 1 || len(got.Items[0].Spec.TLS) == 0 {
		t.Fatalf("setup: Traefik-mode ingress must have TLS, got %+v", got.Items)
	}

	// Flip to tunnel mode → same Ingress re-applied, TLS section gone.
	rt.Cfg.Providers.Ingress = config.IngressCloudflare
	if err := workload.ApplyAll(context.Background(), rt, kc, silentLog()); err != nil {
		t.Fatalf("ApplyAll (tunnel): %v", err)
	}
	got, _ = cs.NetworkingV1().Ingresses("default").List(context.Background(), metav1.ListOptions{})
	if len(got.Items) != 1 {
		t.Fatalf("flip → tunnel: expected 1 ingress, got %d", len(got.Items))
	}
	if len(got.Items[0].Spec.TLS) != 0 {
		t.Errorf("flip Traefik → tunnel: Ingress TLS section must be cleared, got %+v", got.Items[0].Spec.TLS)
	}
}
