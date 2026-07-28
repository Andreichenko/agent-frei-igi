package httpserver

import (
	"encoding/json"
	"io"
	"log"
	"net/http"

	"agent-frei-igi/internal/githubapp/webhook"
)

// githubWebhookHandler processes incoming webhooks from GitHub App.
func (s *Server) githubWebhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// 1. Extract headers
	event := r.Header.Get("X-GitHub-Event")
	deliveryID := r.Header.Get("X-GitHub-Delivery")
	signature := r.Header.Get("X-Hub-Signature-256")

	// 2. Validate webhook secret configuration
	if s.config.WebhookSecret == "" {
		log.Printf("[ERROR] GITHUB_WEBHOOK_SECRET environment variable is not configured on this server")
		http.Error(w, "Webhook secret not configured", http.StatusInternalServerError)
		return
	}

	// Read raw body bytes to verify HMAC signature
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("[ERROR] Failed to read request body: %v", err)
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	// 3. Verify webhook signature
	if !webhook.VerifySignature(body, signature, s.config.WebhookSecret) {
		log.Printf("[WARN] Webhook signature verification failed for delivery: %s", deliveryID)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// 4. Verify database state (safe-fail after signature validation to prevent probing)
	if s.store == nil {
		log.Printf("[WARN] Webhook received but database is not configured on this instance")
		http.Error(w, "Database not configured", http.StatusServiceUnavailable)
		return
	}

	// 5. Parse JSON payload
	var payload webhook.WebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		log.Printf("[ERROR] Failed to unmarshal webhook payload: %v", err)
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	// 6. Handle event inside queue
	ctx := r.Context()
	decision, err := s.enqueuer.HandleEvent(ctx, event, &payload)
	if err != nil {
		log.Printf("[ERROR] Failed to process webhook event: %v", err)
		http.Error(w, "Internal event handler failure", http.StatusInternalServerError)
		return
	}

	// 7. Log outcomes
	repo := "unknown"
	if payload.Repository != nil {
		repo = payload.Repository.FullName
	}
	prNum := 0
	if payload.PullRequest != nil {
		prNum = payload.PullRequest.Number
	} else if payload.Issue != nil {
		prNum = payload.Issue.Number
	}

	log.Printf("[WEBHOOK] event=%s delivery=%s repo=%s pr=%d decision=%s",
		event, deliveryID, repo, prNum, decision)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}
