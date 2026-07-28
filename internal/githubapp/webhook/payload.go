package webhook

// GitHubAccountPayload represents the owner of the installation.
type GitHubAccountPayload struct {
	Login string `json:"login"`
	Type  string `json:"type"` // "User" or "Organization"
}

// GitHubInstallationPayload represents the installation metadata.
type GitHubInstallationPayload struct {
	ID      int64                `json:"id"`
	Account GitHubAccountPayload `json:"account"`
}

// GitHubRepositoryPayload represents repository details.
type GitHubRepositoryPayload struct {
	FullName string `json:"full_name"`
}

// GitHubAssigneePayload represents PR assignees.
type GitHubAssigneePayload struct {
	Login string `json:"login"`
}

// GitHubPRHeadBasePayload represents git ref details in PR.
type GitHubPRHeadBasePayload struct {
	SHA string `json:"sha"`
}

// GitHubPRPayload represents the pull request details.
type GitHubPRPayload struct {
	Number    int                     `json:"number"`
	State     string                  `json:"state"` // "open" or "closed"
	Draft     bool                    `json:"draft"`
	Head      GitHubPRHeadBasePayload `json:"head"`
	Base      GitHubPRHeadBasePayload `json:"base"`
	Assignees []GitHubAssigneePayload `json:"assignees"`
}

// GitHubIssuePRLinkPayload matches the pull_request object inside an issue to identify PR comments.
type GitHubIssuePRLinkPayload struct {
	URL string `json:"url,omitempty"`
}

// GitHubIssuePayload represents the issue details in comment events.
type GitHubIssuePayload struct {
	Number      int                       `json:"number"`
	PullRequest *GitHubIssuePRLinkPayload `json:"pull_request,omitempty"`
}

// GitHubCommentPayload represents the comment text body.
type GitHubCommentPayload struct {
	Body string `json:"body"`
}

// WebhookPayload integrates all required event fields into a single parser layout.
type WebhookPayload struct {
	Action       string                     `json:"action,omitempty"`
	Number       int                        `json:"number,omitempty"`
	PullRequest  *GitHubPRPayload           `json:"pull_request,omitempty"`
	Issue        *GitHubIssuePayload        `json:"issue,omitempty"`
	Comment      *GitHubCommentPayload      `json:"comment,omitempty"`
	Repository   *GitHubRepositoryPayload   `json:"repository,omitempty"`
	Installation *GitHubInstallationPayload `json:"installation,omitempty"`
}
