package setup

import (
	"os"
	"strings"
)

const (
	markerStart = "<!-- mnemon:start -->"
	markerEnd   = "<!-- mnemon:end -->"
)

// EjectMemoryBlock removes everything between <!-- mnemon:start --> and <!-- mnemon:end --> inclusive.
// Returns true if the file was modified.
func EjectMemoryBlock(filePath string) (bool, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	s := string(content)
	startIdx := strings.Index(s, markerStart)
	if startIdx < 0 {
		return false, nil
	}

	endIdxRel := strings.Index(s[startIdx+len(markerStart):], markerEnd)
	if endIdxRel < 0 {
		return false, nil
	}
	endIdx := startIdx + len(markerStart) + endIdxRel
	endIdx += len(markerEnd)

	removedLeadingNewline := false
	removedTrailingNewline := false

	// Also remove a leading newline before the block if present
	if startIdx > 0 && s[startIdx-1] == '\n' {
		startIdx--
		removedLeadingNewline = true
	}
	// Also remove a trailing newline after the block if present
	if endIdx < len(s) && s[endIdx] == '\n' {
		endIdx++
		removedTrailingNewline = true
	}

	result := s[:startIdx] + s[endIdx:]
	if removedLeadingNewline && removedTrailingNewline && startIdx > 0 && endIdx < len(s) {
		result = s[:startIdx] + "\n" + s[endIdx:]
	}
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}
	result = strings.TrimSpace(result)

	// If nothing remains, remove the file entirely
	if result == "" {
		if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
			return false, err
		}
		return true, nil
	}

	result += "\n"
	if err := os.WriteFile(filePath, []byte(result), 0644); err != nil {
		return false, err
	}
	return true, nil
}
