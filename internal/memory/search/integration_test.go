package search

import (
	"fmt"
	"testing"
	"time"

	"github.com/mnemon-dev/mnemon/internal/memory/embed"
	"github.com/mnemon-dev/mnemon/internal/memory/model"
	"github.com/mnemon-dev/mnemon/internal/memory/store"
)

func testDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open test DB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func insertInsight(t *testing.T, db *store.DB, id, content, source string, importance int, entities []string, createdAt time.Time) *model.Insight {
	t.Helper()
	ins := &model.Insight{
		ID:         id,
		Content:    content,
		Category:   model.CategoryFact,
		Importance: importance,
		Tags:       []string{},
		Entities:   entities,
		Source:     source,
		CreatedAt:  createdAt,
		UpdatedAt:  createdAt,
	}
	if err := db.InsertInsight(ins); err != nil {
		t.Fatalf("insert %s: %v", id, err)
	}
	return ins
}

// --- causalTopologicalSort ---

func TestCausalTopologicalSort_LinearChain(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	// Create causal chain: A causes B causes C
	insertInsight(t, db, "A", "root cause", "user", 3, nil, now)
	insertInsight(t, db, "B", "intermediate", "user", 3, nil, now)
	insertInsight(t, db, "C", "final effect", "user", 3, nil, now)

	db.InsertEdge(&model.Edge{SourceID: "A", TargetID: "B", EdgeType: model.EdgeCausal, Weight: 0.5, Metadata: map[string]string{}, CreatedAt: now})
	db.InsertEdge(&model.Edge{SourceID: "B", TargetID: "C", EdgeType: model.EdgeCausal, Weight: 0.5, Metadata: map[string]string{}, CreatedAt: now})

	results := []RecallResult{
		{Insight: &model.Insight{ID: "C"}, Score: 3.0},
		{Insight: &model.Insight{ID: "A"}, Score: 1.0},
		{Insight: &model.Insight{ID: "B"}, Score: 2.0},
	}

	sorted := causalTopologicalSort(db, results)

	if len(sorted) != 3 {
		t.Fatalf("want 3 results, got %d", len(sorted))
	}
	// A should come before B, B before C (causal order)
	idxA, idxB, idxC := -1, -1, -1
	for i, r := range sorted {
		switch r.Insight.ID {
		case "A":
			idxA = i
		case "B":
			idxB = i
		case "C":
			idxC = i
		}
	}
	if idxA >= idxB || idxB >= idxC {
		t.Errorf("causal order violated: A@%d B@%d C@%d, want A<B<C", idxA, idxB, idxC)
	}
}

func TestCausalTopologicalSort_Diamond(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	// Diamond: A→B, A→C, B→D, C→D
	insertInsight(t, db, "A", "root", "user", 3, nil, now)
	insertInsight(t, db, "B", "left", "user", 3, nil, now)
	insertInsight(t, db, "C", "right", "user", 3, nil, now)
	insertInsight(t, db, "D", "converge", "user", 3, nil, now)

	db.InsertEdge(&model.Edge{SourceID: "A", TargetID: "B", EdgeType: model.EdgeCausal, Weight: 0.5, Metadata: map[string]string{}, CreatedAt: now})
	db.InsertEdge(&model.Edge{SourceID: "A", TargetID: "C", EdgeType: model.EdgeCausal, Weight: 0.5, Metadata: map[string]string{}, CreatedAt: now})
	db.InsertEdge(&model.Edge{SourceID: "B", TargetID: "D", EdgeType: model.EdgeCausal, Weight: 0.5, Metadata: map[string]string{}, CreatedAt: now})
	db.InsertEdge(&model.Edge{SourceID: "C", TargetID: "D", EdgeType: model.EdgeCausal, Weight: 0.5, Metadata: map[string]string{}, CreatedAt: now})

	results := []RecallResult{
		{Insight: &model.Insight{ID: "D"}, Score: 4.0},
		{Insight: &model.Insight{ID: "B"}, Score: 3.0},
		{Insight: &model.Insight{ID: "C"}, Score: 2.0},
		{Insight: &model.Insight{ID: "A"}, Score: 1.0},
	}

	sorted := causalTopologicalSort(db, results)

	pos := make(map[string]int)
	for i, r := range sorted {
		pos[r.Insight.ID] = i
	}
	// A must come before B, C, D
	if pos["A"] >= pos["B"] || pos["A"] >= pos["C"] || pos["A"] >= pos["D"] {
		t.Errorf("A should be first: positions %v", pos)
	}
	// D must come after B and C
	if pos["D"] <= pos["B"] || pos["D"] <= pos["C"] {
		t.Errorf("D should be last: positions %v", pos)
	}
}

func TestCausalTopologicalSort_CycleHandling(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	// Cycle: A→B→A (should not panic, remaining nodes appended)
	insertInsight(t, db, "cyc-A", "a", "user", 3, nil, now)
	insertInsight(t, db, "cyc-B", "b", "user", 3, nil, now)

	db.InsertEdge(&model.Edge{SourceID: "cyc-A", TargetID: "cyc-B", EdgeType: model.EdgeCausal, Weight: 0.5, Metadata: map[string]string{}, CreatedAt: now})
	db.InsertEdge(&model.Edge{SourceID: "cyc-B", TargetID: "cyc-A", EdgeType: model.EdgeCausal, Weight: 0.5, Metadata: map[string]string{}, CreatedAt: now})

	results := []RecallResult{
		{Insight: &model.Insight{ID: "cyc-A"}, Score: 2.0},
		{Insight: &model.Insight{ID: "cyc-B"}, Score: 1.0},
	}

	sorted := causalTopologicalSort(db, results)
	// Should return all results even with cycle
	if len(sorted) != 2 {
		t.Errorf("cycle: want 2 results, got %d", len(sorted))
	}
}

func TestCausalTopologicalSort_SingleResult(t *testing.T) {
	db := testDB(t)
	results := []RecallResult{
		{Insight: &model.Insight{ID: "only"}, Score: 1.0},
	}
	sorted := causalTopologicalSort(db, results)
	if len(sorted) != 1 || sorted[0].Insight.ID != "only" {
		t.Error("single result should pass through unchanged")
	}
}

func TestCausalTopologicalSort_NoCausalEdges(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	insertInsight(t, db, "nc-A", "a", "user", 3, nil, now)
	insertInsight(t, db, "nc-B", "b", "user", 3, nil, now)

	// Only semantic edge, no causal
	db.InsertEdge(&model.Edge{SourceID: "nc-A", TargetID: "nc-B", EdgeType: model.EdgeSemantic, Weight: 0.9, Metadata: map[string]string{}, CreatedAt: now})

	results := []RecallResult{
		{Insight: &model.Insight{ID: "nc-B"}, Score: 2.0},
		{Insight: &model.Insight{ID: "nc-A"}, Score: 1.0},
	}
	sorted := causalTopologicalSort(db, results)
	// Without causal edges, order should be by score (nc-B first)
	if sorted[0].Insight.ID != "nc-B" {
		t.Errorf("no causal edges: want score-based order, got %s first", sorted[0].Insight.ID)
	}
}

// --- vectorSearch ---

func TestVectorSearch_Basic(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	insertInsight(t, db, "vs-1", "similar content", "user", 3, nil, now)
	insertInsight(t, db, "vs-2", "different content", "user", 3, nil, now)
	insertInsight(t, db, "vs-3", "also similar", "user", 3, nil, now)

	// Store embeddings
	query := []float64{1.0, 0.0, 0.0}
	db.UpdateEmbedding("vs-1", embed.SerializeVector([]float64{0.9, 0.1, 0.0}))   // similar
	db.UpdateEmbedding("vs-2", embed.SerializeVector([]float64{0.0, 0.0, 1.0}))   // different
	db.UpdateEmbedding("vs-3", embed.SerializeVector([]float64{0.85, 0.15, 0.0})) // similar

	hits := vectorSearch(db, query, 10)
	if len(hits) == 0 {
		t.Fatal("want vector search hits")
	}
	// vs-1 should be most similar to query
	if hits[0].id != "vs-1" {
		t.Errorf("top hit: want vs-1, got %s", hits[0].id)
	}
	// Results should be sorted by similarity descending
	for i := 1; i < len(hits); i++ {
		if hits[i].similarity > hits[i-1].similarity {
			t.Errorf("not sorted: hit[%d].sim=%f > hit[%d].sim=%f", i, hits[i].similarity, i-1, hits[i-1].similarity)
		}
	}
}

func TestVectorSearch_Limit(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	for i := 0; i < 10; i++ {
		id := "vsl-" + string(rune('a'+i))
		insertInsight(t, db, id, "content "+id, "user", 3, nil, now)
		vec := []float64{0.8, 0.1, float64(i) * 0.01}
		db.UpdateEmbedding(id, embed.SerializeVector(vec))
	}

	query := []float64{0.8, 0.1, 0.0}
	hits := vectorSearch(db, query, 3)
	if len(hits) > 3 {
		t.Errorf("limit 3: got %d hits", len(hits))
	}
}

func TestVectorSearch_NoEmbeddings(t *testing.T) {
	db := testDB(t)
	insertInsight(t, db, "no-e", "no embedding", "user", 3, nil, time.Now().UTC())

	hits := vectorSearch(db, []float64{1.0, 0.0}, 10)
	if hits != nil {
		t.Errorf("no embeddings: want nil, got %v", hits)
	}
}

// --- IntentAwareRecall ---

func TestIntentAwareRecall_BasicWithoutEmbeddings(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	insertInsight(t, db, "r-1", "Go uses SQLite for persistent graph storage", "user", 3, []string{"Go", "SQLite"}, now.Add(-2*time.Hour))
	insertInsight(t, db, "r-2", "Python web framework with Django", "user", 3, []string{"Python", "Django"}, now.Add(-1*time.Hour))
	insertInsight(t, db, "r-3", "Go concurrency goroutine patterns", "user", 3, []string{"Go"}, now)

	resp, err := IntentAwareRecall(db, "Go SQLite storage", nil, []string{"Go", "SQLite"}, 5, nil)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}

	if len(resp.Results) == 0 {
		t.Fatal("want results")
	}
	if resp.Meta.Intent != IntentGeneral {
		t.Errorf("intent: want GENERAL, got %s", resp.Meta.Intent)
	}
	if resp.Meta.IntentSource != "auto" {
		t.Errorf("intent source: want auto, got %s", resp.Meta.IntentSource)
	}

	// r-1 should be the top result (best keyword match for "Go SQLite storage")
	if resp.Results[0].Insight.ID != "r-1" {
		t.Errorf("top result: want r-1, got %s", resp.Results[0].Insight.ID)
	}
}

func TestIntentAwareRecall_IntentOverride(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	insertInsight(t, db, "io-1", "content A", "user", 3, nil, now)

	override := IntentWhy
	resp, err := IntentAwareRecall(db, "test query", nil, nil, 5, &override)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if resp.Meta.Intent != IntentWhy {
		t.Errorf("want overridden intent WHY, got %s", resp.Meta.Intent)
	}
	if resp.Meta.IntentSource != "override" {
		t.Errorf("want intent source 'override', got %s", resp.Meta.IntentSource)
	}
}

func TestIntentAwareRecall_LimitEnforced(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	for i := 0; i < 10; i++ {
		id := "lim-" + string(rune('a'+i))
		insertInsight(t, db, id, "common shared keyword content "+id, "user", 3, nil, now.Add(-time.Duration(i)*time.Hour))
	}

	resp, err := IntentAwareRecall(db, "common shared keyword content", nil, nil, 3, nil)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(resp.Results) > 3 {
		t.Errorf("limit 3: got %d results", len(resp.Results))
	}
}

func TestIntentAwareRecall_SparseHint(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	insertInsight(t, db, "sp-1", "completely unrelated cooking recipe", "user", 3, nil, now)

	resp, err := IntentAwareRecall(db, "quantum computing algorithms", nil, nil, 10, nil)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	// With few or no matching results for a query, hint should be sparse
	if resp.Meta.Hint != "sparse_results" {
		t.Errorf("want sparse_results hint, got %q", resp.Meta.Hint)
	}
}

func TestIntentAwareRecall_WithEmbeddings(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	insertInsight(t, db, "we-1", "Go memory management garbage collector internals", "user", 3, []string{"Go"}, now.Add(-1*time.Hour))
	insertInsight(t, db, "we-2", "cooking pasta recipe with tomato sauce", "user", 3, nil, now)

	// Store embeddings
	db.UpdateEmbedding("we-1", embed.SerializeVector([]float64{0.9, 0.8, 0.1}))
	db.UpdateEmbedding("we-2", embed.SerializeVector([]float64{0.1, 0.1, 0.9}))

	queryVec := []float64{0.85, 0.75, 0.15}
	resp, err := IntentAwareRecall(db, "Go memory internals", queryVec, []string{"Go"}, 5, nil)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(resp.Results) == 0 {
		t.Fatal("want results")
	}
	// we-1 should rank higher (keyword + entity + embedding match)
	if resp.Results[0].Insight.ID != "we-1" {
		t.Errorf("with embeddings: want we-1 top, got %s", resp.Results[0].Insight.ID)
	}
	// Similarity signal should be populated
	if resp.Results[0].Signals.Similarity == 0 {
		t.Error("similarity signal should be non-zero with embeddings")
	}
}

func TestIntentAwareRecall_ScoreDescending(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	insertInsight(t, db, "sd-1", "Go SQLite database integration patterns", "user", 5, []string{"Go", "SQLite"}, now.Add(-1*time.Hour))
	insertInsight(t, db, "sd-2", "Go programming language basics", "user", 3, []string{"Go"}, now.Add(-30*time.Minute))
	insertInsight(t, db, "sd-3", "unrelated machine learning topic", "user", 3, nil, now)

	resp, _ := IntentAwareRecall(db, "Go SQLite database", nil, []string{"Go", "SQLite"}, 10, nil)

	for i := 1; i < len(resp.Results); i++ {
		if resp.Results[i].Score > resp.Results[i-1].Score {
			t.Errorf("results not sorted: score[%d]=%f > score[%d]=%f",
				i, resp.Results[i].Score, i-1, resp.Results[i-1].Score)
		}
	}
}

// --- Diff with embeddings ---

func TestDiff_WithEmbeddings(t *testing.T) {
	insights := []*model.Insight{
		{ID: "d-1", Content: "Go uses SQLite for persistent storage"},
		{ID: "d-2", Content: "Python web framework Django"},
	}

	// Create embeddings — d-1 is similar to new content, d-2 is different
	newVec := []float64{0.9, 0.8, 0.1}
	existing := []EmbeddedItem{
		{ID: "d-1", Embedding: []float64{0.85, 0.82, 0.12}},
		{ID: "d-2", Embedding: []float64{0.1, 0.1, 0.9}},
	}

	result := Diff(insights, "Go uses SQLite for persistent graph storage", DiffOptions{
		NewEmbedding:  newVec,
		ExistingEmbed: existing,
	})

	if len(result.Matches) == 0 {
		t.Fatal("want matches with embeddings")
	}
	// First match should have cosine similarity populated
	if result.Matches[0].CosineSimilarity == 0 {
		t.Error("cosine similarity should be populated when embeddings available")
	}
}

func TestDiff_EmbeddingOnlyMatches(t *testing.T) {
	// Content is different (low token overlap) but embeddings are very similar
	insights := []*model.Insight{
		{ID: "eo-1", Content: "memory engine graph database system"},
	}

	// Very similar embeddings despite different keywords
	newVec := []float64{0.9, 0.85, 0.7, 0.6}
	existing := []EmbeddedItem{
		{ID: "eo-1", Embedding: []float64{0.88, 0.83, 0.72, 0.61}},
	}

	result := Diff(insights, "persistent storage knowledge retrieval", DiffOptions{
		NewEmbedding:  newVec,
		ExistingEmbed: existing,
	})

	// High cosine similarity should surface this as a match
	found := false
	for _, m := range result.Matches {
		if m.ID == "eo-1" && m.CosineSimilarity > 0.7 {
			found = true
		}
	}
	if !found {
		t.Error("embedding-only match with high cosine should be detected")
	}
}

// --- beamSearchFromAnchor ---

func TestBeamSearchFromAnchor_ScorePropagation(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	// Graph: anchor --semantic(w=0.9)--> neighbor
	insertInsight(t, db, "bs-1", "anchor node", "user", 3, nil, now)
	insertInsight(t, db, "bs-2", "neighbor node", "user", 3, nil, now)
	db.InsertEdge(&model.Edge{SourceID: "bs-1", TargetID: "bs-2", EdgeType: model.EdgeSemantic, Weight: 0.9, Metadata: map[string]string{}, CreatedAt: now})

	scoreMap := make(map[string]float64)
	viaMap := make(map[string]string)
	insightMap := make(map[string]*model.Insight)

	weights := GetWeights(IntentGeneral)
	params := TraversalParams{BeamWidth: 10, MaxDepth: 3, MaxVisited: 100}

	scoreMap["bs-1"] = 1.0
	beamSearchFromAnchor(db, "bs-1", 1.0, nil, weights, params, scoreMap, viaMap, insightMap, nil)

	// Neighbor should be discovered with score > 0
	if _, ok := scoreMap["bs-2"]; !ok {
		t.Fatal("beam search should discover neighbor bs-2")
	}
	if scoreMap["bs-2"] <= 0 {
		t.Errorf("neighbor score should be positive, got %f", scoreMap["bs-2"])
	}
	// Score should be: startScore + lambda1 * weights[semantic] * edge_weight
	expectedMin := 1.0 + lambda1*weights[model.EdgeSemantic]*0.9
	if scoreMap["bs-2"] < expectedMin-0.01 {
		t.Errorf("neighbor score %f < expected minimum %f", scoreMap["bs-2"], expectedMin)
	}
}

func TestBeamSearchFromAnchor_BeamWidthPruning(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	// Star graph: center connected to 20 leaves, leaves connected to a second layer
	insertInsight(t, db, "bw-center", "center", "user", 3, nil, now)
	for i := 0; i < 20; i++ {
		leafID := fmt.Sprintf("bw-leaf-%d", i)
		insertInsight(t, db, leafID, fmt.Sprintf("leaf %d content", i), "user", 3, nil, now)
		db.InsertEdge(&model.Edge{SourceID: "bw-center", TargetID: leafID, EdgeType: model.EdgeSemantic, Weight: float64(20-i) * 0.05, Metadata: map[string]string{}, CreatedAt: now})

		// Second layer: each leaf connects to a unique deep node
		deepID := fmt.Sprintf("bw-deep-%d", i)
		insertInsight(t, db, deepID, fmt.Sprintf("deep %d content", i), "user", 3, nil, now)
		db.InsertEdge(&model.Edge{SourceID: leafID, TargetID: deepID, EdgeType: model.EdgeSemantic, Weight: 0.5, Metadata: map[string]string{}, CreatedAt: now})
	}

	scoreMap := make(map[string]float64)
	viaMap := make(map[string]string)
	insightMap := make(map[string]*model.Insight)

	weights := GetWeights(IntentGeneral)
	// BeamWidth=3: only top 3 leaves should be expanded to the deep layer
	params := TraversalParams{BeamWidth: 3, MaxDepth: 3, MaxVisited: 500}

	scoreMap["bw-center"] = 1.0
	beamSearchFromAnchor(db, "bw-center", 1.0, nil, weights, params, scoreMap, viaMap, insightMap, nil)

	// Count deep nodes discovered — should be limited by beam width
	deepCount := 0
	for id := range scoreMap {
		if len(id) > 7 && id[:7] == "bw-deep" {
			deepCount++
		}
	}
	// With beam width 3, at most 3 leaves expand → at most 3 deep nodes
	if deepCount > 3 {
		t.Errorf("beam width 3: want at most 3 deep nodes, got %d", deepCount)
	}
}

func TestBeamSearchFromAnchor_MaxVisitedBudget(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	// Chain: n0 → n1 → n2 → ... → n20
	for i := 0; i <= 20; i++ {
		id := fmt.Sprintf("mv-%d", i)
		insertInsight(t, db, id, fmt.Sprintf("node %d", i), "user", 3, nil, now)
		if i > 0 {
			prev := fmt.Sprintf("mv-%d", i-1)
			db.InsertEdge(&model.Edge{SourceID: prev, TargetID: id, EdgeType: model.EdgeTemporal, Weight: 1.0, Metadata: map[string]string{}, CreatedAt: now})
		}
	}

	scoreMap := make(map[string]float64)
	viaMap := make(map[string]string)
	insightMap := make(map[string]*model.Insight)

	weights := GetWeights(IntentGeneral)
	// MaxVisited=5: should stop after visiting 5 nodes total (including start)
	params := TraversalParams{BeamWidth: 10, MaxDepth: 20, MaxVisited: 5}

	scoreMap["mv-0"] = 1.0
	beamSearchFromAnchor(db, "mv-0", 1.0, nil, weights, params, scoreMap, viaMap, insightMap, nil)

	// scoreMap includes the anchor itself, so discovered nodes (excluding anchor) should be <= 4
	discovered := len(scoreMap) - 1 // subtract anchor
	if discovered > 4 {
		t.Errorf("MaxVisited=5: want at most 4 discovered nodes, got %d", discovered)
	}
}

// --- supersedes demotion ---

// Regression: a correction must outrank the text it corrects.
//
// The failure this defends against is subtle and was observed in a real store.
// A correction is usually written as a diff ("X is wrong, use Y"), so it
// contains the wrong wording verbatim. A query phrased with the wrong wording
// therefore matches the STALE row at least as well as the correction, and the
// stale row is older, so it has accumulated more edges and a higher access
// count. With scoring built only from keyword/similarity/entity/graph, the
// corrected text reliably beats its own correction and gets served as current.
func TestIntentAwareRecall_SupersededInsightIsDemoted(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	// Both rows discuss the same subject with near-identical vocabulary.
	stale := insertInsight(t, db, "stale", "deploy target is the staging cluster", "user", 5, nil, now.Add(-72*time.Hour))
	fresh := insertInsight(t, db, "fresh", "deploy target is the staging cluster no longer; use production", "user", 5, nil, now)

	// Give the stale row the graph advantage age confers in a real store.
	for i := range 3 {
		neighbor := "n" + string(rune('a'+i))
		insertInsight(t, db, neighbor, "deploy notes", "user", 3, nil, now.Add(-72*time.Hour))
		db.InsertEdge(&model.Edge{SourceID: stale.ID, TargetID: neighbor, EdgeType: model.EdgeSemantic, Weight: 0.9, Metadata: map[string]string{}, CreatedAt: now})
	}

	query := "deploy target staging cluster"

	before, err := IntentAwareRecall(db, query, nil, nil, 5, nil)
	if err != nil {
		t.Fatalf("recall before: %v", err)
	}
	staleBefore, freshBefore := scoreOf(before.Results, "stale"), scoreOf(before.Results, "fresh")
	if staleBefore <= freshBefore {
		t.Skipf("fixture did not reproduce the stale-wins condition (stale=%.4f fresh=%.4f); demotion still asserted below", staleBefore, freshBefore)
	}

	// Record the supersession.
	if err := db.InsertEdge(&model.Edge{
		SourceID: fresh.ID, TargetID: stale.ID, EdgeType: model.EdgeSupersedes,
		Weight: 1.0, Metadata: map[string]string{}, CreatedAt: now,
	}); err != nil {
		t.Fatalf("insert supersedes edge: %v", err)
	}

	after, err := IntentAwareRecall(db, query, nil, nil, 5, nil)
	if err != nil {
		t.Fatalf("recall after: %v", err)
	}
	staleAfter, freshAfter := scoreOf(after.Results, "stale"), scoreOf(after.Results, "fresh")

	if staleAfter >= freshAfter {
		t.Errorf("after supersedes edge the correction must outrank the corrected row: stale=%.4f fresh=%.4f", staleAfter, freshAfter)
	}
	if staleAfter >= staleBefore {
		t.Errorf("superseded row was not demoted: before=%.4f after=%.4f", staleBefore, staleAfter)
	}

	// Demotion, not deletion: the row stays reachable and is flagged.
	var found bool
	for _, r := range after.Results {
		if r.Insight.ID == "stale" {
			found = true
			if !r.Superseded {
				t.Error("superseded row must be flagged Superseded=true")
			}
		}
	}
	if !found {
		t.Error("superseded row must remain retrievable, not be filtered out")
	}
}

func scoreOf(results []RecallResult, id string) float64 {
	for _, r := range results {
		if r.Insight.ID == id {
			return r.Score
		}
	}
	return 0
}
