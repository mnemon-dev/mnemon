package search

import (
	"container/heap"
	"strings"
	"unicode"

	"github.com/mnemon-dev/mnemon/internal/model"
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

// keywordSearchCached is the internal implementation that optionally populates a token cache.
// If tokenCache is non-nil, it stores each insight's combined token set keyed by ID.
func keywordSearchCached(insights []*model.Insight, query string, limit int, tokenCache map[string]map[string]bool) []ScoredInsight {
	queryTokens := Tokenize(query)
	if len(queryTokens) == 0 {
		return nil
	}

	h := &scoredHeap{}
	for _, ins := range insights {
		contentTokens := insightTokens(ins)
		if tokenCache != nil {
			tokenCache[ins.ID] = contentTokens
		}

		intersection := 0
		for t := range queryTokens {
			if contentTokens[t] {
				intersection++
			}
		}
		if intersection == 0 {
			continue
		}
		score := float64(intersection) / float64(len(queryTokens))

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
		for t := range Tokenize(tag) {
			tokens[t] = true
		}
	}
	for _, ent := range ins.Entities {
		for t := range Tokenize(ent) {
			tokens[t] = true
		}
	}
	return tokens
}

// stopwords contains common English words that are filtered from token sets
// to improve similarity precision (MAGMA compliance: P7).
var stopwords = map[string]bool{
	"a": true, "an": true, "the": true, "is": true, "are": true, "was": true,
	"were": true, "be": true, "been": true, "being": true, "have": true,
	"has": true, "had": true, "do": true, "does": true, "did": true,
	"will": true, "would": true, "could": true, "should": true, "may": true,
	"might": true, "shall": true, "can": true, "to": true, "of": true,
	"in": true, "for": true, "on": true, "with": true, "at": true, "by": true,
	"from": true, "as": true, "into": true, "about": true, "that": true,
	"this": true, "it": true, "its": true, "or": true, "and": true, "but": true,
	"if": true, "not": true, "no": true, "so": true, "up": true, "out": true,
	"than": true, "then": true, "too": true, "very": true, "just": true,
	"also": true, "more": true, "some": true, "any": true, "all": true,
	"each": true, "i": true, "me": true, "my": true, "we": true, "you": true,
	"your": true, "he": true, "she": true, "they": true, "them": true,
	"his": true, "her": true, "our": true, "their": true, "what": true,
	"which": true, "who": true, "how": true, "when": true, "where": true,
}

// Tokenize splits text into lowercase tokens with stopword filtering.
// English words split on whitespace/punctuation; CJK characters generate
// character bigrams. Common English stopwords are excluded.
func Tokenize(text string) map[string]bool {
	tokens := make(map[string]bool)
	text = strings.ToLower(text)

	var word strings.Builder
	runes := []rune(text)
	var cjkBuf []rune

	for _, r := range runes {
		if unicode.Is(unicode.Han, r) {
			if word.Len() > 0 {
				w := word.String()
				if !stopwords[w] {
					tokens[w] = true
				}
				word.Reset()
			}
			cjkBuf = append(cjkBuf, r)
		} else {
			if len(cjkBuf) > 0 {
				flushCJK(cjkBuf, tokens)
				cjkBuf = cjkBuf[:0]
			}
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				word.WriteRune(r)
			} else {
				if word.Len() > 0 {
					w := word.String()
					if !stopwords[w] {
						tokens[w] = true
					}
					word.Reset()
				}
			}
		}
	}
	if word.Len() > 0 {
		w := word.String()
		if !stopwords[w] {
			tokens[w] = true
		}
	}
	if len(cjkBuf) > 0 {
		flushCJK(cjkBuf, tokens)
	}
	return tokens
}

func flushCJK(buf []rune, tokens map[string]bool) {
	for j := 0; j < len(buf)-1; j++ {
		tokens[string(buf[j:j+2])] = true
	}
	if len(buf) == 1 {
		tokens[string(buf)] = true
	}
}

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

	scoreA := float64(intersection) / float64(len(tokA))
	scoreB := float64(intersection) / float64(len(tokB))
	if scoreA > scoreB {
		return scoreA
	}
	return scoreB
}
