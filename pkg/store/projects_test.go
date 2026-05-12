package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"gorm.io/gorm"

	"github.com/getnvoi/core/pkg/config"
)

func TestCreateProjectRoundTrip(t *testing.T) {
	st := freshStore(t)
	ctx := context.Background()
	cfg := minimalConfig(t, "round")

	proj, err := st.CreateProject(ctx, "round", cfg)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if proj.ID == "" {
		t.Fatal("ID not assigned")
	}
	if proj.Name != "round" {
		t.Fatalf("Name: got %q want %q", proj.Name, "round")
	}

	got, err := st.GetProject(ctx, proj.ID)
	if err != nil {
		t.Fatalf("GetProject by ID: %v", err)
	}
	gotCfg, err := got.ParsedConfig()
	if err != nil {
		t.Fatalf("ParsedConfig: %v", err)
	}
	if !reflect.DeepEqual(gotCfg, cfg) {
		t.Fatalf("config mismatch:\n got %+v\nwant %+v", gotCfg, cfg)
	}
}

func TestGetProjectByName(t *testing.T) {
	st := freshStore(t)
	ctx := context.Background()
	proj := mustCreateMinimalProject(t, st, "by-name")

	got, err := st.GetProject(ctx, "by-name")
	if err != nil {
		t.Fatalf("GetProject by name: %v", err)
	}
	if got.ID != proj.ID {
		t.Fatalf("ID mismatch: got %q want %q", got.ID, proj.ID)
	}
}

func TestGetProjectNotFound(t *testing.T) {
	st := freshStore(t)
	_, err := st.GetProject(context.Background(), "ghost")
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("want ErrRecordNotFound, got %v", err)
	}
}

func TestCreateProjectDuplicateName(t *testing.T) {
	st := freshStore(t)
	ctx := context.Background()
	if _, err := st.CreateProject(ctx, "dup", minimalConfig(t, "dup")); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := st.CreateProject(ctx, "dup", minimalConfig(t, "dup"))
	if err == nil {
		t.Fatal("second Create with same name: want error, got nil")
	}
}

func TestCreateProjectValidatesConfig(t *testing.T) {
	st := freshStore(t)
	bad := &config.Config{App: "x"} // missing env, providers, ssh_key, servers
	_, err := st.CreateProject(context.Background(), "bad", bad)
	if err == nil {
		t.Fatal("CreateProject with invalid config: want error, got nil")
	}
}

func TestUpdateProjectConfig(t *testing.T) {
	st := freshStore(t)
	ctx := context.Background()
	proj := mustCreateMinimalProject(t, st, "upd")

	cfg2 := minimalConfig(t, "upd")
	cfg2.Env = "staging"
	if err := st.UpdateProjectConfig(ctx, proj.ID, cfg2); err != nil {
		t.Fatalf("UpdateProjectConfig: %v", err)
	}
	got, err := st.GetProject(ctx, proj.ID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	gotCfg, err := got.ParsedConfig()
	if err != nil {
		t.Fatalf("ParsedConfig: %v", err)
	}
	if gotCfg.Env != "staging" {
		t.Fatalf("Env: got %q want %q", gotCfg.Env, "staging")
	}
}

func TestUpdateProjectConfigValidates(t *testing.T) {
	st := freshStore(t)
	ctx := context.Background()
	proj := mustCreateMinimalProject(t, st, "valid")
	bad := *minimalConfig(t, "valid")
	bad.Env = "" // invalidates
	err := st.UpdateProjectConfig(ctx, proj.ID, &bad)
	if err == nil {
		t.Fatal("UpdateProjectConfig with invalid: want error, got nil")
	}
}

func TestListProjects(t *testing.T) {
	st := freshStore(t)
	ctx := context.Background()
	mustCreateMinimalProject(t, st, "z-last")
	mustCreateMinimalProject(t, st, "a-first")
	mustCreateMinimalProject(t, st, "m-middle")

	rows, err := st.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("len: got %d want 3", len(rows))
	}
	wantOrder := []string{"a-first", "m-middle", "z-last"}
	for i, p := range rows {
		if p.Name != wantOrder[i] {
			t.Fatalf("position %d: got %q want %q", i, p.Name, wantOrder[i])
		}
	}
}

func TestDeleteProjectCascades(t *testing.T) {
	st := freshStore(t)
	ctx := context.Background()
	proj := mustCreateMinimalProject(t, st, "del")
	if err := st.SetSecret(ctx, proj.ID, "API_KEY", "v"); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}
	if _, err := st.CreateRun(ctx, proj.ID, RunVerbPlan, "/tmp/r.jsonl"); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	if err := st.DeleteProject(ctx, proj.ID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}

	// Project gone.
	if _, err := st.GetProject(ctx, proj.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("project after delete: want ErrRecordNotFound, got %v", err)
	}
	// Cascade: secrets gone.
	names, err := st.ListSecretNames(ctx, proj.ID)
	if err != nil {
		t.Fatalf("ListSecretNames: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("cascaded secrets: got %v want []", names)
	}
	// Cascade: runs gone.
	runs, err := st.ListRuns(ctx, proj.ID, 0)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("cascaded runs: got %d want 0", len(runs))
	}
}
