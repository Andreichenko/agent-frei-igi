package detective

import (
	"path/filepath"
	"strings"

	"agent-frei-igi/internal/domain"
)

// TruncatePRFiles filters out skipped paths, limits total file count, and truncates total patch text bytes.
// Returns the truncated changed files list and populated TruncationInfo.
func TruncatePRFiles(files []domain.ChangedFile, maxFiles, maxPatchBytes int) ([]domain.ChangedFile, domain.TruncationInfo) {
	if maxFiles <= 0 {
		maxFiles = 40
	}
	if maxPatchBytes <= 0 {
		maxPatchBytes = 409600
	}

	totalFiles := len(files)
	var processed []domain.ChangedFile

	// Skip paths definition
	skipPrefixes := []string{"vendor/", "node_modules/"}
	skipSuffixes := []string{".min.js", "go.sum", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "bun.lockb"}

	filesIncludedCount := 0
	filesOmittedCount := 0
	patchBytesTotal := 0
	patchBytesKept := 0
	truncatedByBytes := false

	for _, file := range files {
		lowerPath := strings.ToLower(file.Path)

		// 1. Check skip path rules
		isSkipped := false
		for _, prefix := range skipPrefixes {
			if strings.HasPrefix(lowerPath, prefix) {
				isSkipped = true
				break
			}
		}
		if !isSkipped {
			for _, suffix := range skipSuffixes {
				if strings.HasSuffix(lowerPath, suffix) {
					isSkipped = true
					break
				}
			}
		}

		if isSkipped {
			file.Omitted = true
			file.OmitReason = "skipped path"
			file.Patch = ""
			filesOmittedCount++
			processed = append(processed, file)
			continue
		}

		// Heuristic to detect language by extension
		file.Language = detectLanguage(file.Path)

		// 2. Check total files limit
		if filesIncludedCount >= maxFiles {
			file.Omitted = true
			file.OmitReason = "max files limit exceeded"
			file.Patch = ""
			filesOmittedCount++
			processed = append(processed, file)
			continue
		}

		// Check if it is binary or has no patch
		if file.Patch == "" {
			file.Omitted = true
			file.OmitReason = "binary or empty patch"
			filesOmittedCount++
			processed = append(processed, file)
			// Still count towards processed files under limit
			filesIncludedCount++
			continue
		}

		// 3. Check patch bytes limit
		patchLen := len(file.Patch)
		patchBytesTotal += patchLen

		if truncatedByBytes {
			// Already truncated, omit all subsequent patches
			file.Omitted = true
			file.OmitReason = "max patch bytes limit exceeded"
			file.Patch = ""
			filesOmittedCount++
		} else if patchBytesKept+patchLen <= maxPatchBytes {
			// Fits fully
			patchBytesKept += patchLen
			filesIncludedCount++
		} else {
			// Fits partially or not at all
			truncatedByBytes = true
			remaining := maxPatchBytes - patchBytesKept
			if remaining > 0 {
				file.Patch = file.Patch[:remaining]
				file.Omitted = true
				file.OmitReason = "partially truncated due to patch bytes limit"
				patchBytesKept += remaining
				filesIncludedCount++
			} else {
				file.Patch = ""
				file.Omitted = true
				file.OmitReason = "max patch bytes limit exceeded"
				filesOmittedCount++
			}
		}

		processed = append(processed, file)
	}

	truncatedByFiles := totalFiles > filesIncludedCount

	truncInfo := domain.TruncationInfo{
		MaxFiles:         maxFiles,
		MaxPatchBytes:    maxPatchBytes,
		FilesTotal:       totalFiles,
		FilesIncluded:    filesIncludedCount,
		FilesOmitted:     filesOmittedCount,
		PatchBytesTotal:  patchBytesTotal,
		PatchBytesKept:   patchBytesKept,
		TruncatedByFiles: truncatedByFiles,
		TruncatedByBytes: truncatedByBytes,
	}

	return processed, truncInfo
}

// detectLanguage returns a simple programming language identifier by file extension.
func detectLanguage(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".go":
		return "Go"
	case ".ts":
		return "TypeScript"
	case ".js":
		return "JavaScript"
	case ".py":
		return "Python"
	case ".rs":
		return "Rust"
	case ".rb":
		return "Ruby"
	case ".java":
		return "Java"
	case ".kt":
		return "Kotlin"
	case ".swift":
		return "Swift"
	case ".cpp", ".cc", ".cxx":
		return "C++"
	case ".c":
		return "C"
	case ".h":
		return "C Header"
	case ".cs":
		return "C#"
	case ".php":
		return "PHP"
	case ".sh":
		return "Shell"
	case ".md":
		return "Markdown"
	case ".json":
		return "JSON"
	case ".yaml", ".yml":
		return "YAML"
	case ".sql":
		return "SQL"
	case ".html", ".htm":
		return "HTML"
	case ".css":
		return "CSS"
	default:
		return ""
	}
}
