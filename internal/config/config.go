package config

import (
	"os"
	"strings"

	"agent-frei-igi/internal/crypto"
)

// Config holds all configuration values for the application.
type Config struct {
	HTTPAddr           string
	DatabaseURL        string
	TokenEncryptionKey string
	WebhookSecret      string
	ReviewerLogins     []string
}

// Load reads config from environment variables and sets defaults.
func Load() *Config {
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	dbURL := os.Getenv("DATABASE_URL")
	tokenKey := os.Getenv("TOKEN_ENCRYPTION_KEY")
	webhookSecret := os.Getenv("GITHUB_WEBHOOK_SECRET")
	reviewerLoginsStr := os.Getenv("REVIEWER_LOGINS")

	var logins []string
	if reviewerLoginsStr != "" {
		for _, part := range strings.Split(reviewerLoginsStr, ",") {
			trimmed := strings.TrimSpace(part)
			if trimmed != "" {
				logins = append(logins, trimmed)
			}
		}
	}

	return &Config{
		HTTPAddr:           addr,
		DatabaseURL:        dbURL,
		TokenEncryptionKey: tokenKey,
		WebhookSecret:      webhookSecret,
		ReviewerLogins:     logins,
	}
}

// ParseTokenKey validates and decodes the TokenEncryptionKey from the config.
func (c *Config) ParseTokenKey() (crypto.Key, error) {
	return crypto.ParseKey(c.TokenEncryptionKey)
}
