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
// detective and critic are real stages. memory and diplomat are stubs.
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

	// --- 2. Memory Stage (Stub) ---
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if w.isCancelledInDB(ctx, job.ID, job.LockGeneration) {
		return nil, errors.New("job cancelled in database")
	}

	log.Println("[PIPELINE] Running stage \"memory\"...")
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(50 * time.Millisecond):
	}

	// --- 3. Critic Stage (Real) ---
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if w.isCancelledInDB(ctx, job.ID, job.LockGeneration) {
		return nil, errors.New("job cancelled in database")
	}

	log.Printf("[PIPELINE] Running stage \"critic\" for job %s...", job.ID)
	criticResult, err := w.critic.Review(ctx, reviewCtx)
	if err != nil {
		return nil, fmt.Errorf("critic stage failed: %w", err)
	}

	// Save findings result to database
	resultBytes, err := json.Marshal(criticResult)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal critic result: %w", err)
	}

	err = w.store.UpdateJobResult(ctx, job.ID, job.LockGeneration, w.cfg.WorkerID, resultBytes, criticResult.PromptVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to save critic result: %w", err)
	}

	// --- 4. Diplomat Stage (Real) ---
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if w.isCancelledInDB(ctx, job.ID, job.LockGeneration) {
		return nil, errors.New("job cancelled in database")
	}

	log.Printf("[PIPELINE] Running stage \"diplomat\" for job %s...", job.ID)
	err = w.diplomat.Publish(ctx, job, criticResult)
	if err != nil {
		return nil, fmt.Errorf("diplomat stage failed: %w", err)
	}

	// Final check before marking as success
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if w.isCancelledInDB(ctx, job.ID, job.LockGeneration) {
		return nil, errors.New("job cancelled in database")
	}

	return json.RawMessage(resultBytes), nil
}
