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

// recallRerankWeights is the intent-weighted re-ranking table of native
// Smart Recall's step 4 (docs/design/05-pipelines.md §5.2, "Multi-Factor
// Re-Ranking"). internal/memory/search/recall.go weighs by embedding
// availability instead (rerank*WithEmbed, rerank*NoEmbed); this port follows
// the design's intent table.
var recallRerankWeights = map[retrieval.Intent]rerankWeights{
	retrieval.IntentWhy:     {keyword: 0.10, entity: 0.10, similarity: 0.30, graph: 0.50},
	retrieval.IntentWhen:    {keyword: 0.15, entity: 0.15, similarity: 0.30, graph: 0.40},
	retrieval.IntentEntity:  {keyword: 0.20, entity: 0.40, similarity: 0.20, graph: 0.20},
	retrieval.IntentGeneral: {keyword: 0.25, entity: 0.25, similarity: 0.25, graph: 0.25},
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

// similarities memoizes one query's cosines with stored record vectors,
// shared by the beam search and the re-ranking (native's recallCache). With
// no reader every cosine is 0.
type similarities struct {
	read  func([]string) (map[string]float64, error)
	known map[string]float64
}

// load reads the cosines of ids not read yet, in one call.
func (s *similarities) load(ids []string) error {
	if s == nil || s.read == nil {
		return nil
	}
	if s.known == nil {
		s.known = map[string]float64{}
	}
	missing, asked := []string{}, map[string]bool{}
	for _, id := range ids {
		if _, ok := s.known[id]; !ok && !asked[id] {
			asked[id] = true
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	found, err := s.read(missing)
	if err != nil {
		return err
	}
	for _, id := range missing {
		s.known[id] = found[id]
	}
	return nil
}

// of is a loaded cosine, counted only when positive as native's are.
func (s *similarities) of(id string) float64 {
	if s == nil || s.known[id] <= 0 {
		return 0
	}
	return s.known[id]
}

// recallQuery is what the re-ranking compares each collected record with.
type recallQuery struct {
	intent     retrieval.Intent
	tokens     map[string]bool // the query's tokens (retrieval.Tokenize)
	entities   []string        // the query's entities as the index resolves them
	similarity func([]string) (map[string]float64, error)
	limit      int
}

// recall is native Smart Recall from the anchors on (recall.go
// IntentAwareRecallFrom, steps 3–5): the beam search collects records, every
// collected record (anchors included) is re-ranked by four signals, the
// best limit are kept, and for WHY they are put in causal order. The result
// is the traversal route.
//
// The signals, each in [0, 1] as native's: keyword, the share of the query's
// tokens in the record's search text (the lexical index's tokenizer);
// entity, the query's entities the record names over max(1, their number);
// similarity, the cosine of the stored vectors (0 without one); graph, the
// beam search's score, min-max normalized over the collected records.
func (index *derivedIndex) recall(ctx context.Context, anchors []scored, q recallQuery) ([]string, error) {
	similar := &similarities{read: q.similarity}
	best, err := index.traverse(ctx, anchors, q.intent, similar)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(best))
	for id := range best {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	if err := similar.load(ids); err != nil {
		return nil, err
	}
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
		signals := recallSignals{similarity: similar.of(id), graph: best[id]}
		if len(q.tokens) > 0 {
			signals.keyword = float64(keyword[id]) / float64(len(q.tokens))
		}
		if len(entities) > 0 {
			matched := 0
			for _, entity := range index.named[id] {
				if entities[strings.ToLower(entity)] {
					matched++
				}
			}
			signals.entity = float64(matched) / math.Max(1, float64(len(entities)))
		}
		candidates[id] = signals
	}
	ranked := rerankRecall(candidates, q.intent)
	ranked = ranked[:min(q.limit, len(ranked))]
	if q.intent == retrieval.IntentWhy {
		ranked = index.causalOrder(ranked)
	}
	return ranked, nil
}

// recallSignals is a collected record's re-ranking signals; graph holds the
// raw beam score until rerankRecall normalizes it.
type recallSignals struct{ keyword, entity, similarity, graph float64 }

// rerankRecall is native's multi-factor re-ranking: the graph score min-max
// normalized over the candidates (all 0 when they are equal), then the
// intent-weighted sum; best first, ties by id.
func rerankRecall(candidates map[string]recallSignals, intent retrieval.Intent) []string {
	w, ok := recallRerankWeights[intent]
	if !ok {
		w = recallRerankWeights[retrieval.IntentGeneral]
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
	best := map[string]float64{}
	for _, anchor := range anchors {
		score := anchor.score
		if top > 0 {
			score /= top
		}
		best[anchor.id] = score
	}
	for _, anchor := range anchors {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := t.beam(anchor.id, best[anchor.id], budget, best); err != nil {
			return nil, err
		}
	}
	return best, nil
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
	targets := make([]string, len(links))
	for i, link := range links {
		targets[i] = link.ID
	}
	if err := t.similar.load(targets); err != nil {
		return nil, err
	}
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
	return steps, nil
}
