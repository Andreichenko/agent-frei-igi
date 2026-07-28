package worker

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"agent-frei-igi/internal/config"
	"agent-frei-igi/internal/critic"
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

func (f *fakeStore) UpdateJobContext(ctx context.Context, jobID uuid.UUID, gen int64, workerID string, contextBytes json.RawMessage) error {
	return nil
}

func (f *fakeStore) UpdateJobResult(ctx context.Context, jobID uuid.UUID, gen int64, workerID string, resultBytes json.RawMessage, promptVersion string) error {
	return nil
}

type fakeDetective struct {
	context *domain.ReviewContext
	err     error
}

func (f *fakeDetective) Build(ctx context.Context, job *domain.ReviewJob) (*domain.ReviewContext, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.context != nil {
		return f.context, nil
	}
	return &domain.ReviewContext{
		JobID: job.ID.String(),
		Files: []domain.ChangedFile{{Path: "main.go"}},
	}, nil
}

type fakeCritic struct {
	result *critic.Result
	err    error
}

func (f *fakeCritic) Review(ctx context.Context, reviewCtx *domain.ReviewContext) (*critic.Result, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.result != nil {
		return f.result, nil
	}
	return &critic.Result{
		PromptVersion: "critic-v1",
		Verdict:       "LGTM",
		Findings:      []critic.Finding{},
	}, nil
}

type fakePublisher struct {
	err error
}

func (f *fakePublisher) Publish(ctx context.Context, job *domain.ReviewJob, result *critic.Result) error {
	return f.err
}

func TestWorker_Pipeline_Success(t *testing.T) {
	cfg := &config.Config{
		WorkerID: "test-worker",
	}
	store := &fakeStore{}
	det := &fakeDetective{}
	crit := &fakeCritic{}
	pub := &fakePublisher{}
	w := NewWorker(cfg, store, det, crit, pub)

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

	if parsed["verdict"] != "LGTM" {
		t.Errorf("expected 'verdict' to be 'LGTM', got %v", parsed["verdict"])
	}

	if parsed["prompt_version"] != "critic-v1" {
		t.Errorf("expected 'prompt_version' to be 'critic-v1', got %v", parsed["prompt_version"])
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

	det := &fakeDetective{}
	crit := &fakeCritic{}
	pub := &fakePublisher{}
	w := NewWorker(cfg, store, det, crit, pub)

	// Simulate cancellation by changing job status inside fakeStore concurrently
	go func() {
		time.Sleep(20 * time.Millisecond) // DELAY: middle of the stages
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
