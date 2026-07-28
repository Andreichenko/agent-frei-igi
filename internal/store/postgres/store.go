package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"agent-frei-igi/internal/domain"
	"agent-frei-igi/migrations"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// ErrJobAlreadyExists is returned when attempting to create a job that violates
// the unique constraint (installation_id, repo_full_name, pr_number, head_sha).
var ErrJobAlreadyExists = errors.New("job already exists")

// Store provides access to the PostgreSQL database.
type Store struct {
	db *sql.DB
}

// New opens a database connection using the provided connection string.
func New(databaseURL string) (*Store, error) {
	if databaseURL == "" {
		return nil, errors.New("database URL cannot be empty")
	}

	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Validate connectivity
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &Store{db: db}, nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// DB returns the underlying sql.DB connection. Used primarily for integration testing.
func (s *Store) DB() *sql.DB {
	return s.db
}

// Migrate applies all embedded SQL migrations in sequential order.
// Uses schema_migrations table to track already applied files.
func (s *Store) Migrate(ctx context.Context) error {
	// Create migration tracking table if it does not exist
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		return fmt.Errorf("failed to initialize schema_migrations table: %w", err)
	}

	// Read migration files embedded in FS
	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		return fmt.Errorf("failed to read migrations directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()

		// Verify if migration was already applied
		var exists bool
		err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)", name).Scan(&exists)
		if err != nil {
			return fmt.Errorf("failed to check migration history for %s: %w", name, err)
		}

		if exists {
			continue
		}

		// Read migration contents
		content, err := migrations.FS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("failed to read migration content for %s: %w", name, err)
		}

		log.Printf("Applying database migration: %s", name)

		// Execute migration inside a transaction
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to begin transaction: %w", err)
		}

		if _, err := tx.ExecContext(ctx, string(content)); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to execute migration script %s: %w", name, err)
		}

		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", name); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to insert migration version record for %s: %w", name, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit transaction: %w", err)
		}
	}

	return nil
}

// UpsertInstallation inserts a new GitHub installation record or updates an existing one on conflict.
func (s *Store) UpsertInstallation(ctx context.Context, inst *domain.Installation) error {
	query := `
		INSERT INTO installations (github_installation_id, account_login, account_type, suspended_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (github_installation_id) DO UPDATE SET
			account_login = EXCLUDED.account_login,
			account_type = EXCLUDED.account_type,
			suspended_at = EXCLUDED.suspended_at,
			updated_at = CURRENT_TIMESTAMP
		RETURNING id, created_at, updated_at
	`
	err := s.db.QueryRowContext(ctx, query,
		inst.GitHubInstallationID,
		inst.AccountLogin,
		inst.AccountType,
		inst.SuspendedAt,
	).Scan(&inst.ID, &inst.CreatedAt, &inst.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to upsert installation: %w", err)
	}

	return nil
}

// GetInstallation retrieves a GitHub App installation record by its internal ID.
func (s *Store) GetInstallation(ctx context.Context, id int64) (*domain.Installation, error) {
	query := `
		SELECT id, github_installation_id, account_login, account_type, suspended_at, created_at, updated_at
		FROM installations
		WHERE id = $1
	`
	inst := &domain.Installation{}
	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&inst.ID,
		&inst.GitHubInstallationID,
		&inst.AccountLogin,
		&inst.AccountType,
		&inst.SuspendedAt,
		&inst.CreatedAt,
		&inst.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get installation: %w", err)
	}

	return inst, nil
}

// GetInstallationByGitHubID retrieves a GitHub App installation record by its GitHub installation ID.
func (s *Store) GetInstallationByGitHubID(ctx context.Context, githubInstallationID int64) (*domain.Installation, error) {
	query := `
		SELECT id, github_installation_id, account_login, account_type, suspended_at, created_at, updated_at
		FROM installations
		WHERE github_installation_id = $1
	`
	inst := &domain.Installation{}
	err := s.db.QueryRowContext(ctx, query, githubInstallationID).Scan(
		&inst.ID,
		&inst.GitHubInstallationID,
		&inst.AccountLogin,
		&inst.AccountType,
		&inst.SuspendedAt,
		&inst.CreatedAt,
		&inst.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get installation by github id: %w", err)
	}

	return inst, nil
}

// CreateJob inserts a new review job into the queue.
// Returns ErrJobAlreadyExists on unique constraint violation.
func (s *Store) CreateJob(ctx context.Context, job *domain.ReviewJob) error {
	query := `
		INSERT INTO review_jobs (
			installation_id, repo_full_name, pr_number, head_sha, base_sha,
			publisher_account_id, publisher_login, status, lock_generation, locked_until,
			locked_by, attempt, last_error, context_blob, result_blob, prompt_version
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		RETURNING id, created_at, updated_at
	`
	err := s.db.QueryRowContext(ctx, query,
		job.InstallationID,
		job.RepoFullName,
		job.PRNumber,
		job.HeadSHA,
		job.BaseSHA,
		job.PublisherAccountID,
		job.PublisherLogin,
		job.Status,
		job.LockGeneration,
		job.LockedUntil,
		job.LockedBy,
		job.Attempt,
		job.LastError,
		job.ContextBlob,
		job.ResultBlob,
		job.PromptVersion,
	).Scan(&job.ID, &job.CreatedAt, &job.UpdatedAt)

	if err != nil {
		if isUniqueViolation(err) {
			return ErrJobAlreadyExists
		}
		return fmt.Errorf("failed to create job: %w", err)
	}

	return nil
}

// isUniqueViolation checks if the database error is a unique constraint violation (PostgreSQL 23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// GetJob retrieves a review job by its UUID.
func (s *Store) GetJob(ctx context.Context, id uuid.UUID) (*domain.ReviewJob, error) {
	query := `
		SELECT id, installation_id, repo_full_name, pr_number, head_sha, base_sha,
		       publisher_account_id, publisher_login, status, lock_generation, locked_until,
		       locked_by, attempt, last_error, context_blob, result_blob, prompt_version,
		       created_at, updated_at
		FROM review_jobs
		WHERE id = $1
	`
	job := &domain.ReviewJob{}
	var contextBlob []byte
	var resultBlob []byte

	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&job.ID,
		&job.InstallationID,
		&job.RepoFullName,
		&job.PRNumber,
		&job.HeadSHA,
		&job.BaseSHA,
		&job.PublisherAccountID,
		&job.PublisherLogin,
		&job.Status,
		&job.LockGeneration,
		&job.LockedUntil,
		&job.LockedBy,
		&job.Attempt,
		&job.LastError,
		&contextBlob,
		&resultBlob,
		&job.PromptVersion,
		&job.CreatedAt,
		&job.UpdatedAt,
	)
	if err == nil {
		job.ContextBlob = json.RawMessage(contextBlob)
		job.ResultBlob = json.RawMessage(resultBlob)
	}

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil // Return nil, nil when no row is found
		}
		return nil, fmt.Errorf("failed to get job: %w", err)
	}

	return job, nil
}

// CreateAccount inserts a new reviewer account configuration.
func (s *Store) CreateAccount(ctx context.Context, acc *domain.GitHubAccount) error {
	query := `
		INSERT INTO github_accounts (github_login, access_token_enc, token_expires_at, status, last_used_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at, updated_at
	`
	err := s.db.QueryRowContext(ctx, query,
		acc.GitHubLogin,
		acc.AccessTokenEnc,
		acc.TokenExpiresAt,
		acc.Status,
		acc.LastUsedAt,
	).Scan(&acc.ID, &acc.CreatedAt, &acc.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to create account: %w", err)
	}

	return nil
}

// GetAccountByLogin retrieves a reviewer account by its GitHub username login.
func (s *Store) GetAccountByLogin(ctx context.Context, login string) (*domain.GitHubAccount, error) {
	query := `
		SELECT id, github_login, access_token_enc, token_expires_at, status, last_used_at, created_at, updated_at
		FROM github_accounts
		WHERE github_login = $1
	`
	acc := &domain.GitHubAccount{}
	err := s.db.QueryRowContext(ctx, query, login).Scan(
		&acc.ID,
		&acc.GitHubLogin,
		&acc.AccessTokenEnc,
		&acc.TokenExpiresAt,
		&acc.Status,
		&acc.LastUsedAt,
		&acc.CreatedAt,
		&acc.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get account: %w", err)
	}

	return acc, nil
}

// CancelJobsForPR cancels all non-terminal jobs for a specific PR except the specified head SHA (supersede logic).
// Returns the number of cancelled jobs.
func (s *Store) CancelJobsForPR(ctx context.Context, installationID int64, repo string, prNumber int, exceptHeadSHA string) (int64, error) {
	query := `
		UPDATE review_jobs
		SET status = 'cancelled', updated_at = CURRENT_TIMESTAMP
		WHERE installation_id = $1
		  AND repo_full_name = $2
		  AND pr_number = $3
		  AND head_sha != $4
		  AND status IN ('pending', 'running', 'retry_wait')
	`
	res, err := s.db.ExecContext(ctx, query, installationID, repo, prNumber, exceptHeadSHA)
	if err != nil {
		return 0, fmt.Errorf("failed to cancel jobs: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}

	return rows, nil
}

// CancelOpenJobsForPR cancels all open/non-terminal jobs for a specific PR (e.g. closed/converted_to_draft).
// Returns the number of cancelled jobs.
func (s *Store) CancelOpenJobsForPR(ctx context.Context, installationID int64, repo string, prNumber int) (int64, error) {
	query := `
		UPDATE review_jobs
		SET status = 'cancelled', updated_at = CURRENT_TIMESTAMP
		WHERE installation_id = $1
		  AND repo_full_name = $2
		  AND pr_number = $3
		  AND status IN ('pending', 'running', 'retry_wait')
	`
	res, err := s.db.ExecContext(ctx, query, installationID, repo, prNumber)
	if err != nil {
		return 0, fmt.Errorf("failed to cancel open jobs: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}

	return rows, nil
}

// ClaimJob atomically selects one claimable job and marks it running using FOR UPDATE SKIP LOCKED.
// Returns nil, nil if the queue is empty.
func (s *Store) ClaimJob(ctx context.Context, workerID string, lease time.Duration) (*domain.ReviewJob, error) {
	query := `
		WITH candidate AS (
			SELECT id FROM review_jobs
			WHERE
				status IN ('pending', 'retry_wait', 'needs_reconcile')
				OR (status = 'running' AND locked_until IS NOT NULL AND locked_until < NOW())
			ORDER BY created_at ASC
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE review_jobs j
		SET
			status = 'running',
			lock_generation = j.lock_generation + 1,
			locked_by = $1,
			locked_until = NOW() + $2::interval,
			attempt = j.attempt + 1,
			updated_at = NOW()
		FROM candidate c
		WHERE j.id = c.id
		RETURNING j.id, j.installation_id, j.repo_full_name, j.pr_number, j.head_sha, j.base_sha,
		          j.publisher_account_id, j.publisher_login, j.status, j.lock_generation, j.locked_until,
		          j.locked_by, j.attempt, j.last_error, j.context_blob, j.result_blob, j.prompt_version,
		          j.created_at, j.updated_at
	`
	intervalStr := fmt.Sprintf("%d microseconds", lease.Microseconds())
	job := &domain.ReviewJob{}
	var contextBlob []byte
	var resultBlob []byte

	err := s.db.QueryRowContext(ctx, query, workerID, intervalStr).Scan(
		&job.ID,
		&job.InstallationID,
		&job.RepoFullName,
		&job.PRNumber,
		&job.HeadSHA,
		&job.BaseSHA,
		&job.PublisherAccountID,
		&job.PublisherLogin,
		&job.Status,
		&job.LockGeneration,
		&job.LockedUntil,
		&job.LockedBy,
		&job.Attempt,
		&job.LastError,
		&contextBlob,
		&resultBlob,
		&job.PromptVersion,
		&job.CreatedAt,
		&job.UpdatedAt,
	)
	if err == nil {
		job.ContextBlob = json.RawMessage(contextBlob)
		job.ResultBlob = json.RawMessage(resultBlob)
	}

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to claim job: %w", err)
	}

	return job, nil
}

// HeartbeatJob extends locked_until if the job is still owned by the specified workerID and lock generation.
// Returns false if ownership is lost.
func (s *Store) HeartbeatJob(ctx context.Context, jobID uuid.UUID, gen int64, workerID string, lease time.Duration) (bool, error) {
	query := `
		UPDATE review_jobs
		SET locked_until = NOW() + $1::interval, updated_at = NOW()
		WHERE id = $2 AND lock_generation = $3 AND locked_by = $4 AND status = 'running'
	`
	intervalStr := fmt.Sprintf("%d microseconds", lease.Microseconds())
	res, err := s.db.ExecContext(ctx, query, intervalStr, jobID, gen, workerID)
	if err != nil {
		return false, fmt.Errorf("heartbeat query failed: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to get rows affected: %w", err)
	}

	return rows > 0, nil
}

// CompleteJob sets status=completed, result_blob, clears lock fields — only if still owned by this workerID/generation.
func (s *Store) CompleteJob(ctx context.Context, jobID uuid.UUID, gen int64, workerID string, result json.RawMessage) error {
	query := `
		UPDATE review_jobs
		SET status = 'completed',
		    result_blob = $1,
		    locked_by = NULL,
		    locked_until = NULL,
		    updated_at = NOW()
		WHERE id = $2 AND lock_generation = $3 AND locked_by = $4 AND status = 'running'
	`
	res, err := s.db.ExecContext(ctx, query, result, jobID, gen, workerID)
	if err != nil {
		return fmt.Errorf("complete query failed: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("failed to complete job: lost ownership or job not running")
	}

	return nil
}

// FailJob sets status=failed, last_error, clears lock fields — only if still owned by this workerID/generation.
func (s *Store) FailJob(ctx context.Context, jobID uuid.UUID, gen int64, workerID string, errMsg string) error {
	query := `
		UPDATE review_jobs
		SET status = 'failed',
		    last_error = $1,
		    locked_by = NULL,
		    locked_until = NULL,
		    updated_at = NOW()
		WHERE id = $2 AND lock_generation = $3 AND locked_by = $4 AND status = 'running'
	`
	res, err := s.db.ExecContext(ctx, query, errMsg, jobID, gen, workerID)
	if err != nil {
		return fmt.Errorf("fail query failed: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("failed to fail job: lost ownership or job not running")
	}

	return nil
}

// UpdateJobContext updates the context_blob of a review job.
// Uses ownership fencing (only updates if generation matches and is running/locked by workerID).
func (s *Store) UpdateJobContext(ctx context.Context, jobID uuid.UUID, gen int64, workerID string, contextBytes json.RawMessage) error {
	query := `
		UPDATE review_jobs
		SET context_blob = $1, updated_at = NOW()
		WHERE id = $2 AND lock_generation = $3 AND locked_by = $4 AND status = 'running'
	`
	res, err := s.db.ExecContext(ctx, query, contextBytes, jobID, gen, workerID)
	if err != nil {
		return fmt.Errorf("failed to update job context query: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("failed to update job context: lost ownership or job not running")
	}

	return nil
}

// UpdateJobResult updates the result_blob and prompt_version of a review job.
// Uses ownership fencing (only updates if generation matches and is running/locked by workerID).
func (s *Store) UpdateJobResult(ctx context.Context, jobID uuid.UUID, gen int64, workerID string, resultBytes json.RawMessage, promptVersion string) error {
	query := `
		UPDATE review_jobs
		SET result_blob = $1, prompt_version = $2, updated_at = NOW()
		WHERE id = $3 AND lock_generation = $4 AND locked_by = $5 AND status = 'running'
	`
	res, err := s.db.ExecContext(ctx, query, resultBytes, promptVersion, jobID, gen, workerID)
	if err != nil {
		return fmt.Errorf("failed to update job result query: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("failed to update job result: lost ownership or job not running")
	}

	return nil
}

// ErrPublicationExists is returned when trying to begin a publication that already exists.
var ErrPublicationExists = errors.New("publication already exists for this job")

// BeginPublication inserts a new review_publications record with pending_github state.
// If unique violation occurs, returns ErrPublicationExists.
func (s *Store) BeginPublication(ctx context.Context, jobID uuid.UUID, publisherAccountID *int64, kind string) (*domain.ReviewPublication, error) {
	query := `
		INSERT INTO review_publications (job_id, publisher_account_id, publisher_kind, state)
		VALUES ($1, $2, $3, 'pending_github')
		RETURNING id, job_id, publisher_account_id, publisher_kind, state, github_review_id, published_at, created_at, updated_at
	`
	pub := &domain.ReviewPublication{}
	err := s.db.QueryRowContext(ctx, query, jobID, publisherAccountID, kind).Scan(
		&pub.ID,
		&pub.JobID,
		&pub.PublisherAccountID,
		&pub.PublisherKind,
		&pub.State,
		&pub.GitHubReviewID,
		&pub.PublishedAt,
		&pub.CreatedAt,
		&pub.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrPublicationExists
		}
		return nil, fmt.Errorf("failed to begin publication: %w", err)
	}
	return pub, nil
}

// FinalizePublication updates state to 'published' and updates review ID and time.
func (s *Store) FinalizePublication(ctx context.Context, jobID uuid.UUID, githubReviewID *int64, dryRun bool) error {
	var query string
	var err error
	if dryRun {
		query = `
			UPDATE review_publications
			SET state = 'published', github_review_id = NULL, published_at = NOW(), updated_at = NOW()
			WHERE job_id = $1
		`
		_, err = s.db.ExecContext(ctx, query, jobID)
	} else {
		query = `
			UPDATE review_publications
			SET state = 'published', github_review_id = $1, published_at = NOW(), updated_at = NOW()
			WHERE job_id = $2
		`
		_, err = s.db.ExecContext(ctx, query, githubReviewID, jobID)
	}
	if err != nil {
		return fmt.Errorf("failed to finalize publication: %w", err)
	}
	return nil
}

// FailPublication updates state to 'failed'.
func (s *Store) FailPublication(ctx context.Context, jobID uuid.UUID, errMsg string) error {
	query := `
		UPDATE review_publications
		SET state = 'failed', updated_at = NOW()
		WHERE job_id = $1
	`
	_, err := s.db.ExecContext(ctx, query, jobID)
	if err != nil {
		return fmt.Errorf("failed to fail publication: %w", err)
	}
	return nil
}

// GetPublication retrieves a review publication by job ID.
func (s *Store) GetPublication(ctx context.Context, jobID uuid.UUID) (*domain.ReviewPublication, error) {
	query := `
		SELECT id, job_id, publisher_account_id, publisher_kind, state, github_review_id, published_at, created_at, updated_at
		FROM review_publications
		WHERE job_id = $1
	`
	pub := &domain.ReviewPublication{}
	err := s.db.QueryRowContext(ctx, query, jobID).Scan(
		&pub.ID,
		&pub.JobID,
		&pub.PublisherAccountID,
		&pub.PublisherKind,
		&pub.State,
		&pub.GitHubReviewID,
		&pub.PublishedAt,
		&pub.CreatedAt,
		&pub.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get publication: %w", err)
	}
	return pub, nil
}
