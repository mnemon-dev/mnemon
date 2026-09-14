package search

// scoredInsightBefore keeps relevance and importance first. Creation time and
// the unique ID resolve ties before either top-k selection or final truncation.
func scoredInsightBefore(a, b ScoredInsight) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	if a.Insight.Importance != b.Insight.Importance {
		return a.Insight.Importance > b.Insight.Importance
	}
	if !a.Insight.CreatedAt.Equal(b.Insight.CreatedAt) {
		return a.Insight.CreatedAt.After(b.Insight.CreatedAt)
	}
	return a.Insight.ID < b.Insight.ID
}
