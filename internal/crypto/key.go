package crypto

import (
	"encoding/base64"
	"errors"
	"fmt"
)

// Key represents a 256-bit AES key.
type Key [32]byte

var (
	ErrEmptyKey        = errors.New("encryption key cannot be empty")
	ErrInvalidBase64   = errors.New("encryption key must be a valid base64 encoded string")
	ErrInvalidKeyLength = errors.New("decrypted key length must be exactly 32 bytes")
)

// ParseKey decodes a base64 encoded string into a 32-byte key.
// Returns an error if the key is empty, invalid base64, or is not exactly 32 bytes when decoded.
func ParseKey(b64 string) (Key, error) {
	if b64 == "" {
		return Key{}, ErrEmptyKey
	}

	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return Key{}, fmt.Errorf("%w: %v", ErrInvalidBase64, err)
	}

	if len(decoded) != 32 {
		return Key{}, fmt.Errorf("%w: got %d bytes", ErrInvalidKeyLength, len(decoded))
	}

	var key Key
	copy(key[:], decoded)
	return key, nil
}
