package httpserver

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"agent-frei-igi/internal/crypto"
	"agent-frei-igi/internal/domain"
)

// getClientIP extracts client IP address from headers or connection metadata.
func getClientIP(r *http.Request) string {
	xff := r.Header.Get("X-Forwarded-For")
	if xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	addr := r.RemoteAddr
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		return addr[:idx]
	}
	return addr
}

// allowIPRateLimit evaluates whether the client IP request rate is under the limit (RPM).
func (s *Server) allowIPRateLimit(ip string, rpm int) bool {
	s.limiterMu.Lock()
	defer s.limiterMu.Unlock()

	now := time.Now()
	cutoff := now.Add(-1 * time.Minute)

	// Clean up old entries
	var active []time.Time
	for _, t := range s.limits[ip] {
		if t.After(cutoff) {
			active = append(active, t)
		}
	}

	if len(active) >= rpm {
		s.limits[ip] = active
		return false
	}

	active = append(active, now)
	s.limits[ip] = active
	return true
}

// validateAdmin checks if the shared admin token matches the request query or header.
func (s *Server) validateAdmin(r *http.Request) bool {
	if s.config.AdminToken == "" {
		return false
	}
	token := r.URL.Query().Get("admin_token")
	if token == "" {
		token = r.Header.Get("X-Admin-Token")
	}
	return token == s.config.AdminToken
}

// oauthStartHandler initiates the OAuth sequence by checking limits, generating state, and redirecting.
func (s *Server) oauthStartHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// 1. Validate admin privilege
	if !s.validateAdmin(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// 2. Enforce rate limiting
	ip := getClientIP(r)
	if !s.allowIPRateLimit(ip, s.config.AdminOAuthRPM) {
		http.Error(w, "Rate limit exceeded (Too Many Requests)", http.StatusTooManyRequests)
		return
	}

	ctx := r.Context()

	// 3. Reject if maximum reviewer accounts reached
	count, err := s.store.CountAccounts(ctx)
	if err != nil {
		log.Printf("[ERROR] Failed to count active reviewer accounts: %v", err)
		http.Error(w, "Internal database error", http.StatusInternalServerError)
		return
	}
	if count >= s.config.MaxReviewerAccounts {
		http.Error(w, "Maximum reviewer accounts reached", http.StatusForbidden)
		return
	}

	// 4. Generate random state with 10-minute TTL
	stateBytes := make([]byte, 16)
	_, _ = rand.Read(stateBytes)
	state := hex.EncodeToString(stateBytes)

	s.stateMu.Lock()
	s.states[state] = time.Now()
	s.stateMu.Unlock()

	// 5. Build authorization URL and redirect
	authURL := fmt.Sprintf("https://github.com/login/oauth/authorize?client_id=%s&redirect_uri=%s&scope=%s&state=%s",
		url.QueryEscape(s.config.GitHubOAuthClientID),
		url.QueryEscape(s.config.GitHubOAuthRedirectURL),
		url.QueryEscape(s.config.GitHubOAuthScopes),
		url.QueryEscape(state),
	)

	http.Redirect(w, r, authURL, http.StatusTemporaryRedirect)
}

// oauthCallbackHandler processes the callback code, exchanges it for token, decrypts/encrypts and upserts.
func (s *Server) oauthCallbackHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()

	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	if code == "" || state == "" {
		http.Error(w, "Missing code or state parameter", http.StatusBadRequest)
		return
	}

	// 1. Verify CSRF state and clean it up (one-time use)
	s.stateMu.Lock()
	created, ok := s.states[state]
	if ok {
		delete(s.states, state)
	}
	s.stateMu.Unlock()

	if !ok || time.Since(created) > 10*time.Minute {
		http.Error(w, "State mismatch or state expired", http.StatusBadRequest)
		return
	}

	// 2. Exchange code for access token
	tokenURL := "https://github.com/login/oauth/access_token"
	form := url.Values{}
	form.Set("client_id", s.config.GitHubOAuthClientID)
	form.Set("client_secret", s.config.GitHubOAuthClientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", s.config.GitHubOAuthRedirectURL)
	form.Set("state", state)

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		http.Error(w, "Failed to create token exchange request", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("[ERROR] GitHub token exchange HTTP call failed: %v", err)
		http.Error(w, "Token exchange failed", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		log.Printf("[ERROR] GitHub token exchange returned status %d: %s", resp.StatusCode, string(bodyBytes))
		http.Error(w, "GitHub token exchange returned error status", http.StatusInternalServerError)
		return
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		http.Error(w, "Failed to decode response from GitHub", http.StatusInternalServerError)
		return
	}

	if tokenResp.Error != "" {
		log.Printf("[ERROR] GitHub token exchange returned logic error: %s (%s)", tokenResp.Error, tokenResp.ErrorDesc)
		http.Error(w, fmt.Sprintf("OAuth error: %s", tokenResp.ErrorDesc), http.StatusBadRequest)
		return
	}

	if tokenResp.AccessToken == "" {
		http.Error(w, "No access token returned from GitHub", http.StatusInternalServerError)
		return
	}

	// 3. Fetch user info using token to resolve login name
	userURL := "https://api.github.com/user"
	userReq, err := http.NewRequestWithContext(ctx, "GET", userURL, nil)
	if err != nil {
		http.Error(w, "Failed to create user info request", http.StatusInternalServerError)
		return
	}
	userReq.Header.Set("Authorization", "Bearer "+tokenResp.AccessToken)
	userReq.Header.Set("Accept", "application/vnd.github+json")
	userReq.Header.Set("User-Agent", "agent-frei-igi")

	userResp, err := http.DefaultClient.Do(userReq)
	if err != nil {
		log.Printf("[ERROR] Failed to fetch user info: %v", err)
		http.Error(w, "Failed to fetch user info", http.StatusInternalServerError)
		return
	}
	defer userResp.Body.Close()

	if userResp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(userResp.Body)
		log.Printf("[ERROR] User info returned status %d: %s", userResp.StatusCode, string(bodyBytes))
		http.Error(w, "Failed to get user profile details", http.StatusInternalServerError)
		return
	}

	var userPayload struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(userResp.Body).Decode(&userPayload); err != nil {
		http.Error(w, "Failed to parse user profile payload", http.StatusInternalServerError)
		return
	}

	if userPayload.Login == "" {
		http.Error(w, "Empty username login returned from GitHub profile", http.StatusInternalServerError)
		return
	}

	// 4. Encrypt token using AES-GCM and stored key
	key, err := s.config.ParseTokenKey()
	if err != nil {
		log.Printf("[ERROR] Encryption key parsing failed: %v", err)
		http.Error(w, "Cryptographic key error", http.StatusInternalServerError)
		return
	}

	encrypted, err := crypto.Encrypt(key, []byte(tokenResp.AccessToken))
	if err != nil {
		log.Printf("[ERROR] Token encryption failed: %v", err)
		http.Error(w, "Token encryption failed", http.StatusInternalServerError)
		return
	}

	// 5. Upsert token record in DB
	err = s.store.UpsertAccountToken(ctx, userPayload.Login, encrypted, nil)
	if err != nil {
		log.Printf("[ERROR] UpsertAccountToken failed: %v", err)
		http.Error(w, "Failed to upsert reviewer account", http.StatusInternalServerError)
		return
	}

	log.Printf("Successfully authenticated reviewer account %s via OAuth", userPayload.Login)

	// 6. Respond with a terminal-themed success page
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	html := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
    <title>Authentication Success</title>
    <style>
        body { background: #0f0f0f; color: #39ff14; font-family: monospace; display: flex; justify-content: center; align-items: center; height: 100vh; margin: 0; }
        .box { border: 1px solid #39ff14; padding: 30px; border-radius: 4px; box-shadow: 0 0 15px rgba(57, 255, 20, 0.2); max-width: 400px; text-align: center; }
        h1 { font-size: 1.2rem; margin-bottom: 20px; letter-spacing: 2px; }
        p { font-size: 1rem; color: #a0a0a0; line-height: 1.5; }
        .user { color: #fff; font-weight: bold; background: #222; padding: 2px 8px; border-radius: 2px; }
    </style>
</head>
<body>
    <div class="box">
        <h1>[ AUTHENTICATION SUCCESS ]</h1>
        <p>Reviewer <span class="user">%s</span> has been successfully authenticated via OAuth.</p>
        <p style="font-size: 0.8rem; color: #666; margin-top: 20px;">Credentials encrypted and stored securely in reviewer pool.</p>
    </div>
</body>
</html>`, userPayload.Login)
	_, _ = w.Write([]byte(html))
}

// adminAccountsHandler lists all accounts (logins, status, last_used_at, no tokens).
func (s *Server) adminAccountsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// 1. Validate admin privilege
	if !s.validateAdmin(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ctx := r.Context()

	// 2. Fetch list
	list, err := s.store.ListAccounts(ctx)
	if err != nil {
		log.Printf("[ERROR] ListAccounts failed: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(list)
}

// adminAccountsActionHandler dispatches actions like disable for specific account paths.
func (s *Server) adminAccountsActionHandler(w http.ResponseWriter, r *http.Request) {
	// Path should be like /admin/accounts/{login}/disable
	path := strings.TrimPrefix(r.URL.Path, "/admin/accounts/")
	parts := strings.Split(path, "/")

	if len(parts) != 2 || parts[1] != "disable" {
		http.NotFound(w, r)
		return
	}

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// 1. Validate admin privilege
	if !s.validateAdmin(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	login := parts[0]
	ctx := r.Context()

	// 2. Disable account
	err := s.store.SetAccountStatus(ctx, login, domain.AccountStatusDisabled)
	if err != nil {
		log.Printf("[ERROR] SetAccountStatus failed for %s: %v", login, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.Printf("Reviewer account %s manually disabled by admin", login)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}
