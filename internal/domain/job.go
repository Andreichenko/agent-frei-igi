package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// JobStatus defines the current processing state of a review job.
type JobStatus string

const (
	JobStatusPending        JobStatus = "pending"
	JobStatusRunning        JobStatus = "running"
	JobStatusRetryWait      JobStatus = "retry_wait"
	JobStatusCompleted      JobStatus = "completed"
	JobStatusFailed         JobStatus = "failed"
	JobStatusCancelled      JobStatus = "cancelled"
	JobStatusNeedsReconcile JobStatus = "needs_reconcile"
)

// ReviewJob represents an execution unit of a PR review process.
type ReviewJob struct {
	ID                 uuid.UUID       `json:"id"`
	InstallationID     int64           `json:"installation_id"`
	RepoFullName       string          `json:"repo_full_name"`
	PRNumber           int             `json:"pr_number"`
	HeadSHA            string          `json:"head_sha"`
	BaseSHA            string          `json:"base_sha,omitempty"`
	PublisherAccountID *int64          `json:"publisher_account_id,omitempty"`
	Status             JobStatus       `json:"status"`
	LockGeneration     int64           `json:"lock_generation"`
	LockedUntil        *time.Time      `json:"locked_until,omitempty"`
	LockedBy           *string         `json:"locked_by,omitempty"`
	Attempt            int             `json:"attempt"`
	LastError          *string         `json:"last_error,omitempty"`
	ContextBlob        json.RawMessage `json:"context_blob,omitempty"`
	ResultBlob         json.RawMessage `json:"result_blob,omitempty"`
	PromptVersion      *string         `json:"prompt_version,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}
