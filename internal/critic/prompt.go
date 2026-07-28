package critic

import (
	"fmt"
	"strings"

	"agent-frei-igi/internal/domain"
)

// BuildPrompt creates the instruction and context text to feed the LLM.
// Respects promptBudgetTokens by omitting patches from files if the estimate exceeds budget.
func BuildPrompt(reviewCtx *domain.ReviewContext, promptBudgetTokens int) string {
	if promptBudgetTokens <= 0 {
		promptBudgetTokens = 100000
	}
	charBudget := promptBudgetTokens * 4 // chars/4 estimate

	var sb strings.Builder

	// Write System Instructions
	sb.WriteString("You are a Senior Software Engineer and Expert Code Reviewer.\n")
	sb.WriteString("Analyze the following Pull Request details and changed files.\n")
	sb.WriteString("You MUST return output EXACTLY matching this JSON schema. Do not include markdown fences like ```json, just return raw JSON text.\n\n")

	sb.WriteString(`JSON Schema:
{
  "verdict": "Overall summary review comment.",
  "summary": "Optional short overall text summary.",
  "findings": [
    {
      "severity": "error|warning|info",
      "category": "bug|security|performance|style|test|docs|other",
      "path": "file/path.go",
      "start_line": 10,
      "end_line": 12,
      "title": "Short title describing the issue.",
      "body": "Detailed explanation of what is wrong and how to fix it.",
      "confidence": 0.85
    }
  ]
}
`)

	sb.WriteString("\n=== Pull Request Metadata ===\n")
	sb.WriteString(fmt.Sprintf("Repository: %s\n", reviewCtx.RepoFullName))
	sb.WriteString(fmt.Sprintf("Pull Request: #%d\n", reviewCtx.PRNumber))
	sb.WriteString(fmt.Sprintf("Head SHA: %s\n", reviewCtx.HeadSHA))
	if reviewCtx.BaseSHA != "" {
		sb.WriteString(fmt.Sprintf("Base SHA: %s\n", reviewCtx.BaseSHA))
	}
	if reviewCtx.PublisherLogin != "" {
		sb.WriteString(fmt.Sprintf("Publisher Login: %s\n", reviewCtx.PublisherLogin))
	}
	sb.WriteString(fmt.Sprintf("Truncation Info: Files included: %d/%d, Patches kept: %d bytes\n\n",
		reviewCtx.Truncation.FilesIncluded,
		reviewCtx.Truncation.FilesTotal,
		reviewCtx.Truncation.PatchBytesKept,
	))

	sb.WriteString("=== Changed Files ===\n")

	// Write files. Keep track of estimated characters.
	for _, f := range reviewCtx.Files {
		fileHeader := fmt.Sprintf("\nFile: %s\nStatus: %s\n", f.Path, f.Status)
		if f.Language != "" {
			fileHeader += fmt.Sprintf("Language: %s\n", f.Language)
		}

		if f.Omitted {
			fileHeader += fmt.Sprintf("Omitted reason: %s\n", f.OmitReason)
			sb.WriteString(fileHeader)
			continue
		}

		// Estimate patch size addition
		patchText := fmt.Sprintf("Patch:\n%s\n", f.Patch)
		totalEst := sb.Len() + len(fileHeader) + len(patchText)

		if totalEst > charBudget {
			// Budget exceeded, write path-only note
			fileHeader += "Omitted reason: patch omitted due to prompt budget limit\n"
			sb.WriteString(fileHeader)
		} else {
			sb.WriteString(fileHeader)
			sb.WriteString(patchText)
		}
	}

	return sb.String()
}
