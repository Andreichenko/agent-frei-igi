package detective

import (
	"context"
	"os"
	"strings"
	"testing"

	"agent-frei-igi/internal/config"
	"agent-frei-igi/internal/domain"

	"github.com/google/uuid"
)

// fakeInstGetter mock implementation.
type fakeInstGetter struct {
	inst *domain.Installation
}

func (f *fakeInstGetter) GetInstallation(ctx context.Context, id int64) (*domain.Installation, error) {
	return f.inst, nil
}

func TestTruncatePRFiles(t *testing.T) {
	files := []domain.ChangedFile{
		{Path: "main.go", Status: "modified", Patch: "package main\n\nfunc main() {}"}, // 30 bytes
		{Path: "vendor/github.com/lib/foo.go", Status: "added", Patch: "func foo() {}"},
		{Path: "node_modules/npm/index.js", Status: "modified", Patch: "console.log()"},
		{Path: "helper.go", Status: "modified", Patch: "package helper"}, // 14 bytes
		{Path: "data.bin", Status: "added", Patch: ""}, // binary
		{Path: "extra.go", Status: "added", Patch: "func extra() {}"},
	}

	// Test 1: Skip paths and binary omit
	res, info := TruncatePRFiles(files, 10, 1000)
	if info.FilesTotal != 6 {
		t.Errorf("expected FilesTotal 6, got %d", info.FilesTotal)
	}
	if res[1].OmitReason != "skipped path" || !res[1].Omitted {
		t.Error("expected vendor file to be omitted as skipped path")
	}
	if res[2].OmitReason != "skipped path" || !res[2].Omitted {
		t.Error("expected node_modules file to be omitted as skipped path")
	}
	if res[4].OmitReason != "binary or empty patch" || !res[4].Omitted {
		t.Error("expected binary file to be marked as binary or empty patch")
	}
	if res[0].Language != "Go" {
		t.Errorf("expected language Go, got %s", res[0].Language)
	}

	// Test 2: Max files limit (limit = 2 non-skipped files)
	// non-skipped files are: main.go (included 1), helper.go (included 2), data.bin (omitted binary, included 3), extra.go (omitted limit)
	resFiles, infoFiles := TruncatePRFiles(files, 2, 1000)
	if infoFiles.FilesIncluded != 2 {
		t.Errorf("expected 2 files included under limit, got %d", infoFiles.FilesIncluded)
	}
	if !resFiles[5].Omitted || resFiles[5].OmitReason != "max files limit exceeded" {
		t.Errorf("expected extra.go to be omitted due to file limit, got omitted=%v, reason=%q", resFiles[5].Omitted, resFiles[5].OmitReason)
	}

	// Test 3: Max patch bytes limit (limit = 35 bytes)
	// main.go (28 bytes) -> included.
	// helper.go (14 bytes) -> partially truncated (7 bytes kept), total kept = 35 bytes.
	// extra.go (15 bytes) -> fully omitted due to budget.
	resBytes, infoBytes := TruncatePRFiles(files, 10, 35)
	if infoBytes.PatchBytesKept != 35 {
		t.Errorf("expected exactly 35 bytes kept, got %d", infoBytes.PatchBytesKept)
	}
	if !infoBytes.TruncatedByBytes {
		t.Error("expected TruncatedByBytes to be true")
	}
	if resBytes[3].OmitReason != "partially truncated due to patch bytes limit" {
		t.Errorf("expected partial truncation reason, got %q", resBytes[3].OmitReason)
	}
	if len(resBytes[3].Patch) != 7 {
		t.Errorf("expected partial patch of 7 bytes, got %d", len(resBytes[3].Patch))
	}
	if resBytes[5].OmitReason != "max patch bytes limit exceeded" || resBytes[5].Patch != "" {
		t.Errorf("expected extra.go to be completely omitted by bytes, got reason=%q, patch=%q", resBytes[5].OmitReason, resBytes[5].Patch)
	}
}

func TestDetective_Build_InvalidRepoName(t *testing.T) {
	cfg := &config.Config{
		GitHubAppID:             "123",
		GitHubAppPrivateKeyPath: "nonexistent.pem",
	}
	// Stub write a fake pem file to bypass fast check
	_ = os.WriteFile("nonexistent.pem", []byte("-----BEGIN RSA PRIVATE KEY-----\nfake\n-----END RSA PRIVATE KEY-----"), 0644)
	defer os.Remove("nonexistent.pem")

	db := &fakeInstGetter{
		inst: &domain.Installation{GitHubInstallationID: 999},
	}
	d := New(cfg, db, nil)

	// job with invalid repo format
	job := &domain.ReviewJob{
		ID:             uuid.New(),
		InstallationID: 1,
		RepoFullName:   "single-name-no-slash",
		PRNumber:       1,
	}

	_, err := d.Build(context.Background(), job)
	if err == nil {
		t.Fatal("expected error on invalid repository name, got nil")
	}
	if !strings.Contains(err.Error(), "invalid repo_full_name format") {
		t.Errorf("unexpected error: %v", err)
	}
}
