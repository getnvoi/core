package main

import (
	"strings"
	"testing"

	"github.com/getnvoi/core/internal/config"
)

func TestExecTarget_StatelessIsDeployment(t *testing.T) {
	cfg := &config.Config{Services: map[string]config.ServiceSpec{
		"web": {Image: "nvoi/web", Port: 8080},
	}}
	got, err := execTarget(cfg, "web")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "deployment/web" {
		t.Errorf("stateless: got %q want %q", got, "deployment/web")
	}
}

func TestExecTarget_StatefulIsStatefulSet(t *testing.T) {
	cfg := &config.Config{Services: map[string]config.ServiceSpec{
		"postgres": {
			Image:   "postgres:16",
			Port:    5432,
			Storage: &config.StorageSpec{Size: "10Gi", MountPath: "/var/lib/postgresql/data"},
		},
	}}
	got, err := execTarget(cfg, "postgres")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "statefulset/postgres" {
		t.Errorf("stateful: got %q want %q", got, "statefulset/postgres")
	}
}

func TestExecTarget_UnknownServiceErrors(t *testing.T) {
	cfg := &config.Config{Services: map[string]config.ServiceSpec{
		"web": {Image: "nvoi/web", Port: 8080},
	}}
	_, err := execTarget(cfg, "ghost")
	if err == nil {
		t.Fatal("expected error for unknown service")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should mention the missing service name: %v", err)
	}
}
