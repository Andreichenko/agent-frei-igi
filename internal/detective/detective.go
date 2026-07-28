package detective

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"agent-frei-igi/internal/config"
	"agent-frei-igi/internal/domain"
	"agent-frei-igi/internal/githubapp"
)

// InstallationGetter specifies the database operations required by the detective.
type InstallationGetter interface {
	GetInstallation(ctx context.Context, id int64) (*domain.Installation, error)
}

// Detective coordinates auth and download from GitHub to build review context.
type Detective struct {
	cfg      config.Config
	db       InstallationGetter
	ghClient *githubapp.Client
}

// New initializes a new Detective coordinator.
func New(cfg *config.Config, db InstallationGetter, ghClient *githubapp.Client) *Detective {
	var c config.Config
	if cfg != nil {
		c = *cfg
	}
	return &Detective{
		cfg:      c,
		db:       db,
		ghClient: ghClient,
	}
}

// Build gathers installation data, gets the access token, lists PR files, and applies truncation.
// Returns a populated domain.ReviewContext or an error.
func (d *Detective) Build(ctx context.Context, job *domain.ReviewJob) (*domain.ReviewContext, error) {
	if job == nil {
		return nil, errors.New("cannot build context for nil job")
	}

	var pemBytes []byte
	if d.ghClient == nil {
		// Fail fast if credentials are not configured
		if d.cfg.GitHubAppID == "" || d.cfg.GitHubAppPrivateKeyPath == "" {
			return nil, errors.New("GitHub App credentials (GITHUB_APP_ID or GITHUB_APP_PRIVATE_KEY_PATH) are not configured")
		}

		// Read private key PEM file
		var err error
		pemBytes, err = os.ReadFile(d.cfg.GitHubAppPrivateKeyPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read private key PEM file: %w", err)
		}
	}

	// 1. Get installation from DB
	inst, err := d.db.GetInstallation(ctx, job.InstallationID)
	if err != nil {
		return nil, fmt.Errorf("failed to load installation: %w", err)
	}
	if inst == nil {
		return nil, fmt.Errorf("installation %d not found in database", job.InstallationID)
	}

	// 2. Split repository name into owner and repo
	parts := strings.Split(job.RepoFullName, "/")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid repo_full_name format: %q (expected owner/repo)", job.RepoFullName)
	}
	owner, repo := parts[0], parts[1]

	// 3. Request token & fetch files from API
	ghClient := d.ghClient
	if ghClient == nil {
		ghClient = githubapp.NewClient(d.cfg.GitHubAPIBaseURL, d.cfg.GitHubAppID, pemBytes)
	}
	token, err := ghClient.GetInstallationToken(ctx, inst.GitHubInstallationID)
	if err != nil {
		return nil, fmt.Errorf("failed to get installation access token: %w", err)
	}

	files, err := ghClient.ListPRFiles(ctx, token, owner, repo, job.PRNumber)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch PR files: %w", err)
	}

	// 4. Truncate files based on limits
	truncatedFiles, truncInfo := TruncatePRFiles(files, d.cfg.DetectiveMaxFiles, d.cfg.DetectiveMaxPatchBytes)

	pubLogin := ""
	if job.PublisherLogin != nil {
		pubLogin = *job.PublisherLogin
	}

	// 5. Build ReviewContext
	reviewCtx := &domain.ReviewContext{
		JobID:           job.ID.String(),
		InstallationID:  job.InstallationID,
		GitHubInstallID: inst.GitHubInstallationID,
		RepoFullName:    job.RepoFullName,
		PRNumber:        job.PRNumber,
		HeadSHA:         job.HeadSHA,
		BaseSHA:         job.BaseSHA,
		PublisherLogin:  pubLogin,
		Files:           truncatedFiles,
		Truncation:      truncInfo,
		BuiltAt:         time.Now(),
	}

	return reviewCtx, nil
}
