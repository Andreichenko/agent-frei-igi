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
	GitHubAppID              string
	GitHubAppPrivateKeyPath  string
	GitHubAPIBaseURL         string
	DetectiveMaxFiles        int
	DetectiveMaxPatchBytes   int
	LLMProvider              string
	LLMFallbackProvider      string
	LLMTimeout               time.Duration
	LLMMaxOutputTokens       int
	FindingConfidenceMin     float64
	PromptBudgetTokens       int
	AgyBin                   string
	GrokBin                  string
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

	// Parse GitHub App configs
	githubAppID := os.Getenv("GITHUB_APP_ID")
	githubPrivateKeyPath := os.Getenv("GITHUB_APP_PRIVATE_KEY_PATH")
	githubAPIBaseURL := os.Getenv("GITHUB_API_BASE_URL")
	if githubAPIBaseURL == "" {
		githubAPIBaseURL = "https://api.github.com"
	}

	maxFiles := 40
	if envVal := os.Getenv("DETECTIVE_MAX_FILES"); envVal != "" {
		val, err := strconv.Atoi(envVal)
		if err == nil {
			maxFiles = val
		}
	}

	maxPatchBytes := 409600
	if envVal := os.Getenv("DETECTIVE_MAX_PATCH_BYTES"); envVal != "" {
		val, err := strconv.Atoi(envVal)
		if err == nil {
			maxPatchBytes = val
		}
	}

	// Parse LLM config
	llmProvider := os.Getenv("LLM_PROVIDER")
	if llmProvider == "" {
		llmProvider = "agy"
	}
	llmFallbackProvider := os.Getenv("LLM_FALLBACK_PROVIDER")
	if llmFallbackProvider == "" {
		llmFallbackProvider = "grok"
	}
	llmTimeout := parseDuration("LLM_TIMEOUT", "120s")

	maxOutputTokens := 8192
	if envVal := os.Getenv("LLM_MAX_OUTPUT_TOKENS"); envVal != "" {
		val, err := strconv.Atoi(envVal)
		if err == nil {
			maxOutputTokens = val
		}
	}

	confidenceMin := 0.55
	if envVal := os.Getenv("FINDING_CONFIDENCE_MIN"); envVal != "" {
		val, err := strconv.ParseFloat(envVal, 64)
		if err == nil {
			confidenceMin = val
		}
	}

	promptBudget := 100000
	if envVal := os.Getenv("PROMPT_BUDGET_TOKENS"); envVal != "" {
		val, err := strconv.Atoi(envVal)
		if err == nil {
			promptBudget = val
		}
	}

	agyBin := os.Getenv("AGY_BIN")
	if agyBin == "" {
		agyBin = "agy"
	}
	grokBin := os.Getenv("GROK_BIN")
	if grokBin == "" {
		grokBin = "grok"
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
		GitHubAppID:              githubAppID,
		GitHubAppPrivateKeyPath:  githubPrivateKeyPath,
		GitHubAPIBaseURL:         githubAPIBaseURL,
		DetectiveMaxFiles:        maxFiles,
		DetectiveMaxPatchBytes:   maxPatchBytes,
		LLMProvider:              llmProvider,
		LLMFallbackProvider:      llmFallbackProvider,
		LLMTimeout:               llmTimeout,
		LLMMaxOutputTokens:       maxOutputTokens,
		FindingConfidenceMin:     confidenceMin,
		PromptBudgetTokens:       promptBudget,
		AgyBin:                   agyBin,
		GrokBin:                  grokBin,
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
