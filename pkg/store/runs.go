package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Run status values. Match the lifecycle of an nvoi verb invocation;
// these are written by the agent verb's tool executors (phase 8) and
// by future direct callers.
const (
	RunStatusRunning   = "running"
	RunStatusSucceeded = "succeeded"
	RunStatusFailed    = "failed"
	RunStatusCancelled = "cancelled"
)

// Run verbs. Match cmd/cli verb names that produce JSONL streams.
const (
	RunVerbPlan    = "plan"
	RunVerbDeploy  = "deploy"
	RunVerbDestroy = "destroy"
)

// CreateRun inserts a fresh run row in RunStatusRunning state. Returns
// the persisted Run with auto-generated ID + StartedAt. Callers pass
// the absolute path where they'll be writing the JSONL stream — the
// store holds only the index.
func (s *Store) CreateRun(ctx context.Context, projectID, verb, jsonlPath string) (*Run, error) {
	if projectID == "" {
		return nil, errors.New("store: project id required")
	}
	if verb == "" {
		return nil, errors.New("store: verb required")
	}
	if jsonlPath == "" {
		return nil, errors.New("store: jsonl path required")
	}
	row := &Run{
		ID:        uuid.NewString(),
		ProjectID: projectID,
		Verb:      verb,
		Status:    RunStatusRunning,
		JSONLPath: jsonlPath,
		StartedAt: time.Now().UTC(),
	}
	if err := s.db.WithContext(ctx).Create(row).Error; err != nil {
		return nil, fmt.Errorf("store: create run for project %q: %w", projectID, err)
	}
	return row, nil
}

// UpdateRunProgress flushes a snapshot of in-flight progress (event
// count + last step). Safe to call repeatedly; debounced by the caller
// (the JSONL sink doesn't need to flush on every event).
func (s *Store) UpdateRunProgress(ctx context.Context, runID string, eventCount int, lastStep string) error {
	if runID == "" {
		return errors.New("store: run id required")
	}
	res := s.db.WithContext(ctx).Model(&Run{}).
		Where("id = ?", runID).
		Updates(map[string]any{
			"event_count": eventCount,
			"last_step":   lastStep,
		})
	if res.Error != nil {
		return fmt.Errorf("store: update run %q progress: %w", runID, res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("store: run %q: %w", runID, gorm.ErrRecordNotFound)
	}
	return nil
}

// FinishRun writes the terminal state (status + ended_at, optional
// exit_error). status should be RunStatusSucceeded / Failed / Cancelled.
func (s *Store) FinishRun(ctx context.Context, runID, status, exitErr string) error {
	if runID == "" {
		return errors.New("store: run id required")
	}
	switch status {
	case RunStatusSucceeded, RunStatusFailed, RunStatusCancelled:
	default:
		return fmt.Errorf("store: invalid terminal status %q", status)
	}
	now := time.Now().UTC()
	res := s.db.WithContext(ctx).Model(&Run{}).
		Where("id = ?", runID).
		Updates(map[string]any{
			"status":     status,
			"exit_error": exitErr,
			"ended_at":   &now,
		})
	if res.Error != nil {
		return fmt.Errorf("store: finish run %q: %w", runID, res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("store: run %q: %w", runID, gorm.ErrRecordNotFound)
	}
	return nil
}

// GetRun returns one run by ID.
func (s *Store) GetRun(ctx context.Context, runID string) (*Run, error) {
	if runID == "" {
		return nil, errors.New("store: run id required")
	}
	var row Run
	res := s.db.WithContext(ctx).Where("id = ?", runID).Take(&row)
	if errors.Is(res.Error, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("store: run %q: %w", runID, gorm.ErrRecordNotFound)
	}
	if res.Error != nil {
		return nil, fmt.Errorf("store: get run %q: %w", runID, res.Error)
	}
	return &row, nil
}

// ListRuns returns the project's runs, newest first. limit <= 0 means
// no limit.
func (s *Store) ListRuns(ctx context.Context, projectID string, limit int) ([]Run, error) {
	if projectID == "" {
		return nil, errors.New("store: project id required")
	}
	q := s.db.WithContext(ctx).
		Where("project_id = ?", projectID).
		Order("started_at DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	var rows []Run
	if err := q.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("store: list runs for project %q: %w", projectID, err)
	}
	return rows, nil
}
