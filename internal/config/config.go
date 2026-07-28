package config

import (
	"os"
)

// Config holds all configuration values for the application.
type Config struct {
	HTTPAddr string
}

// Load reads config from environment variables and sets defaults.
func Load() *Config {
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	return &Config{
		HTTPAddr: addr,
	}
}
