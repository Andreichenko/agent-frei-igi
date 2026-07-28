package critic

import (
	"context"
	"strings"
	"testing"

	"agent-frei-igi/internal/config"
	"agent-frei-igi/internal/domain"
	"agent-frei-igi/internal/llm"
)

func TestParseAndValidate(t *testing.T) {
	reviewCtx := &domain.ReviewContext{
		Files: []domain.ChangedFile{
			{Path: "main.go"},
			{Path: "helper.go"},
		},
	}

	rawJSON := `
	{
		"verdict": "Needs improvement",
		"summary": "Found code quality issues.",
		"findings": [
			{
				"severity": "error",
				"category": "bug",
				"path": "main.go",
				"start_line": 10,
				"end_line": 15,
				"title": "nil check missing",
				"body": "should check nil before access",
				"confidence": 0.9
			},
			{
				"severity": "warning",
				"category": "style",
				"path": "nonexistent.go",
				"start_line": 1,
				"end_line": 2,
				"title": "bad path",
				"body": "this file is not in PR",
				"confidence": 0.8
			},
			{
				"severity": "info",
				"category": "performance",
				"path": "helper.go",
				"start_line": 5,
				"end_line": 10,
				"title": "low confidence",
				"body": "just a guess",
				"confidence": 0.3
			},
			{
				"severity": "invalid-severity",
				"category": "invalid-category",
				"path": "main.go",
				"start_line": 20,
				"end_line": 19,
				"title": "invalid line logic",
				"body": "end_line before start_line",
				"confidence": 0.7
			}
		]
	}
	`

	// Clean markdown block test
	rawJSONWithFences := "```json\n" + rawJSON + "\n```"

	res, dropped, err := ParseAndValidate(rawJSONWithFences, reviewCtx, 0.55)
	if err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	if res.Verdict != "Needs improvement" {
		t.Errorf("expected verdict 'Needs improvement', got %q", res.Verdict)
	}

	// 1 finding should be accepted: main.go start_line=10
	// 3 should be dropped:
	// - nonexistent.go (bad path)
	// - helper.go confidence 0.3 < 0.55 (low confidence)
	// - main.go start_line=20 end_line=19 (invalid line logic)
	if len(res.Findings) != 1 {
		t.Errorf("expected 1 accepted finding, got %d", len(res.Findings))
	} else {
		f := res.Findings[0]
		if f.Path != "main.go" || f.StartLine != 10 {
			t.Errorf("unexpected accepted finding: %+v", f)
		}
	}

	if dropped.BadPath != 1 {
		t.Errorf("expected 1 bad path, got %d", dropped.BadPath)
	}
	if dropped.LowConfidence != 1 {
		t.Errorf("expected 1 low confidence, got %d", dropped.LowConfidence)
	}
	if dropped.Invalid != 1 {
		t.Errorf("expected 1 invalid line logic, got %d", dropped.Invalid)
	}
}

func TestGenerateFingerprint(t *testing.T) {
	fp1 := GenerateFingerprint("main.go", "Fix nil check", "bug")
	fp2 := GenerateFingerprint("main.go", "fix nil check  ", "bug") // trailing spaces / casing should normalize
	fp3 := GenerateFingerprint("main.go", "Different title", "bug")

	if fp1 != fp2 {
		t.Errorf("expected stable fingerprint on normalized parameters, got %s and %s", fp1, fp2)
	}
	if fp1 == fp3 {
		t.Errorf("expected different fingerprints for different titles")
	}
}

func TestBuildPrompt_Budget(t *testing.T) {
	reviewCtx := &domain.ReviewContext{
		RepoFullName: "owner/repo",
		PRNumber:     42,
		HeadSHA:      "headsha123",
		Files: []domain.ChangedFile{
			{Path: "main.go", Status: "modified", Patch: "package main\n\nfunc main() {}"},
			{Path: "helper.go", Status: "added", Patch: "package main\n\nfunc helper() {}"},
		},
	}

	// Budget that allows both (generous)
	p1 := BuildPrompt(reviewCtx, 1000)
	if !strings.Contains(p1, "File: main.go") || !strings.Contains(p1, "File: helper.go") {
		t.Error("expected both files to be listed")
	}
	if !strings.Contains(p1, "func main()") || !strings.Contains(p1, "func helper()") {
		t.Error("expected both file patches to be included")
	}

	// Tiny budget that forces helper.go patch to be omitted (estimated budget = 50 tokens ~ 200 chars)
	// System prompt + main.go exceeds 200 chars, so helper.go patch will be omitted.
	p2 := BuildPrompt(reviewCtx, 50)
	if !strings.Contains(p2, "File: helper.go") {
		t.Error("expected helper.go to be listed")
	}
	if strings.Contains(p2, "func helper()") {
		t.Error("expected helper.go patch to be omitted due to budget limit")
	}
	if !strings.Contains(p2, "patch omitted due to prompt budget limit") {
		t.Error("expected omission reason to be present in prompt")
	}
}

type mockLLM struct {
	stdout string
	err    error
}

func (m *mockLLM) Name() string {
	return "mock"
}

func (m *mockLLM) Complete(ctx context.Context, prompt string) (string, error) {
	return m.stdout, m.err
}

func TestCritic_Review_Success(t *testing.T) {
	cfg := &config.Config{
		FindingConfidenceMin: 0.55,
		PromptBudgetTokens:   100,
	}

	llmResponse := `
	{
		"verdict": "LGTM",
		"findings": [
			{
				"severity": "info",
				"category": "style",
				"path": "main.go",
				"start_line": 10,
				"end_line": 10,
				"title": "indent style",
				"body": "use tabs",
				"confidence": 0.8
			}
		]
	}
	`

	m := &mockLLM{stdout: llmResponse}
	router := llm.NewRouter(m, nil)
	c := New(cfg, router)

	reviewCtx := &domain.ReviewContext{
		Files: []domain.ChangedFile{
			{Path: "main.go"},
		},
	}

	res, err := c.Review(context.Background(), reviewCtx)
	if err != nil {
		t.Fatalf("unexpected review failure: %v", err)
	}

	if res.Verdict != "LGTM" {
		t.Errorf("expected verdict 'LGTM', got %s", res.Verdict)
	}

	if len(res.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(res.Findings))
	}

	f := res.Findings[0]
	if f.Path != "main.go" || f.Severity != "info" || f.Category != "style" {
		t.Errorf("unexpected enriched finding: %+v", f)
	}
	if f.ID == "" || f.Fingerprint == "" {
		t.Errorf("missing UUID or fingerprint in enriched result")
	}
}
