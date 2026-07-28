package domain

import (
	"time"
)

// ReviewContext collects PR files and repository metadata to provide context for reviews.
type ReviewContext struct {
	JobID           string         `json:"job_id"`
	InstallationID  int64          `json:"installation_id"` // internal DB installation ID
	GitHubInstallID int64          `json:"github_installation_id"`
	RepoFullName    string         `json:"repo_full_name"` // "owner/repo"
	PRNumber        int            `json:"pr_number"`
	HeadSHA         string         `json:"head_sha"`
	BaseSHA         string         `json:"base_sha,omitempty"`
	PublisherLogin  string         `json:"publisher_login,omitempty"`
	Files           []ChangedFile  `json:"files"`
	Truncation      TruncationInfo `json:"truncation"`
	BuiltAt         time.Time      `json:"built_at"`
	Rules           []ProjectRule  `json:"rules,omitempty"` // populated during Memory stage
}

// ChangedFile represents a single file modified within the Pull Request.
type ChangedFile struct {
	Path         string `json:"path"`
	Status       string `json:"status"` // e.g. "added", "modified", "removed", "renamed"
	PreviousPath string `json:"previous_path,omitempty"`
	Language     string `json:"language,omitempty"` // optional heuristic based on extension
	Patch        string `json:"patch,omitempty"`
	Omitted      bool   `json:"omitted,omitempty"`
	OmitReason   string `json:"omit_reason,omitempty"`
}

// TruncationInfo tracks files and patch sizes to respect context window limits.
type TruncationInfo struct {
	MaxFiles         int  `json:"max_files"`
	MaxPatchBytes    int  `json:"max_patch_bytes"`
	FilesTotal       int  `json:"files_total"`
	FilesIncluded    int  `json:"files_included"`
	FilesOmitted     int  `json:"files_omitted"`
	PatchBytesTotal  int  `json:"patch_bytes_total"`
	PatchBytesKept   int  `json:"patch_bytes_kept"`
	TruncatedByFiles bool `json:"truncated_by_files"`
	TruncatedByBytes bool `json:"truncated_by_bytes"`
}
