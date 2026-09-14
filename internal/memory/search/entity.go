package search

import (
	"container/heap"
	"strings"

	"github.com/mnemon-dev/mnemon/internal/memory/model"
)

func normalizedEntitySet(entities []string) map[string]bool {
	set := make(map[string]bool, len(entities))
	for _, entity := range entities {
		if entity != "" {
			set[strings.ToLower(entity)] = true
		}
	}
	return set
}

// entityOverlapScore compares whole entity values. Repeated or case-varied
// stored entries count once, keeping both anchor and reranking signals in [0, 1].
func entityOverlapScore(entities []string, queryEntities map[string]bool) float64 {
	if len(queryEntities) == 0 {
		return 0
	}
	matched := make(map[string]bool)
	for _, entity := range entities {
		key := strings.ToLower(entity)
		if queryEntities[key] {
			matched[key] = true
		}
	}
	return float64(len(matched)) / float64(len(queryEntities))
}

// selectEntityAnchors gives exact entity matches an independent, bounded path
// into RRF even when common query words fill the keyword candidate budget.
func selectEntityAnchors(insights []*model.Insight, queryEntities map[string]bool) []ScoredInsight {
	if len(queryEntities) == 0 {
		return nil
	}
	h := &scoredHeap{}
	for _, insight := range insights {
		score := entityOverlapScore(insight.Entities, queryEntities)
		if score == 0 {
			continue
		}
		candidate := ScoredInsight{Insight: insight, Score: score}
		if h.Len() < anchorTopK {
			heap.Push(h, candidate)
		} else if scoredInsightBefore(candidate, (*h)[0]) {
			(*h)[0] = candidate
			heap.Fix(h, 0)
		}
	}
	result := make([]ScoredInsight, h.Len())
	for i := len(result) - 1; i >= 0; i-- {
		result[i] = heap.Pop(h).(ScoredInsight)
	}
	return result
}
