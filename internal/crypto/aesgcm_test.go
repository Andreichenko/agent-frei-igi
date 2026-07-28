package crypto

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"testing"
)

// generateTestKey helper to create a valid 32-byte random key for testing.
func generateTestKey(t *testing.T) Key {
	t.Helper()
	var key Key
	if _, err := io.ReadFull(rand.Reader, key[:]); err != nil {
		t.Fatalf("failed to generate random key: %v", err)
	}
	return key
}

// TestEncryptDecrypt_RoundTrip verifies that encrypting and decrypting a message returns the original message.
func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	key := generateTestKey(t)
	plaintext := []byte("secret GitHub user token data 12345")

	ciphertext, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("encryption failed: %v", err)
	}

	decrypted, err := Decrypt(key, ciphertext)
	if err != nil {
		t.Fatalf("decryption failed: %v", err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Errorf("decrypted data mismatch: got %q, want %q", decrypted, plaintext)
	}
}

// TestEncryptDecrypt_EmptyPlaintext verifies that empty plaintext round-trip works correctly.
func TestEncryptDecrypt_EmptyPlaintext(t *testing.T) {
	key := generateTestKey(t)
	plaintext := []byte("")

	ciphertext, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("encryption of empty plaintext failed: %v", err)
	}

	decrypted, err := Decrypt(key, ciphertext)
	if err != nil {
		t.Fatalf("decryption of empty plaintext failed: %v", err)
	}

	if len(decrypted) != 0 {
		t.Errorf("expected empty decrypted plaintext, got %q", decrypted)
	}
}

// TestEncrypt_DifferentNonces verifies that encrypting the same plaintext twice
// results in different ciphertexts because of random nonces.
func TestEncrypt_DifferentNonces(t *testing.T) {
	key := generateTestKey(t)
	plaintext := []byte("standard-github-token-value")

	ciphertext1, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatal(err)
	}

	ciphertext2, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Equal(ciphertext1, ciphertext2) {
		t.Error("expected different ciphertexts for the same plaintext due to random nonces, but got identical outputs")
	}
}

// TestDecrypt_TamperedCiphertext verifies that changing even a single bit in the ciphertext leads to a decryption failure.
func TestDecrypt_TamperedCiphertext(t *testing.T) {
	key := generateTestKey(t)
	plaintext := []byte("important confidential payload")

	ciphertext, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatal(err)
	}

	// Tamper with the ciphertext by flipping a bit in the encrypted payload section
	ciphertext[len(ciphertext)-1] ^= 0x01

	_, err = Decrypt(key, ciphertext)
	if !errors.Is(err, ErrDecryptionFailed) {
		t.Errorf("expected ErrDecryptionFailed on tampered ciphertext, got: %v", err)
	}

	// Verify that error string doesn't leak any plaintext values
	if err != nil && bytes.Contains([]byte(err.Error()), plaintext) {
		t.Error("security violation: decryption failure error message leaked plaintext information")
	}
}

// TestDecrypt_TruncatedBlob verifies that decryption fails early if the blob is shorter than minimal size (nonce + tag).
func TestDecrypt_TruncatedBlob(t *testing.T) {
	key := generateTestKey(t)

	// Minimal size should be 12 (nonce) + 16 (tag) = 28 bytes.
	// Test with a 15-byte short payload.
	shortBlob := make([]byte, 15)
	if _, err := io.ReadFull(rand.Reader, shortBlob); err != nil {
		t.Fatal(err)
	}

	_, err := Decrypt(key, shortBlob)
	if !errors.Is(err, ErrCiphertextTooShort) {
		t.Errorf("expected ErrCiphertextTooShort on truncated blob, got: %v", err)
	}
}

// TestParseKey_Validation verifies all validation states for base64 key parsing.
func TestParseKey_Validation(t *testing.T) {
	// 1. Success case: Valid base64 encoding of exactly 32 raw bytes
	rawKey := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, rawKey); err != nil {
		t.Fatal(err)
	}
	validB64 := base64.StdEncoding.EncodeToString(rawKey)

	key, err := ParseKey(validB64)
	if err != nil {
		t.Fatalf("ParseKey failed for valid input: %v", err)
	}
	if !bytes.Equal(rawKey, key[:]) {
		t.Error("parsed key bytes mismatch")
	}

	// 2. Error case: Empty string input
	_, err = ParseKey("")
	if !errors.Is(err, ErrEmptyKey) {
		t.Errorf("expected ErrEmptyKey, got %v", err)
	}

	// 3. Error case: Invalid base64 encoding
	_, err = ParseKey("this-is-not-valid-base-64-string!!!")
	if !errors.Is(err, ErrInvalidBase64) {
		t.Errorf("expected ErrInvalidBase64, got %v", err)
	}

	// 4. Error case: Wrong length (decoded to 16 bytes instead of 32)
	shortRawKey := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, shortRawKey); err != nil {
		t.Fatal(err)
	}
	shortB64 := base64.StdEncoding.EncodeToString(shortRawKey)

	_, err = ParseKey(shortB64)
	if !errors.Is(err, ErrInvalidKeyLength) {
		t.Errorf("expected ErrInvalidKeyLength, got %v", err)
	}
}
