package workload

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/getnvoi/core/pkg/config"
)

func TestApplyNodePlacement_EmptyReturnsError(t *testing.T) {
	// Caller is supposed to resolve via defaultPlacementKeys before
	// calling. Empty servers means the cluster has no candidate
	// nodes — config.Validate enforces ≥1 master, so this is
	// effectively unreachable at runtime; the error path keeps a
	// future validator regression from silently scheduling pods on
	// arbitrary nodes.
	var pod corev1.PodSpec
	if err := applyNodePlacement(&pod, "web", nil); err == nil {
		t.Fatal("expected error on empty servers; applyNodePlacement returned nil")
	}
	if pod.NodeSelector != nil || pod.Affinity != nil {
		t.Errorf("error path must leave podSpec untouched: nodeSelector=%v affinity=%v", pod.NodeSelector, pod.Affinity)
	}
}

func TestDefaultPlacementKeys_PrefersWorkers(t *testing.T) {
	cfg := &config.Config{Servers: map[string]config.ServerSpec{
		"master-1": {Role: "master"},
		"master-2": {Role: "master"},
		"worker-1": {Role: "worker"},
		"worker-2": {Role: "worker"},
	}}
	got := defaultPlacementKeys(cfg)
	want := []string{"worker-1", "worker-2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("default-with-workers: got %v want %v", got, want)
	}
}

func TestDefaultPlacementKeys_FallsBackToMastersWhenNoWorkers(t *testing.T) {
	cfg := &config.Config{Servers: map[string]config.ServerSpec{
		"master-1": {Role: "master"},
		"master-2": {Role: "master"},
		"master-3": {Role: "master"},
	}}
	got := defaultPlacementKeys(cfg)
	want := []string{"master-1", "master-2", "master-3"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("default-without-workers: got %v want %v", got, want)
	}
}

func TestDefaultPlacementKeys_SingleMasterCommonCase(t *testing.T) {
	// Common config: one master named `master:`. Old hardcoded
	// ["master"] fallback worked here by coincidence (YAML key matched
	// role). New resolver returns the same answer for the right reason
	// — actual master keys.
	cfg := &config.Config{Servers: map[string]config.ServerSpec{
		"master": {Role: "master"},
	}}
	got := defaultPlacementKeys(cfg)
	want := []string{"master"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("single-master: got %v want %v", got, want)
	}
}

func TestApplyNodePlacement_SingleServer_NodeSelector(t *testing.T) {
	var pod corev1.PodSpec
	if err := applyNodePlacement(&pod, "web", []string{"worker-1"}); err != nil {
		t.Fatalf("applyNodePlacement: %v", err)
	}

	if got := pod.NodeSelector[LabelNvoiRole]; got != "worker-1" {
		t.Errorf("single-server placement: got %v want nvoi-role=worker-1", pod.NodeSelector)
	}
	if pod.Affinity != nil {
		t.Error("single-server case must use nodeSelector only, not Affinity")
	}
}

func TestApplyNodePlacement_MultiServer_AffinityAndSpread(t *testing.T) {
	var pod corev1.PodSpec
	if err := applyNodePlacement(&pod, "web", []string{"worker-1", "worker-2", "worker-3"}); err != nil {
		t.Fatalf("applyNodePlacement: %v", err)
	}

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
