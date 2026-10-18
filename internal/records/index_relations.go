package records

import (
	"encoding/json"
	"slices"

	"github.com/mnemon-dev/mnemon/internal/retrieval"
)

func (index *derivedIndex) related(seeds []string, intent retrieval.Intent, supersedes bool, limit int) []string {
	scores := map[string]int{}
	for rank, id := range seeds[:min(20, len(seeds))] {
		added := 0
		links := index.links[id]
		if supersedes {
			links = append(append([]Link(nil), links...), index.replacements[id]...)
		}
		for _, link := range links {
			if (link.Type == "supersedes") != supersedes || !supersedes && !relationForIntent(link.Type, intent) {
				continue
			}
			scores[link.ID] += int(link.Weight*1000) + 20 - rank
			added++
			if added == 5 || len(scores) >= limit {
				break
			}
		}
		if len(scores) >= limit {
			break
		}
	}
	return orderedScores(scores)
}

func relationForIntent(kind string, intent retrieval.Intent) bool {
	switch intent {
	case retrieval.IntentWhy:
		return kind == "causal" || kind == "entity"
	case retrieval.IntentWhen:
		return kind == "temporal" || kind == "entity"
	case retrieval.IntentEntity:
		return kind == "entity" || kind == "semantic"
	default:
		return kind == "entity" || kind == "causal"
	}
}

func (index *derivedIndex) byTime(seeds []string, input map[string]json.RawMessage) []string {
	ids := append([]string(nil), seeds...)
	if len(ids) == 0 && (input["since"] != nil || input["until"] != nil) {
		for _, r := range index.docs {
			ids = append(ids, r.ID)
		}
	}
	slices.SortStableFunc(ids, func(a, b string) int { return index.meta[b].at.Compare(index.meta[a].at) })
	return ids
}

func (index *derivedIndex) demoteSuperseded(ids []string) {
	stale := map[string]bool{}
	present := map[string]bool{}
	for _, id := range ids {
		present[id] = true
	}
	for id, links := range index.links {
		if !present[id] {
			continue
		}
		for _, link := range links {
			if link.Type == "supersedes" {
				stale[link.ID] = true
			}
		}
	}
	slices.SortStableFunc(ids, func(a, b string) int {
		if stale[a] == stale[b] {
			return 0
		}
		if stale[a] {
			return 1
		}
		return -1
	})
}
