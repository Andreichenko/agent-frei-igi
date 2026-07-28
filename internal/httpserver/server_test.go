package httpserver

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agent-frei-igi/internal/config"
	"agent-frei-igi/internal/domain"
	"agent-frei-igi/internal/queue"
)

// computeHMAC calculates the X-Hub-Signature-256 signature for test requests.
func computeHMAC(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestServer_Healthz(t *testing.T) {
	cfg := &config.Config{HTTPAddr: ":8080"}
	srv := New(cfg, nil, nil)

	req := httptest.NewRequest("GET", "/healthz", nil)
	rr := httptest.NewRecorder()

	srv.healthzHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rr.Code)
	}

	var resp map[string]bool
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if !resp["ok"] {
		t.Error("expected body to contain ok: true")
	}
}

func TestServer_GithubWebhook_SignatureValidation(t *testing.T) {
	secret := "test-webhook-secret-123"
	cfg := &config.Config{
		HTTPAddr:      ":8080",
		WebhookSecret: secret,
	}

	// In testing, enqueuer is nil, and store is nil.
	// If signature is VALID, it will pass through verify and fail with 503 "Database not configured".
	// If signature is INVALID, it will fail earlier with 401 Unauthorized.
	srv := New(cfg, nil, queue.NewEnqueuer(nil, nil))

	payload := []byte(`{"ping": "pong"}`)

	// 1. Case: Invalid signature
	req := httptest.NewRequest("POST", "/webhooks/github", bytes.NewReader(payload))
	req.Header.Set("X-GitHub-Event", "ping")
	req.Header.Set("X-Hub-Signature-256", "sha256=invalid-signature-hex")
	rr := httptest.NewRecorder()

	srv.githubWebhookHandler(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized for invalid signature, got %d", rr.Code)
	}

	// 2. Case: Valid signature, missing DB (fails with 503)
	validSig := computeHMAC(payload, secret)
	reqValid := httptest.NewRequest("POST", "/webhooks/github", bytes.NewReader(payload))
	reqValid.Header.Set("X-GitHub-Event", "ping")
	reqValid.Header.Set("X-Hub-Signature-256", validSig)
	rrValid := httptest.NewRecorder()

	srv.githubWebhookHandler(rrValid, reqValid)
	if rrValid.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 Service Unavailable (due to nil database after valid signature check), got %d", rrValid.Code)
	}
}

func TestServer_GithubWebhook_SecretMissing(t *testing.T) {
	// If the server's GITHUB_WEBHOOK_SECRET is empty, it must reject requests with 500 Internal Server Error
	cfg := &config.Config{
		HTTPAddr:      ":8080",
		WebhookSecret: "", // Secret is empty
	}
	srv := New(cfg, nil, nil)

	req := httptest.NewRequest("POST", "/webhooks/github", bytes.NewReader([]byte(`{}`)))
	rr := httptest.NewRecorder()

	srv.githubWebhookHandler(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 Internal Server Error when secret is empty, got %d", rr.Code)
	}
}

func TestServer_GithubWebhook_OversizedBody(t *testing.T) {
	secret := "test-secret"
	cfg := &config.Config{
		HTTPAddr:      ":8080",
		WebhookSecret: secret,
	}
	srv := New(cfg, nil, nil)

	// Create a payload larger than 5 MiB (5.1 MiB)
	largePayload := make([]byte, 5*1024*1024+102400)
	validSig := computeHMAC(largePayload, secret)

	req := httptest.NewRequest("POST", "/webhooks/github", bytes.NewReader(largePayload))
	req.Header.Set("X-GitHub-Event", "ping")
	req.Header.Set("X-Hub-Signature-256", validSig)
	rr := httptest.NewRecorder()

	srv.githubWebhookHandler(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413 Request Entity Too Large, got %d", rr.Code)
	}
}

type mockServerStore struct {
	count          int
	accounts       []domain.GitHubAccount
	upsertLogin    string
	upsertTokenEnc []byte
	statusLogin    string
	statusVal      domain.AccountStatus
}

func (m *mockServerStore) CountAccounts(ctx context.Context) (int, error) {
	return m.count, nil
}

func (m *mockServerStore) UpsertAccountToken(ctx context.Context, login string, tokenEnc []byte, expiresAt *time.Time) error {
	m.upsertLogin = login
	m.upsertTokenEnc = tokenEnc
	return nil
}

func (m *mockServerStore) ListAccounts(ctx context.Context) ([]domain.GitHubAccount, error) {
	return m.accounts, nil
}

func (m *mockServerStore) SetAccountStatus(ctx context.Context, login string, status domain.AccountStatus) error {
	m.statusLogin = login
	m.statusVal = status
	return nil
}

func TestServer_OAuthStart_SecurityAndLimits(t *testing.T) {
	cfg := &config.Config{
		HTTPAddr:            ":8080",
		AdminToken:          "secret-admin-pass",
		GitHubOAuthClientID: "client-id-123",
		MaxReviewerAccounts: 2,
		AdminOAuthRPM:       2,
	}

	store := &mockServerStore{count: 0}
	srv := New(cfg, store, nil)

	// 1. Unauthorized without admin token
	req := httptest.NewRequest("GET", "/oauth/github/start", nil)
	rr := httptest.NewRecorder()
	srv.oauthStartHandler(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", rr.Code)
	}

	// 2. Success redirects to GitHub
	reqValid := httptest.NewRequest("GET", "/oauth/github/start?admin_token=secret-admin-pass", nil)
	rrValid := httptest.NewRecorder()
	srv.oauthStartHandler(rrValid, reqValid)
	if rrValid.Code != http.StatusTemporaryRedirect {
		t.Errorf("expected 307 Redirect, got %d", rrValid.Code)
	}
	loc := rrValid.Header().Get("Location")
	if !strings.Contains(loc, "github.com/login/oauth/authorize") || !strings.Contains(loc, "client_id=client-id-123") {
		t.Errorf("expected redirect location to be authorize URL, got %q", loc)
	}

	// Extract generated state
	var state string
	srv.stateMu.Lock()
	for s := range srv.states {
		state = s
		break
	}
	srv.stateMu.Unlock()
	if state == "" {
		t.Error("expected OAuth state to be generated in memory map")
	}

	// 3. Max accounts limit enforcement
	store.count = 2 // max is 2
	reqLimit := httptest.NewRequest("GET", "/oauth/github/start?admin_token=secret-admin-pass", nil)
	rrLimit := httptest.NewRecorder()
	srv.oauthStartHandler(rrLimit, reqLimit)
	if rrLimit.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden on max accounts, got %d", rrLimit.Code)
	}
	store.count = 0

	// 4. Rate Limit enforcement (RPM = 2)
	// Make 3 requests from the same IP to trigger rate limit (RPM = 2)
	reqR1 := httptest.NewRequest("GET", "/oauth/github/start?admin_token=secret-admin-pass", nil)
	reqR1.RemoteAddr = "127.0.0.1:1234"
	srv.oauthStartHandler(httptest.NewRecorder(), reqR1) // Request #1

	reqR2 := httptest.NewRequest("GET", "/oauth/github/start?admin_token=secret-admin-pass", nil)
	reqR2.RemoteAddr = "127.0.0.1:1234"
	srv.oauthStartHandler(httptest.NewRecorder(), reqR2) // Request #2

	reqR3 := httptest.NewRequest("GET", "/oauth/github/start?admin_token=secret-admin-pass", nil)
	reqR3.RemoteAddr = "127.0.0.1:1234"
	rrR3 := httptest.NewRecorder()
	srv.oauthStartHandler(rrR3, reqR3) // Request #3 -> triggers limit
	if rrR3.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 Too Many Requests, got %d", rrR3.Code)
	}
}

func TestServer_OAuthCallback_StateMismatch(t *testing.T) {
	cfg := &config.Config{
		HTTPAddr:           ":8080",
		TokenEncryptionKey: "YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWE=",
	}
	srv := New(cfg, &mockServerStore{}, nil)

	// Callback with unknown state
	req := httptest.NewRequest("GET", "/oauth/github/callback?code=abc&state=unknown", nil)
	rr := httptest.NewRecorder()
	srv.oauthCallbackHandler(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request on state mismatch, got %d", rr.Code)
	}
}

func TestServer_AdminEndpoints(t *testing.T) {
	cfg := &config.Config{
		HTTPAddr:   ":8080",
		AdminToken: "admin-pass",
	}
	store := &mockServerStore{
		accounts: []domain.GitHubAccount{
			{ID: 1, GitHubLogin: "reviewer-1", Status: "active"},
		},
	}
	srv := New(cfg, store, nil)

	// 1. List accounts - Unauthorized
	reqList := httptest.NewRequest("GET", "/admin/accounts", nil)
	rrList := httptest.NewRecorder()
	srv.adminAccountsHandler(rrList, reqList)
	if rrList.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized for list, got %d", rrList.Code)
	}

	// 2. List accounts - Success
	reqListOK := httptest.NewRequest("GET", "/admin/accounts?admin_token=admin-pass", nil)
	rrListOK := httptest.NewRecorder()
	srv.adminAccountsHandler(rrListOK, reqListOK)
	if rrListOK.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rrListOK.Code)
	}
	var accounts []domain.GitHubAccount
	_ = json.Unmarshal(rrListOK.Body.Bytes(), &accounts)
	if len(accounts) != 1 || accounts[0].GitHubLogin != "reviewer-1" {
		t.Errorf("expected reviewer-1 in list, got %+v", accounts)
	}

	// 3. Disable account - Success
	reqDisable := httptest.NewRequest("POST", "/admin/accounts/reviewer-1/disable?admin_token=admin-pass", nil)
	rrDisable := httptest.NewRecorder()
	srv.adminAccountsActionHandler(rrDisable, reqDisable)
	if rrDisable.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rrDisable.Code)
	}
	if store.statusLogin != "reviewer-1" || store.statusVal != domain.AccountStatusDisabled {
		t.Errorf("expected reviewer-1 status set to disabled, got %s -> %s", store.statusLogin, store.statusVal)
	}
}
