package search

import (
	"sort"

	"github.com/mnemon-dev/mnemon/internal/memory/embed"
	"github.com/mnemon-dev/mnemon/internal/memory/model"
)

type recallTransition struct {
	edge       *model.Edge
	neighborID string
	score      float64
}

// rankRecallTransitions computes each eligible transition once, then orders it
// by score before the caller applies the visit budget. IDs resolve only ties.
func rankRecallTransitions(edges []*model.Edge, current beamItem, queryVec []float64,
	weights IntentWeights, embedCache map[string][]float64, allowed map[string]*model.Insight) []recallTransition {
	transitions := make([]recallTransition, 0, len(edges))
	for _, edge := range edges {
		neighborID := recallEdgeNeighbor(edge, current.id)
		if allowed != nil && allowed[neighborID] == nil {
			continue
		}
		// MAGMA additive transition: score_u + lambda1*structure + lambda2*similarity.
		structural := weights[edge.EdgeType] * edge.Weight
		semantic := 0.0
		if queryVec != nil && embedCache != nil {
			if vector, ok := embedCache[neighborID]; ok {
				if similarity := embed.CosineSimilarity(queryVec, vector); similarity > 0 {
					semantic = similarity
				}
			}
		}
		transitions = append(transitions, recallTransition{
			edge:       edge,
			neighborID: neighborID,
			score:      current.score + lambda1*structural + lambda2*semantic,
		})
	}
	sort.Slice(transitions, func(i, j int) bool {
		a, b := transitions[i], transitions[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if a.neighborID != b.neighborID {
			return a.neighborID < b.neighborID
		}
		if a.edge.EdgeType != b.edge.EdgeType {
			return a.edge.EdgeType < b.edge.EdgeType
		}
		return a.edge.SourceID < b.edge.SourceID
	})
	return transitions
}
