package llm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// createFakeBinary creates a temporary executable shell script to act as a fake CLI.
func createFakeBinary(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)

	script := `#!/bin/sh
# read from stdin
read -r STDIN_DATA

case "$STDIN_DATA" in
  *rate-limit*)
    echo "429: rate limit exceeded" >&2
    exit 1
    ;;
  *auth-dead*)
    echo "unauthorized session, please login" >&2
    exit 1
    ;;
  *sleep*)
    sleep 5
    echo '{"verdict": "ok-after-sleep"}'
    ;;
  *)
    echo '{"verdict": "ok"}'
    ;;
esac
`
	err := os.WriteFile(path, []byte(script), 0755)
	if err != nil {
		t.Fatalf("failed to write fake CLI binary: %v", err)
	}

	return path
}

func TestCLIProvider_Success(t *testing.T) {
	binPath := createFakeBinary(t, "fake-agy")
	p := NewCLIProvider("agy", binPath, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stdout, err := p.Complete(ctx, "hello")
	if err != nil {
		t.Fatalf("expected success, got err: %v", err)
	}

	trimmed := strings.TrimSpace(stdout)
	if trimmed != `{"verdict": "ok"}` {
		t.Errorf("unexpected stdout: %q", trimmed)
	}
}

func TestCLIProvider_RateLimit(t *testing.T) {
	binPath := createFakeBinary(t, "fake-agy")
	p := NewCLIProvider("agy", binPath, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := p.Complete(ctx, "trigger-rate-limit")
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("expected ErrRateLimited, got: %v", err)
	}
}

func TestCLIProvider_SessionDead(t *testing.T) {
	binPath := createFakeBinary(t, "fake-agy")
	p := NewCLIProvider("agy", binPath, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := p.Complete(ctx, "trigger-auth-dead")
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !errors.Is(err, ErrSessionDead) {
		t.Errorf("expected ErrSessionDead, got: %v", err)
	}
}

type mockProvider struct {
	name      string
	stdout    string
	err       error
	calledWith string
}

func (m *mockProvider) Name() string {
	return m.name
}

func (m *mockProvider) Complete(ctx context.Context, prompt string) (string, error) {
	m.calledWith = prompt
	return m.stdout, m.err
}

func TestRouter_Fallback(t *testing.T) {
	primary := &mockProvider{
		name: "agy",
		err:  ErrRateLimited,
	}
	fallback := &mockProvider{
		name:   "grok",
		stdout: `{"verdict": "ok-fallback"}`,
	}

	router := NewRouter(primary, fallback)
	ctx := context.Background()

	stdout, modelUsed, attempts, err := router.Complete(ctx, "hello-prompt")
	if err != nil {
		t.Fatalf("expected router success on fallback, got err: %v", err)
	}

	if modelUsed != "grok" {
		t.Errorf("expected modelUsed to be grok, got %s", modelUsed)
	}

	if len(attempts) != 2 || attempts[0] != "agy:fail" || attempts[1] != "grok:ok" {
		t.Errorf("unexpected attempts layout: %v", attempts)
	}

	if stdout != `{"verdict": "ok-fallback"}` {
		t.Errorf("unexpected stdout: %s", stdout)
	}
}

func TestCLIProvider_Timeout(t *testing.T) {
	binPath := createFakeBinary(t, "fake-agy")
	p := NewCLIProvider("agy", binPath, nil)

	// Set tiny timeout (50ms) to trigger deadline exceeded
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := p.Complete(ctx, "trigger-sleep")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}

	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "context deadline exceeded") && !errors.Is(err, context.Canceled) {
		t.Errorf("expected deadline exceeded or canceled error, got: %v", err)
	}
}
