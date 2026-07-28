# agent-frei-igi

`agent-frei-igi` is a self-hosted AI-driven Code Review Agent for GitHub Pull Requests. It accepts webhooks from GitHub, coordinates context collection, runs reviews via local CLI models (agy / grok) with fallback (not cloud API keys for the LLM path), and publishes reviews back to the PRs.

The architecture follows a modular monolith approach in Go using an asynchronous job pipeline.

## Getting Started

### Prerequisites

- Go 1.26+
- Docker & Docker Compose

### Environment Setup

Copy the example configuration to `.env`:

```bash
cp configs/example.env .env
```

Ensure you generate a secure 32-byte Base64 key for `TOKEN_ENCRYPTION_KEY`.

### Running the Database

Start PostgreSQL using Docker Compose:

```bash
make compose-up
# Or: docker compose up -d
```

### Running Migrations

Before starting the server, apply database migrations to initialize the schema:

```bash
# Apply database migrations
go run ./cmd/agent-frei migrate
```

Ensure `DATABASE_URL` is configured in your `.env` file.

### Running the Server

Start the HTTP Webhook server locally:

```bash
make run
# Or: go run ./cmd/agent-frei serve
```

### Verifying Status

You can verify the HTTP server status using the `/healthz` endpoint:

```bash
curl -s http://localhost:8080/healthz
# Expected output: {"ok":true}
```

### Running Tests

Run the test suite using:

```bash
make test
# Or: go test -v ./...
```

## GitHub App Webhook Setup

To receive events from GitHub, configure a Webhook in your GitHub App settings pointing to:
`https://<your-host-address>/webhooks/github`

### Subscribed Events

Ensure you subscribe to the following events in the GitHub App configuration:
- **`Installation`** (to handle app install, suspend, or delete events)
- **`Pull request`** (to process code changes, assignments, and draft states)
- **`Issue comment`** (to trigger actions via `@mention` on pull requests)

### Required Environment Variables

Configure these variables in your `.env` file:
- **`GITHUB_WEBHOOK_SECRET`**: The secret used to secure and sign the webhooks payload. Incoming unsigned or incorrectly signed requests will be rejected with 401.
- **`REVIEWER_LOGINS`**: Comma-separated list of reviewer accounts in priority order (e.g. `alice,bob,carol`).

> [!NOTE]
> For this milestone, when a webhook triggers a review, the agent updates installation states and schedules a `pending` job in the Postgres queue. The agent does not publish reviews back to GitHub yet.

## Background Worker

The review pipeline is processed asynchronously by a separate `worker` process. The worker claims jobs from PostgreSQL (using safe `FOR UPDATE SKIP LOCKED` locks), maintains execution leases, and handles graceful shutdowns.

### Running the Worker

To run the full stack, you need to execute migrations first, and then run `serve` and `worker` in two separate processes:

```bash
# 1. Apply database migrations
./agent-frei migrate

# 2. Run the HTTP webhook server (Process 1)
./agent-frei serve

# 3. Run the background worker (Process 2)
./agent-frei worker
```

### Worker Configuration

The worker can be customized using the following environment variables:
- **`WORKER_CONCURRENCY`**: Number of parallel jobs a single worker process can execute concurrently (default: `1`).
- **`WORKER_LEASE`**: Duration a job lock remains valid before it can be reclaimed by other workers if this worker crashes (default: `15m`).
- **`WORKER_HEARTBEAT_INTERVAL`**: How often the worker extends its lease on running jobs (default: `30s`).
- **`WORKER_POLL_INTERVAL`**: How long to sleep when the database queue is empty (default: `2s`).
- **`WORKER_ID`**: Unique identifier for this worker process. If empty, defaults to `<hostname>-<pid>`.
- **`SHUTDOWN_TIMEOUT`**: Timeout for active jobs to finish executing during graceful shutdown (default: `60s`).

> [!NOTE]
> Currently, the worker runs a **stub pipeline** showing the lifecycle stages (`detective` ➡️ `memory` ➡️ `critic` ➡️ `diplomat`) and records a stub result JSON in the database. It does not invoke the LLM or publish comments to GitHub yet.

## Secrets and Token Encryption

Reviewer account access tokens are stored in the database as encrypted bytes (`access_token_enc`) using the AES-256-GCM encryption algorithm.

### Key Requirements

The system requires a secret key defined via the `TOKEN_ENCRYPTION_KEY` environment variable.
- The key must be a valid **Base64 encoded string** that decodes to **exactly 32 raw bytes** (256 bits).
- Generating a new key:
  ```bash
  openssl rand -base64 32
  ```

> [!CAUTION]
> **Keep your encryption key safe!** If you lose or rotate the `TOKEN_ENCRYPTION_KEY`, all previously encrypted GitHub tokens stored in the database will become unrecoverable, and you will need to re-authenticate all accounts.

## Project Design & Plans

In accordance with local repository guardrails, the planning files (`PLAN.md`) and system design documents (`design/`) are excluded from Git and kept locally.
