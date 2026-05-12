package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"

	"github.com/getnvoi/core/pkg/config"
)

// CreateProject validates the config, marshals it to YAML, inserts the
// row. Returns the persisted Project (with auto-generated UUID + server
// timestamps) on success.
func (s *Store) CreateProject(ctx context.Context, name string, cfg *config.Config) (*Project, error) {
	if name == "" {
		return nil, errors.New("store: project name required")
	}
	if cfg == nil {
		return nil, errors.New("store: project config required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("store: validate config: %w", err)
	}
	yml, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("store: marshal config: %w", err)
	}
	now := time.Now().UTC()
	row := &Project{
		ID:         uuid.NewString(),
		Name:       name,
		ConfigYAML: string(yml),
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := s.db.WithContext(ctx).Create(row).Error; err != nil {
		return nil, fmt.Errorf("store: create project %q: %w", name, err)
	}
	return row, nil
}

// GetProject looks up by UUID or name. Returns gorm.ErrRecordNotFound
// (wrapped) when missing — callers errors.Is to detect.
func (s *Store) GetProject(ctx context.Context, idOrName string) (*Project, error) {
	if idOrName == "" {
		return nil, errors.New("store: project id or name required")
	}
	var row Project
	res := s.db.WithContext(ctx).Where("id = ? OR name = ?", idOrName, idOrName).Take(&row)
	if errors.Is(res.Error, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("store: project %q: %w", idOrName, gorm.ErrRecordNotFound)
	}
	if res.Error != nil {
		return nil, fmt.Errorf("store: get project %q: %w", idOrName, res.Error)
	}
	return &row, nil
}

// ListProjects returns all projects ordered by name. No paging — this
// is a single-operator workload; the row count fits in memory.
func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	var rows []Project
	if err := s.db.WithContext(ctx).Order("name ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("store: list projects: %w", err)
	}
	return rows, nil
}

// UpdateProjectConfig replaces the config of an existing project.
// Validates before writing.
func (s *Store) UpdateProjectConfig(ctx context.Context, idOrName string, cfg *config.Config) error {
	if cfg == nil {
		return errors.New("store: config required")
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("store: validate config: %w", err)
	}
	yml, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("store: marshal config: %w", err)
	}
	res := s.db.WithContext(ctx).Model(&Project{}).
		Where("id = ? OR name = ?", idOrName, idOrName).
		Updates(map[string]any{
			"config_yaml": string(yml),
			"updated_at":  time.Now().UTC(),
		})
	if res.Error != nil {
		return fmt.Errorf("store: update project %q: %w", idOrName, res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("store: project %q: %w", idOrName, gorm.ErrRecordNotFound)
	}
	return nil
}

// DeleteProject removes a project and cascades (FK ON DELETE CASCADE).
func (s *Store) DeleteProject(ctx context.Context, idOrName string) error {
	res := s.db.WithContext(ctx).Where("id = ? OR name = ?", idOrName, idOrName).Delete(&Project{})
	if res.Error != nil {
		return fmt.Errorf("store: delete project %q: %w", idOrName, res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("store: project %q: %w", idOrName, gorm.ErrRecordNotFound)
	}
	return nil
}

// ParsedConfig materializes the stored YAML back to *config.Config via
// pkg/config.ParseYAML (which Unmarshal + Validate in one shot — same
// semantics as loading nvoi.yaml from disk).
func (p *Project) ParsedConfig() (*config.Config, error) {
	return config.ParseYAML([]byte(p.ConfigYAML))
}
