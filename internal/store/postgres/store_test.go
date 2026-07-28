package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"testing"
	"time"

	"agent-frei-igi/internal/domain"

	"github.com/google/uuid"
)

// TestPostgresStore_Integration runs end-to-end integration tests on the postgres store.
// It is skipped if the DATABASE_URL environment variable is not defined.
func TestPostgresStore_Integration(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL is not set, skipping integration test")
	}

	ctx := context.Background()

	// Initialize store
	store, err := New(dbURL)
	if err != nil {
		t.Fatalf("failed to initialize store: %v", err)
	}
	defer store.Close()

	// 1. Run migrations
	err = store.Migrate(ctx)
	if err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	// Generate random IDs to prevent collision between test runs
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	githubInstID := int64(rng.Intn(10000000) + 1000)
	githubLogin := fmt.Sprintf("test-reviewer-%d", rng.Intn(100000))

	// 2. Test UpsertInstallation
	inst := &domain.Installation{
		GitHubInstallationID: githubInstID,
		AccountLogin:         "test-org",
		AccountType:          domain.AccountTypeOrganization,
	}

	err = store.UpsertInstallation(ctx, inst)
	if err != nil {
		t.Fatalf("UpsertInstallation failed: %v", err)
	}

	if inst.ID == 0 {
		t.Error("expected installation ID to be populated by RETURNING, got 0")
	}

	// Update installation
	inst.AccountLogin = "test-org-updated"
	err = store.UpsertInstallation(ctx, inst)
	if err != nil {
		t.Fatalf("second UpsertInstallation failed: %v", err)
	}

	// Read back and verify update in DB
	updatedInst, err := store.GetInstallation(ctx, inst.ID)
	if err != nil {
		t.Fatalf("GetInstallation failed: %v", err)
	}
	if updatedInst == nil {
		t.Fatal("expected installation to be found after upsert update")
	}
	if updatedInst.AccountLogin != "test-org-updated" {
		t.Errorf("expected account login in DB to be test-org-updated, got %s", updatedInst.AccountLogin)
	}

	// 3. Test CreateAccount
	account := &domain.GitHubAccount{
		GitHubLogin:    githubLogin,
		AccessTokenEnc: []byte("encrypted-token-data"),
		Status:         domain.AccountStatusActive,
	}

	err = store.CreateAccount(ctx, account)
	if err != nil {
		t.Fatalf("CreateAccount failed: %v", err)
	}
	if account.ID == 0 {
		t.Error("expected account ID to be populated")
	}

	// Test GetAccountByLogin
	retrievedAcc, err := store.GetAccountByLogin(ctx, githubLogin)
	if err != nil {
		t.Fatalf("GetAccountByLogin failed: %v", err)
	}
	if retrievedAcc == nil {
		t.Fatal("expected account to be found")
	}
	if retrievedAcc.GitHubLogin != githubLogin {
		t.Errorf("expected login %s, got %s", githubLogin, retrievedAcc.GitHubLogin)
	}

	// 4. Test CreateJob
	job := &domain.ReviewJob{
		InstallationID: inst.ID,
		RepoFullName:   "test-org-updated/test-repo",
		PRNumber:       42,
		HeadSHA:        "abcdef0123456789",
		BaseSHA:        "1234567890abcdef",
		Status:         domain.JobStatusPending,
		ContextBlob:    json.RawMessage(`{"files": ["main.go"]}`),
		ResultBlob:     json.RawMessage(`{"findings": []}`),
	}

	err = store.CreateJob(ctx, job)
	if err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}

	if job.ID == uuid.Nil {
		t.Error("expected job ID to be populated with generated UUID")
	}

	// Test GetJob
	retrievedJob, err := store.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob failed: %v", err)
	}
	if retrievedJob == nil {
		t.Fatal("expected job to be found")
	}
	if retrievedJob.RepoFullName != job.RepoFullName {
		t.Errorf("expected repo %s, got %s", job.RepoFullName, retrievedJob.RepoFullName)
	}
	if string(retrievedJob.ContextBlob) != string(job.ContextBlob) {
		t.Errorf("expected context %s, got %s", string(job.ContextBlob), string(retrievedJob.ContextBlob))
	}

	// 5. Test CreateJob Idempotency / Duplicate constraint violation
	duplicateJob := &domain.ReviewJob{
		InstallationID: inst.ID,
		RepoFullName:   "test-org-updated/test-repo",
		PRNumber:       42,
		HeadSHA:        "abcdef0123456789", // Same key fields
		Status:         domain.JobStatusPending,
	}

	err = store.CreateJob(ctx, duplicateJob)
	if !errors.Is(err, ErrJobAlreadyExists) {
		t.Errorf("expected ErrJobAlreadyExists, got %v", err)
	}

	// 6. Test GetJob with non-existing ID
	nonExistingID := uuid.New()
	missingJob, err := store.GetJob(ctx, nonExistingID)
	if err != nil {
		t.Fatalf("GetJob error for missing job: %v", err)
	}
	if missingJob != nil {
		t.Errorf("expected nil for non-existing job, got %+v", missingJob)
	}
}
