package search

import (
	"testing"

	"github.com/mnemon-dev/mnemon/internal/model"
)

func TestClassifySuggestion_Add(t *testing.T) {
	got := classifySuggestion(0.3, "completely new content", "existing different content")
	if got != DiffAdd {
		t.Errorf("low similarity: want ADD, got %s", got)
	}
}

func TestClassifySuggestion_Duplicate(t *testing.T) {
	got := classifySuggestion(0.95, "very similar content here", "very similar content here indeed")
	if got != DiffDuplicate {
		t.Errorf("high similarity: want DUPLICATE, got %s", got)
	}
}

func TestClassifySuggestion_Update(t *testing.T) {
	got := classifySuggestion(0.7, "Go uses SQLite for storage", "Go uses PostgreSQL for storage")
	if got != DiffUpdate {
		t.Errorf("medium similarity: want UPDATE, got %s", got)
	}
}

func TestClassifySuggestion_ConflictNegation(t *testing.T) {
	tests := []struct {
		name     string
		newText  string
		existing string
	}{
		{"no longer", "no longer supports Python 2", "supports Python 2"},
		{"replaced", "replaced Flask with FastAPI", "uses Flask for API"},
		{"chinese_negation", "不再使用Redis", "使用Redis"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Similarity >= 0.7 so negation check is active.
			got := classifySuggestion(0.7, tt.newText, tt.existing)
			if got != DiffConflict {
				t.Errorf("want CONFLICT, got %s", got)
			}
		})
	}
}

func TestClassifySuggestion_NotWordNoConflict(t *testing.T) {
	// "not" alone must NOT trigger CONFLICT — it appears constantly in
	// scientific text ("species not previously recorded") and would cause
	// false replacements of distinct survey records.
	got := classifySuggestion(0.7, "species not recorded at this site", "species recorded at Kinabalu")
	if got == DiffConflict {
		t.Error("bare 'not' should not trigger CONFLICT")
	}
}

func TestClassifySuggestion_ConflictBelowThreshold(t *testing.T) {
	// Negation words must NOT trigger CONFLICT when similarity < 0.7.
	// Two survey records from different locations may share domain vocabulary
	// and contain "no longer" or "replaced" in unrelated sentences.
	got := classifySuggestion(0.6, "no longer present at Raub site", "butterfly survey Kinabalu")
	if got == DiffConflict {
		t.Error("negation below similarity 0.7 should not trigger CONFLICT")
	}
}

func TestClassifySuggestion_Boundary(t *testing.T) {
	// Exactly 0.5 should not be ADD (it's >= 0.5)
	got := classifySuggestion(0.5, "some content here", "other content here")
	if got == DiffAdd {
		t.Error("similarity=0.5: should not be ADD (threshold is < 0.5)")
	}

	// Exactly 0.9 should not be DUPLICATE (threshold is > 0.9)
	got = classifySuggestion(0.9, "some content here", "other content here")
	if got == DiffDuplicate {
		t.Error("similarity=0.9: should not be DUPLICATE (threshold is > 0.9)")
	}
}

func TestDiff_TokenOnly(t *testing.T) {
	insights := []*model.Insight{
		{ID: "1", Content: "Go uses SQLite for persistent memory storage"},
		{ID: "2", Content: "Python machine learning with TensorFlow"},
		{ID: "3", Content: "Go uses SQLite for memory persistence"},
	}

	result := Diff(insights, "Go uses SQLite for persistent memory storage", DiffOptions{})

	if result.Suggestion == DiffAdd {
		t.Error("should detect duplicate/update, not ADD")
	}
	if len(result.Matches) == 0 {
		t.Fatal("want at least one match")
	}
	// First match should be the most similar
	if result.Matches[0].ID != "1" {
		t.Errorf("first match should be exact content (ID=1), got ID=%s", result.Matches[0].ID)
	}
}

func TestDiff_NoMatches(t *testing.T) {
	insights := []*model.Insight{
		{ID: "1", Content: "something completely different about cooking recipes"},
	}
	result := Diff(insights, "Go database library benchmarks", DiffOptions{})
	if result.Suggestion != DiffAdd {
		t.Errorf("no matching content: want ADD, got %s", result.Suggestion)
	}
}

func TestDiff_DuplicateOverridesOverall(t *testing.T) {
	insights := []*model.Insight{
		{ID: "1", Content: "Go uses SQLite for storage"},
		{ID: "2", Content: "Go uses SQLite for storage exactly the same content repeated verbatim"},
	}
	// The new content is identical to ID=1
	result := Diff(insights, "Go uses SQLite for storage", DiffOptions{})
	if result.Suggestion != DiffDuplicate {
		t.Errorf("exact match present: want overall DUPLICATE, got %s", result.Suggestion)
	}
}

func TestDiff_SameDomainCosineNoOverride(t *testing.T) {
	// Regression: same-domain facts with different locations must not trigger UPDATE.
	// nomic-embed-text produces cosine ~0.75 for same-domain different-fact pairs.
	// The old threshold (0.70) let cosine override token similarity and incorrectly
	// classified as UPDATE, replacing the original insight. The fix raises it to 0.85.
	insights := []*model.Insight{
		{ID: "kinabalu", Content: "Dichorragia nesimachus singleton at Kinabalu Park, Sabah."},
	}
	// Two unit vectors with cosine similarity = 0.75: simulates same-domain different-fact embeddings.
	newVec := []float64{1.0, 0.0}
	existVec := []float64{0.75, 0.6614} // cos(newVec, existVec) = 0.75

	result := Diff(insights,
		"Dichorragia nesimachus first record in Bentong, Pahang.",
		DiffOptions{
			NewEmbedding:  newVec,
			ExistingEmbed: []EmbeddedItem{{ID: "kinabalu", Embedding: existVec}},
		})
	if result.Suggestion != DiffAdd {
		t.Errorf("cosine=0.75 (same domain, different location): want ADD, got %s", result.Suggestion)
	}
}

func TestDiff_LimitDefault(t *testing.T) {
	insights := make([]*model.Insight, 20)
	for i := range insights {
		insights[i] = &model.Insight{
			ID:      string(rune('A' + i)),
			Content: "shared words database memory graph",
		}
	}
	result := Diff(insights, "shared words database memory", DiffOptions{})
	// Default limit is 5
	if len(result.Matches) > 5 {
		t.Errorf("default limit 5: got %d matches", len(result.Matches))
	}
}

func TestDiff_LowerKeywordScoreUpdateNotMasked(t *testing.T) {
	// insightA: all of new's tokens are present (keyword score = 5/5 = 1.0),
	// but Jaccard = 5/14 ≈ 0.36 → ADD. KeywordSearch puts this first.
	insightA := &model.Insight{
		ID:      "a",
		Content: "project uses redis for caching database monitoring alerting logging tracing scaling replication failover clustering sharding",
	}
	// insightB: keyword score = 4/5 = 0.8 (ranks second), Jaccard = 4/6 ≈ 0.67 → UPDATE.
	// Without sorting by Similarity, insightA's ADD masks this UPDATE.
	insightB := &model.Insight{
		ID:      "b",
		Content: "project uses redis postgresql caching",
	}

	result := Diff(
		[]*model.Insight{insightA, insightB},
		"project uses redis for caching database",
		DiffOptions{},
	)

	if result.Suggestion != DiffUpdate {
		t.Errorf("want UPDATE (insightB is more similar by Jaccard), got %s — "+
			"high-keyword-score ADD from insightA masked the UPDATE", result.Suggestion)
	}
}
