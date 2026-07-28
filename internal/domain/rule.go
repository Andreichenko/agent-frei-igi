package domain

import (
	"time"
)

// ProjectRule represents an rule or standard configured for a specific repository.
type ProjectRule struct {
	ID             int64     `json:"id"`
	InstallationID int64     `json:"installation_id"`
	RepoFullName   string    `json:"repo_full_name"`
	RuleKey        string    `json:"rule_key"`
	RuleBody       string    `json:"rule_body"`
	Enabled        bool      `json:"enabled"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
