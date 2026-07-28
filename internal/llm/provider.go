package llm

import (
	"context"
	"errors"
	"fmt"
)

var (
	// ErrRateLimited represents provider API rate-limiting.
	ErrRateLimited = errors.New("provider rate limited")
	// ErrSessionDead represents invalid login/auth session.
	ErrSessionDead = errors.New("provider session dead")
)

// Provider defines the interface to interact with a CLI-based LLM.
type Provider interface {
	Name() string
	Complete(ctx context.Context, prompt string) (string, error)
}

// Router orchestrates primary and fallback LLM completions.
type Router struct {
	primary  Provider
	fallback Provider
}

// NewRouter initializes a Router with optional fallback.
func NewRouter(primary, fallback Provider) *Router {
	return &Router{
		primary:  primary,
		fallback: fallback,
	}
}

// Complete executes the prompt on the primary provider. If it rate-limits or fails,
// it falls back to the secondary provider (if configured).
// Returns the output, the model/provider used, list of provider attempts with statuses, and an error.
func (r *Router) Complete(ctx context.Context, prompt string) (string, string, []string, error) {
	var attempts []string

	// 1. Try primary
	attempts = append(attempts, r.primary.Name())
	stdout, err := r.primary.Complete(ctx, prompt)
	if err == nil {
		attempts[len(attempts)-1] += ":ok"
		return stdout, r.primary.Name(), attempts, nil
	}

	attempts[len(attempts)-1] += ":fail"

	// If no fallback is configured, return the primary's error
	if r.fallback == nil {
		return "", "", attempts, fmt.Errorf("primary provider failed (no fallback configured): %w", err)
	}

	// 2. Try fallback
	attempts = append(attempts, r.fallback.Name())
	fallbackStdout, fallbackErr := r.fallback.Complete(ctx, prompt)
	if fallbackErr == nil {
		attempts[len(attempts)-1] += ":ok"
		return fallbackStdout, r.fallback.Name(), attempts, nil
	}

	attempts[len(attempts)-1] += ":fail"
	return "", "", attempts, fmt.Errorf("both primary and fallback failed. primary err: %v, fallback err: %v", err, fallbackErr)
}
