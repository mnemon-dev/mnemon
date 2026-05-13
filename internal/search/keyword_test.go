package search

import (
	"testing"

	"github.com/mnemon-dev/mnemon/internal/model"
)

func TestTokenize_English(t *testing.T) {
	tokens := Tokenize("Go uses SQLite for persistent storage")
	// "Go" becomes "go" — not a stopword after lowercase? Actually "go" is not in the stopword list.
	// Wait — checking: no, "go" is not in stopwords. Let me verify with actual stopwords.
	// Stopwords: "for", "is", etc. "uses" is not a stopword.
	if !tokens["go"] {
		t.Error("want token 'go'")
	}
	if !tokens["sqlite"] {
		t.Error("want token 'sqlite'")
	}
	if !tokens["persistent"] {
		t.Error("want token 'persistent'")
	}
	if !tokens["storage"] {
		t.Error("want token 'storage'")
	}
	// "for" is a stopword
	if tokens["for"] {
		t.Error("'for' should be filtered as stopword")
	}
}

func TestTokenize_StopwordFiltering(t *testing.T) {
	tokens := Tokenize("the quick fox is very fast")
	// "the", "is", "very" are stopwords
	if tokens["the"] || tokens["is"] || tokens["very"] {
		t.Error("stopwords should be filtered")
	}
	if !tokens["quick"] || !tokens["fox"] || !tokens["fast"] {
		t.Error("non-stopwords should be kept")
	}
}

func TestTokenize_CJKBigrams(t *testing.T) {
	tokens := Tokenize("知识图谱")
	// Should produce bigrams: 知识, 识图, 图谱
	if !tokens["知识"] {
		t.Error("want bigram '知识'")
	}
	if !tokens["识图"] {
		t.Error("want bigram '识图'")
	}
	if !tokens["图谱"] {
		t.Error("want bigram '图谱'")
	}
}

func TestTokenize_SingleCJK(t *testing.T) {
	tokens := Tokenize("图")
	// Single CJK char: should be kept as-is
	if !tokens["图"] {
		t.Error("single CJK char should be kept")
	}
}

func TestTokenize_MixedEnglishCJK(t *testing.T) {
	tokens := Tokenize("Go语言很强大")
	if !tokens["go"] {
		t.Error("want 'go'")
	}
	// CJK bigrams from 语言很强大
	if !tokens["语言"] {
		t.Error("want bigram '语言'")
	}
	if !tokens["强大"] {
		t.Error("want bigram '强大'")
	}
}

func TestTokenize_Empty(t *testing.T) {
	tokens := Tokenize("")
	if len(tokens) != 0 {
		t.Errorf("empty string: want 0 tokens, got %d", len(tokens))
	}
}

func TestTokenize_AllStopwords(t *testing.T) {
	tokens := Tokenize("the is a an")
	if len(tokens) != 0 {
		t.Errorf("all stopwords: want 0 tokens, got %d: %v", len(tokens), tokens)
	}
}

func TestContentSimilarity_Identical(t *testing.T) {
	sim := ContentSimilarity("Go uses SQLite", "Go uses SQLite")
	if sim != 1.0 {
		t.Errorf("identical text: want 1.0, got %f", sim)
	}
}

func TestContentSimilarity_Disjoint(t *testing.T) {
	sim := ContentSimilarity("apple banana cherry", "dog elephant fox")
	if sim != 0 {
		t.Errorf("disjoint text: want 0, got %f", sim)
	}
}

func TestContentSimilarity_BidirectionalMax(t *testing.T) {
	// "Go" is short → "Go SQLite memory" has 1/3 overlap from A→B perspective
	// But "Go" alone has 1/1=100% overlap from B→A if B="Go"
	sim := ContentSimilarity("Go", "Go SQLite memory graph")
	if sim != 1.0 {
		// "Go" has 1 token, "Go SQLite memory graph" has 4 tokens
		// A→B: 1/1 = 1.0, B→A: 1/4 = 0.25, max = 1.0
		t.Errorf("bidirectional max: want 1.0, got %f", sim)
	}
}

func TestContentSimilarity_Empty(t *testing.T) {
	if sim := ContentSimilarity("", "hello"); sim != 0 {
		t.Errorf("empty first: want 0, got %f", sim)
	}
	if sim := ContentSimilarity("hello", ""); sim != 0 {
		t.Errorf("empty second: want 0, got %f", sim)
	}
}

func TestJaccardSimilarity_Identical(t *testing.T) {
	if sim := JaccardSimilarity("Go uses SQLite", "Go uses SQLite"); sim != 1.0 {
		t.Errorf("identical: want 1.0, got %f", sim)
	}
}

func TestJaccardSimilarity_Disjoint(t *testing.T) {
	if sim := JaccardSimilarity("apple banana", "dog elephant"); sim != 0 {
		t.Errorf("disjoint: want 0, got %f", sim)
	}
}

func TestJaccardSimilarity_SameDomainDifferentFact(t *testing.T) {
	// Same species name (shared tokens) but different location (distinct tokens).
	// Jaccard penalises the distinct tokens; bidirectional max would not.
	a := "Dichorragia nesimachus singleton at Kinabalu Park Sabah lowland forest elevation"
	b := "Dichorragia nesimachus first record Raub Pahang dipterocarp forest canopy specimen"
	sim := JaccardSimilarity(a, b)
	if sim >= 0.5 {
		t.Errorf("same-domain different-fact: want Jaccard < 0.5 (ADD territory), got %f", sim)
	}
}

func TestJaccardSimilarity_OneWordChange(t *testing.T) {
	// Same sentence with one word swapped — genuine update, Jaccard should be >= 0.5.
	sim := JaccardSimilarity("Go uses SQLite for storage", "Go uses PostgreSQL for storage")
	if sim < 0.5 {
		t.Errorf("one-word-change: want Jaccard >= 0.5 (UPDATE territory), got %f", sim)
	}
}

func TestKeywordSearch_Ranking(t *testing.T) {
	insights := []*model.Insight{
		{ID: "1", Content: "Go language for building CLI tools", Importance: 3},
		{ID: "2", Content: "SQLite database for Go applications", Importance: 3},
		{ID: "3", Content: "Python machine learning framework", Importance: 3},
	}

	results := KeywordSearch(insights, "Go CLI tools", 10)

	if len(results) < 2 {
		t.Fatalf("want at least 2 results, got %d", len(results))
	}
	// First result should be ID=1 (best match for "Go CLI tools")
	if results[0].Insight.ID != "1" {
		t.Errorf("top result: want ID=1, got ID=%s", results[0].Insight.ID)
	}
	// Scores should be descending
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Errorf("results not sorted: score[%d]=%f > score[%d]=%f",
				i, results[i].Score, i-1, results[i-1].Score)
		}
	}
}

func TestKeywordSearch_Limit(t *testing.T) {
	insights := make([]*model.Insight, 20)
	for i := range insights {
		insights[i] = &model.Insight{ID: string(rune('a' + i)), Content: "common shared words here", Importance: 3}
	}

	results := KeywordSearch(insights, "common shared words", 5)
	if len(results) > 5 {
		t.Errorf("limit 5: got %d results", len(results))
	}
}

func TestKeywordSearch_ImportanceTiebreaker(t *testing.T) {
	insights := []*model.Insight{
		{ID: "low", Content: "Go memory graph", Importance: 1},
		{ID: "high", Content: "Go memory graph", Importance: 5},
	}
	results := KeywordSearch(insights, "Go memory graph", 10)
	if len(results) < 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	// Same score, higher importance first
	if results[0].Insight.ID != "high" {
		t.Errorf("tiebreaker: want high importance first, got ID=%s", results[0].Insight.ID)
	}
}

func TestKeywordSearch_EmptyQuery(t *testing.T) {
	insights := []*model.Insight{{ID: "1", Content: "some content"}}
	results := KeywordSearch(insights, "", 10)
	if results != nil {
		t.Errorf("empty query: want nil, got %v", results)
	}
}

func TestKeywordSearch_IncludesTagsAndEntities(t *testing.T) {
	insights := []*model.Insight{
		{
			ID:       "1",
			Content:  "something unrelated",
			Tags:     []string{"database"},
			Entities: []string{"SQLite"},
		},
	}
	results := KeywordSearch(insights, "SQLite database", 10)
	if len(results) == 0 {
		t.Error("tags and entities should contribute to matching")
	}
}
