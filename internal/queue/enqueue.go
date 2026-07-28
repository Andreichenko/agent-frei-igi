package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"agent-frei-igi/internal/domain"
	"agent-frei-igi/internal/githubapp/webhook"
	"agent-frei-igi/internal/store/postgres"
)

// EnqueuerStore specifies the database operations required by the Enqueuer.
type EnqueuerStore interface {
	UpsertInstallation(ctx context.Context, inst *domain.Installation) error
	GetInstallationByGitHubID(ctx context.Context, githubInstID int64) (*domain.Installation, error)
	CancelOpenJobsForPR(ctx context.Context, instID int64, repo string, prNumber int) (int64, error)
	CancelJobsForPR(ctx context.Context, instID int64, repo string, prNumber int, headSHA string) (int64, error)
	GetAccountByLogin(ctx context.Context, login string) (*domain.GitHubAccount, error)
	CreateJob(ctx context.Context, job *domain.ReviewJob) error
}

// Enqueuer coordinates incoming GitHub App webhooks and schedules review jobs.
type Enqueuer struct {
	store  EnqueuerStore
	logins []string
}

// NewEnqueuer initializes a new Enqueuer with a database store and reviewer logins list.
func NewEnqueuer(store EnqueuerStore, logins []string) *Enqueuer {
	return &Enqueuer{
		store:  store,
		logins: logins,
	}
}

// HandleEvent processes the parsed webhook payload based on the event type.
// Returns a decision string (e.g. "enqueued", "cancelled", "skipped", "ignored") and an error.
func (e *Enqueuer) HandleEvent(ctx context.Context, eventType string, payload *webhook.WebhookPayload) (string, error) {
	switch eventType {
	case "ping":
		return "ignored", nil

	case "installation":
		return e.handleInstallationEvent(ctx, payload)

	case "pull_request":
		return e.handlePullRequestEvent(ctx, payload)

	case "issue_comment":
		return e.handleIssueCommentEvent(ctx, payload)

	default:
		return "ignored", nil
	}
}

// handleInstallationEvent upserts or suspends the installation record.
func (e *Enqueuer) handleInstallationEvent(ctx context.Context, payload *webhook.WebhookPayload) (string, error) {
	if payload.Installation == nil {
		return "ignored", errors.New("installation payload missing in installation event")
	}

	inst := &domain.Installation{
		GitHubInstallationID: payload.Installation.ID,
		AccountLogin:         payload.Installation.Account.Login,
		AccountType:          domain.AccountType(payload.Installation.Account.Type),
	}

	action := payload.Action
	switch action {
	case "suspend", "deleted":
		now := time.Now()
		inst.SuspendedAt = &now
	case "created", "unsuspend", "new_permissions_accepted":
		inst.SuspendedAt = nil
	}

	err := e.store.UpsertInstallation(ctx, inst)
	if err != nil {
		return "failed", fmt.Errorf("failed to upsert installation: %w", err)
	}

	log.Printf("Processed installation event: id=%d action=%s login=%s",
		payload.Installation.ID, action, payload.Installation.Account.Login)

	return "processed", nil
}

// handlePullRequestEvent handles PR lifecycle triggers (opened, reopened, synchronize, ready_for_review, closed, converted_to_draft).
func (e *Enqueuer) handlePullRequestEvent(ctx context.Context, payload *webhook.WebhookPayload) (string, error) {
	if payload.Installation == nil || payload.Repository == nil || payload.PullRequest == nil {
		return "ignored", errors.New("malformed pull_request payload")
	}

	// 1. Ensure installation exists in database
	inst, err := e.ensureInstallation(ctx, payload.Installation.ID, payload)
	if err != nil {
		return "failed", fmt.Errorf("failed to ensure installation: %w", err)
	}

	pr := payload.PullRequest
	repo := payload.Repository.FullName

	// 2. Handle PR closed or converted to draft -> cancel pending/running jobs (even if suspended)
	isClosed := payload.Action == "closed" || pr.State == "closed"
	isConvertedToDraft := payload.Action == "converted_to_draft"

	if isClosed || isConvertedToDraft {
		cancelled, err := e.store.CancelOpenJobsForPR(ctx, inst.ID, repo, pr.Number)
		if err != nil {
			return "failed", fmt.Errorf("failed to cancel open jobs: %w", err)
		}
		log.Printf("Cancelled %d open jobs for PR %s#%d (action=%s)", cancelled, repo, pr.Number, payload.Action)
		return "cancelled", nil
	}

	// 3. Skip enqueue if installation is suspended
	if inst.SuspendedAt != nil {
		log.Printf("Skipping PR event for suspended installation: %d", inst.GitHubInstallationID)
		return "skipped_suspended", nil
	}

	// 4. Allowlist check: only process opened, reopened, synchronize, ready_for_review, assigned actions
	switch payload.Action {
	case "opened", "reopened", "synchronize", "ready_for_review", "assigned":
		// Allowed, continue processing
	default:
		log.Printf("Ignoring PR action: %s for PR %s#%d", payload.Action, repo, pr.Number)
		return "ignored", nil
	}

	// 3. Skip draft PRs unless it's a ready_for_review action
	if pr.Draft && payload.Action != "ready_for_review" {
		log.Printf("Skipping draft PR %s#%d", repo, pr.Number)
		return "skipped", nil
	}

	// 4. Resolve reviewer login based on assignees (Trigger A)
	var assignees []string
	for _, a := range pr.Assignees {
		assignees = append(assignees, a.Login)
	}

	chosenLogin, matched := e.matchReviewer(assignees, "")
	if !matched {
		log.Printf("No whitelisted reviewer assigned to PR %s#%d", repo, pr.Number)
		return "skipped", nil
	}

	// 5. Cancel older jobs for same PR (supersede logic)
	headSHA := pr.Head.SHA
	superseded, err := e.store.CancelJobsForPR(ctx, inst.ID, repo, pr.Number, headSHA)
	if err != nil {
		return "failed", fmt.Errorf("failed to supersede older jobs: %w", err)
	}
	if superseded > 0 {
		log.Printf("Superseded %d older jobs for PR %s#%d to head_sha=%s", superseded, repo, pr.Number, headSHA)
	}

	// 6. Check if reviewer account already exists in database (to resolve internal ID)
	acc, err := e.store.GetAccountByLogin(ctx, chosenLogin)
	if err != nil {
		return "failed", fmt.Errorf("failed to query reviewer account: %w", err)
	}

	var publisherAccountID *int64
	if acc != nil {
		publisherAccountID = &acc.ID
	}

	// 7. Enqueue new job
	payloadBytes, _ := json.Marshal(payload)
	job := &domain.ReviewJob{
		InstallationID:     inst.ID,
		RepoFullName:       repo,
		PRNumber:           pr.Number,
		HeadSHA:            headSHA,
		BaseSHA:            pr.Base.SHA,
		PublisherAccountID: publisherAccountID,
		PublisherLogin:     &chosenLogin,
		Status:             domain.JobStatusPending,
		ContextBlob:        payloadBytes,
	}

	err = e.store.CreateJob(ctx, job)
	if err != nil {
		if errors.Is(err, postgres.ErrJobAlreadyExists) {
			log.Printf("Job already enqueued for PR %s#%d with head_sha=%s (no-op success)", repo, pr.Number, headSHA)
			return "enqueued_noop", nil
		}
		return "failed", fmt.Errorf("failed to queue job: %w", err)
	}

	log.Printf("Enqueued job %s for PR %s#%d (head_sha=%s, assignee=%s)",
		job.ID, repo, pr.Number, headSHA, chosenLogin)

	return "enqueued", nil
}

// handleIssueCommentEvent handles PR comment mentions (Trigger B).
func (e *Enqueuer) handleIssueCommentEvent(ctx context.Context, payload *webhook.WebhookPayload) (string, error) {
	if payload.Installation == nil || payload.Repository == nil || payload.Issue == nil || payload.Comment == nil {
		return "ignored", errors.New("malformed issue_comment payload")
	}

	// Only process comments on pull requests
	if payload.Issue.PullRequest == nil {
		return "ignored", nil
	}

	// 1. Ensure installation exists in database
	inst, err := e.ensureInstallation(ctx, payload.Installation.ID, payload)
	if err != nil {
		return "failed", fmt.Errorf("failed to ensure installation: %w", err)
	}

	// 1.5 Skip if installation is suspended
	if inst.SuspendedAt != nil {
		log.Printf("Skipping Issue Comment event for suspended installation: %d", inst.GitHubInstallationID)
		return "skipped_suspended", nil
	}

	repo := payload.Repository.FullName
	prNumber := payload.Issue.Number

	// 2. Resolve reviewer from mention body
	chosenLogin, matched := e.matchReviewer(nil, payload.Comment.Body)
	if !matched {
		return "skipped", nil
	}

	// 3. Skip with log if head_sha is missing from the event payload (T2 requirement)
	// issue_comment events do not contain the git head_sha of the PR.
	// We log "mention_without_sha" and skip calling GitHub API.
	log.Printf("[mention_without_sha] PR %s#%d mentioned %s but head_sha is missing from webhook payload",
		repo, prNumber, chosenLogin)

	return "skipped_no_sha", nil
}

// ensureInstallation makes sure that the installation record is present in the database.
// Creates a stub if it is missing.
func (e *Enqueuer) ensureInstallation(ctx context.Context, githubInstID int64, payload *webhook.WebhookPayload) (*domain.Installation, error) {
	inst, err := e.store.GetInstallationByGitHubID(ctx, githubInstID)
	if err != nil {
		return nil, err
	}

	if inst != nil {
		return inst, nil
	}

	// Create stub installation (default login and type, will be updated by installation event)
	stub := &domain.Installation{
		GitHubInstallationID: githubInstID,
		AccountLogin:         "unknown-stub",
		AccountType:          domain.AccountTypeOrganization,
	}

	// If payload contains account login, use it
	if payload.Installation != nil && payload.Installation.Account.Login != "" {
		stub.AccountLogin = payload.Installation.Account.Login
		stub.AccountType = domain.AccountType(payload.Installation.Account.Type)
	}

	err = e.store.UpsertInstallation(ctx, stub)
	if err != nil {
		return nil, err
	}

	return stub, nil
}

// matchReviewer matches the PR assignees or comment body against the prioritized ReviewerLogins whitelist.
// Returns the matched login and true if found, empty string and false otherwise.
func (e *Enqueuer) matchReviewer(assignees []string, commentBody string) (string, bool) {
	// ReviewerLogins order defines priority
	for _, whitelistLogin := range e.logins {
		lowerWhitelist := strings.ToLower(whitelistLogin)

		// Check Assignees (Trigger A)
		for _, assignee := range assignees {
			if strings.ToLower(assignee) == lowerWhitelist {
				return whitelistLogin, true
			}
		}

		// Check Mentions in Comment (Trigger B)
		if commentBody != "" {
			// Search for @login case-insensitive, matching word boundaries
			pattern := `(?i)(^|[^A-Za-z0-9_-])@` + regexp.QuoteMeta(whitelistLogin) + `([^A-Za-z0-9_-]|$)`
			re, err := regexp.Compile(pattern)
			if err == nil && re.MatchString(commentBody) {
				return whitelistLogin, true
			}
		}
	}

	return "", false
}
