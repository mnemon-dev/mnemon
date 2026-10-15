package search

import (
	"container/heap"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/mnemon-dev/mnemon/internal/memory/model"
	"github.com/mnemon-dev/mnemon/internal/memory/store"
)

// Beam search parameters (MAGMA-aligned).
const (
	anchorTopK = 20  // per-signal anchor limit (MAGMA: 15-30)
	lambda1    = 1.0 // structural weight (MAGMA paper: 1.0)
	lambda2    = 0.4 // semantic weight (MAGMA paper: 0.3-0.7)
)

// TraversalParams holds per-intent adaptive beam search parameters (MAGMA §4.2).
type TraversalParams struct {
	BeamWidth  int // candidates retained per level
	MaxDepth   int // max traversal hops
	MaxVisited int // total node budget
}

// intentTraversalParams maps intent to adaptive traversal parameters.
// MAGMA reference code uses different depths/budgets per query type.
var intentTraversalParams = map[Intent]TraversalParams{
	IntentWhy: {
		BeamWidth:  15,
		MaxDepth:   5,
		MaxVisited: 500,
	},
	IntentWhen: {
		BeamWidth:  10,
		MaxDepth:   5,
		MaxVisited: 400,
	},
	IntentEntity: {
		BeamWidth:  10,
		MaxDepth:   4,
		MaxVisited: 400,
	},
	IntentGeneral: {
		BeamWidth:  10,
		MaxDepth:   4,
		MaxVisited: 500,
	},
}

// getTraversalParams returns the adaptive traversal parameters for the given intent.
func getTraversalParams(intent Intent) TraversalParams {
	if p, ok := intentTraversalParams[intent]; ok {
		return p
	}
	return intentTraversalParams[IntentGeneral]
}

// RRF constant (standard value from Cormack et al. 2009).
const rrfK = 60

// Reranking weight constants.
const (
	// Weights when embeddings are available.
	rerankKeywordWithEmbed    = 0.30
	rerankEntityWithEmbed     = 0.15
	rerankSimilarityWithEmbed = 0.35
	rerankGraphWithEmbed      = 0.20

	// Weights when embeddings are NOT available (similarity share redistributed).
	rerankKeywordNoEmbed = 0.45
	rerankEntityNoEmbed  = 0.25
	rerankGraphNoEmbed   = 0.30
)

// supersededScoreFactor multiplies the final score of an insight that some
// other insight claims to supersede. It is a demotion, not a filter: the row
// stays reachable (lineage, audit, explicit recall) but stops outranking the
// content that replaced it. Without this, a correction competes with the text
// it corrects on similarity alone and routinely loses, because the stale row
// is older and therefore better connected and more frequently accessed.
const supersededScoreFactor = 0.25

// SignalScores holds the individual reranking signal scores for a result.
type SignalScores struct {
	Keyword    float64 `json:"keyword"`
	Entity     float64 `json:"entity"`
	Similarity float64 `json:"similarity"`
	Graph      float64 `json:"graph"`
}

// RecallMeta holds metadata about a smart recall operation.
type RecallMeta struct {
	Intent       Intent `json:"intent"`
	IntentSource string `json:"intent_source"` // "auto" or "override"
	AnchorCount  int    `json:"anchor_count"`
	Traversed    int    `json:"traversed"`
	Hint         string `json:"hint,omitempty"`
}

// RecallResponse wraps recall results with metadata.
type RecallResponse struct {
	Results []RecallResult `json:"results"`
	Meta    RecallMeta     `json:"meta"`
}

// RecallResult represents a recalled insight with its relevance score.
type RecallResult struct {
	Insight *model.Insight `json:"insight"`
	Score   float64        `json:"score"`
	Intent  Intent         `json:"intent"`
	Via     string         `json:"via,omitempty"`
	Signals SignalScores   `json:"signals"`
	// Superseded reports that another insight claims to replace this one.
	// Surfaced so a caller can see why a result ranked low, and so an agent
	// reading raw results is not silently handed stale content.
	Superseded bool `json:"superseded,omitempty"`
	// Relevance is the decision-model judge's probability that this result
	// answers the query. It is nil unless recall ran with --judge.
	Relevance *float64 `json:"relevance,omitempty"`
}

// IntentAwareRecall performs MAGMA-aligned intent-aware retrieval:
// 1. Detect query intent (or use override)
// 2. Multi-signal anchor selection via RRF (keyword + vector + time)
// 3. Beam search from anchors with additive transition scoring
// 4. Multi-factor reranking (keyword + entity + similarity + graph)
// 5. WHY intent → causal topological sort
// 6. Sparse hint detection
func IntentAwareRecall(db *store.DB, query string, queryVec []float64,
	queryEntities []string, limit int, intentOverride *Intent) (RecallResponse, error) {
	all, err := db.GetAllActiveInsights()
	if err != nil {
		return RecallResponse{}, err
	}
	return IntentAwareRecallFrom(db, all, query, queryVec, queryEntities, limit, intentOverride)
}

// IntentAwareRecallFrom is IntentAwareRecall over a caller-loaded snapshot of
// the active insights, so a caller that already scanned them (for example to
// build the known-entity set) does not scan the table twice.
func IntentAwareRecallFrom(db *store.DB, all []*model.Insight, query string, queryVec []float64,
	queryEntities []string, limit int, intentOverride *Intent) (RecallResponse, error) {

	// Step 1: Intent determination
	var intent Intent
	intentSource := "auto"
	if intentOverride != nil {
		intent = *intentOverride
		intentSource = "override"
	} else {
		intent = DetectIntent(query)
	}

	weights := GetWeights(intent)
	params := getTraversalParams(intent)

	// Cache only this query's scalar signals and ranked transitions it visits.
	// Full vectors and token sets need not survive their first scoring pass.
	cache := newRecallCache(db, all, queryVec, weights)
	hasEmbeddings := len(cache.similarities) > 0

	// Step 2: Multi-signal anchor selection via RRF
	type anchor struct {
		score float64
		via   string
	}
	anchorMap := make(map[string]*anchor)

	// Signal 1: Keyword search (retains scalar scores for reranking)
	keywordScores := make(map[string]float64, len(all))
	keywordAnchors := keywordSearchCached(all, query, anchorTopK, keywordScores)
	for rank, a := range keywordAnchors {
		anchorMap[a.Insight.ID] = &anchor{
			score: 1.0 / float64(rrfK+rank+1),
			via:   "keyword",
		}
	}

	// Signal 2: Vector search (when available, uses pre-loaded cache)
	if hasEmbeddings {
		vectorHits := vectorSearchFromScores(cache.similarities, anchorTopK)
		for rank, vh := range vectorHits {
			rrfScore := 1.0 / float64(rrfK+rank+1)
			if existing, ok := anchorMap[vh.id]; ok {
				existing.score += rrfScore
				existing.via = "hybrid"
			} else {
				ins := cache.insights[vh.id]
				if ins == nil {
					continue
				}
				anchorMap[vh.id] = &anchor{
					score: rrfScore,
					via:   "vector",
				}
			}
		}
	}

	// Signal 3: Time-based ranking (MAGMA third RRF signal)
	timeSorted := all
	byNewest := func(i, j int) bool { return timeSorted[i].CreatedAt.After(timeSorted[j].CreatedAt) }
	// SQL already orders normal UTC timestamps. Verify absolute time order
	// before reusing it: imported RFC3339 offsets need not sort lexically.
	if !sort.SliceIsSorted(timeSorted, byNewest) {
		timeSorted = append([]*model.Insight(nil), all...)
		sort.Slice(timeSorted, byNewest)
	}
	timeLimit := anchorTopK
	if timeLimit > len(timeSorted) {
		timeLimit = len(timeSorted)
	}
	for rank := 0; rank < timeLimit; rank++ {
		ins := timeSorted[rank]
		rrfScore := 1.0 / float64(rrfK+rank+1)
		if existing, ok := anchorMap[ins.ID]; ok {
			existing.score += rrfScore
			if existing.via == "keyword" || existing.via == "vector" {
				existing.via = "hybrid"
			}
		} else {
			anchorMap[ins.ID] = &anchor{
				score: rrfScore,
				via:   "time",
			}
		}
	}

	// Normalize anchor scores to [0, 1]
	var maxAnchorScore float64
	for _, a := range anchorMap {
		if a.score > maxAnchorScore {
			maxAnchorScore = a.score
		}
	}
	if maxAnchorScore > 0 {
		for _, a := range anchorMap {
			a.score /= maxAnchorScore
		}
	}

	anchorCount := len(anchorMap)

	// Initialize score map with anchors
	scoreMap := make(map[string]float64)
	viaMap := make(map[string]string)

	for id, a := range anchorMap {
		scoreMap[id] = a.score
		viaMap[id] = a.via
	}

	// Step 3: Beam search from each anchor
	for id, a := range anchorMap {
		cache.beamSearchFromAnchor(id, a.score, params, scoreMap, viaMap)
	}

	traversedCount := len(scoreMap)

	// Step 4: Multi-factor reranking
	queryEntitySet := make(map[string]bool, len(queryEntities))
	for _, e := range queryEntities {
		queryEntitySet[strings.ToLower(e)] = true
	}

	// Compute raw graph scores and find min/max for normalization
	type candidate struct {
		id         string
		ins        *model.Insight
		via        string
		graphRaw   float64
		kwScore    float64
		entScore   float64
		simScore   float64
		graphScore float64
	}

	candidates := make([]candidate, 0, len(scoreMap))
	var graphMin, graphMax float64
	first := true
	for id, graphRaw := range scoreMap {
		ins, ok := cache.insights[id]
		if !ok {
			continue
		}
		if first {
			graphMin = graphRaw
			graphMax = graphRaw
			first = false
		} else {
			if graphRaw < graphMin {
				graphMin = graphRaw
			}
			if graphRaw > graphMax {
				graphMax = graphRaw
			}
		}
		candidates = append(candidates, candidate{
			id: id, ins: ins, via: viaMap[id], graphRaw: graphRaw,
		})
	}

	graphRange := graphMax - graphMin
	if graphRange == 0 {
		graphRange = 1.0 // prevent division by zero
	}

	// Compute per-candidate signal scores
	for i := range candidates {
		c := &candidates[i]

		// keyword_score: exactly the overlap computed during anchor selection.
		c.kwScore = keywordScores[c.id]

		// entity_score: entity overlap
		if len(queryEntitySet) > 0 {
			matched := 0
			for _, ent := range c.ins.Entities {
				if queryEntitySet[strings.ToLower(ent)] {
					matched++
				}
			}
			c.entScore = float64(matched) / math.Max(1, float64(len(queryEntitySet)))
		}

		// similarity: reuse the cosine computed during the embedding scan.
		if sim := cache.similarities[c.id]; sim > 0 {
			c.simScore = sim
		}

		// graph_score: min-max normalized beam search score
		c.graphScore = (c.graphRaw - graphMin) / graphRange
	}

	// Compute final weighted score
	wKw, wEnt, wSim, wGr := rerankKeywordWithEmbed, rerankEntityWithEmbed, rerankSimilarityWithEmbed, rerankGraphWithEmbed
	if !hasEmbeddings {
		wKw, wEnt, wSim, wGr = rerankKeywordNoEmbed, rerankEntityNoEmbed, 0, rerankGraphNoEmbed
	}

	// An insight that another insight claims to supersede is demoted before
	// ranking, and only the candidates in hand are looked up. A failed lookup
	// must not serve corrected content without its superseded marker.
	candidateIDs := make([]string, len(candidates))
	for i := range candidates {
		candidateIDs[i] = candidates[i].id
	}
	superseded, supersededErr := db.GetSupersededIDs(candidateIDs)
	if supersededErr != nil {
		return RecallResponse{}, fmt.Errorf("lookup superseded insights: %w", supersededErr)
	}

	results := make([]RecallResult, 0, len(candidates))
	for _, c := range candidates {
		finalScore := wKw*c.kwScore + wEnt*c.entScore + wSim*c.simScore + wGr*c.graphScore
		isSuperseded := superseded[c.id]
		if isSuperseded {
			finalScore *= supersededScoreFactor
		}
		results = append(results, RecallResult{
			Insight: c.ins,
			Score:   finalScore,
			Intent:  intent,
			Via:     c.via,
			Signals: SignalScores{
				Keyword:    c.kwScore,
				Entity:     c.entScore,
				Similarity: c.simScore,
				Graph:      c.graphScore,
			},
			Superseded: isSuperseded,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].Insight.Importance > results[j].Insight.Importance
	})

	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	// Step 5: WHY intent → causal topological sort
	if intent == IntentWhy {
		results = causalTopologicalSort(db, results)
	}

	// Step 6: Sparse hint
	hint := ""
	if len(results) == 0 || (limit > 0 && len(results) < limit/2) {
		hint = "sparse_results"
	}

	return RecallResponse{
		Results: results,
		Meta: RecallMeta{
			Intent:       intent,
			IntentSource: intentSource,
			AnchorCount:  anchorCount,
			Traversed:    traversedCount,
			Hint:         hint,
		},
	}, nil
}

// causalTopologicalSort reorders results so that causes appear before effects.
// It uses causal edges to build a DAG among the result set, then applies
// Kahn's algorithm. Nodes without causal ordering retain their score-based position.
func causalTopologicalSort(db *store.DB, results []RecallResult) []RecallResult {
	if len(results) <= 1 {
		return results
	}

	// Build a set of IDs in the result set for quick lookup
	idSet := make(map[string]bool, len(results))
	idToResult := make(map[string]RecallResult, len(results))
	for _, r := range results {
		idSet[r.Insight.ID] = true
		idToResult[r.Insight.ID] = r
	}

	// Build DAG from causal edges: source → target means source causes target
	adj := make(map[string][]string) // source → targets
	inDegree := make(map[string]int) // target → incoming edge count

	for _, r := range results {
		inDegree[r.Insight.ID] = 0
	}

	for _, r := range results {
		edges, err := db.GetEdgesBySourceAndType(r.Insight.ID, model.EdgeCausal)
		if err != nil {
			continue
		}
		for _, e := range edges {
			if idSet[e.TargetID] {
				adj[e.SourceID] = append(adj[e.SourceID], e.TargetID)
				inDegree[e.TargetID]++
			}
		}
	}

	// Kahn's algorithm with score-based tie-breaking via max-heap
	pq := &kahnMaxHeap{}
	for _, r := range results {
		if inDegree[r.Insight.ID] == 0 {
			heap.Push(pq, kahnItem{id: r.Insight.ID, score: idToResult[r.Insight.ID].Score})
		}
	}

	ordered := make([]RecallResult, 0, len(results))
	for pq.Len() > 0 {
		item := heap.Pop(pq).(kahnItem)
		ordered = append(ordered, idToResult[item.id])

		for _, target := range adj[item.id] {
			inDegree[target]--
			if inDegree[target] == 0 {
				heap.Push(pq, kahnItem{id: target, score: idToResult[target].Score})
			}
		}
	}

	// If topological sort didn't cover all nodes (cycles), append remaining
	if len(ordered) < len(results) {
		covered := make(map[string]bool, len(ordered))
		for _, r := range ordered {
			covered[r.Insight.ID] = true
		}
		for _, r := range results {
			if !covered[r.Insight.ID] {
				ordered = append(ordered, r)
			}
		}
	}

	return ordered
}

// beamSearchFromAnchor keeps traversal budgets and per-anchor visited sets local;
// ranked transitions and similarities are reused within this recall only.
func (cache *recallCache) beamSearchFromAnchor(
	startID string,
	startScore float64,
	params TraversalParams,
	scoreMap map[string]float64,
	viaMap map[string]string,
) {
	visited := map[string]bool{startID: true}
	totalVisited := 1

	// Seed the beam with the anchor
	current := &beamHeap{{id: startID, score: startScore, depth: 0}}
	heap.Init(current)

	for depth := 0; depth < params.MaxDepth; depth++ {
		if current.Len() == 0 || totalVisited >= params.MaxVisited {
			break
		}

		// Collect all candidates for the next level
		next := &beamHeap{}
		heap.Init(next)

		// Process all nodes at the current level
		for current.Len() > 0 && totalVisited < params.MaxVisited {
			cur := heap.Pop(current).(beamItem)
			if cur.depth != depth {
				// Put it back — it's for a future level
				heap.Push(current, cur)
				break
			}

			transitions, err := cache.transitionsFor(cur.id)
			if err != nil {
				continue
			}

			for _, transition := range transitions {
				if totalVisited >= params.MaxVisited {
					break
				}
				neighborID := transition.id
				neighborScore := cur.score + transition.delta

				// Update global score map if this path is better
				if existing, ok := scoreMap[neighborID]; !ok || neighborScore > existing {
					scoreMap[neighborID] = neighborScore
					viaMap[neighborID] = string(transition.edgeType)
				}

				if !visited[neighborID] {
					visited[neighborID] = true
					totalVisited++
					heap.Push(next, beamItem{
						id:    neighborID,
						score: neighborScore,
						depth: depth + 1,
					})
				}
			}
		}

		// Prune beam: keep only top beamWidth candidates for next level
		pruned := &beamHeap{}
		heap.Init(pruned)
		for next.Len() > 0 && pruned.Len() < params.BeamWidth {
			heap.Push(pruned, heap.Pop(next).(beamItem))
		}
		current = pruned
	}
}

// beamItem is a node in the beam search priority queue.
type beamItem struct {
	id    string
	score float64
	depth int
}

// beamHeap implements a max-heap for beam search (highest score first).
type beamHeap []beamItem

func (h beamHeap) Len() int            { return len(h) }
func (h beamHeap) Less(i, j int) bool  { return h[i].score > h[j].score } // max-heap
func (h beamHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *beamHeap) Push(x interface{}) { *h = append(*h, x.(beamItem)) }
func (h *beamHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

// kahnItem is a node in Kahn's topological sort priority queue.
type kahnItem struct {
	id    string
	score float64
}

// kahnMaxHeap implements a max-heap for Kahn's algorithm (highest score first).
type kahnMaxHeap []kahnItem

func (h kahnMaxHeap) Len() int            { return len(h) }
func (h kahnMaxHeap) Less(i, j int) bool  { return h[i].score > h[j].score }
func (h kahnMaxHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *kahnMaxHeap) Push(x interface{}) { *h = append(*h, x.(kahnItem)) }
func (h *kahnMaxHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

// vectorHit is a vector search result.
type vectorHit struct {
	id         string
	similarity float64
}

// vectorHitMinHeap implements a min-heap for top-k vector search (lowest similarity at root).
type vectorHitMinHeap []vectorHit

func (h vectorHitMinHeap) Len() int            { return len(h) }
func (h vectorHitMinHeap) Less(i, j int) bool  { return h[i].similarity < h[j].similarity }
func (h vectorHitMinHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *vectorHitMinHeap) Push(x interface{}) { *h = append(*h, x.(vectorHit)) }
func (h *vectorHitMinHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

// vectorSearch is the database-backed entry used by the vector search tests.
func vectorSearch(db *store.DB, queryVec []float64, limit int) []vectorHit {
	return vectorSearchFromScores(loadQuerySimilarities(db, queryVec), limit)
}

// vectorSearchFromScores selects exact top-k vector anchors in O(N log K),
// reusing similarities that beam search and reranking also consume.
func vectorSearchFromScores(similarities map[string]float64, limit int) []vectorHit {
	h := &vectorHitMinHeap{}
	for id, sim := range similarities {
		if sim <= 0.1 {
			continue
		}
		if limit <= 0 || h.Len() < limit {
			heap.Push(h, vectorHit{id: id, similarity: sim})
		} else if sim > (*h)[0].similarity {
			(*h)[0] = vectorHit{id: id, similarity: sim}
			heap.Fix(h, 0)
		}
	}
	if h.Len() == 0 {
		return nil
	}

	// Extract results in descending order (highest similarity first).
	result := make([]vectorHit, h.Len())
	for i := len(result) - 1; i >= 0; i-- {
		result[i] = heap.Pop(h).(vectorHit)
	}
	return result
}
