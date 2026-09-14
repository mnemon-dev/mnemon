package search

import "github.com/mnemon-dev/mnemon/internal/memory/model"

// RecallFilter limits candidates and graph traversal to matching stored fields.
// Empty fields impose no restriction, matching the basic recall filter contract.
type RecallFilter struct {
	Category string
	Source   string
}

func filterRecallInsights(all []*model.Insight, filter RecallFilter) ([]*model.Insight, map[string]*model.Insight) {
	if filter.Category == "" && filter.Source == "" {
		return all, nil
	}
	filtered := make([]*model.Insight, 0, len(all))
	allowed := make(map[string]*model.Insight)
	for _, insight := range all {
		if filter.Category != "" && string(insight.Category) != filter.Category {
			continue
		}
		if filter.Source != "" && insight.Source != filter.Source {
			continue
		}
		filtered = append(filtered, insight)
		allowed[insight.ID] = insight
	}
	return filtered, allowed
}
