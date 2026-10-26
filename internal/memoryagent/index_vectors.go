package memoryagent

import (
	"context"
	"encoding/json"
	"math"

	"github.com/mnemon-dev/mnemon/internal/records"
)

func (a *Agent) indexVectors(ctx context.Context, state *overlayState) bool {
	if a.config.Embedding == nil || len(state.Items) == 0 {
		return false
	}
	raw, _ := json.Marshal(a.config.Embedding)
	key := reference("embedding", string(raw)).Hash
	if state.VectorModel != key || state.Vectors == nil {
		state.Vectors = map[string][]float32{}
		state.VectorModel = key
	}
	missing, texts := []string{}, []string{}
	current := map[string]bool{}
	for _, item := range state.Items {
		current[item.ID] = true
		if len(state.Vectors[item.ID]) == 0 {
			missing = append(missing, item.ID)
			texts = append(texts, item.Title+"\n"+item.Text)
		}
	}
	for id := range state.Vectors {
		if !current[id] {
			delete(state.Vectors, id)
		}
	}
	if len(texts) == 0 {
		return true
	}
	vectors, err := a.store.DerivedVectors(ctx, texts, *a.config.Embedding, true)
	if err != nil {
		return false // The lexical overlay still publishes; a later job can fill vectors.
	}
	for i, id := range missing {
		state.Vectors[id] = vectors[i]
	}
	return true
}

func (r *readRun) indexSimilarities(ctx context.Context, state overlayState) map[string]float64 {
	out := map[string]float64{}
	if r.agent.config.Embedding == nil || len(state.Vectors) == 0 {
		return out
	}
	raw, _ := json.Marshal(r.agent.config.Embedding)
	if state.VectorModel != reference("embedding", string(raw)).Hash {
		r.indexStale = true
		return out
	}
	vector, err := r.agent.store.QueryVector(ctx, r.request.Question, *r.agent.config.Embedding)
	if err != nil {
		r.indexStale = true
		return out
	}
	for id, candidate := range state.Vectors {
		if len(candidate) != len(vector) {
			continue
		}
		score := records.VectorDot(vector, candidate)
		if !math.IsNaN(score) && !math.IsInf(score, 0) {
			out[id] = max(0, score)
		}
	}
	return out
}
