package search

import (
	"container/heap"
	"math"
	"sort"
	"strings"

	"github.com/mnemon-dev/mnemon/internal/memory/embed"
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

	// Get all active insights
	all, err := db.GetAllActiveInsights()
	if err != nil {
		return RecallResponse{}, err
	}

	// Pre-load all embeddings once (avoids N+1 queries in beam search and reranking).
	var embedCache map[string][]float64
	if queryVec != nil {
		if dbEmbeds, err := db.GetAllEmbeddings(); err == nil {
			embedCache = make(map[string][]float64, len(dbEmbeds))
			for _, e := range dbEmbeds {
				if v := embed.DeserializeVector(e.Embedding); v != nil {
					embedCache[e.ID] = v
				}
			}
		}
	}
	hasEmbeddings := embedCache != nil && len(embedCache) > 0

	// Step 2: Multi-signal anchor selection via RRF
	type anchor struct {
		insight *model.Insight
		score   float64
		via     string
	}
	anchorMap := make(map[string]*anchor)

	// Signal 1: Keyword search (populates tokenCache for reranking reuse)
	tokenCache := make(map[string]map[string]bool, len(all))
	keywordAnchors := keywordSearchCached(all, query, anchorTopK, tokenCache)
	for rank, a := range keywordAnchors {
		anchorMap[a.Insight.ID] = &anchor{
			insight: a.Insight,
			score:   1.0 / float64(rrfK+rank+1),
			via:     "keyword",
		}
	}

	// Signal 2: Vector search (when available, uses pre-loaded cache)
	if hasEmbeddings {
		vectorHits := vectorSearchFromCache(embedCache, queryVec, anchorTopK)
		for rank, vh := range vectorHits {
			rrfScore := 1.0 / float64(rrfK+rank+1)
			if existing, ok := anchorMap[vh.id]; ok {
				existing.score += rrfScore
				existing.via = "hybrid"
			} else {
				ins, err := db.GetInsightByID(vh.id)
				if err != nil || ins == nil {
					continue
				}
				anchorMap[vh.id] = &anchor{
					insight: ins,
					score:   rrfScore,
					via:     "vector",
				}
			}
		}
	}

	// Signal 3: Time-based ranking (MAGMA third RRF signal)
	timeSorted := make([]*model.Insight, len(all))
	copy(timeSorted, all)
	sort.Slice(timeSorted, func(i, j int) bool {
		return timeSorted[i].CreatedAt.After(timeSorted[j].CreatedAt)
	})
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
				insight: ins,
				score:   rrfScore,
				via:     "time",
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
	insightMap := make(map[string]*model.Insight)

	for id, a := range anchorMap {
		scoreMap[id] = a.score
		viaMap[id] = a.via
		insightMap[id] = a.insight
	}

	// Step 3: Beam search from each anchor
	for id, a := range anchorMap {
		beamSearchFromAnchor(db, id, a.score, queryVec, weights, params, scoreMap, viaMap, insightMap, embedCache)
	}

	traversedCount := len(scoreMap)

	// Step 4: Multi-factor reranking
	queryTokens := Tokenize(query)
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
		ins, ok := insightMap[id]
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

		// keyword_score: token overlap (reuses pre-computed tokens from KeywordSearch)
		if len(queryTokens) > 0 {
			contentTokens := tokenCache[c.id]
			if contentTokens == nil {
				contentTokens = insightTokens(c.ins)
			}
			intersection := 0
			for t := range queryTokens {
				if contentTokens[t] {
					intersection++
				}
			}
			c.kwScore = float64(intersection) / float64(len(queryTokens))
		}

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

		// similarity: cosine similarity with query vector (uses pre-loaded cache)
		if hasEmbeddings {
			if nVec, ok := embedCache[c.id]; ok {
				sim := embed.CosineSimilarity(queryVec, nVec)
				if sim > 0 {
					c.simScore = sim
				}
			}
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
	// ranking, and only the candidates in hand are looked up. Failing to look
	// this up is not fatal: a superseded row ranking normally is the
	// pre-existing behavior, which is worse but not broken.
	candidateIDs := make([]string, len(candidates))
	for i := range candidates {
		candidateIDs[i] = candidates[i].id
	}
	superseded, supersededErr := db.GetSupersededIDs(candidateIDs)
	if supersededErr != nil {
		superseded = nil
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

// beamSearchFromAnchor performs beam search starting from a single anchor node.
// It uses a priority queue to keep the top beamWidth candidates at each depth level.
// embedCache provides pre-loaded embedding vectors (nil = no embeddings).
func beamSearchFromAnchor(
	db *store.DB,
	startID string,
	startScore float64,
	queryVec []float64,
	weights IntentWeights,
	params TraversalParams,
	scoreMap map[string]float64,
	viaMap map[string]string,
	insightMap map[string]*model.Insight,
	embedCache map[string][]float64,
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

			edges, err := db.GetEdgesByNode(cur.id)
			if err != nil {
				continue
			}

			for _, e := range edges {
				if totalVisited >= params.MaxVisited {
					break
				}
				neighborID := e.TargetID
				if neighborID == cur.id {
					neighborID = e.SourceID
				}

				// MAGMA transition score (P6): additive accumulation
				// score_v = score_u + λ₁·φ(edgeType, intent) + λ₂·sim(v_neighbor, v_query)
				structural := weights[e.EdgeType] * e.Weight // φ(edgeType, intent) * edge_weight
				semantic := 0.0
				if queryVec != nil && embedCache != nil {
					if nVec, ok := embedCache[neighborID]; ok {
						cosSim := embed.CosineSimilarity(queryVec, nVec)
						if cosSim > 0 {
							semantic = cosSim
						}
					}
				}
				neighborScore := cur.score + lambda1*structural + lambda2*semantic

				// Update global score map if this path is better
				if existing, ok := scoreMap[neighborID]; !ok || neighborScore > existing {
					scoreMap[neighborID] = neighborScore
					viaMap[neighborID] = string(e.EdgeType)
					if _, loaded := insightMap[neighborID]; !loaded {
						ins, err := db.GetInsightByID(neighborID)
						if err == nil && ins != nil {
							insightMap[neighborID] = ins
						}
					}
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

// vectorSearch performs brute-force cosine similarity search, loading embeddings from DB.
// Used by tests; the main recall path uses vectorSearchFromCache with a pre-loaded cache.
func vectorSearch(db *store.DB, queryVec []float64, limit int) []vectorHit {
	dbEmbeds, err := db.GetAllEmbeddings()
	if err != nil || len(dbEmbeds) == 0 {
		return nil
	}
	cache := make(map[string][]float64, len(dbEmbeds))
	for _, e := range dbEmbeds {
		if v := embed.DeserializeVector(e.Embedding); v != nil {
			cache[e.ID] = v
		}
	}
	return vectorSearchFromCache(cache, queryVec, limit)
}

// vectorSearchFromCache performs cosine similarity search over pre-loaded embeddings.
// Uses a min-heap to maintain the top-k results in O(n log k) instead of O(n log n).
func vectorSearchFromCache(embedCache map[string][]float64, queryVec []float64, limit int) []vectorHit {
	h := &vectorHitMinHeap{}
	for id, vec := range embedCache {
		sim := embed.CosineSimilarity(queryVec, vec)
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
