package httpserver

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"context"

	"agent-frei-igi/internal/config"
	"agent-frei-igi/internal/domain"
	"agent-frei-igi/internal/queue"
)

// ServerStore specifies the database operations required by the Server.
type ServerStore interface {
	CountAccounts(ctx context.Context) (int, error)
	UpsertAccountToken(ctx context.Context, login string, tokenEnc []byte, expiresAt *time.Time) error
	ListAccounts(ctx context.Context) ([]domain.GitHubAccount, error)
	SetAccountStatus(ctx context.Context, login string, status domain.AccountStatus) error
}

// Server coordinates the HTTP routes and injects dependencies into handlers.
type Server struct {
	config   *config.Config
	store    ServerStore
	enqueuer *queue.Enqueuer

	stateMu  sync.Mutex
	states   map[string]time.Time

	limiterMu sync.Mutex
	limits    map[string][]time.Time
}

// New initializes a new Server instance.
func New(cfg *config.Config, store ServerStore, enqueuer *queue.Enqueuer) *Server {
	return &Server{
		config:   cfg,
		store:    store,
		enqueuer: enqueuer,
		states:   make(map[string]time.Time),
		limits:   make(map[string][]time.Time),
	}
}

// Start starts the HTTP server listening on the configured address.
func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.healthzHandler)
	mux.HandleFunc("/webhooks/github", s.githubWebhookHandler)
	mux.HandleFunc("/oauth/github/start", s.oauthStartHandler)
	mux.HandleFunc("/oauth/github/callback", s.oauthCallbackHandler)
	mux.HandleFunc("/admin/accounts", s.adminAccountsHandler)
	mux.HandleFunc("/admin/accounts/", s.adminAccountsActionHandler)

	log.Printf("Starting HTTP Webhook server on %s", s.config.HTTPAddr)
	return http.ListenAndServe(s.config.HTTPAddr, mux)
}

// healthzHandler handles basic liveness checks.
func (s *Server) healthzHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	response := map[string]bool{"ok": true}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Failed to encode healthz response: %v", err)
	}
}
