package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"agent-frei-igi/internal/domain"
)

// runPipeline executes the review stages (detective -> memory -> critic -> diplomat).
// Currently, detective is a real stage, and memory/critic/diplomat are stubs.
func (w *Worker) runPipeline(ctx context.Context, job *domain.ReviewJob) (json.RawMessage, error) {
	// --- 1. Detective Stage ---
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if w.isCancelledInDB(ctx, job.ID, job.LockGeneration) {
		return nil, errors.New("job cancelled in database")
	}

	log.Printf("[PIPELINE] Running stage \"detective\" for job %s...", job.ID)
	reviewCtx, err := w.detective.Build(ctx, job)
	if err != nil {
		return nil, fmt.Errorf("detective stage failed: %w", err)
	}

	// Save the built ReviewContext into the database job.context_blob
	ctxBytes, err := json.Marshal(reviewCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal review context: %w", err)
	}

	err = w.store.UpdateJobContext(ctx, job.ID, job.LockGeneration, w.cfg.WorkerID, ctxBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to save review context: %w", err)
	}

	// --- 2. Memory / Critic / Diplomat Stub Stages ---
	stubStages := []string{"memory", "critic", "diplomat"}
	for _, stage := range stubStages {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if w.isCancelledInDB(ctx, job.ID, job.LockGeneration) {
			return nil, errors.New("job cancelled in database")
		}

		log.Printf("[PIPELINE] Running stage %q for job %s...", stage, job.ID)

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
		"stub":           true,
		"detective":      "ok",
		"files_included": len(reviewCtx.Files),
		"stages":         []string{"detective", "memory", "critic", "diplomat"},
	}

	resBytes, err := json.Marshal(resultMap)
	if err != nil {
		return nil, err
	}

	return json.RawMessage(resBytes), nil
}
