package httpserver

import (
	"encoding/json"
	"log"
	"net/http"

	"agent-frei-igi/internal/config"
	"agent-frei-igi/internal/queue"
	"agent-frei-igi/internal/store/postgres"
)

// Server coordinates the HTTP routes and injects dependencies into handlers.
type Server struct {
	config   *config.Config
	store    *postgres.Store
	enqueuer *queue.Enqueuer
}

// New initializes a new Server instance.
func New(cfg *config.Config, store *postgres.Store, enqueuer *queue.Enqueuer) *Server {
	return &Server{
		config:   cfg,
		store:    store,
		enqueuer: enqueuer,
	}
}

// Start starts the HTTP server listening on the configured address.
func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.healthzHandler)
	mux.HandleFunc("/webhooks/github", s.githubWebhookHandler)

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
