package memory

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const defaultBriefExcerptChars = 240

// briefResult is the intentionally small discovery projection shared by
// recall and search. Full content remains available through `mnemon show`.
type briefResult struct {
	ID         string   `json:"id"`
	Excerpt    string   `json:"excerpt"`
	Category   string   `json:"category,omitempty"`
	Score      *float64 `json:"score,omitempty"`
	Confidence string   `json:"confidence,omitempty"`
}

type briefResponse struct {
	Results       []briefResult `json:"results"`
	Hint          string        `json:"hint,omitempty"`
	DetailCommand string        `json:"detail_command,omitempty"`
}

func newBriefResponse(results []briefResult, hint string) briefResponse {
	if results == nil {
		results = []briefResult{}
	}
	response := briefResponse{Results: results, Hint: hint}
	if len(results) > 0 {
		response.DetailCommand = "mnemon show <id>"
	}
	return response
}

func encodeBrief(w io.Writer, response briefResponse) error {
	// Brief mode deliberately emits compact JSON. Canonical/default and verbose
	// output stay pretty-printed for compatibility and human inspection.
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(response)
}

func validateBriefExcerptChars(enabled bool, limit int) error {
	if enabled && limit <= 0 {
		return fmt.Errorf("--excerpt-chars must be greater than 0")
	}
	return nil
}

func makeBriefExcerpt(content string, maxChars int) string {
	// Flatten whitespace so one memory cannot turn a discovery row into a large
	// multi-line block. strings.Fields is Unicode-aware.
	content = strings.Join(strings.Fields(content), " ")
	if utf8.RuneCountInString(content) <= maxChars {
		return content
	}
	if maxChars == 1 {
		return "…"
	}
	runes := []rune(content)
	return strings.TrimSpace(string(runes[:maxChars-1])) + "…"
}

func scorePointer(score float64) *float64 {
	return &score
}
