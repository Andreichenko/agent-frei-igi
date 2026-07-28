package diplomat

import (
	"fmt"
	"strings"

	"agent-frei-igi/internal/critic"
	"agent-frei-igi/internal/githubapp"
)

// MapResultToRequest transforms Critic findings into a structured GitHub review request.
// It respects review signatures, limits comments count to 30 to avoid spam,
// and maps line-less findings as file-level summaries inside the review body.
func MapResultToRequest(result *critic.Result, headSHA string, signatureFlag bool, shortJobID string) githubapp.ReviewRequest {
	req := githubapp.ReviewRequest{
		CommitID: headSHA,
		Event:    "COMMENT", // T6 requires always COMMENT
	}

	var inlineComments []githubapp.ReviewComment
	var fileLevelNotes []string
	var overflowNotes []string

	commentLimit := 30
	commentCount := 0

	for _, f := range result.Findings {
		// Line comments must have a valid line
		line := f.EndLine
		if line <= 0 {
			line = f.StartLine
		}

		if line <= 0 {
			// File-level or line-less finding
			note := fmt.Sprintf("* [%s] **%s**: %s - %s", f.Severity, f.Path, f.Title, f.Body)
			fileLevelNotes = append(fileLevelNotes, note)
			continue
		}

		// Title and body formatting
		prefix := ""
		if strings.ToLower(f.Severity) == "error" {
			prefix = "[Error] "
		}
		commentBody := fmt.Sprintf("**%s%s**\n\n%s", prefix, f.Title, f.Body)

		if commentCount < commentLimit {
			inlineComments = append(inlineComments, githubapp.ReviewComment{
				Path: f.Path,
				Line: line,
				Side: "RIGHT",
				Body: commentBody,
			})
			commentCount++
		} else {
			// Put overflow findings into summary body
			note := fmt.Sprintf("* [%s] **%s#L%d**: %s - %s", f.Severity, f.Path, line, f.Title, f.Body)
			overflowNotes = append(overflowNotes, note)
		}
	}

	req.Comments = inlineComments

	// Build review summary body
	var bodyParts []string
	if result.Summary != "" {
		bodyParts = append(bodyParts, result.Summary)
	} else if result.Verdict != "" {
		bodyParts = append(bodyParts, result.Verdict)
	}

	if len(fileLevelNotes) > 0 {
		bodyParts = append(bodyParts, "\n### File-Level Notes:\n"+strings.Join(fileLevelNotes, "\n"))
	}

	if len(overflowNotes) > 0 {
		bodyParts = append(bodyParts, "\n### Additional Findings:\n"+strings.Join(overflowNotes, "\n"))
	}

	if signatureFlag {
		signature := fmt.Sprintf("\n---\nvia agent-frei · model %s · job %s", result.ModelUsed, shortJobID)
		bodyParts = append(bodyParts, signature)
	}

	req.Body = strings.TrimSpace(strings.Join(bodyParts, "\n"))

	return req
}
