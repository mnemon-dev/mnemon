package cmd

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mnemon-dev/mnemon/internal/model"
	"github.com/mnemon-dev/mnemon/internal/search"
)

func makeTestRecallResponse() search.RecallResponse {
	return search.RecallResponse{
		Results: []search.RecallResult{
			{
				Insight: &model.Insight{
					ID:          "550e8400-e29b-41d4-a716-446655440000",
					Content:     "User prefers Qdrant for vector DB",
					Category:    model.CategoryPreference,
					Importance:  4,
					Tags:        []string{"tool", "db"},
					Entities:    []string{"Qdrant"},
					Source:      "agent",
					AccessCount: 2,
					CreatedAt:   time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC),
					UpdatedAt:   time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC),
				},
				Score:  0.84217,
				Intent: search.IntentGeneral,
				Via:    "keyword",
				Signals: search.SignalScores{
					Keyword:    0.6,
					Entity:     0.2,
					Similarity: 0.0,
					Graph:      0.04,
				},
			},
			{
				Insight: &model.Insight{
					ID:          "660e8400-e29b-41d4-a716-446655440001",
					Content:     "Chose Qdrant because of Rust performance",
					Category:    model.CategoryDecision,
					Importance:  5,
					Tags:        []string{"architecture"},
					Entities:    []string{"Qdrant", "Rust"},
					Source:      "user",
					AccessCount: 0,
					CreatedAt:   time.Date(2026, 1, 14, 8, 0, 0, 0, time.UTC),
					UpdatedAt:   time.Date(2026, 1, 14, 8, 0, 0, 0, time.UTC),
				},
				Score:  0.55432,
				Intent: search.IntentGeneral,
				Via:    "graph",
				Signals: search.SignalScores{
					Keyword:    0.3,
					Entity:     0.1,
					Similarity: 0.0,
					Graph:      0.15,
				},
			},
		},
		Meta: search.RecallMeta{
			Intent:       search.IntentGeneral,
			IntentSource: "auto",
			AnchorCount:  3,
			Traversed:    12,
			Hint:         "sparse_results",
		},
	}
}

func TestRecall_CompactProjection_PreservesContentAndIntent(t *testing.T) {
	resp := makeTestRecallResponse()
	compact := toCompact(resp)

	if len(compact.Results) != 2 {
		t.Fatalf("want 2 results, got %d", len(compact.Results))
	}

	r := compact.Results[0]
	if r.Content != "User prefers Qdrant for vector DB" {
		t.Errorf("content: got %q", r.Content)
	}
	if r.Intent != "GENERAL" {
		t.Errorf("intent: got %q", r.Intent)
	}
	if r.Category != "preference" {
		t.Errorf("category: got %q", r.Category)
	}
	if r.Importance != 4 {
		t.Errorf("importance: got %d", r.Importance)
	}
}

func TestRecall_CompactProjection_DropsSignalsAndTimestamps(t *testing.T) {
	resp := makeTestRecallResponse()
	compact := toCompact(resp)

	// Marshal to JSON and verify that dropped fields are absent.
	data, err := json.Marshal(compact)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	raw := string(data)

	absent := []string{
		`"signals"`,
		`"meta"`,
		`"created_at"`,
		`"updated_at"`,
		`"access_count"`,
		`"tags"`,
		`"entities"`,
		`"source"`,
		`"anchor_count"`,
		`"traversed"`,
	}
	for _, sub := range absent {
		if strings.Contains(raw, sub) {
			t.Errorf("compact JSON should not contain %s, got: %s", sub, raw)
		}
	}
}

func TestRecall_CompactProjection_PreservesHint(t *testing.T) {
	resp := makeTestRecallResponse()
	compact := toCompact(resp)

	if compact.Hint != "sparse_results" {
		t.Errorf("hint: want sparse_results, got %q", compact.Hint)
	}

	// Empty hint case
	resp.Meta.Hint = ""
	compact = toCompact(resp)
	if compact.Hint != "" {
		t.Errorf("empty hint: want empty, got %q", compact.Hint)
	}
}

func TestRecall_CompactProjection_PreservesFullID(t *testing.T) {
	resp := makeTestRecallResponse()
	compact := toCompact(resp)

	expectedID := "550e8400-e29b-41d4-a716-446655440000"
	if compact.Results[0].ID != expectedID {
		t.Errorf("ID should be full UUID: want %q, got %q", expectedID, compact.Results[0].ID)
	}
}

func TestRecall_CompactProjection_MatchedVia(t *testing.T) {
	resp := makeTestRecallResponse()
	compact := toCompact(resp)

	if compact.Results[0].MatchedVia != "keyword" {
		t.Errorf("matched_via[0]: want keyword, got %q", compact.Results[0].MatchedVia)
	}
	if compact.Results[1].MatchedVia != "graph" {
		t.Errorf("matched_via[1]: want graph, got %q", compact.Results[1].MatchedVia)
	}
}

func TestRecall_ConfidenceLabel_Buckets(t *testing.T) {
	cases := []struct {
		score float64
		want  string
	}{
		{0.0, "low"},
		{0.1, "low"},
		{0.24, "low"},
		{0.25, "medium"},
		{0.3, "medium"},
		{0.59, "medium"},
		{0.6, "high"},
		{0.842, "high"},
		{1.0, "high"},
		{1.5, "high"},
	}

	for _, tc := range cases {
		got := confidenceLabel(tc.score)
		if got != tc.want {
			t.Errorf("confidenceLabel(%f): want %q, got %q", tc.score, tc.want, got)
		}
	}
}

func TestRecall_ScoreRoundedToThreeDecimals(t *testing.T) {
	cases := []struct {
		input float64
		want  float64
	}{
		{0.84217, 0.842},
		{0.55432, 0.554},
		{0.1, 0.1},
		{0.9999, 1.0},
		{0.0005, 0.001},
		{0.0004, 0.0},
	}

	for _, tc := range cases {
		got := roundScore(tc.input)
		if got != tc.want {
			t.Errorf("roundScore(%f): want %f, got %f", tc.input, tc.want, got)
		}
	}
}

func TestRecall_CompactProjection_ConfidenceMatchesRoundedScore(t *testing.T) {
	// Verify that the confidence label is derived from the rounded score,
	// not the raw score. This matters at bucket boundaries where rounding
	// can cross a cutoff.
	cases := []struct {
		rawScore  float64
		wantScore float64
		wantLabel string
	}{
		{0.5996, 0.6, "high"},     // boundary: rounding crosses 0.6 cutoff
		{0.2496, 0.25, "medium"},  // boundary: rounding crosses 0.25 cutoff
		{0.5994, 0.599, "medium"}, // just below boundary, no crossing
	}

	for _, tc := range cases {
		resp := search.RecallResponse{
			Results: []search.RecallResult{
				{
					Insight: &model.Insight{
						ID:      "test-id",
						Content: "test content",
					},
					Score:  tc.rawScore,
					Intent: search.IntentGeneral,
				},
			},
		}
		compact := toCompact(resp)
		r := compact.Results[0]
		if r.Score != tc.wantScore {
			t.Errorf("rawScore=%f: want Score=%f, got %f", tc.rawScore, tc.wantScore, r.Score)
		}
		if r.Confidence != tc.wantLabel {
			t.Errorf("rawScore=%f: want Confidence=%q, got %q", tc.rawScore, tc.wantLabel, r.Confidence)
		}
	}
}

func TestRecall_CompactProjection_EmptyResults(t *testing.T) {
	resp := search.RecallResponse{
		Results: []search.RecallResult{},
		Meta: search.RecallMeta{
			Intent:       search.IntentGeneral,
			IntentSource: "auto",
			AnchorCount:  0,
			Traversed:    0,
			Hint:         "sparse_results",
		},
	}
	compact := toCompact(resp)
	if len(compact.Results) != 0 {
		t.Errorf("want 0 results, got %d", len(compact.Results))
	}
	if compact.Hint != "sparse_results" {
		t.Errorf("hint should be preserved even with empty results")
	}
}
