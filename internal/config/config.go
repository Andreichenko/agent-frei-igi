package config

import (
	"os"

	"agent-frei-igi/internal/crypto"
)

// Config holds all configuration values for the application.
type Config struct {
	HTTPAddr           string
	DatabaseURL        string
	TokenEncryptionKey string
}

// Load reads config from environment variables and sets defaults.
func Load() *Config {
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	dbURL := os.Getenv("DATABASE_URL")
	tokenKey := os.Getenv("TOKEN_ENCRYPTION_KEY")

	return &Config{
		HTTPAddr:           addr,
		DatabaseURL:        dbURL,
		TokenEncryptionKey: tokenKey,
	}
}

// ParseTokenKey validates and decodes the TokenEncryptionKey from the config.
func (c *Config) ParseTokenKey() (crypto.Key, error) {
	return crypto.ParseKey(c.TokenEncryptionKey)
}
