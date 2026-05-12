package observability

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/runtime"
)

// fixtureRuntime returns a *runtime.Runtime with a minimal Cfg + an
// optional Monitor block. Used for builders that branch on
// rt.Monitor.Domain / rt.Monitor.AdminPassword.
func fixtureRuntime(mon *runtime.ResolvedMonitor) *runtime.Runtime {
	return &runtime.Runtime{
		Cfg:     &config.Config{App: "hello", Env: "dev"},
		Monitor: mon,
	}
}

func TestBuildGrafanaAdminSecret_TunnelMode(t *testing.T) {
	rt := fixtureRuntime(nil) // no monitor block — tunnel-only sentinel
	s := buildGrafanaAdminSecret(rt)
	if s.StringData["admin-password"] != grafanaAnonPassword {
		t.Errorf("tunnel-mode admin should fall back to sentinel, got %q", s.StringData["admin-password"])
	}
}

func TestBuildGrafanaAdminSecret_PublicMode(t *testing.T) {
	rt := fixtureRuntime(&runtime.ResolvedMonitor{
		Domain:        "grafana.nvoi.to",
		AdminPassword: "operator-secret",
	})
	s := buildGrafanaAdminSecret(rt)
	if s.StringData["admin-password"] != "operator-secret" {
		t.Errorf("public-mode admin should be operator-supplied, got %q", s.StringData["admin-password"])
	}
}

func TestBuildGrafanaConfigMap_AnonymousInTunnelMode(t *testing.T) {
	rt := fixtureRuntime(nil)
	cm := buildGrafanaConfigMap(rt)
	ini := cm.Data["grafana.ini"]
	if !strings.Contains(ini, "[auth.anonymous]") || !strings.Contains(ini, "enabled = true") {
		t.Errorf("tunnel mode should enable anonymous viewer:\n%s", ini)
	}
	if !strings.Contains(ini, "org_role = Viewer") {
		t.Errorf("tunnel mode anonymous should be Viewer role:\n%s", ini)
	}
}

func TestBuildGrafanaConfigMap_DisabledAnonymousInPublicMode(t *testing.T) {
	rt := fixtureRuntime(&runtime.ResolvedMonitor{Domain: "grafana.nvoi.to"})
	cm := buildGrafanaConfigMap(rt)
	ini := cm.Data["grafana.ini"]
	if !strings.Contains(ini, "[auth.anonymous]") || !strings.Contains(ini, "enabled = false") {
		t.Errorf("public mode should disable anonymous:\n%s", ini)
	}
	if !strings.Contains(ini, "root_url = https://grafana.nvoi.to/") {
		t.Errorf("public mode should set root_url to the domain:\n%s", ini)
	}
}

func TestBuildGrafanaDeployment_ContainersAndMounts(t *testing.T) {
	dep := buildGrafanaDeployment()
	if len(dep.Spec.Template.Spec.Containers) != 2 {
		t.Fatalf("want 2 containers (grafana + sidecar), got %d", len(dep.Spec.Template.Spec.Containers))
	}
	grafana := dep.Spec.Template.Spec.Containers[0]
	sidecar := dep.Spec.Template.Spec.Containers[1]
	if grafana.Name != "grafana" || sidecar.Name != "sidecar" {
		t.Errorf("container names: %q, %q", grafana.Name, sidecar.Name)
	}
	// Admin env-from-Secret discipline: env var sourced from admin
	// Secret, never inlined.
	found := false
	for _, e := range grafana.Env {
		if e.Name == "GF_SECURITY_ADMIN_PASSWORD" {
			if e.ValueFrom == nil || e.ValueFrom.SecretKeyRef == nil {
				t.Errorf("admin password should come from SecretKeyRef, got Value=%q", e.Value)
			}
			if e.ValueFrom.SecretKeyRef.Name != grafanaAdminSecretName {
				t.Errorf("admin password Secret ref: %q", e.ValueFrom.SecretKeyRef.Name)
			}
			found = true
		}
	}
	if !found {
		t.Error("missing GF_SECURITY_ADMIN_PASSWORD env var")
	}
	// Shared provisioning emptyDir between containers.
	if !hasMount(grafana.VolumeMounts, "provisioning", "/etc/grafana/provisioning") {
		t.Errorf("grafana missing provisioning mount: %v", grafana.VolumeMounts)
	}
	if !hasMount(sidecar.VolumeMounts, "provisioning", "/etc/grafana/provisioning") {
		t.Errorf("sidecar missing provisioning mount: %v", sidecar.VolumeMounts)
	}
}

func TestBuildGrafanaIngress_NilWhenNoDomain(t *testing.T) {
	rt := fixtureRuntime(nil)
	if ing := buildGrafanaIngress(rt); ing != nil {
		t.Errorf("expected nil ingress when monitor nil, got %+v", ing)
	}
	rt = fixtureRuntime(&runtime.ResolvedMonitor{}) // monitor present but no domain
	if ing := buildGrafanaIngress(rt); ing != nil {
		t.Errorf("expected nil ingress when domain empty, got %+v", ing)
	}
}

func TestBuildGrafanaIngress_WithDomain(t *testing.T) {
	rt := fixtureRuntime(&runtime.ResolvedMonitor{Domain: "grafana.nvoi.to"})
	ing := buildGrafanaIngress(rt)
	if ing == nil {
		t.Fatal("expected non-nil ingress")
	}
	if ing.Namespace != Namespace {
		t.Errorf("namespace: %q", ing.Namespace)
	}
	if len(ing.Spec.TLS) != 1 || ing.Spec.TLS[0].Hosts[0] != "grafana.nvoi.to" {
		t.Errorf("TLS host wrong: %+v", ing.Spec.TLS)
	}
	// TLS Secret name must match what a cert-manager Certificate
	// resource would produce for this hostname.
	wantSecret := "grafana-nvoi-to-tls"
	if ing.Spec.TLS[0].SecretName != wantSecret {
		t.Errorf("TLS Secret name: got %q, want %q", ing.Spec.TLS[0].SecretName, wantSecret)
	}
	if ing.Spec.Rules[0].Host != "grafana.nvoi.to" {
		t.Errorf("Rule host: %q", ing.Spec.Rules[0].Host)
	}
	backend := ing.Spec.Rules[0].HTTP.Paths[0].Backend.Service
	if backend.Name != "grafana" || backend.Port.Number != 3000 {
		t.Errorf("ingress backend wrong: %+v", backend)
	}
}

func TestBuildStack_AllKindsPresent(t *testing.T) {
	rt := fixtureRuntime(&runtime.ResolvedMonitor{Domain: "grafana.nvoi.to", AdminPassword: "p"})
	objects, names := BuildStack(rt, fixtureCreds())

	if len(objects) == 0 {
		t.Fatal("empty stack")
	}

	// Spot-check each kind is non-empty.
	if len(names.Secrets) < 2 {
		t.Errorf("expected at least 2 Secrets, got %d", len(names.Secrets))
	}
	if len(names.ConfigMaps) < 4 {
		t.Errorf("expected at least 4 ConfigMaps, got %d", len(names.ConfigMaps))
	}
	if len(names.StatefulSets) < 2 {
		t.Errorf("expected at least 2 StatefulSets, got %d", len(names.StatefulSets))
	}
	if len(names.DaemonSets) < 1 {
		t.Errorf("expected at least 1 DaemonSet, got %d", len(names.DaemonSets))
	}
	if len(names.Deployments) < 3 {
		t.Errorf("expected at least 3 Deployments, got %d", len(names.Deployments))
	}
	if len(names.Services) < 6 {
		t.Errorf("expected at least 6 Services, got %d", len(names.Services))
	}
	if len(names.Ingresses) != 1 {
		t.Errorf("expected 1 Ingress (domain set), got %d", len(names.Ingresses))
	}
}

func TestBuildStack_NoIngressInTunnelMode(t *testing.T) {
	rt := fixtureRuntime(nil)
	_, names := BuildStack(rt, fixtureCreds())
	if len(names.Ingresses) != 0 {
		t.Errorf("expected 0 Ingresses in tunnel mode, got %d", len(names.Ingresses))
	}
}

func TestBuildStack_NamespacedConsistency(t *testing.T) {
	// Every namespaced object in the stack should land in the
	// observability namespace. Cluster-scoped objects (ClusterRole,
	// ClusterRoleBinding) skip the check.
	rt := fixtureRuntime(nil)
	objects, _ := BuildStack(rt, fixtureCreds())
	for _, obj := range objects {
		acc, ok := obj.(interface{ GetNamespace() string })
		if !ok {
			continue
		}
		ns := acc.GetNamespace()
		if ns == "" {
			continue // cluster-scoped
		}
		if ns != Namespace {
			t.Errorf("%T %s in wrong namespace: %q", obj, anyName(obj), ns)
		}
	}
}

// anyName extracts an object's Name via the GetName interface
// (every typed k8s object satisfies it via ObjectMeta).
func anyName(o interface{}) string {
	if a, ok := o.(interface{ GetName() string }); ok {
		return a.GetName()
	}
	return "<unnamed>"
}

// Ensure corev1 import stays exercised when test list shrinks.
var _ corev1.Service
