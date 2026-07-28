package diplomat

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"agent-frei-igi/internal/config"
	"agent-frei-igi/internal/crypto"
	"agent-frei-igi/internal/critic"
	"agent-frei-igi/internal/domain"
	"agent-frei-igi/internal/githubapp"
	"agent-frei-igi/internal/store/postgres"

	"github.com/google/uuid"
)

type mockStore struct {
	mu           sync.Mutex
	accounts     map[string]*domain.GitHubAccount
	publications map[uuid.UUID]*domain.ReviewPublication
}

func newMockStore() *mockStore {
	return &mockStore{
		accounts:     make(map[string]*domain.GitHubAccount),
		publications: make(map[uuid.UUID]*domain.ReviewPublication),
	}
}

func (m *mockStore) GetAccountByLogin(ctx context.Context, login string) (*domain.GitHubAccount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	acc, ok := m.accounts[login]
	if !ok {
		return nil, nil
	}
	return acc, nil
}

func (m *mockStore) GetInstallation(ctx context.Context, id int64) (*domain.Installation, error) {
	return &domain.Installation{
		ID:                     id,
		GitHubInstallationID: 123456,
		AccountLogin:           "test-org",
		AccountType:            "Organization",
	}, nil
}

func (m *mockStore) BeginPublication(ctx context.Context, jobID uuid.UUID, publisherAccountID *int64, kind string) (*domain.ReviewPublication, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.publications[jobID]; ok {
		return nil, postgres.ErrPublicationExists
	}

	pub := &domain.ReviewPublication{
		ID:                 999,
		JobID:              jobID,
		PublisherAccountID: publisherAccountID,
		PublisherKind:      domain.PublisherKind(kind),
		State:              domain.PublicationStatePending,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}
	m.publications[jobID] = pub
	return pub, nil
}

func (m *mockStore) FinalizePublication(ctx context.Context, jobID uuid.UUID, githubReviewID *int64, dryRun bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	pub, ok := m.publications[jobID]
	if !ok {
		return errors.New("publication not found")
	}
	pub.State = domain.PublicationStatePublished
	pub.GitHubReviewID = githubReviewID
	now := time.Now()
	pub.PublishedAt = &now
	pub.UpdatedAt = now
	return nil
}

func (m *mockStore) FailPublication(ctx context.Context, jobID uuid.UUID, errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	pub, ok := m.publications[jobID]
	if !ok {
		return errors.New("publication not found")
	}
	pub.State = domain.PublicationStateFailed
	pub.UpdatedAt = time.Now()
	return nil
}

func (m *mockStore) GetPublication(ctx context.Context, jobID uuid.UUID) (*domain.ReviewPublication, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pub, ok := m.publications[jobID]
	if !ok {
		return nil, nil
	}
	return pub, nil
}

type mockGHClient struct {
	mu           sync.Mutex
	requests     []githubapp.ReviewRequest
	returnID     int64
	returnErr    error
	calledTokens []string
}

func (m *mockGHClient) CreatePullReview(ctx context.Context, token, owner, repo string, prNumber int, req githubapp.ReviewRequest) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, req)
	m.calledTokens = append(m.calledTokens, token)
	return m.returnID, m.returnErr
}

func TestMapResultToRequest_Formatting(t *testing.T) {
	res := &critic.Result{
		ModelUsed: "agy",
		Verdict:   "Verdict statement",
		Summary:   "Summary statement",
		Findings: []critic.Finding{
			{
				Path:       "main.go",
				StartLine:  10,
				EndLine:    12,
				Severity:   "warning",
				Title:      "Warning Title",
				Body:       "Warning body description",
				Confidence: 0.85,
			},
			{
				Path:       "helper.go",
				StartLine:  20,
				EndLine:    0,
				Severity:   "error",
				Title:      "Error Title",
				Body:       "Error body description",
				Confidence: 0.95,
			},
			{
				Path:       "config.json",
				StartLine:  0,
				EndLine:    0,
				Severity:   "info",
				Title:      "Info Title",
				Body:       "Info body description",
				Confidence: 0.70,
			},
		},
	}

	// 1. Signature Enabled
	req := MapResultToRequest(res, "headsha123", true, "abcdef12")

	if req.CommitID != "headsha123" {
		t.Errorf("expected commit_id headsha123, got %s", req.CommitID)
	}
	if req.Event != "COMMENT" {
		t.Errorf("expected event COMMENT, got %s", req.Event)
	}

	// Two inline comments: L10-12 in main.go, L20-20 in helper.go
	if len(req.Comments) != 2 {
		t.Fatalf("expected 2 inline comments, got %d", len(req.Comments))
	}

	c1 := req.Comments[0]
	if c1.Path != "main.go" || c1.Line != 12 || c1.Side != "RIGHT" {
		t.Errorf("unexpected comment 1 layout: %+v", c1)
	}
	if !strings.Contains(c1.Body, "**Warning Title**") || strings.Contains(c1.Body, "confidence") {
		t.Errorf("unexpected comment 1 body: %s", c1.Body)
	}

	c2 := req.Comments[1]
	if c2.Path != "helper.go" || c2.Line != 20 {
		t.Errorf("unexpected comment 2 layout: %+v", c2)
	}
	// Prefix [Error] must be added
	if !strings.Contains(c2.Body, "**[Error] Error Title**") {
		t.Errorf("unexpected comment 2 body: %s", c2.Body)
	}

	// One file-level note should go to body
	if !strings.Contains(req.Body, "Summary statement") {
		t.Errorf("body missing summary: %s", req.Body)
	}
	if !strings.Contains(req.Body, "config.json") || !strings.Contains(req.Body, "Info Title") {
		t.Errorf("body missing file-level note: %s", req.Body)
	}

	// Signature footer format
	expectedSig := "via agent-frei · model agy · job abcdef12"
	if !strings.Contains(req.Body, expectedSig) {
		t.Errorf("body missing expected signature: %s", req.Body)
	}

	// 2. Signature Disabled
	reqNoSig := MapResultToRequest(res, "headsha123", false, "abcdef12")
	if strings.Contains(reqNoSig.Body, expectedSig) || strings.Contains(reqNoSig.Body, "via agent-frei") {
		t.Errorf("body should not contain signature: %s", reqNoSig.Body)
	}
}

func TestMapResultToRequest_OverflowLimit(t *testing.T) {
	// Create 35 findings to trigger the 30 comments limit
	var findings []critic.Finding
	for i := 1; i <= 35; i++ {
		findings = append(findings, critic.Finding{
			Path:      "main.go",
			StartLine: i,
			EndLine:   i,
			Severity:  "warning",
			Title:     "Warning",
			Body:      "Details",
		})
	}

	res := &critic.Result{
		Findings: findings,
	}

	req := MapResultToRequest(res, "sha", false, "job")
	if len(req.Comments) != 30 {
		t.Errorf("expected exactly 30 inline comments, got %d", len(req.Comments))
	}

	// 5 overflow findings must be placed in the body
	if !strings.Contains(req.Body, "Additional Findings:") {
		t.Errorf("expected additional findings section, body is: %s", req.Body)
	}
	if !strings.Contains(req.Body, "main.go#L31") || !strings.Contains(req.Body, "main.go#L35") {
		t.Errorf("body missing overflow inline comments: %s", req.Body)
	}
}

func TestPublish_DryRun(t *testing.T) {
	cfg := &config.Config{
		FFPublishComments: false,
	}
	store := newMockStore()
	gh := &mockGHClient{}
	pub := New(cfg, store, gh)

	job := &domain.ReviewJob{
		ID:           uuid.New(),
		RepoFullName: "owner/repo",
	}

	err := pub.Publish(context.Background(), job, &critic.Result{})
	if err != nil {
		t.Fatalf("unexpected dry-run error: %v", err)
	}

	if len(gh.requests) != 0 {
		t.Error("dry-run should not perform any HTTP requests")
	}

	p, err := store.GetPublication(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("failed to retrieve publication: %v", err)
	}

	if p == nil || p.State != domain.PublicationStatePublished || p.GitHubReviewID != nil {
		t.Errorf("unexpected publication state: %+v", p)
	}
}

func TestPublish_Success(t *testing.T) {
	tokenKeyStr := "dGVzdC1rZXktMzItYnl0ZXMtZW5jcnlwdGlvbi1rZXk=" // Exactly 32 raw bytes b64
	cfg := &config.Config{
		FFPublishComments:  true,
		TokenEncryptionKey: tokenKeyStr,
		FFReviewSignature:  true,
	}

	rawKey, _ := crypto.ParseKey(tokenKeyStr)
	plainToken := "test_secure_github_user_personal_token"
	encToken, err := crypto.Encrypt(rawKey, []byte(plainToken))
	if err != nil {
		t.Fatalf("failed to encrypt test token: %v", err)
	}

	store := newMockStore()
	loginName := "alice"
	store.accounts[loginName] = &domain.GitHubAccount{
		ID:             42,
		GitHubLogin:    loginName,
		AccessTokenEnc: encToken,
		Status:         "active",
	}

	gh := &mockGHClient{
		returnID: 777,
	}
	pub := New(cfg, store, gh)

	job := &domain.ReviewJob{
		ID:             uuid.New(),
		RepoFullName:   "owner/repo",
		PublisherLogin: &loginName,
		HeadSHA:        "commit123",
	}

	criticRes := &critic.Result{
		ModelUsed: "agy",
		Verdict:   "Verdict summary",
	}

	err = pub.Publish(context.Background(), job, criticRes)
	if err != nil {
		t.Fatalf("failed to publish review: %v", err)
	}

	if len(gh.requests) != 1 {
		t.Fatalf("expected 1 HTTP review creation request, got %d", len(gh.requests))
	}

	req := gh.requests[0]
	if req.CommitID != "commit123" {
		t.Errorf("expected commit_id commit123, got %s", req.CommitID)
	}

	if len(gh.calledTokens) != 1 || gh.calledTokens[0] != plainToken {
		t.Errorf("expected call with decrypted token %q, got %q", plainToken, gh.calledTokens)
	}

	p, err := store.GetPublication(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("failed to retrieve publication: %v", err)
	}

	if p == nil || p.State != domain.PublicationStatePublished || p.GitHubReviewID == nil || *p.GitHubReviewID != 777 {
		t.Errorf("unexpected publication state: %+v", p)
	}
}

func TestPublish_IdempotencyFencing(t *testing.T) {
	tokenKeyStr := "dGVzdC1rZXktMzItYnl0ZXMtZW5jcnlwdGlvbi1rZXk="
	cfg := &config.Config{
		FFPublishComments:  true,
		TokenEncryptionKey: tokenKeyStr,
	}

	rawKey, _ := crypto.ParseKey(tokenKeyStr)
	encToken, err := crypto.Encrypt(rawKey, []byte("test_some_token"))
	if err != nil {
		t.Fatalf("failed to encrypt test token: %v", err)
	}

	store := newMockStore()
	loginName := "alice"
	store.accounts[loginName] = &domain.GitHubAccount{
		ID:             42,
		GitHubLogin:    loginName,
		AccessTokenEnc: encToken,
		Status:         "active",
	}

	jobID := uuid.New()

	// Pretend job has already been marked as published in DB
	store.publications[jobID] = &domain.ReviewPublication{
		JobID:          jobID,
		State:          domain.PublicationStatePublished,
		GitHubReviewID: ptrInt64(888),
	}

	gh := &mockGHClient{}
	pub := New(cfg, store, gh)

	job := &domain.ReviewJob{
		ID:             jobID,
		RepoFullName:   "owner/repo",
		PublisherLogin: &loginName,
	}

	err = pub.Publish(context.Background(), job, &critic.Result{})
	if err != nil {
		t.Fatalf("expected idempotency nil return, got: %v", err)
	}

	if len(gh.requests) != 0 {
		t.Error("fencing failed: double POST was attempted")
	}
}

func ptrInt64(v int64) *int64 {
	return &v
}
