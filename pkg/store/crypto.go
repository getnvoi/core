package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// KeySize is the master-key length in bytes. AES-256-GCM uses 32-byte
// keys; the keyring backends (phase 2) all hand us exactly this many
// bytes, base64-decoded from their storage layer.
const KeySize = 32

// nonceSize is the AES-GCM standard nonce length. Per-row, generated
// with crypto/rand on every encrypt call.
const nonceSize = 12

// encrypt seals plaintext under key with a fresh random nonce. Returns
// (nonce, ciphertext+tag). The returned nonce MUST be persisted with
// the ciphertext — decrypt needs both.
//
// Errors only on cipher construction failure (which happens only when
// key length is wrong — we validate KeySize at Store construction so
// in practice this never fires for live callers; we still surface it
// rather than panic).
func encrypt(key, plaintext []byte) (nonce, ciphertext []byte, err error) {
	if len(key) != KeySize {
		return nil, nil, fmt.Errorf("crypto: key length %d, want %d", len(key), KeySize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, fmt.Errorf("crypto: aes.NewCipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("crypto: cipher.NewGCM: %w", err)
	}
	nonce = make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("crypto: rand.Read: %w", err)
	}
	ciphertext = gcm.Seal(nil, nonce, plaintext, nil)
	return nonce, ciphertext, nil
}

// decrypt opens ciphertext under key with the given nonce. Returns the
// plaintext or an error. Auth-failure (wrong key, corrupted blob)
// surfaces as a generic error — we never echo any part of the key or
// ciphertext in the message, just "decrypt failed".
func decrypt(key, nonce, ciphertext []byte) ([]byte, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("crypto: key length %d, want %d", len(key), KeySize)
	}
	if len(nonce) != nonceSize {
		return nil, fmt.Errorf("crypto: nonce length %d, want %d", len(nonce), nonceSize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypto: aes.NewCipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: cipher.NewGCM: %w", err)
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, errors.New("crypto: decrypt failed (wrong key or corrupted data)")
	}
	return plaintext, nil
}

// keyFingerprint returns the first 16 bytes of sha256(key), hex-encoded
// (32 chars). Stored at Init in meta.key_fp; verified on Open. A
// mismatch means "wrong master key for this database" — we surface
// that without echoing either side of the comparison.
//
// We truncate to 16 bytes because a fingerprint is for identity, not
// cryptographic strength: 128 bits of collision resistance against an
// attacker who can both create the database AND choose the key is more
// than enough.
func keyFingerprint(key []byte) string {
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:16])
}
