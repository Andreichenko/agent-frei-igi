package diplomat

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"agent-frei-igi/internal/config"
	"agent-frei-igi/internal/crypto"
	"agent-frei-igi/internal/critic"
	"agent-frei-igi/internal/domain"
	"agent-frei-igi/internal/githubapp"
	"agent-frei-igi/internal/store/postgres"

	"github.com/google/uuid"
)

// PublicationStore defines database requirements for publication lifecycle.
type PublicationStore interface {
	GetAccountByLogin(ctx context.Context, login string) (*domain.GitHubAccount, error)
	GetInstallation(ctx context.Context, id int64) (*domain.Installation, error)
	BeginPublication(ctx context.Context, jobID uuid.UUID, publisherAccountID *int64, kind string) (*domain.ReviewPublication, error)
	FinalizePublication(ctx context.Context, jobID uuid.UUID, githubReviewID *int64, dryRun bool) error
	FailPublication(ctx context.Context, jobID uuid.UUID, errMsg string) error
	GetPublication(ctx context.Context, jobID uuid.UUID) (*domain.ReviewPublication, error)
}

// GitHubReviewClient defines requirements for publishing pull request reviews.
type GitHubReviewClient interface {
	CreatePullReview(ctx context.Context, token, owner, repo string, prNumber int, req githubapp.ReviewRequest) (int64, error)
}

// Publisher orchestrates review results serialization and distribution to GitHub.
type Publisher struct {
	cfg   *config.Config
	store PublicationStore
	gh    GitHubReviewClient
}

// New initializes a new Publisher.
func New(cfg *config.Config, store PublicationStore, gh GitHubReviewClient) *Publisher {
	return &Publisher{
		cfg:   cfg,
		store: store,
		gh:    gh,
	}
}

// Publish takes job findings and publishes a GitHub pull request review.
// It supports dry-run, user token decryption, fallback app bot auth, and idempotent database fencing.
func (p *Publisher) Publish(ctx context.Context, job *domain.ReviewJob, result *critic.Result) error {
	shortJobID := job.ID.String()
	if len(shortJobID) > 8 {
		shortJobID = shortJobID[:8]
	}

	// 1. Dry-run Mode
	if !p.cfg.FFPublishComments {
		log.Printf("[DIPLOMAT] Dry-run enabled. Skipping real GitHub publish for job %s.", job.ID)

		// Insert dry-run publication fence
		_, err := p.store.BeginPublication(ctx, job.ID, nil, string(domain.PublisherKindUser))
		if err != nil {
			if errors.Is(err, postgres.ErrPublicationExists) {
				pub, getErr := p.store.GetPublication(ctx, job.ID)
				if getErr == nil && pub != nil {
					log.Printf("[DIPLOMAT] Fencing: dry-run publication already exists in state %s. No-op.", pub.State)
					return nil
				}
			}
			return fmt.Errorf("failed to register dry-run publication: %w", err)
		}

		err = p.store.FinalizePublication(ctx, job.ID, nil, true)
		if err != nil {
			return fmt.Errorf("failed to finalize dry-run publication: %w", err)
		}

		return nil
	}

	// 2. Token resolution
	var token string
	var pubAccountID *int64
	publisherKind := domain.PublisherKindUser

	var account *domain.GitHubAccount
	var decryptErr error

	if job.PublisherLogin != nil && *job.PublisherLogin != "" {
		var err error
		account, err = p.store.GetAccountByLogin(ctx, *job.PublisherLogin)
		if err != nil {
			log.Printf("[DIPLOMAT] Failed to look up account %s: %v", *job.PublisherLogin, err)
		}
	}

	if account != nil && len(account.AccessTokenEnc) > 0 {
		key, keyErr := p.cfg.ParseTokenKey()
		if keyErr != nil {
			return fmt.Errorf("cryptographic key setup failed: %w", keyErr)
		}

		decrypted, err := crypto.Decrypt(key, account.AccessTokenEnc)
		if err == nil {
			token = string(decrypted)
			pubAccountID = &account.ID
		} else {
			decryptErr = err
			log.Printf("[DIPLOMAT] Failed to decrypt token for account %s: %v", account.GitHubLogin, err)
		}
	}

	// Fallback to App installation token if requested
	if token == "" {
		if p.cfg.FFAppBotPublishFallback {
			log.Printf("[DIPLOMAT] User token unavailable. Falling back to App Bot publication for installation %d.", job.InstallationID)
			pemBytes, err := os.ReadFile(p.cfg.GitHubAppPrivateKeyPath)
			if err != nil {
				return fmt.Errorf("failed to read private key PEM file for fallback: %w", err)
			}

			inst, err := p.store.GetInstallation(ctx, job.InstallationID)
			if err != nil {
				return fmt.Errorf("failed to load installation: %w", err)
			}
			if inst == nil {
				return fmt.Errorf("installation %d not found in database", job.InstallationID)
			}

			ghClient := githubapp.NewClient(p.cfg.GitHubAPIBaseURL, p.cfg.GitHubAppID, pemBytes)
			appToken, err := ghClient.GetInstallationToken(ctx, inst.GitHubInstallationID)
			if err != nil {
				return fmt.Errorf("failed to get installation access token for fallback: %w", err)
			}

			token = appToken
			publisherKind = domain.PublisherKindAppBot
		} else {
			if decryptErr != nil {
				return fmt.Errorf("failed to decrypt user token and App fallback is disabled: %w", decryptErr)
			}
			return fmt.Errorf("user token is missing or empty for publisher %v, and App fallback is disabled", job.PublisherLogin)
		}
	}

	// 3. Begin Publication (Fencing)
	_, err := p.store.BeginPublication(ctx, job.ID, pubAccountID, string(publisherKind))
	if err != nil {
		if errors.Is(err, postgres.ErrPublicationExists) {
			pub, getErr := p.store.GetPublication(ctx, job.ID)
			if getErr != nil {
				return fmt.Errorf("failed to load existing publication: %w", getErr)
			}
			if pub != nil {
				if pub.State == domain.PublicationStatePublished {
					log.Printf("[DIPLOMAT] Fencing: review already published (review_id: %v). No-op.", pub.GitHubReviewID)
					return nil
				}
				if pub.State == domain.PublicationStatePending {
					log.Printf("[DIPLOMAT] Fencing: review is already pending_github. Skipping double POST.")
					return nil
				}
			}
		}
		return fmt.Errorf("failed to reserve publication slot: %w", err)
	}

	// 4. Map findings to GitHub review payload
	parts := strings.Split(job.RepoFullName, "/")
	if len(parts) != 2 {
		_ = p.store.FailPublication(ctx, job.ID, "invalid repository name format")
		return fmt.Errorf("invalid repository format: %q", job.RepoFullName)
	}
	owner, repo := parts[0], parts[1]

	reqPayload := MapResultToRequest(result, job.HeadSHA, p.cfg.FFReviewSignature, shortJobID)

	// 5. POST create review
	reviewID, err := p.gh.CreatePullReview(ctx, token, owner, repo, job.PRNumber, reqPayload)
	if err != nil {
		_ = p.store.FailPublication(ctx, job.ID, err.Error())
		return fmt.Errorf("failed to create PR review on GitHub: %w", err)
	}

	// 6. Finalize Publication
	err = p.store.FinalizePublication(ctx, job.ID, &reviewID, false)
	if err != nil {
		return fmt.Errorf("failed to finalize publication in DB: %w", err)
	}

	log.Printf("[DIPLOMAT] Successfully published PR review %d for job %s", reviewID, job.ID)
	return nil
}
