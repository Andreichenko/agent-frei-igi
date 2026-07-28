package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"agent-frei-igi/internal/config"
	"agent-frei-igi/internal/httpserver"
	"agent-frei-igi/internal/queue"
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

	var store *postgres.Store
	var err error

	// Database is optional for running serve, but required to handle webhooks successfully
	if cfg.DatabaseURL != "" {
		store, err = postgres.New(cfg.DatabaseURL)
		if err != nil {
			log.Fatalf("Failed to initialize database store: %v", err)
		}
		defer store.Close()
		log.Println("Database connection established for server.")
	} else {
		log.Println("[WARN] DATABASE_URL is not set. Webhook events requiring DB operations will fail with 503.")
	}

	enqueuer := queue.NewEnqueuer(store, cfg.ReviewerLogins)
	srv := httpserver.New(cfg, store, enqueuer)

	if err := srv.Start(); err != nil {
		log.Fatalf("HTTP server failed: %v", err)
	}
}
