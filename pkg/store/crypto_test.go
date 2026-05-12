package store

import (
	"bytes"
	"crypto/rand"
	"strings"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	plain := []byte("HCLOUD_TOKEN=hetzner-token-value-redacted")
	nonce, ct, err := encrypt(testKey, plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if len(nonce) != nonceSize {
		t.Fatalf("nonce length: got %d, want %d", len(nonce), nonceSize)
	}
	if len(ct) == 0 || bytes.Equal(ct, plain) {
		t.Fatal("ciphertext empty or identical to plaintext")
	}
	got, err := decrypt(testKey, nonce, ct)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("decrypted mismatch: got %q want %q", got, plain)
	}
}

func TestEncryptNoncesUnique(t *testing.T) {
	// 100 encryptions of the same plaintext under the same key MUST
	// produce 100 distinct nonces (probability of collision in 12
	// random bytes is negligible).
	plain := []byte("same plaintext")
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		nonce, _, err := encrypt(testKey, plain)
		if err != nil {
			t.Fatalf("encrypt iter %d: %v", i, err)
		}
		if seen[string(nonce)] {
			t.Fatalf("nonce reuse at iter %d", i)
		}
		seen[string(nonce)] = true
	}
}

func TestDecryptWrongKeyFails(t *testing.T) {
	plain := []byte("a value the wrong key should not see")
	nonce, ct, err := encrypt(testKey, plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	other := freshTestKey(t)
	_, err = decrypt(other, nonce, ct)
	if err == nil {
		t.Fatal("decrypt with wrong key: want error, got nil")
	}
	// Error must NOT echo any key material.
	if strings.Contains(err.Error(), string(other)) || strings.Contains(err.Error(), string(testKey)) {
		t.Fatalf("error message leaks key material: %q", err.Error())
	}
}

func TestDecryptTamperedCiphertextFails(t *testing.T) {
	plain := []byte("authentic data")
	nonce, ct, err := encrypt(testKey, plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	// Flip one bit in the middle of the ciphertext.
	ct[len(ct)/2] ^= 0x01
	_, err = decrypt(testKey, nonce, ct)
	if err == nil {
		t.Fatal("decrypt of tampered ciphertext: want error, got nil")
	}
}

func TestKeyFingerprintStable(t *testing.T) {
	a := keyFingerprint(testKey)
	b := keyFingerprint(testKey)
	if a != b {
		t.Fatalf("fingerprint not stable: %s vs %s", a, b)
	}
	if len(a) != 32 {
		t.Fatalf("fingerprint length: got %d, want 32 (hex of 16 bytes)", len(a))
	}
}

func TestKeyFingerprintDifferent(t *testing.T) {
	other := freshTestKey(t)
	if keyFingerprint(testKey) == keyFingerprint(other) {
		t.Fatal("different keys produced same fingerprint")
	}
}

func TestEncryptKeyLengthChecked(t *testing.T) {
	shortKey := make([]byte, KeySize-1)
	if _, err := rand.Read(shortKey); err != nil {
		t.Fatalf("rand: %v", err)
	}
	if _, _, err := encrypt(shortKey, []byte("x")); err == nil {
		t.Fatal("encrypt with short key: want error, got nil")
	}
}
