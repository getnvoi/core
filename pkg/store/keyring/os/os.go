// Package os is the OS-native KeyRing backend, via
// github.com/zalando/go-keyring. Maps to:
//
//   - macOS Keychain
//   - Windows Credential Manager
//   - Linux Secret Service (libsecret / gnome-keyring / KWallet via
//     D-Bus). Returns a clear "no backend available" error if the
//     platform has no Secret Service running (e.g. headless servers
//     without D-Bus).
//
// Service = "nvoi-cli". Account = "master:" + sha256_hex_short(absDBPath)
// so multiple databases on the same machine each have their own
// keychain entry. The path is hashed so the account name doesn't leak
// filesystem layout into the keychain UI.
package os

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"

	gokeyring "github.com/zalando/go-keyring"

	"github.com/getnvoi/core/pkg/store/keyring"
)

const (
	service  = "nvoi-cli"
	keySize  = 32
	acctPref = "master:"
)

type ring struct {
	account string
	locator string
}

// New constructs an os-keychain KeyRing for the given database path.
// The account is hashed from the absolute path for stability across
// invocations and brevity in the keychain UI.
func New(spec keyring.Spec) (keyring.KeyRing, error) {
	if spec.DBPath == "" {
		return nil, errors.New("keyring: os backend requires a database path")
	}
	abs, err := filepath.Abs(spec.DBPath)
	if err != nil {
		return nil, fmt.Errorf("keyring: resolve db path: %w", err)
	}
	sum := sha256.Sum256([]byte(abs))
	acct := acctPref + hex.EncodeToString(sum[:8])
	return &ring{
		account: acct,
		locator: "os:" + service + ":" + acct,
	}, nil
}

func (r *ring) Locator() string { return r.locator }

// Get reads the master key from the OS keychain. Returns (nil, nil)
// — distinct from an error — when no entry exists. The `auto`
// resolver uses this to fall back to env.
//
// Maps go-keyring.ErrNotFound to (nil, nil). Any other backend error
// (e.g. SecretService unavailable on Linux) surfaces as an error so
// the operator gets a useful message.
func (r *ring) Get(ctx context.Context) ([]byte, error) {
	v, err := gokeyring.Get(service, r.account)
	if err != nil {
		if errors.Is(err, gokeyring.ErrNotFound) {
			return nil, nil
		}
		// Hint when libsecret/D-Bus isn't available.
		if isUnsupportedPlatformErr(err) {
			return nil, fmt.Errorf("keyring: os backend unavailable on this platform (%w); use --keyring env or --keyring file:<path>", err)
		}
		return nil, fmt.Errorf("keyring: read %s: %w", r.locator, err)
	}
	key, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		return nil, fmt.Errorf("keyring: %s: stored value is not valid base64: %w", r.locator, err)
	}
	if len(key) != keySize {
		return nil, fmt.Errorf("keyring: %s: stored value decoded to %d bytes, want %d", r.locator, len(key), keySize)
	}
	return key, nil
}

// Set installs the master key in the OS keychain. Overwrites any
// existing entry under the same (service, account) — caller is
// responsible for sequencing (db init writes once; db rekey writes
// the new key after re-encrypting all secrets in the same transaction).
func (r *ring) Set(ctx context.Context, key []byte) error {
	if len(key) != keySize {
		return fmt.Errorf("keyring: key length %d, want %d", len(key), keySize)
	}
	encoded := base64.StdEncoding.EncodeToString(key)
	if err := gokeyring.Set(service, r.account, encoded); err != nil {
		if isUnsupportedPlatformErr(err) {
			return fmt.Errorf("keyring: os backend unavailable on this platform (%w); use --keyring env or --keyring file:<path>", err)
		}
		return fmt.Errorf("keyring: write %s: %w", r.locator, err)
	}
	return nil
}

// isUnsupportedPlatformErr detects "no backend available" errors
// go-keyring surfaces. The library exposes ErrUnsupportedPlatform as
// the canonical sentinel; Linux/D-Bus failures surface as wrapped
// godbus errors which the caller's error message already covers.
func isUnsupportedPlatformErr(err error) bool {
	return errors.Is(err, gokeyring.ErrUnsupportedPlatform)
}
