package config

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"agent-frei-igi/internal/crypto"
)

// Config holds all configuration values for the application.
type Config struct {
	HTTPAddr                 string
	DatabaseURL              string
	TokenEncryptionKey       string
	WebhookSecret            string
	ReviewerLogins           []string
	WorkerConcurrency        int
	WorkerLease              time.Duration
	WorkerHeartbeatInterval  time.Duration
	WorkerPollInterval       time.Duration
	WorkerID                 string
	ShutdownTimeout          time.Duration
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

	// Parse worker concurrency
	concurrency := 1
	if envVal := os.Getenv("WORKER_CONCURRENCY"); envVal != "" {
		val, err := strconv.Atoi(envVal)
		if err != nil {
			log.Fatalf("Invalid WORKER_CONCURRENCY configuration: %v", err)
		}
		concurrency = val
	}

	// Parse worker durations
	lease := parseDuration("WORKER_LEASE", "15m")
	heartbeat := parseDuration("WORKER_HEARTBEAT_INTERVAL", "30s")
	poll := parseDuration("WORKER_POLL_INTERVAL", "2s")
	shutdown := parseDuration("SHUTDOWN_TIMEOUT", "60s")

	// Resolve worker ID
	workerID := os.Getenv("WORKER_ID")
	if workerID == "" {
		hostname, err := os.Hostname()
		if err != nil {
			hostname = "unknown-host"
		}
		workerID = fmt.Sprintf("%s-%d", hostname, os.Getpid())
	}

	return &Config{
		HTTPAddr:                 addr,
		DatabaseURL:              dbURL,
		TokenEncryptionKey:       tokenKey,
		WebhookSecret:            webhookSecret,
		ReviewerLogins:           logins,
		WorkerConcurrency:        concurrency,
		WorkerLease:              lease,
		WorkerHeartbeatInterval:  heartbeat,
		WorkerPollInterval:       poll,
		WorkerID:                 workerID,
		ShutdownTimeout:          shutdown,
	}
}

// parseDuration helper to safely parse duration config or fallback to default.
func parseDuration(envKey, defaultVal string) time.Duration {
	valStr := os.Getenv(envKey)
	if valStr == "" {
		valStr = defaultVal
	}
	d, err := time.ParseDuration(valStr)
	if err != nil {
		log.Fatalf("Invalid duration configuration for %s: %v", envKey, err)
	}
	return d
}

// ParseTokenKey validates and decodes the TokenEncryptionKey from the config.
func (c *Config) ParseTokenKey() (crypto.Key, error) {
	return crypto.ParseKey(c.TokenEncryptionKey)
}
