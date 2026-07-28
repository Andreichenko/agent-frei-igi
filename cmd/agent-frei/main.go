package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"agent-frei-igi/internal/config"
	"agent-frei-igi/internal/crypto"
	"agent-frei-igi/internal/critic"
	"agent-frei-igi/internal/detective"
	"agent-frei-igi/internal/diplomat"
	"agent-frei-igi/internal/githubapp"
	"agent-frei-igi/internal/httpserver"
	"agent-frei-igi/internal/llm"
	"agent-frei-igi/internal/queue"
	"agent-frei-igi/internal/store/postgres"
	"agent-frei-igi/internal/worker"
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
	case "worker":
		runWorker()
	case "seed-account":
		runSeedAccount()
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
	fmt.Println("  version       Print version information")
	fmt.Println("  serve         Start HTTP webhook server")
	fmt.Println("  migrate       Apply database migrations")
	fmt.Println("  worker        Start background review job worker")
	fmt.Println("  seed-account  Seed a reviewer account with encrypted token")
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

// runWorker starts the background job execution worker loop.
func runWorker() {
	cfg := config.Load()
	if cfg.DatabaseURL == "" {
		log.Fatal("DATABASE_URL environment variable is not set")
	}

	store, err := postgres.New(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Failed to initialize database store: %v", err)
	}
	defer store.Close()

	var primary, fallback llm.Provider

	// Resolve primary provider
	if cfg.LLMProvider == "grok" {
		primary = llm.NewCLIProvider("grok", cfg.GrokBin, []string{"--single"})
	} else {
		primary = llm.NewCLIProvider("agy", cfg.AgyBin, []string{"--print"})
	}

	// Resolve fallback provider
	if cfg.LLMFallbackProvider == "grok" {
		fallback = llm.NewCLIProvider("grok", cfg.GrokBin, []string{"--single"})
	} else if cfg.LLMFallbackProvider == "agy" {
		fallback = llm.NewCLIProvider("agy", cfg.AgyBin, []string{"--print"})
	}

	var pemBytes []byte
	if cfg.GitHubAppPrivateKeyPath != "" {
		var err error
		pemBytes, err = os.ReadFile(cfg.GitHubAppPrivateKeyPath)
		if err != nil {
			log.Printf("[WARN] Failed to read private key for GitHub App fallback bot: %v", err)
		}
	}

	ghClient := githubapp.NewClient(cfg.GitHubAPIBaseURL, cfg.GitHubAppID, pemBytes)
	dip := diplomat.New(cfg, store, ghClient)

	router := llm.NewRouter(primary, fallback)
	crit := critic.New(cfg, router)
	det := detective.New(cfg, store)
	w := worker.NewWorker(cfg, store, det, crit, dip)

	// Setup context that is cancelled on SIGINT/SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Println("[WORKER] Worker started. Press Ctrl+C to shut down gracefully.")
	w.Start(ctx)
	log.Println("[WORKER] Graceful shutdown finished.")
}

// runSeedAccount encrypts a GitHub personal access token and stores it in the database reviewer pool.
func runSeedAccount() {
	var login, token string
	for i := 2; i < len(os.Args); i++ {
		if os.Args[i] == "--login" && i+1 < len(os.Args) {
			login = os.Args[i+1]
			i++
		} else if os.Args[i] == "--token" && i+1 < len(os.Args) {
			token = os.Args[i+1]
			i++
		}
	}

	if login == "" || token == "" {
		log.Fatal("Usage: agent-frei seed-account --login <login> --token <token>")
	}

	cfg := config.Load()
	if cfg.DatabaseURL == "" {
		log.Fatal("DATABASE_URL environment variable is not set")
	}

	key, err := cfg.ParseTokenKey()
	if err != nil {
		log.Fatalf("Invalid TOKEN_ENCRYPTION_KEY config: %v", err)
	}

	encryptedToken, err := crypto.Encrypt(key, []byte(token))
	if err != nil {
		log.Fatalf("Failed to encrypt access token: %v", err)
	}

	store, err := postgres.New(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Failed to initialize database store: %v", err)
	}
	defer store.Close()

	query := `
		INSERT INTO github_accounts (github_login, access_token_enc, status, updated_at)
		VALUES ($1, $2, 'active', NOW())
		ON CONFLICT (github_login) DO UPDATE
		SET access_token_enc = EXCLUDED.access_token_enc, status = 'active', updated_at = NOW()
	`

	_, err = store.DB().ExecContext(context.Background(), query, login, encryptedToken)
	if err != nil {
		log.Fatalf("Failed to seed account to database: %v", err)
	}

	log.Printf("Successfully seeded account: %s (token encrypted and stored in reviewer pool)", login)
}
