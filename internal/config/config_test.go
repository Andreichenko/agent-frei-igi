package config

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"io"
	"os"
	"testing"
)

// TestConfig_ParseTokenKey verifies config loading and key parsing from environment variables.
func TestConfig_ParseTokenKey(t *testing.T) {
	// Backup and restore environment
	oldKey := os.Getenv("TOKEN_ENCRYPTION_KEY")
	defer os.Setenv("TOKEN_ENCRYPTION_KEY", oldKey)

	// 1. Test parsing with a valid Base64 32-byte key
	rawKey := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, rawKey); err != nil {
		t.Fatal(err)
	}
	validB64 := base64.StdEncoding.EncodeToString(rawKey)
	os.Setenv("TOKEN_ENCRYPTION_KEY", validB64)

	cfg := Load()
	parsedKey, err := cfg.ParseTokenKey()
	if err != nil {
		t.Fatalf("expected successful parse of valid config key, got error: %v", err)
	}

	if !bytes.Equal(rawKey, parsedKey[:]) {
		t.Error("parsed key bytes do not match expected raw key")
	}

	// 2. Test parsing when TOKEN_ENCRYPTION_KEY is not set
	os.Setenv("TOKEN_ENCRYPTION_KEY", "")
	cfgEmpty := Load()
	_, err = cfgEmpty.ParseTokenKey()
	if err == nil {
		t.Error("expected error when parsing an empty configuration key, but got nil")
	}
}

// TestConfig_ReviewerLogins verifies that REVIEWER_LOGINS is correctly split, trimmed, and ordered.
func TestConfig_ReviewerLogins(t *testing.T) {
	oldLogins := os.Getenv("REVIEWER_LOGINS")
	defer os.Setenv("REVIEWER_LOGINS", oldLogins)

	os.Setenv("REVIEWER_LOGINS", " alice,  , bob,carol, ")
	cfg := Load()

	expected := []string{"alice", "bob", "carol"}
	if len(cfg.ReviewerLogins) != len(expected) {
		t.Fatalf("expected %d logins, got %d", len(expected), len(cfg.ReviewerLogins))
	}

	for i, v := range expected {
		if cfg.ReviewerLogins[i] != v {
			t.Errorf("expected login at index %d to be %q, got %q", i, v, cfg.ReviewerLogins[i])
		}
	}
}
