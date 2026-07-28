package critic

import (
	"context"
	"fmt"

	"agent-frei-igi/internal/config"
	"agent-frei-igi/internal/domain"
	"agent-frei-igi/internal/llm"
)

// Critic orchestrates the review prompt construction, execution, and validation.
type Critic struct {
	cfg    *config.Config
	router *llm.Router
}

// New initializes a Critic reviewer.
func New(cfg *config.Config, router *llm.Router) *Critic {
	return &Critic{
		cfg:    cfg,
		router: router,
	}
}

// Review generates prompt, triggers LLM router execution, and normalizes findings.
func (c *Critic) Review(ctx context.Context, reviewCtx *domain.ReviewContext) (*Result, error) {
	// 1. Build context prompt
	prompt := BuildPrompt(reviewCtx, c.cfg.PromptBudgetTokens)

	// Apply configured LLM execution timeout
	ctx, cancel := context.WithTimeout(ctx, c.cfg.LLMTimeout)
	defer cancel()

	// 2. Complete prompt on providers
	stdout, modelUsed, attempts, err := c.router.Complete(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("llm completion failed: %w", err)
	}

	// 3. Parse and validate LLM output
	rawResult, dropped, err := ParseAndValidate(stdout, reviewCtx, c.cfg.FindingConfidenceMin)
	if err != nil {
		return nil, fmt.Errorf("failed to parse findings: %w", err)
	}

	// 4. Enrich result with UUIDs and fingerprints
	promptVersion := "critic-v1"
	enriched := EnrichResult(rawResult, modelUsed, attempts, *dropped, promptVersion)

	return enriched, nil
}
