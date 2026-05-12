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

	Secrets []Secret `gorm:"foreignKey:ProjectID;constraint:OnDelete:CASCADE"`
	Runs    []Run    `gorm:"foreignKey:ProjectID;constraint:OnDelete:CASCADE"`
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

// allModels is the canonical list passed to gorm.AutoMigrate at
// Init/Open. Adding a new model = adding it here. Order matters only
// in that the parent table must exist before the child for FK
// resolution; gorm handles dependency ordering, but listing parents
// first matches the schema's mental model.
func allModels() []any {
	return []any{
		&Meta{},
		&Project{},
		&Secret{},
		&Run{},
	}
}
