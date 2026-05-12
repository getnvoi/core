package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/getnvoi/core/pkg/agent"
)

// CreateSession inserts a fresh session row. Returns the persisted
// Session with auto-generated ID + timestamps.
func (s *Store) CreateSession(ctx context.Context, projectID, provider, model, title string) (*Session, error) {
	if projectID == "" {
		return nil, errors.New("store: project id required")
	}
	if provider == "" {
		return nil, errors.New("store: provider required")
	}
	now := time.Now().UTC()
	row := &Session{
		ID:        uuid.NewString(),
		ProjectID: projectID,
		Provider:  provider,
		Model:     model,
		Title:     title,
		CreatedAt: now,
		LastAt:    now,
	}
	if err := s.db.WithContext(ctx).Create(row).Error; err != nil {
		return nil, fmt.Errorf("store: create session for project %q: %w", projectID, err)
	}
	return row, nil
}

// GetSession returns one session by ID.
func (s *Store) GetSession(ctx context.Context, sessionID string) (*Session, error) {
	if sessionID == "" {
		return nil, errors.New("store: session id required")
	}
	var row Session
	res := s.db.WithContext(ctx).Where("id = ?", sessionID).Take(&row)
	if errors.Is(res.Error, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("store: session %q: %w", sessionID, gorm.ErrRecordNotFound)
	}
	if res.Error != nil {
		return nil, fmt.Errorf("store: get session %q: %w", sessionID, res.Error)
	}
	return &row, nil
}

// ListSessions returns a project's sessions, most-recently-touched
// first.
func (s *Store) ListSessions(ctx context.Context, projectID string) ([]Session, error) {
	if projectID == "" {
		return nil, errors.New("store: project id required")
	}
	var rows []Session
	if err := s.db.WithContext(ctx).
		Where("project_id = ?", projectID).
		Order("last_at DESC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("store: list sessions for project %q: %w", projectID, err)
	}
	return rows, nil
}

// TouchSession updates last_at to now. Called when a new message is
// appended (AppendMessage does this transactionally) or when a UI
// surfaces a session as active.
func (s *Store) TouchSession(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return errors.New("store: session id required")
	}
	res := s.db.WithContext(ctx).Model(&Session{}).
		Where("id = ?", sessionID).
		Update("last_at", time.Now().UTC())
	if res.Error != nil {
		return fmt.Errorf("store: touch session %q: %w", sessionID, res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("store: session %q: %w", sessionID, gorm.ErrRecordNotFound)
	}
	return nil
}

// AppendMessage atomically assigns the next per-session seq and
// inserts the message. Concurrent calls from multiple goroutines
// against the same session produce gapless ordered seqs. Also bumps
// the session's last_at.
//
// Metadata is JSON-marshalled to MetadataJSON; nil metadata stores as
// empty string (not "null") so MetadataJSON columns remain plain text.
func (s *Store) AppendMessage(ctx context.Context, sessionID string, m agent.Message) (*Message, error) {
	if sessionID == "" {
		return nil, errors.New("store: session id required")
	}
	if !validKind(m.Kind) {
		return nil, fmt.Errorf("store: invalid message kind %q", m.Kind)
	}
	var metaJSON string
	if m.Metadata != nil {
		b, err := json.Marshal(m.Metadata)
		if err != nil {
			return nil, fmt.Errorf("store: marshal message metadata: %w", err)
		}
		metaJSON = string(b)
	}
	// Existence check OUTSIDE the transaction. SQLite's
	// reader-then-writer promotion within a transaction triggers
	// SQLITE_BUSY_SNAPSHOT under concurrent writers — busy_timeout
	// doesn't recover from that. So we check separately; a TOCTOU
	// delete between the check and the insert surfaces as a FK
	// violation, which we wrap to ErrRecordNotFound below.
	if err := s.db.WithContext(ctx).
		Where("id = ?", sessionID).
		Take(&Session{}).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("store: session %q: %w", sessionID, gorm.ErrRecordNotFound)
		}
		return nil, fmt.Errorf("store: get session %q: %w", sessionID, err)
	}

	// The INSERT is its own implicit transaction in SQLite; the
	// subquery + insert is atomic. Concurrent appenders serialize on
	// the write lock via busy_timeout. The unique index on
	// (session_id, seq) is the final safety net.
	now := time.Now().UTC()
	row := &Message{
		SessionID:    sessionID,
		Kind:         string(m.Kind),
		Content:      m.Content,
		MetadataJSON: metaJSON,
		CreatedAt:    now,
	}
	if err := s.db.WithContext(ctx).Raw(
		`INSERT INTO messages
			 (session_id, seq, kind, content, metadata_json, created_at)
		 VALUES
			 (?, COALESCE((SELECT MAX(seq) FROM messages WHERE session_id = ?), 0) + 1, ?, ?, ?, ?)
		 RETURNING id, seq`,
		sessionID, sessionID, row.Kind, row.Content, row.MetadataJSON, row.CreatedAt,
	).Row().Scan(&row.ID, &row.Seq); err != nil {
		return nil, fmt.Errorf("store: insert message in session %q: %w", sessionID, err)
	}
	// Bump last_at — separate op, best-effort. A failure here doesn't
	// invalidate the message that was just persisted.
	if err := s.db.WithContext(ctx).Model(&Session{}).
		Where("id = ?", sessionID).
		Update("last_at", now).Error; err != nil {
		return row, fmt.Errorf("store: bump session %q last_at: %w", sessionID, err)
	}
	return row, nil
}

// ListMessages returns messages for a session in seq order. Decodes
// the stored MetadataJSON back into a map[string]any so the returned
// agent.Message values are immediately usable.
func (s *Store) ListMessages(ctx context.Context, sessionID string) ([]agent.Message, error) {
	if sessionID == "" {
		return nil, errors.New("store: session id required")
	}
	var rows []Message
	if err := s.db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		Order("seq ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("store: list messages for session %q: %w", sessionID, err)
	}
	out := make([]agent.Message, 0, len(rows))
	for _, r := range rows {
		m := agent.Message{
			Kind:    agent.Kind(r.Kind),
			Content: r.Content,
		}
		if r.MetadataJSON != "" {
			if err := json.Unmarshal([]byte(r.MetadataJSON), &m.Metadata); err != nil {
				return nil, fmt.Errorf("store: parse metadata for message %d: %w", r.ID, err)
			}
		}
		out = append(out, m)
	}
	return out, nil
}

// NextSeq returns what AppendMessage would assign for the next message
// in this session, without writing. Useful for callers that pre-allocate
// IDs.
func (s *Store) NextSeq(ctx context.Context, sessionID string) (int64, error) {
	if sessionID == "" {
		return 0, errors.New("store: session id required")
	}
	var maxSeq int64
	if err := s.db.WithContext(ctx).Model(&Message{}).
		Where("session_id = ?", sessionID).
		Select("COALESCE(MAX(seq), 0)").
		Scan(&maxSeq).Error; err != nil {
		return 0, fmt.Errorf("store: read max seq for session %q: %w", sessionID, err)
	}
	return maxSeq + 1, nil
}

// ── helpers ──────────────────────────────────────────────────────────

func validKind(k agent.Kind) bool {
	for _, kk := range agent.AllKinds() {
		if kk == k {
			return true
		}
	}
	return false
}
