package observability

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/runtime"
)

// fixtureConfig is the canonical YAML shape every dashboard test
// drives off. Two services (one stateful for storage panels), two
// domains, three masters (so HA branches activate).
func fixtureConfig() *config.Config {
	return &config.Config{
		App: "hello",
		Env: "dev",
		Servers: map[string]config.ServerSpec{
			"master-1": {Type: "cax21", Region: "nbg1", Role: "master", Primary: true},
			"master-2": {Type: "cax21", Region: "nbg1", Role: "master"},
			"master-3": {Type: "cax21", Region: "nbg1", Role: "master"},
		},
		Services: map[string]config.ServiceSpec{
			"web":      {Image: "nvoi/web", Port: 8080},
			"postgres": {Image: "postgres:16", Port: 5432, Storage: &config.StorageSpec{Size: "10Gi", MountPath: "/var/lib/postgresql/data"}},
		},
		Domains: config.Domains{
			"web": []string{"www.nvoi.to", "alt.nvoi.to"},
		},
	}
}

func TestBuildDashboards_AllRenderValidJSON(t *testing.T) {
	rt := &runtime.Runtime{Cfg: fixtureConfig()}
	cms, names, err := BuildDashboards(rt)
	if err != nil {
		t.Fatalf("BuildDashboards: %v", err)
	}
	if len(cms) != 5 {
		t.Errorf("want 5 dashboard ConfigMaps, got %d", len(cms))
	}
	if len(names) != 5 {
		t.Errorf("want 5 declared names, got %d", len(names))
	}

	for _, cm := range cms {
		if cm.Namespace != Namespace {
			t.Errorf("%s in wrong namespace: %q", cm.Name, cm.Namespace)
		}
		if cm.Labels[dashboardLabel] != dashboardLabelValue {
			t.Errorf("%s missing sidecar discovery label", cm.Name)
		}
		// Data has exactly one key — the dashboard JSON file.
		if len(cm.Data) != 1 {
			t.Errorf("%s should have exactly 1 data key, got %d", cm.Name, len(cm.Data))
		}
		for key, body := range cm.Data {
			if !strings.HasSuffix(key, ".json") {
				t.Errorf("%s data key should end in .json, got %q", cm.Name, key)
			}
			// Roundtrip JSON — guarantees the template produced valid output.
			var dashboard map[string]any
			if err := json.Unmarshal([]byte(body), &dashboard); err != nil {
				t.Errorf("%s JSON invalid: %v\n%s", cm.Name, err, body)
			}
			// Schema sanity — every dashboard must have a UID and a panels array.
			if uid, ok := dashboard["uid"].(string); !ok || uid == "" {
				t.Errorf("%s missing uid", cm.Name)
			}
			if _, ok := dashboard["panels"].([]any); !ok {
				t.Errorf("%s missing panels array", cm.Name)
			}
		}
	}
}

func TestBuildOverviewDashboard_PanelPerService(t *testing.T) {
	cfg := fixtureConfig()
	js, err := buildOverviewDashboard(cfg)
	if err != nil {
		t.Fatalf("buildOverviewDashboard: %v", err)
	}
	// Each service should produce a "ready replicas" panel.
	for _, svc := range []string{"postgres", "web"} {
		want := svc + " — ready replicas"
		if !strings.Contains(js, want) {
			t.Errorf("overview missing panel for %q\n%s", svc, js)
		}
	}
}

func TestBuildServicesDashboard_VariableOptions(t *testing.T) {
	cfg := fixtureConfig()
	js, err := buildServicesDashboard(cfg)
	if err != nil {
		t.Fatalf("buildServicesDashboard: %v", err)
	}
	// Both service names should appear as variable options.
	for _, svc := range []string{"postgres", "web"} {
		if !strings.Contains(js, `"text": "`+svc+`"`) {
			t.Errorf("services dashboard missing variable option %q\n%s", svc, js)
		}
	}
}

func TestBuildClusterDashboard_EtcdPanelOnlyInHA(t *testing.T) {
	cfg := fixtureConfig() // 3 masters → HA
	js, err := buildClusterDashboard(cfg)
	if err != nil {
		t.Fatalf("buildClusterDashboard HA: %v", err)
	}
	if !strings.Contains(js, "etcd has leader") {
		t.Error("HA cluster should include etcd panel")
	}

	// Reduce to one master — etcd panel should vanish.
	cfg.Servers = map[string]config.ServerSpec{
		"master": {Type: "cax11", Region: "nbg1", Role: "master"},
	}
	js, err = buildClusterDashboard(cfg)
	if err != nil {
		t.Fatalf("buildClusterDashboard single: %v", err)
	}
	if strings.Contains(js, "etcd has leader") {
		t.Error("single-master cluster should NOT include etcd panel")
	}
}

func TestBuildOverviewDashboard_NoServices(t *testing.T) {
	cfg := &config.Config{App: "x", Env: "y"}
	js, err := buildOverviewDashboard(cfg)
	if err != nil {
		t.Fatalf("buildOverviewDashboard empty: %v", err)
	}
	var dashboard map[string]any
	if err := json.Unmarshal([]byte(js), &dashboard); err != nil {
		t.Fatalf("invalid JSON for empty config: %v", err)
	}
}
