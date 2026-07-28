package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

const (
	nonceSize = 12
	tagSize   = 16
)

var (
	ErrCiphertextTooShort = errors.New("ciphertext block is too short")
	ErrDecryptionFailed   = errors.New("failed to decrypt ciphertext or authentication failed")
)

// Encrypt encrypts the plaintext using AES-256-GCM with a random 12-byte nonce.
// Output format: nonce (12 bytes) || ciphertext + authentication tag.
func Encrypt(key Key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("failed to create aes cipher block: %w", err)
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create gcm cipher: %w", err)
	}

	// Output buffer: first nonceSize bytes reserved for nonce, then encrypted ciphertext
	output := make([]byte, nonceSize+len(plaintext)+tagSize)
	nonce := output[:nonceSize]

	// Read random bytes for nonce
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate random nonce: %w", err)
	}

	// Seal plaintext into output buffer starting from nonceSize offset.
	// Seal appends the result to the dst slice, so we pass output[:nonceSize:nonceSize] to prevent allocation.
	aesGCM.Seal(output[nonceSize:nonceSize], nonce, plaintext, nil)

	return output, nil
}

// Decrypt decrypts the blob produced by Encrypt.
// The blob is expected to have the format: nonce (12 bytes) || ciphertext + tag.
// Returns an error if the blob is too short or if decryption/authentication fails.
func Decrypt(key Key, blob []byte) ([]byte, error) {
	if len(blob) < nonceSize+tagSize {
		return nil, ErrCiphertextTooShort
	}

	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("failed to create aes cipher block: %w", err)
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create gcm cipher: %w", err)
	}

	nonce := blob[:nonceSize]
	ciphertext := blob[nonceSize:]

	plaintext, err := aesGCM.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		// Do not expose plaintext or decryption details in error strings
		return nil, ErrDecryptionFailed
	}

	return plaintext, nil
}
