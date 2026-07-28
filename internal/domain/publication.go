package domain

import (
	"time"

	"github.com/google/uuid"
)

// PublicationState represents the publishing transaction status on GitHub.
type PublicationState string

const (
	PublicationStatePending   PublicationState = "pending_github"
	PublicationStatePublished PublicationState = "published"
	PublicationStateFailed    PublicationState = "failed"
)

// PublisherKind represents whether publishing is done by user token or app bot identity.
type PublisherKind string

const (
	PublisherKindUser   PublisherKind = "user"
	PublisherKindAppBot PublisherKind = "app_bot"
)

// ReviewPublication represents the publication event of a review on GitHub.
type ReviewPublication struct {
	ID                 int64            `json:"id"`
	JobID              uuid.UUID        `json:"job_id"`
	PublisherAccountID *int64           `json:"publisher_account_id,omitempty"`
	PublisherKind      PublisherKind    `json:"publisher_kind"`
	State              PublicationState `json:"state"`
	GitHubReviewID     *int64           `json:"github_review_id,omitempty"`
	PublishedAt        *time.Time       `json:"published_at,omitempty"`
	CreatedAt          time.Time        `json:"created_at"`
	UpdatedAt          time.Time        `json:"updated_at"`
}
