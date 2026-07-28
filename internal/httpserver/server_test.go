package httpserver

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"agent-frei-igi/internal/config"
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
