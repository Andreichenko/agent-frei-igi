package domain

import (
	"time"
)

// AccountStatus defines the state of a reviewer account.
type AccountStatus string

const (
	AccountStatusActive   AccountStatus = "active"
	AccountStatusInvalid  AccountStatus = "invalid"  // Set when auth token is rejected (e.g. 401)
	AccountStatusDisabled AccountStatus = "disabled" // Disabled manually by administrator
)

// GitHubAccount represents a machine user account used to publish reviews.
type GitHubAccount struct {
	ID              int64         `json:"id"`
	GitHubLogin     string        `json:"github_login"`
	AccessTokenEnc  []byte        `json:"-"` // Kept hidden from JSON responses
	TokenExpiresAt  *time.Time    `json:"token_expires_at,omitempty"`
	Status          AccountStatus `json:"status"`
	LastUsedAt      *time.Time    `json:"last_used_at,omitempty"`
	CreatedAt       time.Time     `json:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at"`
}
