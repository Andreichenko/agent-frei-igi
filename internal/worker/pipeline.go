package worker

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"agent-frei-igi/internal/domain"
)

// runPipeline simulates the review process stages (detective -> memory -> critic -> diplomat).
// Returns a stub JSON result upon successful completion.
func (w *Worker) runPipeline(ctx context.Context, job *domain.ReviewJob) (json.RawMessage, error) {
	stages := []string{"detective", "memory", "critic", "diplomat"}

	for _, stage := range stages {
		// Check cancellation before starting each stage
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if w.isCancelledInDB(ctx, job.ID, job.LockGeneration) {
			return nil, errors.New("job cancelled in database")
		}

		log.Printf("[PIPELINE] Running stage %q for job %s...", stage, job.ID)

		// Simulate minor processing delay
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}

	// Final check before marking as success
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if w.isCancelledInDB(ctx, job.ID, job.LockGeneration) {
		return nil, errors.New("job cancelled in database")
	}

	resultMap := map[string]interface{}{
		"stub":   true,
		"stages": stages,
	}

	resBytes, err := json.Marshal(resultMap)
	if err != nil {
		return nil, err
	}

	return json.RawMessage(resBytes), nil
}
