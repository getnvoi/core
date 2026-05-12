// Package store is the optional persistence layer for nvoi, activated
// by --database <path>. Owns the schema (via gorm AutoMigrate), the
// cell-level encryption of secret values, and CRUD over projects,
// secrets, runs, and agent sessions.
//
// pkg/store is PURE: it never reads env, never touches the keychain.
// The master key comes in via a KeyRing (pkg/store/keyring, phase 2)
// resolved at the cmd/cli boundary. Crypto primitives are stdlib
// (crypto/aes + crypto/cipher + crypto/rand).
//
// Database engine: SQLite via gorm + github.com/glebarez/sqlite
// (pure-Go, no CGO; wraps modernc.org/sqlite). --database is a
// filesystem path, conventionally "$HOME/.nvoi/database.sqlite".
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// metaKeyFingerprint is the well-known Meta row holding the master-key
// fingerprint. Written at Init, verified on Open. Exported (capitalized)
// only by accident? — no: stays unexported so callers can't accidentally
// rewrite it.
const metaKeyFingerprint = "key_fp"

// ErrWrongKey is returned by Open when the master key handed in does
// not match the fingerprint recorded at Init. Surface this to the
// operator with the DSN locator (but never the key or the fingerprint).
var ErrWrongKey = errors.New("store: wrong master key for this database (fingerprint mismatch)")

// ErrAlreadyInitialized is returned by Init when the target database
// already contains a key fingerprint — operator should use Open.
var ErrAlreadyInitialized = errors.New("store: database already initialized; use Open instead of Init")

// ErrNotInitialized is returned by Open when the target database has
// no key fingerprint yet — operator should run `nvoi db init` first.
var ErrNotInitialized = errors.New("store: database not initialized; run `nvoi db init` first")

// Store is the handle every consumer holds. Wraps the gorm session +
// the master key. Goroutine-safe — gorm's *DB is, and the key is read-
// only after construction.
type Store struct {
	db  *gorm.DB
	key []byte // 32 bytes — validated at construction
}

// Init creates a fresh store at path: opens the SQLite file, runs
// AutoMigrate for every model, writes the key fingerprint to meta.
// Errors if the database is already initialized (meta row present).
func Init(ctx context.Context, path string, key []byte) (*Store, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("store: key length %d, want %d", len(key), KeySize)
	}
	db, err := openGorm(path)
	if err != nil {
		return nil, err
	}
	if err := db.WithContext(ctx).AutoMigrate(allModels()...); err != nil {
		return nil, fmt.Errorf("store: AutoMigrate: %w", err)
	}
	var existing Meta
	res := db.WithContext(ctx).Where("k = ?", metaKeyFingerprint).Take(&existing)
	if res.Error == nil {
		return nil, ErrAlreadyInitialized
	}
	if !errors.Is(res.Error, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("store: probe meta: %w", res.Error)
	}
	fp := keyFingerprint(key)
	if err := db.WithContext(ctx).Create(&Meta{K: metaKeyFingerprint, V: fp}).Error; err != nil {
		return nil, fmt.Errorf("store: write key fingerprint: %w", err)
	}
	return &Store{db: db, key: key}, nil
}

// Open opens an existing initialized store at path and verifies the
// master key matches the recorded fingerprint. Runs AutoMigrate
// idempotently — safe across upgrades that add new tables/columns.
func Open(ctx context.Context, path string, key []byte) (*Store, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("store: key length %d, want %d", len(key), KeySize)
	}
	db, err := openGorm(path)
	if err != nil {
		return nil, err
	}
	if err := db.WithContext(ctx).AutoMigrate(allModels()...); err != nil {
		return nil, fmt.Errorf("store: AutoMigrate: %w", err)
	}
	var fpRow Meta
	res := db.WithContext(ctx).Where("k = ?", metaKeyFingerprint).Take(&fpRow)
	if errors.Is(res.Error, gorm.ErrRecordNotFound) {
		return nil, ErrNotInitialized
	}
	if res.Error != nil {
		return nil, fmt.Errorf("store: read key fingerprint: %w", res.Error)
	}
	if fpRow.V != keyFingerprint(key) {
		return nil, ErrWrongKey
	}
	return &Store{db: db, key: key}, nil
}

// Close releases the underlying sql.DB connection pool. After Close,
// every method on the Store returns errors.
func (s *Store) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return fmt.Errorf("store: handle: %w", err)
	}
	return sqlDB.Close()
}

// DB exposes the underlying *gorm.DB for tests + advanced callers.
// Internal callers prefer the dedicated CRUD methods on Store; this
// escape hatch is to support inline gorm queries in tests without
// growing the API surface for every shape we want to assert.
func (s *Store) DB() *gorm.DB { return s.db }

// Rekey re-encrypts every secret cell under newKey and updates the
// fingerprint. Runs in a single transaction; aborts (rolling back) on
// any decrypt or write failure. On success, the Store's master key is
// swapped — subsequent operations use newKey.
func (s *Store) Rekey(ctx context.Context, newKey []byte) error {
	if len(newKey) != KeySize {
		return fmt.Errorf("store: new key length %d, want %d", len(newKey), KeySize)
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var secrets []Secret
		if err := tx.Find(&secrets).Error; err != nil {
			return fmt.Errorf("store: enumerate secrets: %w", err)
		}
		for i := range secrets {
			plain, err := decrypt(s.key, secrets[i].Nonce, secrets[i].Ciphertext)
			if err != nil {
				return fmt.Errorf("store: decrypt project=%s name=%s during rekey: %w",
					secrets[i].ProjectID, secrets[i].Name, err)
			}
			nonce, ct, err := encrypt(newKey, plain)
			if err != nil {
				return fmt.Errorf("store: re-encrypt project=%s name=%s during rekey: %w",
					secrets[i].ProjectID, secrets[i].Name, err)
			}
			if err := tx.Model(&Secret{}).
				Where("project_id = ? AND name = ?", secrets[i].ProjectID, secrets[i].Name).
				Updates(map[string]any{
					"nonce":      nonce,
					"ciphertext": ct,
				}).Error; err != nil {
				return fmt.Errorf("store: update secret project=%s name=%s during rekey: %w",
					secrets[i].ProjectID, secrets[i].Name, err)
			}
		}
		newFP := keyFingerprint(newKey)
		if err := tx.Model(&Meta{}).
			Where("k = ?", metaKeyFingerprint).
			Update("v", newFP).Error; err != nil {
			return fmt.Errorf("store: update fingerprint: %w", err)
		}
		return nil
	})
	if err != nil {
		return err //nolint:wrapcheck // already wrapped inside the closure
	}
	// Transaction committed — swap the in-memory key so subsequent
	// operations on this Store handle decrypt with the new key.
	s.key = newKey
	return nil
}

// openGorm opens the SQLite file with our standard config.
//
// The DSN appends `?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)`
// to enable foreign-key enforcement (off by default in SQLite!) and
// WAL journal mode (concurrent readers + better crash recovery).
//
// Logger is silenced — every emit in nvoi goes through pkg/log; gorm's
// own logger would write to stderr and pollute the canonical stream.
func openGorm(path string) (*gorm.DB, error) {
	dsn := path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger:                                   logger.Default.LogMode(logger.Silent),
		DisableForeignKeyConstraintWhenMigrating: false,
	})
	if err != nil {
		return nil, fmt.Errorf("store: gorm.Open: %w", err)
	}
	return db, nil
}

// rekeyInPlace is used by tests to forcibly swap a Store's key after
// an external Rekey (e.g. when a sibling process rotated). Internal —
// not part of the public API. Lives here next to Rekey for context.
//
//nolint:unused // reserved for follow-up tests on multi-process rekey
func (s *Store) rekeyInPlace(newKey []byte) { s.key = newKey }
