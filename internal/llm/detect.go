package llm

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
)

// classifyError analyzes exit errors and stderr to yield structured LLM errors.
func (p *CLIProvider) classifyError(err error, stderr string) error {
	if err == nil {
		return nil
	}

	// Respect context cancellations (e.g. timeouts)
	if errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "context deadline exceeded") {
		return err
	}

	lowerStderr := strings.ToLower(stderr)
	lowerErr := strings.ToLower(err.Error())

	// Heuristic: Rate Limits
	if strings.Contains(lowerStderr, "rate limit") || strings.Contains(lowerStderr, "429") || strings.Contains(lowerStderr, "quota") ||
		strings.Contains(lowerErr, "rate limit") || strings.Contains(lowerErr, "429") || strings.Contains(lowerErr, "quota") {
		return ErrRateLimited
	}

	// Heuristic: Dead / unauthorized sessions
	if strings.Contains(lowerStderr, "login") || strings.Contains(lowerStderr, "unauthorized") || strings.Contains(lowerStderr, "auth") ||
		strings.Contains(lowerErr, "login") || strings.Contains(lowerErr, "unauthorized") || strings.Contains(lowerErr, "auth") {
		// Emit structured critical log line for future hooks (e.g. Telegram notifications)
		log.Printf("[CRITICAL] provider_session_dead: provider %q (bin: %q) has unauthorized or dead session. Stderr: %q", p.name, p.bin, stderr)
		return ErrSessionDead
	}

	return fmt.Errorf("CLI provider error (%s): %w. stderr: %s", p.name, err, stderr)
}
