// Package env is the env-var KeyRing backend. The master key is read
// from a process environment variable (default NVOI_MASTER_KEY,
// override via `--keyring env:VARNAME`). Read-only — the operator
// installs the key by exporting the env var, not by calling Set.
//
// Encodings tried in order: standard base64 (padded), URL-safe base64,
// raw base64 (unpadded). First one yielding exactly 32 bytes wins. We
// accept all three because `openssl rand -base64 32` produces padded
// std encoding; other tools produce raw or URL-safe.
package env

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"

	"github.com/getnvoi/core/pkg/store/keyring"
)

const defaultVar = "NVOI_MASTER_KEY"

// keySize must match pkg/store.KeySize. Inlined to avoid a circular
// import (keyring → store → keyring would be the path); the constant
// is the AES-256 key size, fixed by spec.
const keySize = 32

type ring struct {
	varName string
}

// New constructs an env KeyRing from a Spec. spec.Arg is the env var
// name; empty falls back to defaultVar.
func New(spec keyring.Spec) (keyring.KeyRing, error) {
	name := spec.Arg
	if name == "" {
		name = defaultVar
	}
	return &ring{varName: name}, nil
}

func (r *ring) Locator() string { return "env:" + r.varName }

// Get reads the env var, decodes base64, returns 32 raw bytes. Returns
// (nil, nil) when the env var is unset or empty — the `auto` resolver
// uses this to fall back to env after os.
func (r *ring) Get(ctx context.Context) ([]byte, error) {
	v := os.Getenv(r.varName)
	if v == "" {
		return nil, nil
	}
	key, err := decodeBase64Any(v)
	if err != nil {
		return nil, fmt.Errorf("keyring: $%s set but invalid: %w", r.varName, err)
	}
	if len(key) != keySize {
		// Don't echo the value or its length-relative size.
		return nil, fmt.Errorf("keyring: $%s decoded to %d bytes, want %d", r.varName, len(key), keySize)
	}
	return key, nil
}

// Set is intentionally not supported. The env backend is read-only —
// operators install the key by exporting the env var before running
// nvoi (in a shell profile, systemd unit, k8s Secret, CI variable).
func (r *ring) Set(ctx context.Context, key []byte) error {
	return fmt.Errorf("keyring: env backend is read-only; export %s before running nvoi", r.varName)
}

// decodeBase64Any tries std → raw → URL-safe (both padded and raw
// variants of URL-safe). First decoder yielding exactly keySize bytes
// wins.
//
// Error priority: if at least one encoding decoded successfully but to
// the wrong length, surface a length error (more useful to the
// operator than "illegal base64 data"). Otherwise, surface the
// underlying base64 error from any attempt.
func decodeBase64Any(s string) ([]byte, error) {
	encs := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	var (
		lenErr error
		decErr error
	)
	for _, enc := range encs {
		b, err := enc.DecodeString(s)
		if err == nil {
			if len(b) == keySize {
				return b, nil
			}
			if lenErr == nil {
				lenErr = fmt.Errorf("decoded to %d bytes, want %d", len(b), keySize)
			}
			continue
		}
		if decErr == nil {
			decErr = err
		}
	}
	if lenErr != nil {
		return nil, lenErr
	}
	if decErr != nil {
		return nil, decErr
	}
	return nil, errors.New("not a valid base64 32-byte key")
}
