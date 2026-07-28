package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"testing"
	"time"

	"agent-frei-igi/internal/domain"
	"agent-frei-igi/internal/githubapp/webhook"
	"agent-frei-igi/internal/queue"
	"agent-frei-igi/internal/store/postgres"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// loadFixture reads a JSON webhook payload file from testdata.
func loadFixture(t *testing.T, filepath string) *webhook.WebhookPayload {
	t.Helper()
	data, err := os.ReadFile(filepath)
	if err != nil {
		t.Fatalf("failed to read fixture %s: %v", filepath, err)
	}

	var payload webhook.WebhookPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("failed to parse fixture %s: %v", filepath, err)
	}

	return &payload
}

// TestStore_Integration_EnqueueWebhook runs the end-to-end integration test
// matching webhook events against Database and Enqueuer logic.
func TestStore_Integration_EnqueueWebhook(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL is not set, skipping end-to-end integration test")
	}

	ctx := context.Background()

	// Initialize store and migrate (applies both 0001 and 0002)
	store, err := postgres.New(dbURL)
	if err != nil {
		t.Fatalf("failed to connect to DB: %v", err)
	}
	defer store.Close()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	// Whitelist review logins
	enqueuer := queue.NewEnqueuer(store, []string{"alice", "bob"})

	// Setup unique installation ID for this test run to isolate from other tests
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	githubInstID := int64(rng.Intn(1000000) + 100000)
	repoName := fmt.Sprintf("test-org-%d/test-repo", rng.Intn(100000))
	prNumber := rng.Intn(1000) + 1

	// Setup whitelisted assignee opened PR payload
	whitelistedPayload := loadFixture(t, "../../../testdata/github/pull_request_opened_assignee_whitelisted.json")
	whitelistedPayload.Installation.ID = githubInstID
	whitelistedPayload.Repository.FullName = repoName
	whitelistedPayload.PullRequest.Number = prNumber

	// 1. opened PR by whitelisted assignee -> should enqueue
	decision, err := enqueuer.HandleEvent(ctx, "pull_request", whitelistedPayload)
	if err != nil {
		t.Fatalf("failed to handle event: %v", err)
	}
	if decision != "enqueued" {
		t.Errorf("expected enqueued, got %s", decision)
	}

	// Verify installation and job are created in DB
	inst, err := store.GetInstallationByGitHubID(ctx, githubInstID)
	if err != nil {
		t.Fatal(err)
	}
	if inst == nil {
		t.Fatal("expected installation record to be created in DB")
	}

	// 2. Test duplicate delivery -> should return enqueued_noop (idempotency)
	decision, err = enqueuer.HandleEvent(ctx, "pull_request", whitelistedPayload)
	if err != nil {
		t.Fatal(err)
	}
	if decision != "enqueued_noop" {
		t.Errorf("expected enqueued_noop, got %s", decision)
	}

	// 3. Test non-whitelisted assignee -> should return skipped
	nonWhitelistedPayload := loadFixture(t, "../../../testdata/github/pull_request_opened_assignee_non_whitelisted.json")
	nonWhitelistedPayload.Installation.ID = githubInstID
	nonWhitelistedPayload.Repository.FullName = repoName
	nonWhitelistedPayload.PullRequest.Number = prNumber + 1 // New PR number

	decision, err = enqueuer.HandleEvent(ctx, "pull_request", nonWhitelistedPayload)
	if err != nil {
		t.Fatal(err)
	}
	if decision != "skipped" {
		t.Errorf("expected skipped, got %s", decision)
	}

	// 4. Test synchronize PR with new SHA -> should cancel the old job and enqueue a new one (supersede)
	syncPayload := loadFixture(t, "../../../testdata/github/pull_request_synchronize.json")
	syncPayload.Installation.ID = githubInstID
	syncPayload.Repository.FullName = repoName
	syncPayload.PullRequest.Number = prNumber // Same PR number
	syncPayload.PullRequest.Head.SHA = "headsha333333333333333333333333333333" // New SHA

	decision, err = enqueuer.HandleEvent(ctx, "pull_request", syncPayload)
	if err != nil {
		t.Fatal(err)
	}
	if decision != "enqueued" {
		t.Errorf("expected enqueued, got %s", decision)
	}

	// Verify old job status is 'cancelled'
	oldJobKey := &domain.ReviewJob{
		InstallationID: inst.ID,
		RepoFullName:   repoName,
		PRNumber:       prNumber,
		HeadSHA:        "headsha111111111111111111111111111111", // Old SHA
	}
	var oldStatus string
	err = store.DB().QueryRowContext(ctx, "SELECT status FROM review_jobs WHERE installation_id = $1 AND repo_full_name = $2 AND pr_number = $3 AND head_sha = $4",
		oldJobKey.InstallationID, oldJobKey.RepoFullName, oldJobKey.PRNumber, oldJobKey.HeadSHA).Scan(&oldStatus)
	if err != nil {
		t.Fatalf("failed to query old job status: %v", err)
	}
	if oldStatus != string(domain.JobStatusCancelled) {
		t.Errorf("expected old job status to be cancelled, got %s", oldStatus)
	}

	// Verify new job is in 'pending' status
	var newStatus string
	var pubLogin *string
	err = store.DB().QueryRowContext(ctx, "SELECT status, publisher_login FROM review_jobs WHERE installation_id = $1 AND repo_full_name = $2 AND pr_number = $3 AND head_sha = $4",
		inst.ID, repoName, prNumber, syncPayload.PullRequest.Head.SHA).Scan(&newStatus, &pubLogin)
	if err != nil {
		t.Fatalf("failed to query new job status: %v", err)
	}
	if newStatus != string(domain.JobStatusPending) {
		t.Errorf("expected new job status to be pending, got %s", newStatus)
	}
	if pubLogin == nil || *pubLogin != "alice" {
		t.Errorf("expected publisher_login to be alice, got %v", pubLogin)
	}

	// 5. Test issue comment mention -> should return skipped_no_sha
	commentPayload := loadFixture(t, "../../../testdata/github/issue_comment_mention.json")
	commentPayload.Installation.ID = githubInstID
	commentPayload.Repository.FullName = repoName
	commentPayload.Issue.Number = prNumber

	decision, err = enqueuer.HandleEvent(ctx, "issue_comment", commentPayload)
	if err != nil {
		t.Fatal(err)
	}
	if decision != "skipped_no_sha" {
		t.Errorf("expected skipped_no_sha, got %s", decision)
	}
}

// TestStore_Integration_WorkerLifeCycle verifies Claim, Heartbeat, Complete, Reclaim, and Cancel states.
func TestStore_Integration_WorkerLifeCycle(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL is not set, skipping worker integration test")
	}

	ctx := context.Background()

	store, err := postgres.New(dbURL)
	if err != nil {
		t.Fatalf("failed to connect to DB: %v", err)
	}
	defer store.Close()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	// 1. Setup a pending job manually
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	instID := int64(rng.Intn(1000000) + 200000)
	repo := fmt.Sprintf("test-worker-org/repo-%d", rng.Intn(100000))
	prNum := rng.Intn(1000) + 1

	// Insert installation stub first
	inst := &domain.Installation{
		GitHubInstallationID: instID,
		AccountLogin:         "test-worker-org",
		AccountType:          domain.AccountTypeOrganization,
	}
	if err := store.UpsertInstallation(ctx, inst); err != nil {
		t.Fatal(err)
	}

	// Insert job
	job := &domain.ReviewJob{
		InstallationID: inst.ID,
		RepoFullName:   repo,
		PRNumber:       prNum,
		HeadSHA:        "sha-initial-111111111111111111111111",
		Status:         domain.JobStatusPending,
	}
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatal(err)
	}

	workerA := "worker-A"
	workerB := "worker-B"
	lease := 10 * time.Second

	// 2. ClaimJob with workerA -> check fields
	claimedJob, err := store.ClaimJob(ctx, workerA, lease)
	if err != nil {
		t.Fatalf("failed to claim job: %v", err)
	}
	if claimedJob == nil {
		t.Fatal("expected to claim the pending job, got nil")
	}
	if claimedJob.Status != domain.JobStatusRunning {
		t.Errorf("expected status to be running, got %s", claimedJob.Status)
	}
	if claimedJob.LockedBy == nil || *claimedJob.LockedBy != workerA {
		t.Errorf("expected locked_by to be %s, got %v", workerA, claimedJob.LockedBy)
	}
	if claimedJob.LockGeneration != 1 {
		t.Errorf("expected lock_generation to be 1, got %d", claimedJob.LockGeneration)
	}

	// 3. Second claim -> must return nil (locked)
	secondClaim, err := store.ClaimJob(ctx, workerB, lease)
	if err != nil {
		t.Fatal(err)
	}
	if secondClaim != nil {
		t.Errorf("expected second claim on locked job to return nil, got job: %s", secondClaim.ID)
	}

	// 4. Heartbeat checks
	// Heartbeat from owner workerA -> should succeed (true)
	hbOk, err := store.HeartbeatJob(ctx, claimedJob.ID, claimedJob.LockGeneration, workerA, lease)
	if err != nil {
		t.Fatal(err)
	}
	if !hbOk {
		t.Error("expected heartbeat from owner to succeed, got false")
	}

	// Heartbeat from non-owner workerB -> should fail (false)
	hbOk, err = store.HeartbeatJob(ctx, claimedJob.ID, claimedJob.LockGeneration, workerB, lease)
	if err != nil {
		t.Fatal(err)
	}
	if hbOk {
		t.Error("expected heartbeat from non-owner to fail, got true")
	}

	// 5. CompleteJob checks
	// B trying to complete -> must fail
	err = store.CompleteJob(ctx, claimedJob.ID, claimedJob.LockGeneration, workerB, json.RawMessage(`{}`))
	if err == nil {
		t.Error("expected CompleteJob from non-owner B to fail, but it succeeded")
	}

	// A completes -> must succeed
	err = store.CompleteJob(ctx, claimedJob.ID, claimedJob.LockGeneration, workerA, json.RawMessage(`{"success":true}`))
	if err != nil {
		t.Fatalf("failed to complete job by owner A: %v", err)
	}

	// Verify status in DB is completed
	var completedStatus string
	err = store.DB().QueryRowContext(ctx, "SELECT status FROM review_jobs WHERE id = $1", claimedJob.ID).Scan(&completedStatus)
	if err != nil {
		t.Fatal(err)
	}
	if completedStatus != string(domain.JobStatusCompleted) {
		t.Errorf("expected job status to be completed, got %s", completedStatus)
	}

	// 6. Test Lease Reclaim (reclaiming expired running job)
	// Create another pending job
	job2 := &domain.ReviewJob{
		InstallationID: inst.ID,
		RepoFullName:   repo,
		PRNumber:       prNum + 1,
		HeadSHA:        "sha-lease-2222222222222222222222222",
		Status:         domain.JobStatusPending,
	}
	if err := store.CreateJob(ctx, job2); err != nil {
		t.Fatal(err)
	}

	// Claim with worker A
	claimed2, err := store.ClaimJob(ctx, workerA, lease)
	if err != nil {
		t.Fatal(err)
	}
	if claimed2 == nil {
		t.Fatal("failed to claim job2")
	}

	// Manually force locked_until to be in the past via SQL to simulate lease expiration
	_, err = store.DB().ExecContext(ctx, "UPDATE review_jobs SET locked_until = NOW() - INTERVAL '1 minute' WHERE id = $1", claimed2.ID)
	if err != nil {
		t.Fatalf("failed to force lease expiration in SQL: %v", err)
	}

	// Claim with worker B -> B should reclaim it because locked_until is in the past!
	reclaimedByB, err := store.ClaimJob(ctx, workerB, lease)
	if err != nil {
		t.Fatalf("failed to reclaim expired job: %v", err)
	}
	if reclaimedByB == nil {
		t.Fatal("expected worker B to reclaim the expired job, got nil")
	}
	if reclaimedByB.ID != claimed2.ID {
		t.Errorf("expected reclaimed job ID to match original %s, got %s", claimed2.ID, reclaimedByB.ID)
	}
	if reclaimedByB.LockGeneration != 2 {
		t.Errorf("expected lock_generation to be bumped to 2, got %d", reclaimedByB.LockGeneration)
	}
	if reclaimedByB.LockedBy == nil || *reclaimedByB.LockedBy != workerB {
		t.Errorf("expected owner to be reclaimed by %s, got %v", workerB, reclaimedByB.LockedBy)
	}

	// 7. Test Cancel abort during running state
	// Create job3
	job3 := &domain.ReviewJob{
		InstallationID: inst.ID,
		RepoFullName:   repo,
		PRNumber:       prNum + 2,
		HeadSHA:        "sha-cancel-3333333333333333333333333",
		Status:         domain.JobStatusPending,
	}
	if err := store.CreateJob(ctx, job3); err != nil {
		t.Fatal(err)
	}

	// Claim with worker A
	claimed3, err := store.ClaimJob(ctx, workerA, lease)
	if err != nil {
		t.Fatal(err)
	}

	// Set job status to cancelled externally (simulating webhook cancellation)
	_, err = store.DB().ExecContext(ctx, "UPDATE review_jobs SET status = 'cancelled' WHERE id = $1", claimed3.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Try to complete it -> should fail because status != 'running'
	err = store.CompleteJob(ctx, claimed3.ID, claimed3.LockGeneration, workerA, json.RawMessage(`{}`))
	if err == nil {
		t.Error("expected CompleteJob on cancelled job to fail, but it succeeded")
	}
}
