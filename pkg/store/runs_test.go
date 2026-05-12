package store

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"
)

func TestCreateRun(t *testing.T) {
	st := freshStore(t)
	ctx := context.Background()
	proj := mustCreateMinimalProject(t, st, "p")
	r, err := st.CreateRun(ctx, proj.ID, RunVerbPlan, "/tmp/x.jsonl")
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if r.ID == "" {
		t.Fatal("ID not assigned")
	}
	if r.Status != RunStatusRunning {
		t.Fatalf("Status: got %q want %q", r.Status, RunStatusRunning)
	}
	if r.StartedAt.IsZero() {
		t.Fatal("StartedAt zero")
	}
	if r.EndedAt != nil {
		t.Fatalf("EndedAt: want nil, got %v", r.EndedAt)
	}
}

func TestUpdateRunProgress(t *testing.T) {
	st := freshStore(t)
	ctx := context.Background()
	proj := mustCreateMinimalProject(t, st, "p")
	r, _ := st.CreateRun(ctx, proj.ID, RunVerbDeploy, "/tmp/x.jsonl")

	if err := st.UpdateRunProgress(ctx, r.ID, 42, "tf-apply"); err != nil {
		t.Fatalf("UpdateRunProgress: %v", err)
	}
	got, err := st.GetRun(ctx, r.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got.EventCount != 42 {
		t.Fatalf("EventCount: got %d want 42", got.EventCount)
	}
	if got.LastStep != "tf-apply" {
		t.Fatalf("LastStep: got %q want %q", got.LastStep, "tf-apply")
	}
}

func TestFinishRunSuccess(t *testing.T) {
	st := freshStore(t)
	ctx := context.Background()
	proj := mustCreateMinimalProject(t, st, "p")
	r, _ := st.CreateRun(ctx, proj.ID, RunVerbDeploy, "/tmp/x.jsonl")

	if err := st.FinishRun(ctx, r.ID, RunStatusSucceeded, ""); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}
	got, _ := st.GetRun(ctx, r.ID)
	if got.Status != RunStatusSucceeded {
		t.Fatalf("Status: got %q want %q", got.Status, RunStatusSucceeded)
	}
	if got.EndedAt == nil {
		t.Fatal("EndedAt: want non-nil after Finish")
	}
}

func TestFinishRunFailureCarriesError(t *testing.T) {
	st := freshStore(t)
	ctx := context.Background()
	proj := mustCreateMinimalProject(t, st, "p")
	r, _ := st.CreateRun(ctx, proj.ID, RunVerbDestroy, "/tmp/x.jsonl")

	if err := st.FinishRun(ctx, r.ID, RunStatusFailed, "tofu plan rejected"); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}
	got, _ := st.GetRun(ctx, r.ID)
	if got.ExitError != "tofu plan rejected" {
		t.Fatalf("ExitError: got %q want %q", got.ExitError, "tofu plan rejected")
	}
}

func TestFinishRunRejectsInvalidStatus(t *testing.T) {
	st := freshStore(t)
	ctx := context.Background()
	proj := mustCreateMinimalProject(t, st, "p")
	r, _ := st.CreateRun(ctx, proj.ID, RunVerbPlan, "/tmp/x.jsonl")
	err := st.FinishRun(ctx, r.ID, "weird", "")
	if err == nil {
		t.Fatal("FinishRun with invalid status: want error, got nil")
	}
}

func TestGetRunNotFound(t *testing.T) {
	st := freshStore(t)
	_, err := st.GetRun(context.Background(), "ghost")
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("want ErrRecordNotFound, got %v", err)
	}
}

func TestListRunsNewestFirst(t *testing.T) {
	st := freshStore(t)
	ctx := context.Background()
	proj := mustCreateMinimalProject(t, st, "p")
	r1, _ := st.CreateRun(ctx, proj.ID, RunVerbPlan, "/tmp/1.jsonl")
	r2, _ := st.CreateRun(ctx, proj.ID, RunVerbPlan, "/tmp/2.jsonl")
	r3, _ := st.CreateRun(ctx, proj.ID, RunVerbPlan, "/tmp/3.jsonl")

	rows, err := st.ListRuns(ctx, proj.ID, 0)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("len: got %d want 3", len(rows))
	}
	if rows[0].ID != r3.ID || rows[1].ID != r2.ID || rows[2].ID != r1.ID {
		t.Fatalf("order: got %v want [%s %s %s]", []string{rows[0].ID, rows[1].ID, rows[2].ID}, r3.ID, r2.ID, r1.ID)
	}
}

func TestListRunsLimit(t *testing.T) {
	st := freshStore(t)
	ctx := context.Background()
	proj := mustCreateMinimalProject(t, st, "p")
	for i := 0; i < 5; i++ {
		st.CreateRun(ctx, proj.ID, RunVerbPlan, "/tmp/x.jsonl")
	}
	rows, _ := st.ListRuns(ctx, proj.ID, 2)
	if len(rows) != 2 {
		t.Fatalf("limit=2: got %d want 2", len(rows))
	}
}
