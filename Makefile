.PHONY: all build run test tidy compose-up compose-down clean

# Default target
all: build

# Go commands
build:
	@echo "Building binary..."
	go build -o bin/agent-frei ./cmd/agent-frei

run:
	@echo "Running HTTP server..."
	go run ./cmd/agent-frei serve

test:
	@echo "Running tests..."
	go test -v ./...

tidy:
	@echo "Tidying up Go modules..."
	go mod tidy

clean:
	@echo "Cleaning build artifacts..."
	rm -rf bin/

# Docker Compose commands
compose-up:
	@echo "Starting Docker Compose services..."
	docker compose up -d

compose-down:
	@echo "Stopping Docker Compose services..."
	docker compose down
