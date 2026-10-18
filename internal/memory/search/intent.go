package search

import (
	"github.com/mnemon-dev/mnemon/internal/memory/model"
	"github.com/mnemon-dev/mnemon/internal/retrieval"
)

type Intent = retrieval.Intent

const (
	IntentWhy     = retrieval.IntentWhy
	IntentWhen    = retrieval.IntentWhen
	IntentEntity  = retrieval.IntentEntity
	IntentGeneral = retrieval.IntentGeneral
)

func IntentFromString(s string) (Intent, error) { return retrieval.IntentFromString(s) }
func DetectIntent(s string) Intent              { return retrieval.DetectIntent(s) }

// IntentWeights maps intent to edge type weights for traversal.
type IntentWeights map[model.EdgeType]float64

var intentWeightsMap = map[Intent]IntentWeights{
	IntentWhy: {
		model.EdgeCausal:   0.70,
		model.EdgeTemporal: 0.20,
		model.EdgeEntity:   0.05,
		model.EdgeSemantic: 0.05,
	},
	IntentWhen: {
		model.EdgeTemporal: 0.65,
		model.EdgeCausal:   0.15,
		model.EdgeEntity:   0.10,
		model.EdgeSemantic: 0.10,
	},
	IntentEntity: {
		model.EdgeEntity:   0.55,
		model.EdgeSemantic: 0.30,
		model.EdgeTemporal: 0.05,
		model.EdgeCausal:   0.10,
	},
	IntentGeneral: {
		model.EdgeTemporal: 0.25,
		model.EdgeSemantic: 0.25,
		model.EdgeCausal:   0.25,
		model.EdgeEntity:   0.25,
	},
}

// GetWeights returns the edge type weights for the given intent.
func GetWeights(intent Intent) IntentWeights {
	w, ok := intentWeightsMap[intent]
	if !ok {
		return intentWeightsMap[IntentGeneral]
	}
	return w
}
