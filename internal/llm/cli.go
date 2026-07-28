package llm

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"syscall"
)

// CLIProvider executes a local CLI binary (like agy/grok) and captures output.
type CLIProvider struct {
	name string
	bin  string
	args []string
}

// NewCLIProvider initializes a new CLI runner provider.
func NewCLIProvider(name, bin string, args []string) *CLIProvider {
	return &CLIProvider{
		name: name,
		bin:  bin,
		args: args,
	}
}

// Name returns the provider's identifier.
func (p *CLIProvider) Name() string {
	return p.name
}

// Complete runs the CLI command with the prompt written to stdin.
func (p *CLIProvider) Complete(ctx context.Context, prompt string) (string, error) {
	cmdArgs := append([]string{}, p.args...)
	cmd := exec.CommandContext(ctx, p.bin, cmdArgs...)

	// Enable process group attributes to kill descendant processes on timeout
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer

	// Cap memory output buffers at 10 MiB to prevent RAM bloat
	limitStdout := &limitWriter{w: &stdoutBuf, maxBytes: 10 * 1024 * 1024}
	limitStderr := &limitWriter{w: &stderrBuf, maxBytes: 10 * 1024 * 1024}

	cmd.Stdout = limitStdout
	cmd.Stderr = limitStderr

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("failed to create stdin pipe for CLI: %w", err)
	}

	// Start command execution
	if err := cmd.Start(); err != nil {
		return "", p.classifyError(err, "")
	}

	// Pipe the prompt asynchronously to stdin
	go func() {
		defer stdinPipe.Close()
		_, _ = io.WriteString(stdinPipe, prompt)
	}()

	// Wait for the command to finish. Wait handles ctx cancellations internally.
	err = cmd.Wait()

	stdoutStr := stdoutBuf.String()
	stderrStr := stderrBuf.String()

	if err != nil {
		// Clean up process group if hung/failed
		pgid, pgidErr := syscall.Getpgid(cmd.Process.Pid)
		if pgidErr == nil {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		}

		return "", p.classifyError(err, stderrStr)
	}

	return stdoutStr, nil
}

// limitWriter intercepts writing to cap the memory buffers.
type limitWriter struct {
	w        io.Writer
	written  int
	maxBytes int
}

func (l *limitWriter) Write(p []byte) (n int, err error) {
	if l.written >= l.maxBytes {
		return len(p), nil // Silently discard extra bytes
	}
	remaining := l.maxBytes - l.written
	toWrite := len(p)
	if toWrite > remaining {
		toWrite = remaining
	}
	n, err = l.w.Write(p[:toWrite])
	l.written += n
	if err == nil && len(p) > toWrite {
		return len(p), nil
	}
	return n, err
}
