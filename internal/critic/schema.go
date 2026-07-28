package critic

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"agent-frei-igi/internal/domain"

	"github.com/google/uuid"
)

// LLMResult represents the raw structured output expected from the LLM.
type LLMResult struct {
	Verdict  string       `json:"verdict"`
	Summary  string       `json:"summary,omitempty"`
	Findings []LLMFinding `json:"findings"`
}

// LLMFinding represents a single raw finding from the LLM.
type LLMFinding struct {
	Severity   string  `json:"severity"`
	Category   string  `json:"category"`
	Path       string  `json:"path"`
	StartLine  int     `json:"start_line"`
	EndLine    int     `json:"end_line"`
	Title      string  `json:"title"`
	Body       string  `json:"body"`
	Confidence float64 `json:"confidence"`
}

// Result represents the enriched, validated review result stored in result_blob.
type Result struct {
	PromptVersion    string       `json:"prompt_version"`
	ModelUsed        string       `json:"model_used"`
	Verdict          string       `json:"verdict"`
	Summary          string       `json:"summary,omitempty"`
	Findings         []Finding    `json:"findings"`
	Dropped          DroppedStats `json:"dropped"`
	ProviderAttempts []string     `json:"provider_attempts"`
}

// Finding represents a validated and uniquely identified code finding.
type Finding struct {
	ID          string  `json:"id"`
	Fingerprint string  `json:"fingerprint"`
	Severity    string  `json:"severity"`
	Category    string  `json:"category"`
	Path        string  `json:"path"`
	StartLine   int     `json:"start_line"`
	EndLine     int     `json:"end_line"`
	Title       string  `json:"title"`
	Body        string  `json:"body"`
	Confidence  float64 `json:"confidence"`
}

// DroppedStats keeps track of findings discarded during validation.
type DroppedStats struct {
	LowConfidence int `json:"low_confidence"`
	BadPath       int `json:"bad_path"`
	Invalid       int `json:"invalid"`
}

// ParseAndValidate parses the LLM output, strips markdown blocks, and validates findings.
func ParseAndValidate(rawOutput string, reviewCtx *domain.ReviewContext, minConfidence float64) (*LLMResult, *DroppedStats, error) {
	// Strip markdown blocks if present
	clean := strings.TrimSpace(rawOutput)
	if strings.HasPrefix(clean, "```json") {
		clean = strings.TrimPrefix(clean, "```json")
		clean = strings.TrimSuffix(clean, "```")
	} else if strings.HasPrefix(clean, "```") {
		clean = strings.TrimPrefix(clean, "```")
		clean = strings.TrimSuffix(clean, "```")
	}
	clean = strings.TrimSpace(clean)

	if clean == "" {
		return nil, nil, fmt.Errorf("LLM output is empty")
	}

	var raw LLMResult
	if err := json.Unmarshal([]byte(clean), &raw); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal LLM json: %w (raw output was: %q)", err, rawOutput)
	}

	// Create helper map of known files to validate paths
	knownFiles := make(map[string]bool)
	for _, f := range reviewCtx.Files {
		knownFiles[f.Path] = true
	}

	dropped := &DroppedStats{}
	var validatedFindings []LLMFinding

	for _, f := range raw.Findings {
		// 1. Validate paths
		if f.Path == "" || !knownFiles[f.Path] {
			dropped.BadPath++
			continue
		}

		// 2. Validate confidence
		if f.Confidence < minConfidence {
			dropped.LowConfidence++
			continue
		}

		// 3. Validate logical lines
		if f.StartLine < 0 || f.EndLine < 0 {
			f.StartLine = 0
			f.EndLine = 0
		}
		if f.StartLine > 0 && f.EndLine > 0 && f.EndLine < f.StartLine {
			dropped.Invalid++
			continue
		}

		// 4. Validate and normalize severity
		sev := strings.ToLower(strings.TrimSpace(f.Severity))
		switch sev {
		case "error", "warning", "info":
			f.Severity = sev
		default:
			f.Severity = "warning"
		}

		// 5. Validate and normalize category
		cat := strings.ToLower(strings.TrimSpace(f.Category))
		switch cat {
		case "bug", "security", "performance", "style", "test", "docs", "other":
			f.Category = cat
		default:
			f.Category = "other"
		}

		validatedFindings = append(validatedFindings, f)
	}

	raw.Findings = validatedFindings

	// Default verdict if empty
	if strings.TrimSpace(raw.Verdict) == "" {
		raw.Verdict = "Review completed."
	}

	return &raw, dropped, nil
}

// GenerateFingerprint computes a stable sha256 hash for duplicate/mutation detection.
func GenerateFingerprint(path, title, category string) string {
	normalizedTitle := strings.TrimSpace(strings.ToLower(title))
	data := path + "\x00" + normalizedTitle + "\x00" + category
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

// EnrichResult transforms validated LLMResult into stored Result.
func EnrichResult(raw *LLMResult, modelUsed string, attempts []string, dropped DroppedStats, promptVersion string) *Result {
	var findings []Finding
	for _, lf := range raw.Findings {
		fid := uuid.New().String()
		fp := GenerateFingerprint(lf.Path, lf.Title, lf.Category)

		findings = append(findings, Finding{
			ID:          fid,
			Fingerprint: fp,
			Severity:    lf.Severity,
			Category:    lf.Category,
			Path:        lf.Path,
			StartLine:   lf.StartLine,
			EndLine:     lf.EndLine,
			Title:       lf.Title,
			Body:        lf.Body,
			Confidence:  lf.Confidence,
		})
	}

	return &Result{
		PromptVersion:    promptVersion,
		ModelUsed:        modelUsed,
		Verdict:          raw.Verdict,
		Summary:          raw.Summary,
		Findings:         findings,
		Dropped:          dropped,
		ProviderAttempts: attempts,
	}
}
