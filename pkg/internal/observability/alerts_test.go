package observability

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/runtime"
)

func TestBuildAlertRules_StructureAndOwnerLabel(t *testing.T) {
	rt := &runtime.Runtime{Cfg: fixtureConfig()}
	cm, name, err := BuildAlertRules(rt)
	if err != nil {
		t.Fatalf("BuildAlertRules: %v", err)
	}
	if name != alertRulesConfigMapName {
		t.Errorf("declared name: %q", name)
	}
	if cm.Labels[alertLabel] != alertLabelValue {
		t.Errorf("missing %s label", alertLabel)
	}
	yml := cm.Data["alerts.yaml"]

	// YAML must parse.
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(yml), &parsed); err != nil {
		t.Fatalf("alerts.yaml does not parse:\n%s\nerr: %v", yml, err)
	}
	if parsed["apiVersion"] != 1 {
		t.Errorf("apiVersion = %v, want 1", parsed["apiVersion"])
	}
}

func TestBuildAlertRules_PerServiceRules(t *testing.T) {
	rt := &runtime.Runtime{Cfg: fixtureConfig()}
	cm, _, _ := BuildAlertRules(rt)
	yml := cm.Data["alerts.yaml"]

	// Every service gets a replicas + crashloop rule.
	for _, svc := range []string{"web", "postgres"} {
		for _, suffix := range []string{"-replicas-not-ready", "-crashloop"} {
			want := "nvoi-" + svc + suffix
			if !strings.Contains(yml, want) {
				t.Errorf("missing rule %q\n%s", want, yml)
			}
		}
	}
}

func TestBuildAlertRules_StatefulPVCRule(t *testing.T) {
	rt := &runtime.Runtime{Cfg: fixtureConfig()}
	cm, _, _ := BuildAlertRules(rt)
	yml := cm.Data["alerts.yaml"]

	// postgres is stateful → PVC rule. web is stateless → no PVC rule.
	if !strings.Contains(yml, "nvoi-postgres-pvc-full") {
		t.Errorf("stateful service missing pvc-full rule:\n%s", yml)
	}
	if strings.Contains(yml, "nvoi-web-pvc-full") {
		t.Errorf("stateless service should NOT have pvc-full rule")
	}
}

func TestBuildAlertRules_PerDomainRules(t *testing.T) {
	rt := &runtime.Runtime{Cfg: fixtureConfig()}
	cm, _, _ := BuildAlertRules(rt)
	yml := cm.Data["alerts.yaml"]

	for _, host := range []string{"www.nvoi.to", "alt.nvoi.to"} {
		for _, suffix := range []string{"-5xx", "-cert-expiring"} {
			want := "nvoi-" + host + suffix
			if !strings.Contains(yml, want) {
				t.Errorf("missing rule %q\n%s", want, yml)
			}
		}
	}
}

func TestBuildAlertRules_EtcdRuleOnlyInHA(t *testing.T) {
	// HA fixture (3 masters) — etcd rule present.
	rt := &runtime.Runtime{Cfg: fixtureConfig()}
	cm, _, _ := BuildAlertRules(rt)
	if !strings.Contains(cm.Data["alerts.yaml"], "nvoi-etcd-no-leader") {
		t.Error("HA config should produce etcd rule")
	}

	// Single-master variant — etcd rule absent.
	cfg := fixtureConfig()
	cfg.Servers = map[string]config.ServerSpec{
		"master": {Type: "cax11", Region: "nbg1", Role: "master"},
	}
	rt2 := &runtime.Runtime{Cfg: cfg}
	cm2, _, _ := BuildAlertRules(rt2)
	if strings.Contains(cm2.Data["alerts.yaml"], "nvoi-etcd-no-leader") {
		t.Error("single-master config should NOT produce etcd rule")
	}
}

func TestBuildAlertRules_AlwaysIncludesNodeNotReady(t *testing.T) {
	rt := &runtime.Runtime{Cfg: fixtureConfig()}
	cm, _, _ := BuildAlertRules(rt)
	if !strings.Contains(cm.Data["alerts.yaml"], "nvoi-node-not-ready") {
		t.Error("node-not-ready rule must always be present")
	}
}

