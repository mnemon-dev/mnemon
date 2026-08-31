package mcp

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/mnemon-dev/mnemon/internal/memory/model"
	"github.com/mnemon-dev/mnemon/internal/memory/search"
	memoryservice "github.com/mnemon-dev/mnemon/internal/memory/service"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultLimit          = 10
	defaultRelatedLimit   = 20
	maxToolResults        = 100
	maxRelatedDepth       = 10
	maxQueryChars         = 2000
	maxIDChars            = 256
	maxSourceChars        = 100
	defaultContentChars   = 600
	confidenceLowMax      = 0.25
	confidenceMediumMax   = 0.6
	truncationInstruction = "Some content was truncated to 600 characters. Call the same tool with full=true to retrieve complete content."
)

type insightResult struct {
	ID         string   `json:"id"`
	Content    string   `json:"content"`
	Category   string   `json:"category,omitempty"`
	Importance int      `json:"importance,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	Intent     string   `json:"intent,omitempty"`
	MatchedVia string   `json:"matched_via,omitempty"`
	Confidence string   `json:"confidence,omitempty"`
	Score      float64  `json:"score,omitempty"`
	Depth      int      `json:"depth,omitempty"`
	EdgeType   string   `json:"via_edge_type,omitempty"`
	Truncated  bool     `json:"truncated,omitempty"`
}

type insightListResult struct {
	Results        []insightResult `json:"results"`
	Hint           string          `json:"hint,omitempty"`
	TruncationHint string          `json:"truncation_hint,omitempty"`
}

func projectRecall(response memoryservice.RecallResponse, full bool) insightListResult {
	if response.SmartResults != nil {
		return projectSmartRecall(*response.SmartResults, full)
	}
	results := make([]insightResult, 0, len(response.BasicResults))
	truncated := false
	for _, insight := range response.BasicResults {
		content, cut := projectContent(insight.Content, full)
		truncated = truncated || cut
		results = append(results, insightResult{
			ID: insight.ID, Content: content, Category: string(insight.Category),
			Importance: insight.Importance, Tags: insight.Tags, Truncated: cut,
		})
	}
	return newInsightList(results, "", truncated)
}

func projectSmartRecall(response search.RecallResponse, full bool) insightListResult {
	results := make([]insightResult, 0, len(response.Results))
	truncated := false
	for _, result := range response.Results {
		content, cut := projectContent(result.Insight.Content, full)
		truncated = truncated || cut
		score := roundScore(result.Score)
		results = append(results, insightResult{
			ID: result.Insight.ID, Content: content,
			Category: string(result.Insight.Category), Importance: result.Insight.Importance,
			Intent: string(result.Intent), MatchedVia: result.Via,
			Confidence: confidenceLabel(score), Score: score, Truncated: cut,
		})
	}
	return newInsightList(results, response.Meta.Hint, truncated)
}

func projectSearch(results []search.ScoredInsight, full bool) insightListResult {
	projected := make([]insightResult, 0, len(results))
	truncated := false
	for _, result := range results {
		content, cut := projectContent(result.Insight.Content, full)
		truncated = truncated || cut
		projected = append(projected, insightResult{
			ID: result.Insight.ID, Content: content,
			Category: string(result.Insight.Category), Importance: result.Insight.Importance,
			Tags: result.Insight.Tags, Score: roundScore(result.Score), Truncated: cut,
		})
	}
	return newInsightList(projected, "", truncated)
}

func projectRelated(results []memoryservice.RelatedResult, full bool) insightListResult {
	projected := make([]insightResult, 0, len(results))
	truncated := false
	for _, result := range results {
		content, cut := projectContent(result.Content, full)
		truncated = truncated || cut
		projected = append(projected, insightResult{
			ID: result.ID, Content: content, Category: result.Category,
			Importance: result.Importance, Depth: result.Depth,
			EdgeType: result.EdgeType, Truncated: cut,
		})
	}
	return newInsightList(projected, "", truncated)
}

func newInsightList(results []insightResult, hint string, truncated bool) insightListResult {
	if results == nil {
		results = []insightResult{}
	}
	output := insightListResult{Results: results, Hint: hint}
	if truncated {
		output.TruncationHint = truncationInstruction
	}
	return output
}

func projectContent(content string, full bool) (string, bool) {
	if full || utf8.RuneCountInString(content) <= defaultContentChars {
		return content, false
	}
	runes := []rune(content)
	return strings.TrimSpace(string(runes[:defaultContentChars-1])) + "…", true
}

func boundedLimit(value *int, fallback int) (int, error) {
	if value == nil {
		return fallback, nil
	}
	if *value < 1 || *value > maxToolResults {
		return 0, fmt.Errorf("limit must be between 1 and %d", maxToolResults)
	}
	return *value, nil
}

func boundedDepth(value *int) (int, error) {
	if value == nil {
		return defaultDepth, nil
	}
	if *value < 1 || *value > maxRelatedDepth {
		return 0, fmt.Errorf("depth must be between 1 and %d", maxRelatedDepth)
	}
	return *value, nil
}

func validateText(name, value string, maxChars int) error {
	count := utf8.RuneCountInString(value)
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s must not be empty", name)
	}
	if count > maxChars {
		return fmt.Errorf("%s is too long (%d characters, max %d)", name, count, maxChars)
	}
	return nil
}

func roundScore(score float64) float64 {
	return math.Round(score*1000) / 1000
}

func confidenceLabel(score float64) string {
	switch {
	case score < confidenceLowMax:
		return "low"
	case score < confidenceMediumMax:
		return "medium"
	default:
		return "high"
	}
}

func toolAnnotations(readOnly, destructive, idempotent bool) *sdkmcp.ToolAnnotations {
	return &sdkmcp.ToolAnnotations{
		ReadOnlyHint: readOnly, DestructiveHint: boolPointer(destructive),
		IdempotentHint: idempotent, OpenWorldHint: boolPointer(false),
	}
}

func boolPointer(value bool) *bool { return &value }

func validCategory(category string) bool {
	return category == "" || model.ValidCategories[model.Category(category)]
}
