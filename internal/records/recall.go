package records

import (
	"container/heap"
	"context"
	"math"
	"slices"
	"strings"

	"github.com/mnemon-dev/mnemon/internal/retrieval"
)

// Native Smart Recall (internal/memory/search/recall.go, docs/design/05-pipelines.md
// §5.2) on the derived index: the recency anchor signal, and from the
// anchors the beam search over the links, the multi-factor re-ranking of
// every record collected, and for WHY the causal order. Records may not
// import internal/memory, so its constants are copied here.

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

// rerankWeights weighs the four re-ranking signals.
type rerankWeights struct{ keyword, entity, similarity, graph float64 }

// The re-ranking weights native Smart Recall runs
// (internal/memory/search/recall.go rerank*WithEmbed, rerank*NoEmbed): they
// depend on whether embeddings are available, not on the intent; without
// them the similarity share goes to the other signals. The intent table of
// docs/design/05-pipelines.md §5.2 step 4 is not what native's code does.
var (
	recallRerankWithVectors    = rerankWeights{keyword: 0.30, entity: 0.15, similarity: 0.35, graph: 0.20}
	recallRerankWithoutVectors = rerankWeights{keyword: 0.45, entity: 0.25, similarity: 0, graph: 0.30}
)

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

// similarities is one query's cosines with the records' vectors: those the
// hybrid base route computed for every record of the selection, in its
// order (native's cache.similarities, which beam search and re-ranking
// share). nil when the query has no vector.
type similarities struct {
	cosines []scored       // the selection's docs, in order
	at      map[string]int // a record's place in cosines (the selection's byID)
}

// of is a record's cosine, counted only when positive as native's are; 0
// for a record the cosines do not cover.
func (s *similarities) of(id string) float64 {
	if s == nil {
		return 0
	}
	at, ok := s.at[id]
	if !ok || at >= len(s.cosines) || s.cosines[at].id != id || !(s.cosines[at].score > 0) {
		return 0
	}
	return s.cosines[at].score
}

// available is native's test for its re-ranking weights, "some embedding
// was compared with the query" (len(similarities) > 0): some record has a
// cosine, of any sign. A NaN marks a vector of another dimension.
func (s *similarities) available() bool {
	if s == nil {
		return false
	}
	for _, c := range s.cosines {
		if !math.IsNaN(c.score) {
			return true
		}
	}
	return false
}

// recallQuery is what the re-ranking compares each collected record with.
type recallQuery struct {
	intent   retrieval.Intent
	tokens   map[string]bool // the query's tokens (retrieval.Tokenize)
	entities []string        // the query's entities as the index resolves them
	similar  *similarities
	limit    int
}

// recall is native Smart Recall from the anchors on (recall.go
// IntentAwareRecallFrom, steps 3–5): the beam search collects records, every
// collected record (anchors included) is re-ranked by four signals, the
// best limit are kept, and for WHY they are put in causal order. It returns
// the traversal route and how many of its records are not anchors (reached
// over links).
//
// The signals, each in [0, 1] as native's: keyword, the share of the query's
// tokens in the record's search text (the lexical index's tokenizer);
// entity, the query's entities the record names over max(1, their number),
// names compared without case and counted once; similarity, the base
// route's cosine (0 without one); graph, the beam search's score, min-max
// normalized over the collected records.
func (index *derivedIndex) recall(ctx context.Context, anchors []scored, q recallQuery) ([]string, int, error) {
	best, err := index.traverse(ctx, anchors, q.intent, q.similar)
	if err != nil {
		return nil, 0, err
	}
	ids := make([]string, 0, len(best))
	for id := range best {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	keyword := map[string]int{}
	for token := range q.tokens {
		for _, id := range index.words[token] {
			if _, ok := best[id]; ok {
				keyword[id]++
			}
		}
	}
	entities := map[string]bool{}
	for _, entity := range q.entities {
		entities[strings.ToLower(entity)] = true
	}
	candidates := make(map[string]recallSignals, len(best))
	for _, id := range ids {
		signals := recallSignals{similarity: q.similar.of(id), graph: best[id]}
		if len(q.tokens) > 0 {
			signals.keyword = float64(keyword[id]) / float64(len(q.tokens))
		}
		if len(entities) > 0 {
			matched, seen := 0, map[string]bool{}
			for _, entity := range index.named[id] {
				if name := strings.ToLower(entity); entities[name] && !seen[name] {
					seen[name] = true
					matched++
				}
			}
			signals.entity = float64(matched) / math.Max(1, float64(len(entities)))
		}
		candidates[id] = signals
	}
	ranked := rerankRecall(candidates, q.similar.available())
	ranked = ranked[:min(q.limit, len(ranked))]
	if q.intent == retrieval.IntentWhy {
		ranked = index.causalOrder(ranked)
	}
	isAnchor := make(map[string]bool, len(anchors))
	for _, anchor := range anchors {
		isAnchor[anchor.id] = true
	}
	reached := 0
	for _, id := range ranked {
		if !isAnchor[id] {
			reached++
		}
	}
	return ranked, reached, nil
}

// recallSignals is a collected record's re-ranking signals; graph holds the
// raw beam score until rerankRecall normalizes it.
type recallSignals struct{ keyword, entity, similarity, graph float64 }

// rerankRecall is native's multi-factor re-ranking: the graph score min-max
// normalized over the candidates (all 0 when they are equal), then the
// weighted sum, with native's weights for whether vectors were available;
// best first, ties by id.
func rerankRecall(candidates map[string]recallSignals, withVectors bool) []string {
	w := recallRerankWithoutVectors
	if withVectors {
		w = recallRerankWithVectors
	}
	first := true
	var low, high float64
	for _, s := range candidates {
		if first || s.graph < low {
			low = s.graph
		}
		if first || s.graph > high {
			high = s.graph
		}
		first = false
	}
	span := high - low
	if span == 0 {
		span = 1
	}
	hits := make([]scored, 0, len(candidates))
	for id, s := range candidates {
		graph := (s.graph - low) / span
		hits = append(hits, scored{id, w.keyword*s.keyword + w.entity*s.entity + w.similarity*s.similarity + w.graph*graph})
	}
	return rank(hits, len(hits))
}

// causalOrder is native's causalTopologicalSort: Kahn's algorithm over the
// causal links among the results (a link's source causes its target), so
// causes come before their effects. Among the records ready, the best
// ranked goes first (native's max-heap on score; equal scores here by id,
// which the results' order already holds). Records on a cycle follow, in
// result order.
func (index *derivedIndex) causalOrder(results []string) []string {
	if len(results) <= 1 {
		return results
	}
	position := make(map[string]int, len(results))
	for i, id := range results {
		position[id] = i
	}
	effects := make([][]int, len(results))
	causes := make([]int, len(results))
	for i, id := range results {
		for _, link := range index.links[id] {
			if at, ok := position[link.ID]; ok && link.Type == "causal" {
				effects[i] = append(effects[i], at)
				causes[at]++
			}
		}
	}
	ready := &positionHeap{}
	for i := range results {
		if causes[i] == 0 {
			heap.Push(ready, i)
		}
	}
	ordered := make([]string, 0, len(results))
	placed := make([]bool, len(results))
	for ready.Len() > 0 {
		i := heap.Pop(ready).(int)
		ordered = append(ordered, results[i])
		placed[i] = true
		for _, effect := range effects[i] {
			if causes[effect]--; causes[effect] == 0 {
				heap.Push(ready, effect)
			}
		}
	}
	for i, id := range results {
		if !placed[i] {
			ordered = append(ordered, id)
		}
	}
	return ordered
}

// positionHeap is a min-heap of result positions: the best ranked first.
type positionHeap []int

func (h positionHeap) Len() int           { return len(h) }
func (h positionHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h positionHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *positionHeap) Push(x any)        { *h = append(*h, x.(int)) }
func (h *positionHeap) Pop() any {
	old := *h
	item := old[len(old)-1]
	*h = old[:len(old)-1]
	return item
}

// traversal is one query's beam search state: native's recallCache, whose
// ranked neighborhoods and similarities serve every anchor of the query.
type traversal struct {
	links   map[string][]Link
	weights map[string]float64
	similar *similarities
	steps   map[string][]traversalStep
}

// traversalStep is a link out of a node with its score increment
// (native's recallTransition).
type traversalStep struct {
	id, kind string
	delta    float64
}

// traverse is native's beam search (recall.go beamSearchFromAnchor) over the
// derived links: from each anchor, with its RRF score normalized by the best
// anchor's as native's are, it expands the beam level by level, adding each
// step's score to the path's. It returns every collected record, anchors
// included, with its best path score (an anchor's own score unless a path
// beats it), as native's scoreMap.
func (index *derivedIndex) traverse(ctx context.Context, anchors []scored, intent retrieval.Intent, similar *similarities) (map[string]float64, error) {
	weights, ok := traversalWeights[intent]
	if !ok {
		weights = traversalWeights[retrieval.IntentGeneral]
	}
	budget, ok := traversalBudgets[intent]
	if !ok {
		budget = traversalBudgets[retrieval.IntentGeneral]
	}
	t := &traversal{links: index.links, weights: weights, similar: similar, steps: map[string][]traversalStep{}}
	top := 0.0
	for _, anchor := range anchors {
		top = max(top, anchor.score)
	}
	normalized := func(score float64) float64 {
		if top > 0 {
			return score / top
		}
		return score
	}
	best := map[string]float64{}
	for _, anchor := range anchors {
		best[anchor.id] = normalized(anchor.score)
	}
	for _, anchor := range anchors {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// From the anchor's own score, as native's beamSearchFromAnchor, not
		// from best, which an earlier anchor's path may have raised: the
		// anchors' order does not change the result.
		t.beam(anchor.id, normalized(anchor.score), budget, best)
	}
	return best, nil
}

// beam is native's beamSearchFromAnchor: visited and the visit budget belong
// to this anchor, best to the query.
func (t *traversal) beam(start string, score float64, budget traversalBudget, best map[string]float64) {
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
			for _, step := range t.stepsFrom(node.id) {
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
}

// stepsFrom is native's rankRecallTransitions: the node's traversable
// links, best step first (ties by id, then type), computed once per query.
func (t *traversal) stepsFrom(id string) []traversalStep {
	if steps, ok := t.steps[id]; ok {
		return steps
	}
	links := t.links[id]
	steps := make([]traversalStep, 0, len(links))
	for _, link := range links {
		weight, ok := t.weights[link.Type]
		if !ok {
			continue
		}
		delta := traversalStructural * weight * link.Weight
		if similarity := t.similar.of(link.ID); similarity > 0 {
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
	return steps
}
