package worker

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sync"
	"time"

	"agent-frei-igi/internal/config"
	"agent-frei-igi/internal/domain"

	"github.com/google/uuid"
)

// JobRepository defines the database interactions required by the worker.
type JobRepository interface {
	ClaimJob(ctx context.Context, workerID string, lease time.Duration) (*domain.ReviewJob, error)
	HeartbeatJob(ctx context.Context, jobID uuid.UUID, gen int64, workerID string, lease time.Duration) (bool, error)
	CompleteJob(ctx context.Context, jobID uuid.UUID, gen int64, workerID string, result json.RawMessage) error
	FailJob(ctx context.Context, jobID uuid.UUID, gen int64, workerID string, errMsg string) error
	GetJob(ctx context.Context, id uuid.UUID) (*domain.ReviewJob, error)
}

// Worker coordinates the background execution of review jobs.
type Worker struct {
	cfg   *config.Config
	store JobRepository
}

// NewWorker initializes a new Worker with configuration and repository.
func NewWorker(cfg *config.Config, store JobRepository) *Worker {
	return &Worker{
		cfg:   cfg,
		store: store,
	}
}

// Start spawns the worker loops running concurrently.
// Blocks until the context is cancelled, ensuring active jobs finish gracefully.
func (w *Worker) Start(ctx context.Context) {
	log.Printf("[WORKER] Starting worker pool with worker_id=%s, concurrency=%d", w.cfg.WorkerID, w.cfg.WorkerConcurrency)

	var wg sync.WaitGroup
	for i := 0; i < w.cfg.WorkerConcurrency; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			w.runLoop(ctx, index)
		}(i)
	}

	wg.Wait()
	log.Println("[WORKER] Worker pool shut down completely.")
}

func (w *Worker) runLoop(ctx context.Context, index int) {
	log.Printf("[WORKER-%d] Loop started", index)
	ticker := time.NewTicker(w.cfg.WorkerPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("[WORKER-%d] Shutting down loop due to context cancellation", index)
			return
		case <-ticker.C:
			job, err := w.store.ClaimJob(ctx, w.cfg.WorkerID, w.cfg.WorkerLease)
			if err != nil {
				log.Printf("[WORKER-%d] Error claiming job: %v", index, err)
				continue
			}

			if job == nil {
				// Queue is empty, keep sleeping
				continue
			}

			log.Printf("[WORKER-%d] Claimed job: id=%s repo=%s pr=%d gen=%d attempt=%d",
				index, job.ID, job.RepoFullName, job.PRNumber, job.LockGeneration, job.Attempt)

			w.process(ctx, job)
		}
	}
}

func (w *Worker) process(ctx context.Context, job *domain.ReviewJob) {
	jobCtx, cancelJob := context.WithCancel(ctx)
	defer cancelJob()

	// 1. Start heartbeat routine in the background
	var hbWg sync.WaitGroup
	hbWg.Add(1)
	go func() {
		defer hbWg.Done()
		ticker := time.NewTicker(w.cfg.WorkerHeartbeatInterval)
		defer ticker.Stop()

		for {
			select {
			case <-jobCtx.Done():
				return
			case <-ticker.C:
				ok, err := w.store.HeartbeatJob(jobCtx, job.ID, job.LockGeneration, w.cfg.WorkerID, w.cfg.WorkerLease)
				if err != nil || !ok {
					log.Printf("[WORKER] Heartbeat failed or lost lease for job %s: ok=%v, err=%v", job.ID, ok, err)
					cancelJob() // Cancel job execution context immediately on heartbeat failure/loss
					return
				}
				log.Printf("[WORKER] Heartbeat renewed for job %s", job.ID)
			}
		}
	}()

	// 2. Run the stub pipeline execution
	result, err := w.runPipeline(jobCtx, job)
	cancelJob() // Stop heartbeat immediately after pipeline finishes
	hbWg.Wait() // Wait for heartbeat goroutine to exit before database finalization

	// 3. Finalize the job status
	if err != nil {
		// Check if it was cancelled internally (due to manual cancel or heartbeat loss)
		if errors.Is(err, context.Canceled) || w.isCancelledInDB(ctx, job.ID, job.LockGeneration) {
			log.Printf("[WORKER] Job %s execution cancelled. Aborting without marking completed.", job.ID)
			return
		}

		log.Printf("[WORKER] Job %s failed: %v", job.ID, err)
		failErr := w.store.FailJob(ctx, job.ID, job.LockGeneration, w.cfg.WorkerID, err.Error())
		if failErr != nil {
			log.Printf("[WORKER] Failed to update job status to failed for job %s: %v", job.ID, failErr)
		}
		return
	}

	// 4. Complete the job
	compErr := w.store.CompleteJob(ctx, job.ID, job.LockGeneration, w.cfg.WorkerID, result)
	if compErr != nil {
		log.Printf("[WORKER] Failed to mark job %s completed: %v", job.ID, compErr)
	} else {
		log.Printf("[WORKER] Job %s completed successfully.", job.ID)
	}
}

// isCancelledInDB queries the database to see if the job has been cancelled or claimed by another worker.
func (w *Worker) isCancelledInDB(ctx context.Context, id uuid.UUID, gen int64) bool {
	dbJob, err := w.store.GetJob(ctx, id)
	if err != nil || dbJob == nil {
		return true
	}
	if dbJob.Status == domain.JobStatusCancelled {
		return true
	}
	if dbJob.Status != domain.JobStatusRunning || dbJob.LockedBy == nil || *dbJob.LockedBy != w.cfg.WorkerID || dbJob.LockGeneration != gen {
		return true
	}
	return false
}
