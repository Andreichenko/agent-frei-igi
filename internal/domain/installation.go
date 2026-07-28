package domain

import (
	"time"
)

// AccountType represents the type of owner of a GitHub installation.
type AccountType string

const (
	AccountTypeUser         AccountType = "User"
	AccountTypeOrganization AccountType = "Organization"
)

// Installation represents a GitHub App installation record.
type Installation struct {
	ID                   int64        `json:"id"`
	GitHubInstallationID int64        `json:"github_installation_id"`
	AccountLogin         string       `json:"account_login"`
	AccountType          AccountType  `json:"account_type"`
	SuspendedAt          *time.Time   `json:"suspended_at,omitempty"`
	CreatedAt            time.Time    `json:"created_at"`
	UpdatedAt            time.Time    `json:"updated_at"`
}
