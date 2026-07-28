package githubapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"agent-frei-igi/internal/domain"
)

// TokenCacheEntry stores a cached GitHub App installation token.
type TokenCacheEntry struct {
	Token     string
	ExpiresAt time.Time
}

// GitHubFile represents the metadata returned by GitHub's PR files API.
type GitHubFile struct {
	Filename         string `json:"filename"`
	Status           string `json:"status"`
	PreviousFilename string `json:"previous_filename,omitempty"`
	Patch            string `json:"patch,omitempty"`
}

// Client acts as the gateway to the GitHub REST API for a GitHub App.
type Client struct {
	baseURL       string
	appID         string
	privateKeyPEM []byte
	httpClient    *http.Client
	tokenCacheMu  sync.Mutex
	tokenCache    map[int64]TokenCacheEntry
}

// NewClient initializes a new GitHub REST client.
func NewClient(baseURL, appID string, privateKeyPEM []byte) *Client {
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	return &Client{
		baseURL:       strings.TrimSuffix(baseURL, "/"),
		appID:         appID,
		privateKeyPEM: privateKeyPEM,
		httpClient:    &http.Client{Timeout: 15 * time.Second},
		tokenCache:    make(map[int64]TokenCacheEntry),
	}
}

// GetInstallationToken retrieves a cached access token or requests a new one if expired.
func (c *Client) GetInstallationToken(ctx context.Context, githubInstID int64) (string, error) {
	c.tokenCacheMu.Lock()
	entry, exists := c.tokenCache[githubInstID]
	c.tokenCacheMu.Unlock()

	// If token exists and is valid (with 1-minute buffer skew), reuse it
	if exists && time.Now().Add(1*time.Minute).Before(entry.ExpiresAt) {
		return entry.Token, nil
	}

	// Fetch a new installation token
	token, expiresAt, err := c.fetchInstallationToken(ctx, githubInstID)
	if err != nil {
		return "", err
	}

	// Update cache
	c.tokenCacheMu.Lock()
	c.tokenCache[githubInstID] = TokenCacheEntry{
		Token:     token,
		ExpiresAt: expiresAt,
	}
	c.tokenCacheMu.Unlock()

	return token, nil
}

type tokenResponse struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

func (c *Client) fetchInstallationToken(ctx context.Context, githubInstID int64) (string, time.Time, error) {
	jwt, err := GenerateAppJWT(c.appID, c.privateKeyPEM)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to generate App JWT: %w", err)
	}

	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", c.baseURL, githubInstID)
	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return "", time.Time{}, err
	}

	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "agent-frei-igi")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", time.Time{}, fmt.Errorf("failed to fetch installation token (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var tokenResp tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", time.Time{}, fmt.Errorf("failed to decode token response: %w", err)
	}

	expiresAt, err := time.Parse(time.RFC3339, tokenResp.ExpiresAt)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to parse token expiry: %w", err)
	}

	return tokenResp.Token, expiresAt, nil
}

// ListPRFiles fetches all files modified in the Pull Request across pages.
func (c *Client) ListPRFiles(ctx context.Context, token, owner, repo string, prNumber int) ([]domain.ChangedFile, error) {
	var files []domain.ChangedFile
	nextURL := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/files?per_page=100", c.baseURL, owner, repo, prNumber)

	for nextURL != "" {
		req, err := http.NewRequestWithContext(ctx, "GET", nextURL, nil)
		if err != nil {
			return nil, err
		}

		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "agent-frei-igi")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("http files request failed: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("failed to list PR files (status %d): %s", resp.StatusCode, string(bodyBytes))
		}

		var ghFiles []GitHubFile
		err = json.NewDecoder(resp.Body).Decode(&ghFiles)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to decode PR files response: %w", err)
		}

		for _, f := range ghFiles {
			files = append(files, domain.ChangedFile{
				Path:         f.Filename,
				Status:       f.Status,
				PreviousPath: f.PreviousFilename,
				Patch:        f.Patch,
			})
		}

		// Pagination: parse Link header
		nextURL = parseNextPageURL(resp.Header.Get("Link"))
	}

	return files, nil
}

// parseNextPageURL parses the Link header and returns the URL for the next page of results.
func parseNextPageURL(linkHeader string) string {
	if linkHeader == "" {
		return ""
	}

	parts := strings.Split(linkHeader, ",")
	for _, part := range parts {
		subparts := strings.Split(part, ";")
		if len(subparts) < 2 {
			continue
		}
		if strings.Contains(subparts[1], `rel="next"`) {
			urlPart := strings.TrimSpace(subparts[0])
			if strings.HasPrefix(urlPart, "<") && strings.HasSuffix(urlPart, ">") {
				return urlPart[1 : len(urlPart)-1]
			}
		}
	}

	return ""
}

// ReviewComment represents a single inline comment inside a PR review.
type ReviewComment struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Side string `json:"side"` // Always "RIGHT"
	Body string `json:"body"`
}

// ReviewRequest represents the JSON payload to create a Pull Request Review on GitHub.
type ReviewRequest struct {
	CommitID string          `json:"commit_id"`
	Body     string          `json:"body"`
	Event    string          `json:"event"`
	Comments []ReviewComment `json:"comments,omitempty"`
}

// CreatePullReview creates a Pull Request Review on GitHub using a user or installation token.
func (c *Client) CreatePullReview(ctx context.Context, token, owner, repo string, prNumber int, req ReviewRequest) (int64, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/reviews", c.baseURL, owner, repo, prNumber)

	reqBytes, err := json.Marshal(req)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal review request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqBytes))
	if err != nil {
		return 0, err
	}

	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Accept", "application/vnd.github+json")
	httpReq.Header.Set("User-Agent", "agent-frei-igi")
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return 0, fmt.Errorf("http review request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("failed to create PR review (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var respMap struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(bodyBytes, &respMap); err != nil {
		return 0, fmt.Errorf("failed to decode review response: %w", err)
	}

	return respMap.ID, nil
}
