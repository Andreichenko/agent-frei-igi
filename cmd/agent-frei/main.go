package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"agent-frei-igi/internal/config"
	"agent-frei-igi/internal/store/postgres"
)

// Version constant defining the current release of the application.
// Can be overridden via ldflags in the future.
const Version = "0.1.0-dev"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]
	switch command {
	case "version":
		fmt.Printf("agent-frei version %s\n", Version)
	case "serve":
		runServer()
	case "migrate":
		runMigrations()
	default:
		fmt.Printf("Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}
}

// printUsage outputs details on how to run the binary and available commands.
func printUsage() {
	fmt.Println("Usage: agent-frei <command>")
	fmt.Println("Available commands:")
	fmt.Println("  version  Print version information")
	fmt.Println("  serve    Start HTTP webhook server")
	fmt.Println("  migrate  Apply database migrations")
}

// runMigrations initializes the store and runs all database migrations.
func runMigrations() {
	cfg := config.Load()
	if cfg.DatabaseURL == "" {
		log.Fatal("DATABASE_URL environment variable is not set")
	}

	store, err := postgres.New(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Failed to initialize database store: %v", err)
	}
	defer store.Close()

	log.Println("Starting database migrations...")
	if err := store.Migrate(context.Background()); err != nil {
		log.Fatalf("Migration execution failed: %v", err)
	}
	log.Println("Migrations executed successfully.")
}

// runServer starts the HTTP server listening on the address from configuration.
func runServer() {
	cfg := config.Load()

	// Register basic routes
	http.HandleFunc("/healthz", healthzHandler)

	log.Printf("Starting HTTP server on %s", cfg.HTTPAddr)
	if err := http.ListenAndServe(cfg.HTTPAddr, nil); err != nil {
		log.Fatalf("HTTP server failed to start: %v", err)
	}
}

// healthzHandler returns 200 OK with {"ok":true} JSON payload.
func healthzHandler(w http.ResponseWriter, r *http.Request) {
	// Only allow GET requests for the healthcheck
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
