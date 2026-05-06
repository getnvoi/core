package workload

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestApplyNodePlacement_DefaultIsMaster(t *testing.T) {
	var pod corev1.PodSpec
	applyNodePlacement(&pod, "web", nil)

	if got := pod.NodeSelector[LabelNvoiRole]; got != "master" {
		t.Errorf("default placement should pin to master via %s, got %v", LabelNvoiRole, pod.NodeSelector)
	}
	if pod.Affinity != nil {
		t.Error("0-server case must use nodeSelector only, not Affinity")
	}
	if len(pod.TopologySpreadConstraints) != 0 {
		t.Error("0-server case must not set topologySpreadConstraints")
	}
}

func TestApplyNodePlacement_SingleServer_NodeSelector(t *testing.T) {
	var pod corev1.PodSpec
	applyNodePlacement(&pod, "web", []string{"worker-1"})

	if got := pod.NodeSelector[LabelNvoiRole]; got != "worker-1" {
		t.Errorf("single-server placement: got %v want nvoi-role=worker-1", pod.NodeSelector)
	}
	if pod.Affinity != nil {
		t.Error("single-server case must use nodeSelector only, not Affinity")
	}
}

func TestApplyNodePlacement_MultiServer_AffinityAndSpread(t *testing.T) {
	var pod corev1.PodSpec
	applyNodePlacement(&pod, "web", []string{"worker-1", "worker-2", "worker-3"})

	if pod.NodeSelector != nil {
		t.Error("multi-server case must NOT set nodeSelector")
	}
	if pod.Affinity == nil || pod.Affinity.NodeAffinity == nil {
		t.Fatal("nodeAffinity required for multi-server placement")
	}
	terms := pod.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	if len(terms) != 1 || len(terms[0].MatchExpressions) != 1 {
		t.Fatalf("match expressions: %+v", terms)
	}
	expr := terms[0].MatchExpressions[0]
	if expr.Key != LabelNvoiRole || expr.Operator != corev1.NodeSelectorOpIn {
		t.Errorf("expr: %+v", expr)
	}
	if len(expr.Values) != 3 {
		t.Errorf("values len: %d want 3", len(expr.Values))
	}

	if len(pod.TopologySpreadConstraints) != 1 {
		t.Fatalf("topologySpreadConstraints: got %d want 1", len(pod.TopologySpreadConstraints))
	}
	c := pod.TopologySpreadConstraints[0]
	if c.MaxSkew != 1 || c.TopologyKey != LabelNvoiRole || c.WhenUnsatisfiable != corev1.ScheduleAnyway {
		t.Errorf("topologySpread: %+v", c)
	}
	if c.LabelSelector == nil || c.LabelSelector.MatchLabels[labelAppName] != "web" {
		t.Errorf("topologySpread label selector: %+v", c.LabelSelector)
	}
}

func TestSecretEnvVars_DeterministicOrder(t *testing.T) {
	got := secretEnvVars([]string{"ZED", "ALPHA", "MIDDLE"})
	if len(got) != 3 {
		t.Fatalf("env count: %d want 3", len(got))
	}
	if got[0].Name != "ALPHA" || got[1].Name != "MIDDLE" || got[2].Name != "ZED" {
		t.Errorf("not sorted: %+v", got)
	}
	for _, e := range got {
		if e.ValueFrom == nil || e.ValueFrom.SecretKeyRef == nil {
			t.Errorf("%s missing secretKeyRef", e.Name)
			continue
		}
		ref := e.ValueFrom.SecretKeyRef
		if ref.Name != appSecretName {
			t.Errorf("%s ref.Name = %q want %q", e.Name, ref.Name, appSecretName)
		}
		if ref.Key != e.Name {
			t.Errorf("%s ref.Key = %q want %q (env name)", e.Name, ref.Key, e.Name)
		}
	}
}

func TestSecretEnvVars_EmptyReturnsNil(t *testing.T) {
	if got := secretEnvVars(nil); got != nil {
		t.Errorf("nil input should return nil, got %v", got)
	}
}
