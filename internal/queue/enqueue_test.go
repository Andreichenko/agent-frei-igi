package queue

import (
	"context"
	"testing"
	"time"

	"agent-frei-igi/internal/domain"
	"agent-frei-igi/internal/githubapp/webhook"
)

// TestEnqueuer_MatchReviewer verifies the whitelist matching logic for assignees and comment body mentions.
func TestEnqueuer_MatchReviewer(t *testing.T) {
	logins := []string{"alice", "bob", "carol"}
	e := NewEnqueuer(nil, logins)

	// 1. Match on assignee (Trigger A)
	matched, ok := e.matchReviewer([]string{"eve", "BOB"}, "")
	if !ok || matched != "bob" {
		t.Errorf("expected to match bob, got %q, %v", matched, ok)
	}

	// 2. Match on mention in comment (Trigger B)
	matched, ok = e.matchReviewer(nil, "Please check this PR @Carol")
	if !ok || matched != "carol" {
		t.Errorf("expected to match carol, got %q, %v", matched, ok)
	}

	// 3. No match
	matched, ok = e.matchReviewer([]string{"eve"}, "Hello world @frank")
	if ok || matched != "" {
		t.Errorf("expected no match, got %q, %v", matched, ok)
	}

	// 4. Word boundary checks
	// 4.1 Substring: @alicebob should NOT match alice
	matched, ok = e.matchReviewer(nil, "hey @alicebob")
	if ok {
		t.Errorf("expected no match for substring @alicebob, got %q, %v", matched, ok)
	}

	// 4.2 Surrounding non-word chars: (@alice) should match alice
	matched, ok = e.matchReviewer(nil, "hey (@alice)")
	if !ok || matched != "alice" {
		t.Errorf("expected to match alice in bracket, got %q, %v", matched, ok)
	}

	// 4.3 Match at start/end of line
	matched, ok = e.matchReviewer(nil, "@alice")
	if !ok || matched != "alice" {
		t.Errorf("expected to match alice at start/end of line, got %q, %v", matched, ok)
	}

	// 5. Priority check (alice should match first even if bob is present)
	matched, ok = e.matchReviewer([]string{"bob", "alice"}, "Hey @bob @alice")
	if !ok || matched != "alice" {
		t.Errorf("expected to match alice first due to config order, got %q, %v", matched, ok)
	}
}

type mockEnqueuerStore struct {
	installations map[int64]*domain.Installation
	jobs          []*domain.ReviewJob
	accounts      map[string]*domain.GitHubAccount
}

func newMockEnqueuerStore() *mockEnqueuerStore {
	return &mockEnqueuerStore{
		installations: make(map[int64]*domain.Installation),
		accounts:      make(map[string]*domain.GitHubAccount),
	}
}

func (m *mockEnqueuerStore) UpsertInstallation(ctx context.Context, inst *domain.Installation) error {
	m.installations[inst.GitHubInstallationID] = inst
	return nil
}

func (m *mockEnqueuerStore) GetInstallationByGitHubID(ctx context.Context, githubInstID int64) (*domain.Installation, error) {
	inst, ok := m.installations[githubInstID]
	if !ok {
		return nil, nil
	}
	return inst, nil
}

func (m *mockEnqueuerStore) CancelOpenJobsForPR(ctx context.Context, instID int64, repo string, prNumber int) (int64, error) {
	return 0, nil
}

func (m *mockEnqueuerStore) CancelJobsForPR(ctx context.Context, instID int64, repo string, prNumber int, headSHA string) (int64, error) {
	return 0, nil
}

func (m *mockEnqueuerStore) GetAccountByLogin(ctx context.Context, login string) (*domain.GitHubAccount, error) {
	return m.accounts[login], nil
}

func (m *mockEnqueuerStore) CreateJob(ctx context.Context, job *domain.ReviewJob) error {
	m.jobs = append(m.jobs, job)
	return nil
}

func TestEnqueuer_InstallationLifecycle(t *testing.T) {
	store := newMockEnqueuerStore()
	e := NewEnqueuer(store, []string{"alice"})

	ctx := context.Background()

	// 1. Created action
	payload := &webhook.WebhookPayload{
		Action: "created",
		Installation: &webhook.GitHubInstallationPayload{
			ID: 100,
			Account: webhook.GitHubAccountPayload{
				Login: "test-org",
				Type:  "Organization",
			},
		},
	}

	decision, err := e.HandleEvent(ctx, "installation", payload)
	if err != nil || decision != "processed" {
		t.Fatalf("expected processed, got %q, %v", decision, err)
	}

	inst := store.installations[100]
	if inst == nil || inst.SuspendedAt != nil {
		t.Errorf("expected installation to be saved and active, got %+v", inst)
	}

	// 2. Suspend action
	payload.Action = "suspend"
	decision, err = e.HandleEvent(ctx, "installation", payload)
	if err != nil || decision != "processed" {
		t.Fatalf("expected processed, got %q, %v", decision, err)
	}
	inst = store.installations[100]
	if inst.SuspendedAt == nil {
		t.Error("expected SuspendedAt to be set after suspend")
	}

	// 3. Unsuspend action
	payload.Action = "unsuspend"
	decision, err = e.HandleEvent(ctx, "installation", payload)
	if err != nil || decision != "processed" {
		t.Fatalf("expected processed, got %q, %v", decision, err)
	}
	inst = store.installations[100]
	if inst.SuspendedAt != nil {
		t.Error("expected SuspendedAt to be nil after unsuspend")
	}

	// 4. Deleted action
	payload.Action = "deleted"
	decision, err = e.HandleEvent(ctx, "installation", payload)
	if err != nil || decision != "processed" {
		t.Fatalf("expected processed, got %q, %v", decision, err)
	}
	inst = store.installations[100]
	if inst.SuspendedAt == nil {
		t.Error("expected SuspendedAt to be set after delete")
	}
}

func TestEnqueuer_SuspendedSkip(t *testing.T) {
	store := newMockEnqueuerStore()
	e := NewEnqueuer(store, []string{"alice"})

	// Mark installation 100 as suspended
	now := time.Now()
	store.installations[100] = &domain.Installation{
		ID:                   1,
		GitHubInstallationID: 100,
		AccountLogin:         "test-org",
		AccountType:          domain.AccountTypeOrganization,
		SuspendedAt:          &now,
	}

	ctx := context.Background()

	// Try PR event
	payload := &webhook.WebhookPayload{
		Action: "opened",
		Installation: &webhook.GitHubInstallationPayload{
			ID: 100,
		},
		Repository: &webhook.GitHubRepositoryPayload{
			FullName: "owner/repo",
		},
		PullRequest: &webhook.GitHubPRPayload{
			Number: 42,
			Head:   webhook.GitHubPRHeadBasePayload{SHA: "abc"},
			Base:   webhook.GitHubPRHeadBasePayload{SHA: "def"},
			Assignees: []webhook.GitHubAssigneePayload{
				{Login: "alice"},
			},
		},
	}

	decision, err := e.HandleEvent(ctx, "pull_request", payload)
	if err != nil {
		t.Fatal(err)
	}
	if decision != "skipped_suspended" {
		t.Errorf("expected skipped_suspended, got %q", decision)
	}
	if len(store.jobs) != 0 {
		t.Error("job was enqueued for suspended installation")
	}
}

func TestEnqueuer_PRActionAllowlist(t *testing.T) {
	store := newMockEnqueuerStore()
	e := NewEnqueuer(store, []string{"alice"})

	// Setup active installation
	store.installations[100] = &domain.Installation{
		ID:                   1,
		GitHubInstallationID: 100,
		AccountLogin:         "test-org",
		AccountType:          domain.AccountTypeOrganization,
	}

	ctx := context.Background()

	payload := &webhook.WebhookPayload{
		Action: "edited", // Not in allowlist
		Installation: &webhook.GitHubInstallationPayload{
			ID: 100,
		},
		Repository: &webhook.GitHubRepositoryPayload{
			FullName: "owner/repo",
		},
		PullRequest: &webhook.GitHubPRPayload{
			Number: 42,
			Head:   webhook.GitHubPRHeadBasePayload{SHA: "abc"},
			Base:   webhook.GitHubPRHeadBasePayload{SHA: "def"},
			Assignees: []webhook.GitHubAssigneePayload{
				{Login: "alice"},
			},
		},
	}

	// 1. Action edited (ignored)
	decision, err := e.HandleEvent(ctx, "pull_request", payload)
	if err != nil {
		t.Fatal(err)
	}
	if decision != "ignored" {
		t.Errorf("expected ignored for edited action, got %q", decision)
	}

	// 2. Action opened (allowed)
	payload.Action = "opened"
	decision, err = e.HandleEvent(ctx, "pull_request", payload)
	if err != nil {
		t.Fatal(err)
	}
	if decision != "enqueued" {
		t.Errorf("expected enqueued for opened action, got %q", decision)
	}

	// 3. Action closed (allowed, cancels jobs)
	payload.Action = "closed"
	decision, err = e.HandleEvent(ctx, "pull_request", payload)
	if err != nil {
		t.Fatal(err)
	}
	if decision != "cancelled" {
		t.Errorf("expected cancelled for closed action, got %q", decision)
	}
}

// TestEnqueuer_SuspendedInstall_ClosedPR_Cancels verifies that when an installation
// is suspended and a PR is closed, open jobs are still cancelled (not skipped_suspended).
func TestEnqueuer_SuspendedInstall_ClosedPR_Cancels(t *testing.T) {
	store := newMockEnqueuerStore()
	e := NewEnqueuer(store, []string{"alice"})

	// Mark installation 100 as suspended
	now := time.Now()
	store.installations[100] = &domain.Installation{
		ID:                   1,
		GitHubInstallationID: 100,
		AccountLogin:         "test-org",
		AccountType:          domain.AccountTypeOrganization,
		SuspendedAt:          &now,
	}

	ctx := context.Background()

	payload := &webhook.WebhookPayload{
		Action: "closed",
		Installation: &webhook.GitHubInstallationPayload{
			ID: 100,
			Account: webhook.GitHubAccountPayload{
				Login: "test-org",
				Type:  "Organization",
			},
		},
		Repository: &webhook.GitHubRepositoryPayload{
			FullName: "owner/repo",
		},
		PullRequest: &webhook.GitHubPRPayload{
			Number: 42,
			State:  "closed",
			Head:   webhook.GitHubPRHeadBasePayload{SHA: "abc"},
			Base:   webhook.GitHubPRHeadBasePayload{SHA: "def"},
		},
	}

	decision, err := e.HandleEvent(ctx, "pull_request", payload)
	if err != nil {
		t.Fatal(err)
	}
	// Must be "cancelled", not "skipped_suspended" — cancellation takes priority
	if decision != "cancelled" {
		t.Errorf("expected cancelled for closed PR on suspended installation, got %q", decision)
	}
}
