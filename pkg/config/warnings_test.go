package config

import (
	"strings"
	"testing"
)

func TestWarnings_NoMonitor_NoWarnings(t *testing.T) {
	c := &Config{
		Providers: Providers{Infra: "hetzner"},
		Servers: map[string]ServerSpec{
			"master": {Type: "cax11", Region: "nbg1", Role: "master"},
		},
	}
	if got := c.Warnings(); len(got) != 0 {
		t.Errorf("no monitor → no warnings, got %v", got)
	}
}

func TestWarnings_Monitor_SmallMasterOnly_Warns(t *testing.T) {
	c := &Config{
		Providers: Providers{Infra: "hetzner"},
		Monitor:   &MonitorSpec{},
		Servers: map[string]ServerSpec{
			"master": {Type: "cax11", Region: "nbg1", Role: "master"},
		},
	}
	got := c.Warnings()
	if len(got) != 1 {
		t.Fatalf("want 1 warning, got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], "sizing") {
		t.Errorf("warning should mention sizing: %q", got[0])
	}
}

func TestWarnings_Monitor_WithWorker_NoWarning(t *testing.T) {
	c := &Config{
		Providers: Providers{Infra: "hetzner"},
		Monitor:   &MonitorSpec{},
		Servers: map[string]ServerSpec{
			"master": {Type: "cax11", Region: "nbg1", Role: "master"},
			"worker": {Type: "cax11", Region: "nbg1", Role: "worker"},
		},
	}
	if got := c.Warnings(); len(got) != 0 {
		t.Errorf("worker present → no sizing warning, got %v", got)
	}
}

func TestWarnings_Monitor_LargerMaster_NoWarning(t *testing.T) {
	c := &Config{
		Providers: Providers{Infra: "hetzner"},
		Monitor:   &MonitorSpec{},
		Servers: map[string]ServerSpec{
			"master": {Type: "cax21", Region: "nbg1", Role: "master"},
		},
	}
	if got := c.Warnings(); len(got) != 0 {
		t.Errorf("cax21 master → no sizing warning, got %v", got)
	}
}

func TestWarnings_Monitor_NonHetzner_NoWarning(t *testing.T) {
	// Cross-provider generalization is out of scope — only hetzner-
	// specific types trigger the warning for v1.
	c := &Config{
		Providers: Providers{Infra: "other"},
		Monitor:   &MonitorSpec{},
		Servers: map[string]ServerSpec{
			"master": {Type: "small", Region: "x", Role: "master"},
		},
	}
	if got := c.Warnings(); len(got) != 0 {
		t.Errorf("non-hetzner infra → no warning, got %v", got)
	}
}
