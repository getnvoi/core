// Package file is the filesystem KeyRing backend. Two modes selected
// by file-name suffix:
//
//   - plain (any name not ending in .age): base64-encoded 32 bytes,
//     file must be chmod 0600. Refuses to read group-/world-readable
//     files.
//
//   - age-encrypted (suffix .age): encrypted with filippo.io/age via a
//     passphrase. Passphrase source on Get: NVOI_MASTER_PASSPHRASE env
//     if set, otherwise interactive tty prompt via golang.org/x/term.
//     On Set, the passphrase is read twice (entry + confirmation) from
//     the tty (or single-read from the env var if set, useful for CI
//     init).
//
// Atomic writes: every Set writes to <path>.tmp first, then renames.
// File mode 0600 enforced on both temp and final.
package file

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
	"golang.org/x/term"

	"github.com/getnvoi/core/pkg/store/keyring"
)

const keySize = 32

// passphraseEnv is the env var operators can set to skip the tty
// prompt — useful for CI / scripted init / containerized runs.
const passphraseEnv = "NVOI_MASTER_PASSPHRASE"

type ring struct {
	path  string
	isAge bool
}

// New constructs a file KeyRing from a Spec. spec.Arg is the file path
// (required — Resolve enforces non-empty). The .age suffix flips the
// backend into age-encrypted mode.
func New(spec keyring.Spec) (keyring.KeyRing, error) {
	if spec.Arg == "" {
		return nil, errors.New("keyring: file backend requires a path")
	}
	return &ring{
		path:  spec.Arg,
		isAge: strings.HasSuffix(spec.Arg, ".age"),
	}, nil
}

func (r *ring) Locator() string { return "file:" + r.path }

// Get reads the key from disk. Returns (nil, nil) when the file
// doesn't exist — the `auto` resolver does not include file in its
// fallback chain today, so in practice Get errors are surfaced
// directly; the (nil, nil) path remains for symmetry.
func (r *ring) Get(ctx context.Context) ([]byte, error) {
	info, err := os.Stat(r.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("keyring: file %s does not exist", r.path)
		}
		return nil, fmt.Errorf("keyring: stat %s: %w", r.path, err)
	}
	// Permission check for plain files only — age files are encrypted
	// so 0644 is not a key-leak risk (decryption still needs the
	// passphrase).
	if !r.isAge {
		if perm := info.Mode().Perm(); perm&0o077 != 0 {
			return nil, fmt.Errorf("keyring: file %s: permissions %o too permissive; chmod 0600", r.path, perm)
		}
	}
	data, err := os.ReadFile(r.path)
	if err != nil {
		return nil, fmt.Errorf("keyring: read %s: %w", r.path, err)
	}
	if r.isAge {
		return r.decryptAge(data)
	}
	return r.decodePlain(data)
}

// Set writes a fresh master key to disk. Refuses to overwrite an
// existing file — the caller (`nvoi db init`) is the only invocation
// path that expects this; `db rekey` writes to a different keyring
// (the new one).
func (r *ring) Set(ctx context.Context, key []byte) error {
	if len(key) != keySize {
		return fmt.Errorf("keyring: key length %d, want %d", len(key), keySize)
	}
	if _, err := os.Stat(r.path); err == nil {
		return fmt.Errorf("keyring: file %s already exists; refusing to overwrite (use `nvoi db rekey` to rotate)", r.path)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("keyring: stat %s: %w", r.path, err)
	}
	// Ensure parent dir exists; create with 0700.
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return fmt.Errorf("keyring: mkdir %s: %w", filepath.Dir(r.path), err)
	}
	var payload []byte
	if r.isAge {
		var err error
		payload, err = r.encryptAge(key)
		if err != nil {
			return err
		}
	} else {
		payload = []byte(base64.StdEncoding.EncodeToString(key))
	}
	return writeAtomic0600(r.path, payload)
}

// ── plain text mode ──────────────────────────────────────────────────

func (r *ring) decodePlain(data []byte) ([]byte, error) {
	s := strings.TrimSpace(string(data))
	encs := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	for _, enc := range encs {
		b, err := enc.DecodeString(s)
		if err == nil && len(b) == keySize {
			return b, nil
		}
	}
	return nil, fmt.Errorf("keyring: file %s: not a valid base64-encoded %d-byte key", r.path, keySize)
}

// ── age mode ─────────────────────────────────────────────────────────

func (r *ring) decryptAge(data []byte) ([]byte, error) {
	pass, err := readPassphrase("Enter passphrase to unlock " + r.path + ": ")
	if err != nil {
		return nil, err
	}
	id, err := age.NewScryptIdentity(pass)
	if err != nil {
		return nil, fmt.Errorf("keyring: age identity: %w", err)
	}
	rc, err := age.Decrypt(bytes.NewReader(data), id)
	if err != nil {
		return nil, fmt.Errorf("keyring: decrypt %s: %w", r.path, err)
	}
	plain, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("keyring: read decrypted %s: %w", r.path, err)
	}
	if len(plain) != keySize {
		return nil, fmt.Errorf("keyring: %s decrypted to %d bytes, want %d", r.path, len(plain), keySize)
	}
	return plain, nil
}

func (r *ring) encryptAge(key []byte) ([]byte, error) {
	pass, err := readPassphraseConfirmed("Choose a passphrase for " + r.path + ": ")
	if err != nil {
		return nil, err
	}
	rcpt, err := age.NewScryptRecipient(pass)
	if err != nil {
		return nil, fmt.Errorf("keyring: age recipient: %w", err)
	}
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, rcpt)
	if err != nil {
		return nil, fmt.Errorf("keyring: age encrypt: %w", err)
	}
	if _, err := w.Write(key); err != nil {
		return nil, fmt.Errorf("keyring: write key to age stream: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("keyring: close age stream: %w", err)
	}
	return buf.Bytes(), nil
}

// readPassphrase returns the passphrase from NVOI_MASTER_PASSPHRASE
// if set; otherwise prompts the tty. Empty env / empty input is an
// error — we don't accept empty passphrases.
func readPassphrase(prompt string) (string, error) {
	if v := os.Getenv(passphraseEnv); v != "" {
		return v, nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", fmt.Errorf("keyring: no tty and %s not set; can't read passphrase", passphraseEnv)
	}
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("keyring: read passphrase: %w", err)
	}
	if len(b) == 0 {
		return "", errors.New("keyring: empty passphrase rejected")
	}
	return string(b), nil
}

// readPassphraseConfirmed reads the passphrase twice (entry + confirm)
// to guard against typos at Set time. Skips confirmation when the env
// var path is taken — CI / non-interactive callers provide one source
// of truth.
func readPassphraseConfirmed(prompt string) (string, error) {
	if v := os.Getenv(passphraseEnv); v != "" {
		return v, nil
	}
	a, err := readPassphrase(prompt)
	if err != nil {
		return "", err
	}
	b, err := readPassphrase("Confirm passphrase: ")
	if err != nil {
		return "", err
	}
	if a != b {
		return "", errors.New("keyring: passphrases do not match")
	}
	return a, nil
}

// ── atomic write helper ──────────────────────────────────────────────

func writeAtomic0600(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("keyring: open %s: %w", tmp, err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("keyring: write %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("keyring: fsync %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("keyring: close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("keyring: rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}
