package records

import (
	"context"
	"math"
	"slices"
	"strings"

	"github.com/mnemon-dev/mnemon/internal/retrieval"
)

// Native Smart Recall (internal/memory/search/recall.go, docs/design/05-pipelines.md
// §5.2) on the derived index: the recency anchor signal and the beam search
// from the anchors over the links. Records may not import internal/memory,
// so its constants are copied here.

// recallAnchors is native's per-signal anchor limit (anchorTopK): the
// recency route's length and the number of fused seeds traversal starts from.
const recallAnchors = 20

// Native's additive transition score (recall.go lambda1, lambda2): a step
// adds λ1 × link weight × intent weight, plus λ2 × the neighbor's cosine
// with the query when that is positive.
const (
	traversalStructural = 1.0
	traversalSemantic   = 0.4
)

// traversalWeights is native's intentWeightsMap
// (internal/memory/search/intent.go): the edge weight of each link type per
// intent. Other link types (supersedes) are not traversed.
var traversalWeights = map[retrieval.Intent]map[string]float64{
	retrieval.IntentWhy:     {"causal": 0.70, "temporal": 0.20, "entity": 0.05, "semantic": 0.05},
	retrieval.IntentWhen:    {"temporal": 0.65, "causal": 0.15, "entity": 0.10, "semantic": 0.10},
	retrieval.IntentEntity:  {"entity": 0.55, "semantic": 0.30, "temporal": 0.05, "causal": 0.10},
	retrieval.IntentGeneral: {"temporal": 0.25, "semantic": 0.25, "causal": 0.25, "entity": 0.25},
}

// traversalBudget is native's TraversalParams: nodes kept per level, levels,
// and nodes one anchor's search may visit (the anchor included).
type traversalBudget struct{ width, depth, visited int }

// traversalBudgets is native's intentTraversalParams (recall.go).
var traversalBudgets = map[retrieval.Intent]traversalBudget{
	retrieval.IntentWhy:     {width: 15, depth: 5, visited: 500},
	retrieval.IntentWhen:    {width: 10, depth: 5, visited: 400},
	retrieval.IntentEntity:  {width: 10, depth: 4, visited: 400},
	retrieval.IntentGeneral: {width: 10, depth: 4, visited: 500},
}

// newest is native's recency anchor signal: the indexed records by index
// time (eventAt, else the record date, else createdAt), newest first, ties
// by id, at most n. One pass keeps the n newest; the index keeps the list.
func (index *derivedIndex) newest(n int) []string {
	newer := func(a, b string) int {
		if c := index.meta[b].at.Compare(index.meta[a].at); c != 0 {
			return c
		}
		return compareID(a, b)
	}
	out := make([]string, 0, n+1)
	for _, r := range index.docs {
		if len(out) == n && newer(r.ID, out[n-1]) >= 0 {
			continue
		}
		at, _ := slices.BinarySearchFunc(out, r.ID, newer)
		out = slices.Insert(out, at, r.ID)
		if len(out) > n {
			out = out[:n]
		}
	}
	return out
}

// recallSeeds is traversal's anchors: the first twenty fused seeds with
// their reciprocal-rank scores, as native's anchors carry their RRF scores.
func recallSeeds(seeds []string, lists [][]string) []scored {
	score := map[string]float64{}
	for _, hit := range fusedHits(lists...) {
		score[hit.id] = hit.score
	}
	anchors := make([]scored, 0, recallAnchors)
	for _, id := range seeds[:min(recallAnchors, len(seeds))] {
		anchors = append(anchors, scored{id, score[id]})
	}
	return anchors
}

// storedSimilarity scores records by the cosine of their stored vectors
// with the query's (the vectors are unit length). It reads the in-memory and
// on-disk embedding caches only and never embeds; a record without a stored
// vector, or of another dimension, has none.
func (s *Store) storedSimilarity(ctx context.Context, index *derivedIndex, cfg EmbeddingConfig, query []float32) func([]string) (map[string]float64, error) {
	return func(ids []string) (map[string]float64, error) {
		known, texts := []string{}, []string{}
		for _, id := range ids {
			if at, ok := index.position[id]; ok {
				known = append(known, id)
				texts = append(texts, index.docs[at].VectorText)
			}
		}
		values, err := s.vectors(ctx, texts, cfg, cfg.DocumentPrefix, false)
		if err != nil {
			return nil, err
		}
		out := make(map[string]float64, len(known))
		for i, value := range values {
			if value == nil {
				continue
			}
			if similarity := dot(query, value); !math.IsNaN(similarity) {
				out[known[i]] = similarity
			}
		}
		return out, nil
	}
}

// traversal is one query's beam search state: native's recallCache, whose
// ranked neighborhoods and similarities serve every anchor of the query.
type traversal struct {
	links      map[string][]Link
	weights    map[string]float64
	similarity func([]string) (map[string]float64, error) // nil: no semantic term
	similar    map[string]float64
	steps      map[string][]traversalStep
}

// traversalStep is a link out of a node with its score increment
// (native's recallTransition).
type traversalStep struct {
	id, kind string
	delta    float64
}

// traverse ports native's beam search (recall.go beamSearchFromAnchor) to
// the derived links: from each anchor, with its RRF score normalized by the
// best anchor's as native's are, it expands the beam level by level, adding
// each step's score to the path's, keeps every record's best path score, and
// returns the records it reached that are not anchors, best first (ties by
// id), at most limit. similarity, when set, scores records against the query
// for the semantic term.
func (index *derivedIndex) traverse(ctx context.Context, anchors []scored, intent retrieval.Intent, similarity func([]string) (map[string]float64, error), limit int) ([]string, error) {
	weights, ok := traversalWeights[intent]
	if !ok {
		weights = traversalWeights[retrieval.IntentGeneral]
	}
	budget, ok := traversalBudgets[intent]
	if !ok {
		budget = traversalBudgets[retrieval.IntentGeneral]
	}
	t := &traversal{links: index.links, weights: weights, similarity: similarity, similar: map[string]float64{}, steps: map[string][]traversalStep{}}
	top := 0.0
	for _, anchor := range anchors {
		top = max(top, anchor.score)
	}
	best, isAnchor := map[string]float64{}, map[string]bool{}
	for _, anchor := range anchors {
		score := anchor.score
		if top > 0 {
			score /= top
		}
		best[anchor.id], isAnchor[anchor.id] = score, true
	}
	for _, anchor := range anchors {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := t.beam(anchor.id, best[anchor.id], budget, best); err != nil {
			return nil, err
		}
	}
	hits := []scored{}
	for id, score := range best {
		if !isAnchor[id] {
			hits = append(hits, scored{id, score})
		}
	}
	return rank(hits, limit), nil
}

// beam is native's beamSearchFromAnchor: visited and the visit budget belong
// to this anchor, best to the query.
func (t *traversal) beam(start string, score float64, budget traversalBudget, best map[string]float64) error {
	visited := map[string]bool{start: true}
	total := 1
	current := []scored{{start, score}}
	for depth := 0; depth < budget.depth; depth++ {
		if len(current) == 0 || total >= budget.visited {
			break
		}
		next := []scored{}
		for _, node := range current {
			if total >= budget.visited {
				break
			}
			steps, err := t.stepsFrom(node.id)
			if err != nil {
				return err
			}
			for _, step := range steps {
				if total >= budget.visited {
					break
				}
				reached := node.score + step.delta
				if old, ok := best[step.id]; !ok || reached > old {
					best[step.id] = reached
				}
				if !visited[step.id] {
					visited[step.id] = true
					total++
					next = append(next, scored{step.id, reached})
				}
			}
		}
		// Keep the beam's best (native pops a max-heap; ties go by id here).
		slices.SortFunc(next, compareScore)
		current = next[:min(budget.width, len(next))]
	}
	return nil
}

// stepsFrom is native's rankRecallTransitions: the node's traversable
// links, best step first (ties by id, then type), computed once per query.
func (t *traversal) stepsFrom(id string) ([]traversalStep, error) {
	if steps, ok := t.steps[id]; ok {
		return steps, nil
	}
	links := t.links[id]
	if t.similarity != nil {
		missing, asked := []string{}, map[string]bool{}
		for _, link := range links {
			if _, ok := t.similar[link.ID]; !ok && !asked[link.ID] {
				asked[link.ID] = true
				missing = append(missing, link.ID)
			}
		}
		if len(missing) > 0 {
			found, err := t.similarity(missing)
			if err != nil {
				return nil, err
			}
			for _, neighbor := range missing {
				t.similar[neighbor] = found[neighbor]
			}
		}
	}
	steps := make([]traversalStep, 0, len(links))
	for _, link := range links {
		weight, ok := t.weights[link.Type]
		if !ok {
			continue
		}
		delta := traversalStructural * weight * link.Weight
		if similarity := t.similar[link.ID]; similarity > 0 {
			delta += traversalSemantic * similarity
		}
		steps = append(steps, traversalStep{id: link.ID, kind: link.Type, delta: delta})
	}
	slices.SortFunc(steps, func(a, b traversalStep) int {
		if a.delta != b.delta {
			if a.delta > b.delta {
				return -1
			}
			return 1
		}
		if c := compareID(a.id, b.id); c != 0 {
			return c
		}
		return strings.Compare(a.kind, b.kind)
	})
	t.steps[id] = steps
	return steps, nil
}
