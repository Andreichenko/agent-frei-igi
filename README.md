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
