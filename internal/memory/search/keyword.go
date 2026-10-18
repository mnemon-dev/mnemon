package search

import (
	"container/heap"
	"github.com/mnemon-dev/mnemon/internal/retrieval"

	"github.com/mnemon-dev/mnemon/internal/memory/model"
)

// ScoredInsight pairs an insight with a relevance score.
type ScoredInsight struct {
	Insight *model.Insight `json:"insight"`
	Score   float64        `json:"score"`
}

// scoredHeap implements a min-heap for top-k ScoredInsight selection.
// Lowest score (weakest candidate) sits at the root for efficient eviction.
type scoredHeap []ScoredInsight

func (h scoredHeap) Len() int { return len(h) }
func (h scoredHeap) Less(i, j int) bool {
	if h[i].Score != h[j].Score {
		return h[i].Score < h[j].Score
	}
	return h[i].Insight.Importance < h[j].Insight.Importance
}
func (h scoredHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *scoredHeap) Push(x interface{}) { *h = append(*h, x.(ScoredInsight)) }
func (h *scoredHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

// KeywordSearch scores insights by token overlap with the query.
// Score = |intersection(query_tokens, content_tokens)| / |query_tokens|
func KeywordSearch(insights []*model.Insight, query string, limit int) []ScoredInsight {
	return keywordSearchCached(insights, query, limit, nil)
}

// keywordSearchCached is the internal implementation that optionally retains keyword scores.
// If scoreCache is non-nil, it stores overlap scores for reranking, including zeros.
func keywordSearchCached(insights []*model.Insight, query string, limit int, scoreCache map[string]float64) []ScoredInsight {
	queryTokens := Tokenize(query)
	if len(queryTokens) == 0 {
		return nil
	}

	h := &scoredHeap{}
	for _, ins := range insights {
		contentTokens := insightTokens(ins)
		intersection := 0
		for t := range queryTokens {
			if contentTokens[t] {
				intersection++
			}
		}
		score := float64(intersection) / float64(len(queryTokens))
		if scoreCache != nil {
			scoreCache[ins.ID] = score
		}
		if intersection == 0 {
			continue
		}

		if limit <= 0 || h.Len() < limit {
			heap.Push(h, ScoredInsight{Insight: ins, Score: score})
		} else if score > (*h)[0].Score || (score == (*h)[0].Score && ins.Importance > (*h)[0].Insight.Importance) {
			(*h)[0] = ScoredInsight{Insight: ins, Score: score}
			heap.Fix(h, 0)
		}
	}

	// Extract results in descending order (highest score first).
	result := make([]ScoredInsight, h.Len())
	for i := len(result) - 1; i >= 0; i-- {
		result[i] = heap.Pop(h).(ScoredInsight)
	}
	return result
}

// insightTokens returns the combined token set from an insight's content, tags, and entities.
func insightTokens(ins *model.Insight) map[string]bool {
	tokens := Tokenize(ins.Content)
	for _, tag := range ins.Tags {
		addTokens(tokens, tag)
	}
	for _, ent := range ins.Entities {
		addTokens(tokens, ent)
	}
	return tokens
}

// Tokenize preserves the native token-set contract.
func Tokenize(text string) map[string]bool          { return retrieval.Tokenize(text) }
func addTokens(tokens map[string]bool, text string) { retrieval.AddTokens(tokens, text) }

// JaccardSimilarity computes token-set Jaccard similarity: |A∩B| / |A∪B|.
// Used for deduplication — stricter than ContentSimilarity because it penalises
// texts that share domain vocabulary but differ in the specific facts they state
// (e.g. same species name, different location).
func JaccardSimilarity(a, b string) float64 {
	tokA := Tokenize(a)
	tokB := Tokenize(b)
	if len(tokA) == 0 || len(tokB) == 0 {
		return 0
	}
	intersection := 0
	for t := range tokA {
		if tokB[t] {
			intersection++
		}
	}
	union := len(tokA) + len(tokB) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// ContentSimilarity computes bidirectional token overlap between two texts.
// Returns max(overlap_a_to_b, overlap_b_to_a) for a symmetric measure.
func ContentSimilarity(a, b string) float64 {
	return TokenOverlapSimilarity(Tokenize(a), Tokenize(b))
}

// TokenOverlapSimilarity computes the same bidirectional overlap as
// ContentSimilarity over already-tokenized text. Neither input is modified.
func TokenOverlapSimilarity(tokA, tokB map[string]bool) float64 {
	if len(tokA) == 0 || len(tokB) == 0 {
		return 0
	}

	intersection := 0
	for t := range tokA {
		if tokB[t] {
			intersection++
		}
	}

	scoreA := float64(intersection) / float64(len(tokA))
	scoreB := float64(intersection) / float64(len(tokB))
	if scoreA > scoreB {
		return scoreA
	}
	return scoreB
}
