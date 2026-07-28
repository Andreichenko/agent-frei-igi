package worker

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"agent-frei-igi/internal/config"
	"agent-frei-igi/internal/domain"

	"github.com/google/uuid"
)

// fakeStore implements JobRepository for unit testing without database.
type fakeStore struct {
	mu            sync.Mutex
	job           *domain.ReviewJob
	completedJSON json.RawMessage
	failedError   string
	heartbeatCount int
}

func (f *fakeStore) ClaimJob(ctx context.Context, workerID string, lease time.Duration) (*domain.ReviewJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.job, nil
}

func (f *fakeStore) HeartbeatJob(ctx context.Context, jobID uuid.UUID, gen int64, workerID string, lease time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.heartbeatCount++
	return true, nil
}

func (f *fakeStore) CompleteJob(ctx context.Context, jobID uuid.UUID, gen int64, workerID string, result json.RawMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completedJSON = result
	return nil
}

func (f *fakeStore) FailJob(ctx context.Context, jobID uuid.UUID, gen int64, workerID string, errMsg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failedError = errMsg
	return nil
}

func (f *fakeStore) GetJob(ctx context.Context, id uuid.UUID) (*domain.ReviewJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.job, nil
}

func TestWorker_Pipeline_Success(t *testing.T) {
	cfg := &config.Config{
		WorkerID: "test-worker",
	}
	store := &fakeStore{}
	w := NewWorker(cfg, store)

	wID := "test-worker"
	job := &domain.ReviewJob{
		ID:             uuid.New(),
		LockGeneration: 1,
		Status:         domain.JobStatusRunning,
		LockedBy:       &wID,
	}
	store.job = job

	result, err := w.runPipeline(context.Background(), job)
	if err != nil {
		t.Fatalf("unexpected pipeline failure: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("failed to parse pipeline result JSON: %v", err)
	}

	if parsed["stub"] != true {
		t.Errorf("expected 'stub' to be true, got %v", parsed["stub"])
	}

	stages, ok := parsed["stages"].([]interface{})
	if !ok || len(stages) != 4 {
		t.Errorf("expected 'stages' to contain 4 elements, got %v", parsed["stages"])
	}
}

func TestWorker_Pipeline_CancelledMidFlight(t *testing.T) {
	cfg := &config.Config{
		WorkerID: "test-worker",
	}
	store := &fakeStore{
		job: &domain.ReviewJob{
			ID:             uuid.New(),
			LockGeneration: 1,
			Status:         domain.JobStatusRunning,
			LockedBy:       nil,
		},
	}
	// Initially owned by the worker
	wID := "test-worker"
	store.job.LockedBy = &wID

	w := NewWorker(cfg, store)

	// Simulate cancellation by changing job status inside fakeStore concurrently
	go func() {
		time.Sleep(70 * time.Millisecond) // DELAY: middle of the stages
		store.mu.Lock()
		store.job.Status = domain.JobStatusCancelled
		store.mu.Unlock()
	}()

	_, err := w.runPipeline(context.Background(), store.job)
	if err == nil {
		t.Fatal("expected pipeline to return cancel error, got nil")
	}

	if !errors.Is(err, context.Canceled) && err.Error() != "job cancelled in database" {
		t.Errorf("expected cancellation error, got %v", err)
	}
}
