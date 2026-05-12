package store

import "time"

// Meta is a key/value table holding store-level metadata. The only
// well-known key today is "key_fp" — the fingerprint of the master AES
// key, written at Init and verified on every Open. Future keys (schema
// version pin, install id, …) land here without a migration.
type Meta struct {
	K string `gorm:"primaryKey;size:64"`
	V string `gorm:"not null"`
}

// Project is one nvoi deployment target. ConfigYAML is the
// *config.Config marshalled (validation runs at marshal/unmarshal time
// in projects.go). The SSH key path lives inside the config
// (config.Config.SSHKey) — single source of truth, no duplication.
// Tilde expansion and key-file reading happen at the cmd/cli boundary
// during PrepareRuntime, same as YAML mode today.
type Project struct {
	ID          string    `gorm:"primaryKey;size:64"`
	Name        string    `gorm:"uniqueIndex;not null;size:128"`
	ConfigYAML  string    `gorm:"type:text;not null"`
	CreatedAt   time.Time `gorm:"not null"`
	UpdatedAt   time.Time `gorm:"not null"`

	Secrets  []Secret  `gorm:"foreignKey:ProjectID;constraint:OnDelete:CASCADE"`
	Runs     []Run     `gorm:"foreignKey:ProjectID;constraint:OnDelete:CASCADE"`
	Sessions []Session `gorm:"foreignKey:ProjectID;constraint:OnDelete:CASCADE"`
}

// Secret is one encrypted credential bound to a Project. Nonce +
// Ciphertext are AES-256-GCM output (see crypto.go); Name is e.g.
// "HCLOUD_TOKEN", "CLOUDFLARE_API_TOKEN", or any user-declared name.
//
// (ProjectID, Name) is the natural key — composite primary key via
// gorm tags so we get the upsert-by-name semantics for free.
type Secret struct {
	ProjectID  string    `gorm:"primaryKey;size:64"`
	Name       string    `gorm:"primaryKey;size:128"`
	Nonce      []byte    `gorm:"not null"`
	Ciphertext []byte    `gorm:"not null"`
	CreatedAt  time.Time `gorm:"not null"`
	UpdatedAt  time.Time `gorm:"not null"`
}

// Run is one invocation of plan/deploy/destroy. JSONLPath points at
// the on-disk full event stream (~/.nvoi/runs/<id>.jsonl or operator-
// configured location); the store holds only the index + summary.
type Run struct {
	ID         string     `gorm:"primaryKey;size:64"`
	ProjectID  string     `gorm:"not null;index:idx_runs_project_started,priority:1;size:64"`
	Verb       string     `gorm:"not null;size:32"`
	Status     string     `gorm:"not null;size:32"`
	JSONLPath  string     `gorm:"not null"`
	EventCount int        `gorm:"not null;default:0"`
	LastStep   string     `gorm:"size:128"`
	ExitError  string     `gorm:"type:text"`
	StartedAt  time.Time  `gorm:"not null;index:idx_runs_project_started,priority:2,sort:desc"`
	EndedAt    *time.Time
}

// Session is one agent chat session bound to a Project + provider.
// Messages dangle off it via foreign key.
type Session struct {
	ID        string    `gorm:"primaryKey;size:64"`
	ProjectID string    `gorm:"not null;index:idx_sessions_project_last,priority:1;size:64"`
	Provider  string    `gorm:"not null;size:32"`
	Model     string    `gorm:"size:128"`
	Title     string    `gorm:"size:256"`
	CreatedAt time.Time `gorm:"not null"`
	LastAt    time.Time `gorm:"not null;index:idx_sessions_project_last,priority:2,sort:desc"`

	Messages []Message `gorm:"foreignKey:SessionID;constraint:OnDelete:CASCADE"`
}

// Message is one event emitted by an agent provider's tool-use loop.
// Mirrors agent.Message but persisted: Seq is the monotonic per-
// session position (assigned atomically in sessions.go AppendMessage),
// MetadataJSON is the gob of provider-specific extras.
type Message struct {
	ID           int64     `gorm:"primaryKey;autoIncrement"`
	SessionID    string    `gorm:"not null;uniqueIndex:idx_messages_session_seq,priority:1;size:64"`
	Seq          int64     `gorm:"not null;uniqueIndex:idx_messages_session_seq,priority:2"`
	Kind         string    `gorm:"not null;size:32"`
	Content      string    `gorm:"type:text;not null"`
	MetadataJSON string    `gorm:"type:text"`
	CreatedAt    time.Time `gorm:"not null"`
}

// allModels is the canonical list passed to gorm.AutoMigrate at
// Init/Open. Adding a new model = adding it here. Order matters only
// in that Postgres creates the parent table before the child for FK
// resolution; gorm handles dependency ordering, but listing parents
// first matches the schema's mental model.
func allModels() []any {
	return []any{
		&Meta{},
		&Project{},
		&Secret{},
		&Run{},
		&Session{},
		&Message{},
	}
}
