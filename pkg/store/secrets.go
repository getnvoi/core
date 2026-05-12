package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SetSecret upserts a secret for the project. Generates a fresh nonce
// and ciphertext under the store's master key. Idempotent — calling
// twice with the same name + different value rotates the value (new
// nonce, new ciphertext).
//
// NEVER logs the value. Errors reference only (project_id, name).
func (s *Store) SetSecret(ctx context.Context, projectID, name, value string) error {
	if projectID == "" {
		return errors.New("store: project id required")
	}
	if name == "" {
		return errors.New("store: secret name required")
	}
	nonce, ct, err := encrypt(s.key, []byte(value))
	if err != nil {
		return fmt.Errorf("store: encrypt secret %q for project %q: %w", name, projectID, err)
	}
	now := time.Now().UTC()
	row := Secret{
		ProjectID:  projectID,
		Name:       name,
		Nonce:      nonce,
		Ciphertext: ct,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	err = s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "project_id"}, {Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"nonce", "ciphertext", "updated_at",
		}),
	}).Create(&row).Error
	if err != nil {
		return fmt.Errorf("store: upsert secret %q for project %q: %w", name, projectID, err)
	}
	return nil
}

// GetSecret returns (value, true, nil) when the secret exists.
// Returns ("", false, nil) — distinct from an error — when no such
// row is in the database; lets callers distinguish "unset" from
// "store broken".
func (s *Store) GetSecret(ctx context.Context, projectID, name string) (string, bool, error) {
	if projectID == "" {
		return "", false, errors.New("store: project id required")
	}
	if name == "" {
		return "", false, errors.New("store: secret name required")
	}
	var row Secret
	res := s.db.WithContext(ctx).
		Where("project_id = ? AND name = ?", projectID, name).
		Take(&row)
	if errors.Is(res.Error, gorm.ErrRecordNotFound) {
		return "", false, nil
	}
	if res.Error != nil {
		return "", false, fmt.Errorf("store: get secret %q for project %q: %w", name, projectID, res.Error)
	}
	plain, err := decrypt(s.key, row.Nonce, row.Ciphertext)
	if err != nil {
		return "", false, fmt.Errorf("store: decrypt secret %q for project %q: %w", name, projectID, err)
	}
	return string(plain), true, nil
}

// ListSecretNames returns the secret names set for a project. Sorted
// for deterministic UI rendering and stable error messages.
func (s *Store) ListSecretNames(ctx context.Context, projectID string) ([]string, error) {
	if projectID == "" {
		return nil, errors.New("store: project id required")
	}
	var names []string
	if err := s.db.WithContext(ctx).
		Model(&Secret{}).
		Where("project_id = ?", projectID).
		Order("name ASC").
		Pluck("name", &names).Error; err != nil {
		return nil, fmt.Errorf("store: list secrets for project %q: %w", projectID, err)
	}
	return names, nil
}

// DeleteSecret removes one secret. Errors with gorm.ErrRecordNotFound
// when the row doesn't exist so callers can errors.Is-detect that case
// for idempotent flows.
func (s *Store) DeleteSecret(ctx context.Context, projectID, name string) error {
	if projectID == "" {
		return errors.New("store: project id required")
	}
	if name == "" {
		return errors.New("store: secret name required")
	}
	res := s.db.WithContext(ctx).
		Where("project_id = ? AND name = ?", projectID, name).
		Delete(&Secret{})
	if res.Error != nil {
		return fmt.Errorf("store: delete secret %q for project %q: %w", name, projectID, res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("store: secret %q for project %q: %w", name, projectID, gorm.ErrRecordNotFound)
	}
	return nil
}

// AllSecretsAsMap decrypts every secret for the project into a
// name->value map. Used by the runtime.Inputs assembler — internal/cli
// runtime build path. Errors out on first decrypt failure; partial
// maps are never returned.
func (s *Store) AllSecretsAsMap(ctx context.Context, projectID string) (map[string]string, error) {
	if projectID == "" {
		return nil, errors.New("store: project id required")
	}
	var rows []Secret
	if err := s.db.WithContext(ctx).
		Where("project_id = ?", projectID).
		Order("name ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("store: read secrets for project %q: %w", projectID, err)
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		plain, err := decrypt(s.key, r.Nonce, r.Ciphertext)
		if err != nil {
			return nil, fmt.Errorf("store: decrypt secret %q for project %q: %w", r.Name, projectID, err)
		}
		out[r.Name] = string(plain)
	}
	return out, nil
}
