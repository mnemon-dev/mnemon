package graph

import (
	"fmt"
	"testing"
	"time"

	"github.com/mnemon-dev/mnemon/internal/embed"
	"github.com/mnemon-dev/mnemon/internal/model"
	"github.com/mnemon-dev/mnemon/internal/store"
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

// --- CreateTemporalEdge ---

func TestCreateTemporalEdge_BackboneChain(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	// Insert two insights from the same source — the second should link back to the first
	insertInsight(t, db, "t-1", "first insight", "user", 3, nil, now.Add(-1*time.Hour))
	ins2 := insertInsight(t, db, "t-2", "second insight", "user", 3, nil, now)

	count := CreateTemporalEdge(db, ins2)
	if count < 2 {
		t.Errorf("backbone chain: want at least 2 edges (bidirectional), got %d", count)
	}

	// Verify edges exist
	edges, err := db.GetEdgesByNodeAndType("t-2", model.EdgeTemporal)
	if err != nil {
		t.Fatalf("get edges: %v", err)
	}
	if len(edges) < 2 {
		t.Errorf("want at least 2 temporal edges, got %d", len(edges))
	}

	// Verify bidirectional: t-1→t-2 and t-2→t-1
	hasForward, hasReverse := false, false
	for _, e := range edges {
		if e.SourceID == "t-1" && e.TargetID == "t-2" {
			hasForward = true
			if e.Weight != 1.0 {
				t.Errorf("backbone weight: want 1.0, got %f", e.Weight)
			}
		}
		if e.SourceID == "t-2" && e.TargetID == "t-1" {
			hasReverse = true
		}
	}
	if !hasForward || !hasReverse {
		t.Error("backbone chain should be bidirectional")
	}
}

func TestCreateTemporalEdge_ProximityDecay(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	// Insert insights at different time offsets
	insertInsight(t, db, "p-close", "close in time", "user", 3, nil, now.Add(-30*time.Minute))
	insertInsight(t, db, "p-far", "far in time", "other", 3, nil, now.Add(-20*time.Hour))
	ins := insertInsight(t, db, "p-new", "new insight", "other", 3, nil, now)

	CreateTemporalEdge(db, ins)

	edges, _ := db.GetEdgesByNodeAndType("p-new", model.EdgeTemporal)
	// Find weights to the close and far neighbors
	var closeWeight, farWeight float64
	for _, e := range edges {
		other := e.TargetID
		if other == "p-new" {
			other = e.SourceID
		}
		if e.Metadata["sub_type"] != "proximity" {
			continue
		}
		switch other {
		case "p-close":
			closeWeight = e.Weight
		case "p-far":
			farWeight = e.Weight
		}
	}
	// Close neighbor should have higher weight than far neighbor
	if closeWeight > 0 && farWeight > 0 && closeWeight <= farWeight {
		t.Errorf("proximity decay: close(%f) should > far(%f)", closeWeight, farWeight)
	}
}

func TestCreateTemporalEdge_NoSource(t *testing.T) {
	db := testDB(t)
	// Only insight — no previous from same source
	ins := insertInsight(t, db, "alone", "only insight", "user", 3, nil, time.Now().UTC())
	count := CreateTemporalEdge(db, ins)
	// No backbone, possibly no proximity either (no other insights within 24h)
	if count != 0 {
		t.Errorf("single insight: want 0 edges, got %d", count)
	}
}

// --- CreateEntityEdges ---

func TestCreateEntityEdges_CoOccurrence(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	// Two insights sharing the "Go" entity
	insertInsight(t, db, "ent-1", "Go is fast", "user", 3, []string{"Go", "performance"}, now.Add(-1*time.Hour))
	ins2 := insertInsight(t, db, "ent-2", "Go concurrency patterns", "user", 3, []string{"Go", "concurrency"}, now)

	count := CreateEntityEdges(db, ins2)
	if count < 2 {
		t.Errorf("co-occurrence: want at least 2 edges (bidirectional for 'Go'), got %d", count)
	}

	// Verify entity edges exist
	edges, _ := db.GetEdgesByNodeAndType("ent-2", model.EdgeEntity)
	if len(edges) == 0 {
		t.Error("want entity edges, got none")
	}
	// Check metadata contains the entity name
	for _, e := range edges {
		if e.Metadata["entity"] == "" {
			t.Error("entity edge metadata should contain 'entity' key")
		}
	}
}

func TestCreateEntityEdges_NoSharedEntities(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	insertInsight(t, db, "ne-1", "Go is fast", "user", 3, []string{"Go"}, now.Add(-1*time.Hour))
	ins2 := insertInsight(t, db, "ne-2", "Python is flexible", "user", 3, []string{"Python"}, now)

	count := CreateEntityEdges(db, ins2)
	if count != 0 {
		t.Errorf("no shared entities: want 0 edges, got %d", count)
	}
}

func TestCreateEntityEdges_EmptyEntities(t *testing.T) {
	db := testDB(t)
	ins := insertInsight(t, db, "empty", "no entities", "user", 3, nil, time.Now().UTC())
	count := CreateEntityEdges(db, ins)
	if count != 0 {
		t.Errorf("empty entities: want 0, got %d", count)
	}
}

func TestCreateEntityEdges_MaxLinks(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	// Insert many insights with "Go" entity — should cap at maxEntityLinks
	for i := 0; i < 10; i++ {
		id := "many-" + string(rune('a'+i))
		insertInsight(t, db, id, "Go content "+id, "user", 3, []string{"Go"}, now.Add(-time.Duration(i)*time.Hour))
	}
	ins := insertInsight(t, db, "many-new", "another Go insight", "user", 3, []string{"Go"}, now)

	count := CreateEntityEdges(db, ins)
	// maxEntityLinks=5, bidirectional = up to 10
	if count > maxEntityLinks*2 {
		t.Errorf("should cap at %d links (bidirectional), got %d", maxEntityLinks*2, count)
	}
}

// --- CreateCausalEdges ---

func TestCreateCausalEdges_DirectionInference(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	// prev has no causal signal, new insight says "because"
	// → new is effect, prev is cause → edge: prev→new
	insertInsight(t, db, "cause", "SQLite has low latency and small footprint", "user", 3, nil, now.Add(-1*time.Hour))
	effect := insertInsight(t, db, "effect", "chose SQLite because of low latency and small footprint", "user", 3, nil, now)

	count := CreateCausalEdges(db, effect)
	if count == 0 {
		t.Fatal("want at least 1 causal edge")
	}

	edges, _ := db.GetEdgesByNodeAndType("effect", model.EdgeCausal)
	if len(edges) == 0 {
		t.Fatal("want causal edge")
	}

	// Direction: cause→effect (prev is source, new is target)
	e := edges[0]
	if e.SourceID != "cause" || e.TargetID != "effect" {
		t.Errorf("direction: want cause→effect, got %s→%s", e.SourceID, e.TargetID)
	}
	if e.Metadata["sub_type"] == "" {
		t.Error("causal edge should have sub_type metadata")
	}
}

func TestCreateCausalEdges_NoCausalSignal(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	insertInsight(t, db, "nc-1", "Go is a programming language", "user", 3, nil, now.Add(-1*time.Hour))
	ins := insertInsight(t, db, "nc-2", "SQLite is a database engine", "user", 3, nil, now)

	count := CreateCausalEdges(db, ins)
	if count != 0 {
		t.Errorf("no causal signal: want 0 edges, got %d", count)
	}
}

func TestCreateCausalEdges_InsufficientOverlap(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	// Both have causal signals but completely different non-causal tokens → overlap < 0.15
	insertInsight(t, db, "lo-1", "apple banana cherry mango peach grape because fruit", "user", 3, nil, now.Add(-1*time.Hour))
	ins := insertInsight(t, db, "lo-2", "therefore dog elephant fox giraffe zebra lion tiger", "user", 3, nil, now)

	count := CreateCausalEdges(db, ins)
	if count != 0 {
		t.Errorf("insufficient overlap: want 0, got %d", count)
	}
}

// --- GetNeighborhood ---

func TestGetNeighborhood_BFS(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	// Build a small graph: A → B → C
	insertInsight(t, db, "A", "node A", "user", 3, nil, now)
	insertInsight(t, db, "B", "node B", "user", 3, nil, now)
	insertInsight(t, db, "C", "node C", "user", 3, nil, now)
	insertInsight(t, db, "D", "node D (disconnected)", "user", 3, nil, now)

	db.InsertEdge(&model.Edge{SourceID: "A", TargetID: "B", EdgeType: model.EdgeSemantic, Weight: 0.9, Metadata: map[string]string{}, CreatedAt: now})
	db.InsertEdge(&model.Edge{SourceID: "B", TargetID: "C", EdgeType: model.EdgeTemporal, Weight: 1.0, Metadata: map[string]string{}, CreatedAt: now})

	neighbors := GetNeighborhood(db, "A", 2, 10)

	if len(neighbors) < 2 {
		t.Fatalf("want at least 2 neighbors (B and C), got %d", len(neighbors))
	}

	// B should be hop 1, C should be hop 2
	idHop := make(map[string]int)
	for _, n := range neighbors {
		idHop[n.ID] = n.Hop
	}
	if idHop["B"] != 1 {
		t.Errorf("B should be hop 1, got %d", idHop["B"])
	}
	if idHop["C"] != 2 {
		t.Errorf("C should be hop 2, got %d", idHop["C"])
	}
	// D should not appear (disconnected)
	if _, ok := idHop["D"]; ok {
		t.Error("D is disconnected, should not appear in neighborhood")
	}
}

func TestGetNeighborhood_MaxHops(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	insertInsight(t, db, "h-1", "node 1", "user", 3, nil, now)
	insertInsight(t, db, "h-2", "node 2", "user", 3, nil, now)
	insertInsight(t, db, "h-3", "node 3", "user", 3, nil, now)

	db.InsertEdge(&model.Edge{SourceID: "h-1", TargetID: "h-2", EdgeType: model.EdgeSemantic, Weight: 1.0, Metadata: map[string]string{}, CreatedAt: now})
	db.InsertEdge(&model.Edge{SourceID: "h-2", TargetID: "h-3", EdgeType: model.EdgeSemantic, Weight: 1.0, Metadata: map[string]string{}, CreatedAt: now})

	// maxHops=1 should only reach h-2, not h-3
	neighbors := GetNeighborhood(db, "h-1", 1, 10)
	for _, n := range neighbors {
		if n.ID == "h-3" {
			t.Error("maxHops=1 should not reach h-3 (2 hops away)")
		}
	}
}

func TestGetNeighborhood_MaxNodes(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	insertInsight(t, db, "mn-0", "center", "user", 3, nil, now)
	for i := 1; i <= 10; i++ {
		id := "mn-" + string(rune('a'+i))
		insertInsight(t, db, id, "leaf "+id, "user", 3, nil, now)
		db.InsertEdge(&model.Edge{SourceID: "mn-0", TargetID: id, EdgeType: model.EdgeEntity, Weight: 1.0, Metadata: map[string]string{}, CreatedAt: now})
	}

	neighbors := GetNeighborhood(db, "mn-0", 3, 3)
	if len(neighbors) > 3 {
		t.Errorf("maxNodes=3: got %d neighbors", len(neighbors))
	}
}

func TestGetNeighborhood_SkipsDeletedNodes(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	insertInsight(t, db, "sd-1", "alive", "user", 3, nil, now)
	insertInsight(t, db, "sd-2", "deleted", "user", 3, nil, now)
	db.InsertEdge(&model.Edge{SourceID: "sd-1", TargetID: "sd-2", EdgeType: model.EdgeSemantic, Weight: 1.0, Metadata: map[string]string{}, CreatedAt: now})

	db.SoftDeleteInsight("sd-2")

	neighbors := GetNeighborhood(db, "sd-1", 2, 10)
	for _, n := range neighbors {
		if n.ID == "sd-2" {
			t.Error("deleted node should not appear in neighborhood")
		}
	}
}

// --- FindCausalCandidates ---

func TestFindCausalCandidates_ViaGraph(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	// Create a small graph with semantic edges
	insertInsight(t, db, "fc-1", "chose Go because of performance reasons", "user", 3, nil, now)
	insertInsight(t, db, "fc-2", "Go has good concurrency support for performance", "user", 3, nil, now)
	db.InsertEdge(&model.Edge{SourceID: "fc-1", TargetID: "fc-2", EdgeType: model.EdgeSemantic, Weight: 0.85, Metadata: map[string]string{}, CreatedAt: now})

	ins := &model.Insight{ID: "fc-1", Content: "chose Go because of performance reasons"}
	candidates := FindCausalCandidates(db, ins)

	if len(candidates) == 0 {
		t.Fatal("want at least 1 causal candidate")
	}
	if candidates[0].ID != "fc-2" {
		t.Errorf("want candidate fc-2, got %s", candidates[0].ID)
	}
	if candidates[0].Hop != 1 {
		t.Errorf("want hop 1, got %d", candidates[0].Hop)
	}
}

// --- FindSemanticCandidates (token overlap fallback) ---

func TestFindSemanticCandidates_TokenOverlap(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	insertInsight(t, db, "sc-1", "Go uses SQLite for persistent graph storage engine", "user", 3, nil, now.Add(-1*time.Hour))
	insertInsight(t, db, "sc-2", "Python uses PostgreSQL for web application database", "user", 3, nil, now.Add(-30*time.Minute))
	ins := insertInsight(t, db, "sc-3", "Go uses SQLite for persistent memory storage engine", "user", 3, nil, now)

	// No embeddings → falls back to token overlap
	candidates := FindSemanticCandidates(db, ins, nil)

	if len(candidates) == 0 {
		t.Fatal("want at least 1 semantic candidate via token overlap")
	}
	// sc-1 should be the top candidate (most token overlap with sc-3)
	if candidates[0].ID != "sc-1" {
		t.Errorf("top candidate: want sc-1, got %s", candidates[0].ID)
	}
}

// --- CreateSemanticEdges ---

func TestCreateSemanticEdges_HighCosineSimilarity(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	// Create two insights with very similar embeddings (cosine > 0.80)
	ins1 := insertInsight(t, db, "se-1", "Go concurrency patterns", "user", 3, nil, now.Add(-1*time.Hour))
	ins2 := insertInsight(t, db, "se-2", "Go goroutine patterns", "user", 3, nil, now)

	_ = ins1

	// Store similar embeddings
	vec1 := []float64{1.0, 0.9, 0.8, 0.7}
	vec2 := []float64{1.0, 0.85, 0.82, 0.71}
	db.UpdateEmbedding("se-1", embed.SerializeVector(vec1))
	db.UpdateEmbedding("se-2", embed.SerializeVector(vec2))

	count := CreateSemanticEdges(db, ins2, nil)
	if count == 0 {
		t.Error("want semantic edges for high cosine similarity")
	}

	edges, _ := db.GetEdgesByNodeAndType("se-2", model.EdgeSemantic)
	if len(edges) == 0 {
		t.Error("want semantic edges in DB")
	}
	for _, e := range edges {
		if e.Metadata["created_by"] != "auto" {
			t.Errorf("auto-created edge should have created_by=auto, got %q", e.Metadata["created_by"])
		}
	}
}

func TestCreateSemanticEdges_LowSimilarityNoEdge(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	insertInsight(t, db, "sl-1", "completely different", "user", 3, nil, now.Add(-1*time.Hour))
	ins2 := insertInsight(t, db, "sl-2", "unrelated topic", "user", 3, nil, now)

	// Store orthogonal embeddings
	vec1 := []float64{1.0, 0.0, 0.0, 0.0}
	vec2 := []float64{0.0, 0.0, 0.0, 1.0}
	db.UpdateEmbedding("sl-1", embed.SerializeVector(vec1))
	db.UpdateEmbedding("sl-2", embed.SerializeVector(vec2))

	count := CreateSemanticEdges(db, ins2, nil)
	if count != 0 {
		t.Errorf("low similarity: want 0 semantic edges, got %d", count)
	}
}

func TestCreateSemanticEdges_NoEmbedding(t *testing.T) {
	db := testDB(t)
	ins := insertInsight(t, db, "no-emb", "no embedding stored", "user", 3, nil, time.Now().UTC())

	count := CreateSemanticEdges(db, ins, nil)
	if count != 0 {
		t.Errorf("no embedding: want 0, got %d", count)
	}
}

// --- Engine (OnInsightCreated) ---

func TestEngine_OnInsightCreated(t *testing.T) {
	db := testDB(t)
	engine := NewEngine(db, nil)
	now := time.Now().UTC()

	// Insert a prior insight with shared entity
	insertInsight(t, db, "eng-1", "Go SQLite integration because of WAL mode", "user", 3, []string{"Go", "SQLite"}, now.Add(-1*time.Hour))

	// New insight with causal signal and shared entity
	ins := insertInsight(t, db, "eng-2", "chose Go because of concurrency and SQLite support", "user", 3, []string{"Go"}, now)
	stats := engine.OnInsightCreated(ins)

	// Should have temporal edges (backbone)
	if stats.Temporal == 0 {
		t.Error("want temporal edges")
	}
	// Should have entity edges (shared "Go")
	if stats.Entity == 0 {
		t.Error("want entity edges from shared 'Go' entity")
	}
	// Entities should be enriched with regex extraction
	if len(ins.Entities) <= 1 {
		t.Errorf("entities should be enriched, got %v", ins.Entities)
	}
}

func TestEngine_OnInsightCreated_EntityModeProvided(t *testing.T) {
	db := testDB(t)
	engine := NewEngineWithEntityMode(db, nil, EntityModeProvided)
	now := time.Now().UTC()

	insertInsight(t, db, "eng-p-1", "Docker Redis prior", "user", 3, []string{"deployment-pipeline"}, now.Add(-1*time.Hour))
	ins := insertInsight(t, db, "eng-p-2", "We deploy HttpServer on Docker with Redis", "user", 3, []string{"deployment-pipeline"}, now)

	stats := engine.OnInsightCreated(ins)
	has := make(map[string]bool, len(ins.Entities))
	for _, e := range ins.Entities {
		has[e] = true
	}
	if !has["deployment-pipeline"] {
		t.Errorf("provided entity should remain, got %v", ins.Entities)
	}
	if has["HttpServer"] || has["Docker"] || has["Redis"] {
		t.Errorf("provided mode should not enrich with extracted entities, got %v", ins.Entities)
	}
	if stats.Entity == 0 {
		t.Error("want entity edges from shared provided entity")
	}
}

// --- FindSemanticCandidates with embeddings ---

func TestFindSemanticCandidates_Embedding(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	insertInsight(t, db, "emb-1", "Go concurrency patterns and goroutines", "user", 3, nil, now.Add(-1*time.Hour))
	insertInsight(t, db, "emb-2", "Python asyncio event loop", "user", 3, nil, now.Add(-30*time.Minute))
	ins := insertInsight(t, db, "emb-3", "Go goroutine scheduling internals", "user", 3, nil, now)

	// Store embeddings: emb-1 similar to emb-3, emb-2 different
	vec1 := []float64{0.9, 0.8, 0.7, 0.6}
	vec2 := []float64{0.1, 0.2, 0.9, 0.1}
	vec3 := []float64{0.85, 0.82, 0.72, 0.58}
	db.UpdateEmbedding("emb-1", embed.SerializeVector(vec1))
	db.UpdateEmbedding("emb-2", embed.SerializeVector(vec2))
	db.UpdateEmbedding("emb-3", embed.SerializeVector(vec3))

	candidates := FindSemanticCandidates(db, ins, nil)
	if len(candidates) == 0 {
		t.Fatal("want candidates via embedding similarity")
	}
	// emb-1 should be the closest
	if candidates[0].ID != "emb-1" {
		t.Errorf("top candidate: want emb-1, got %s", candidates[0].ID)
	}
}

// --- EmbedCache correctness ---

// TestCreateSemanticEdges_WithCache verifies that a pre-built cache is used
// instead of querying the database. We store embeddings only in the cache
// (NOT in the DB) so edges can only be created if the cache is used.
func TestCreateSemanticEdges_WithCache(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	ins1 := insertInsight(t, db, "wc-1", "Go concurrency patterns", "user", 3, nil, now.Add(-1*time.Hour))
	ins2 := insertInsight(t, db, "wc-2", "Go goroutine patterns", "user", 3, nil, now)
	_ = ins1

	// Very similar vectors (cosine > 0.80)
	vec1 := []float64{1.0, 0.9, 0.8, 0.7}
	vec2 := []float64{1.0, 0.85, 0.82, 0.71}

	// Only put embeddings in cache, NOT in DB
	cache := EmbedCache{
		"wc-1": vec1,
		"wc-2": vec2,
	}

	count := CreateSemanticEdges(db, ins2, cache)
	if count == 0 {
		t.Error("want semantic edges via cache, got 0")
	}

	// Verify edges exist
	edges, _ := db.GetEdgesByNodeAndType("wc-2", model.EdgeSemantic)
	if len(edges) == 0 {
		t.Error("want semantic edges in DB")
	}
}

// TestCreateSemanticEdges_CacheExcludesDeleted verifies that removing a
// soft-deleted insight from the cache prevents edges to it.
func TestCreateSemanticEdges_CacheExcludesDeleted(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	insertInsight(t, db, "del-1", "deleted insight about Go", "user", 3, nil, now.Add(-1*time.Hour))
	ins2 := insertInsight(t, db, "del-2", "Go patterns discussion", "user", 3, nil, now)

	// Very similar vectors
	vec1 := []float64{1.0, 0.9, 0.8, 0.7}
	vec2 := []float64{1.0, 0.85, 0.82, 0.71}

	// Soft-delete the first insight
	if err := db.SoftDeleteInsight("del-1"); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}

	// Build cache WITHOUT the deleted insight (simulates remember.go fix)
	cache := EmbedCache{
		// "del-1" intentionally excluded
		"del-2": vec2,
	}

	count := CreateSemanticEdges(db, ins2, cache)
	if count != 0 {
		t.Errorf("want 0 edges (deleted excluded from cache), got %d", count)
	}

	// Contrast: if deleted insight were still in cache, edges would be created
	cacheWithDeleted := EmbedCache{
		"del-1": vec1,
		"del-2": vec2,
	}
	count2 := CreateSemanticEdges(db, ins2, cacheWithDeleted)
	if count2 == 0 {
		t.Error("control: want edges when deleted insight is in cache")
	}
}

// TestFindSemanticCandidates_WithCache verifies the cache path returns candidates.
func TestFindSemanticCandidates_WithCache(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	insertInsight(t, db, "fc-1", "Go concurrency patterns and goroutines", "user", 3, nil, now.Add(-1*time.Hour))
	insertInsight(t, db, "fc-2", "Python asyncio event loop", "user", 3, nil, now.Add(-30*time.Minute))
	ins := insertInsight(t, db, "fc-3", "Go goroutine scheduling internals", "user", 3, nil, now)

	// Similar: fc-1 ↔ fc-3, different: fc-2
	cache := EmbedCache{
		"fc-1": {0.9, 0.8, 0.7, 0.6},
		"fc-2": {0.1, 0.2, 0.9, 0.1},
		"fc-3": {0.85, 0.82, 0.72, 0.58},
	}

	candidates := FindSemanticCandidates(db, ins, cache)
	if len(candidates) == 0 {
		t.Fatal("want candidates via cache embedding similarity")
	}
	if candidates[0].ID != "fc-1" {
		t.Errorf("top candidate: want fc-1, got %s", candidates[0].ID)
	}
	// Verify content and category were fetched from DB
	if candidates[0].Content == "" {
		t.Error("candidate content should be populated from DB")
	}
	if candidates[0].Category == "" {
		t.Error("candidate category should be populated from DB")
	}
}

// TestFindSemanticCandidates_CacheSkipsDeletedInsight verifies that
// soft-deleted insights in the cache are filtered out by GetInsightByID.
func TestFindSemanticCandidates_CacheSkipsDeletedInsight(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	insertInsight(t, db, "fd-1", "Go concurrency patterns", "user", 3, nil, now.Add(-1*time.Hour))
	ins := insertInsight(t, db, "fd-2", "Go goroutine patterns", "user", 3, nil, now)

	// Soft-delete fd-1
	if err := db.SoftDeleteInsight("fd-1"); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}

	// Cache still has deleted insight's embedding (stale)
	cache := EmbedCache{
		"fd-1": {0.9, 0.8, 0.7, 0.6},
		"fd-2": {0.85, 0.82, 0.72, 0.58},
	}

	candidates := FindSemanticCandidates(db, ins, cache)
	// fd-1 should not appear because GetInsightByID filters deleted_at
	for _, c := range candidates {
		if c.ID == "fd-1" {
			t.Error("deleted insight fd-1 should not appear as candidate")
		}
	}
}

// TestFindSemanticCandidates_AllDeletedFallsBackToTokenOverlap verifies that
// when ALL embedding candidates are soft-deleted (stale cache after AutoPrune),
// the function falls back to token overlap instead of returning an empty list.
func TestFindSemanticCandidates_AllDeletedFallsBackToTokenOverlap(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	// fd-only has high token overlap with ins, but also will have an embedding in cache
	insertInsight(t, db, "af-1", "Go uses SQLite for persistent graph storage engine", "user", 3, nil, now.Add(-2*time.Hour))
	// af-2 has embeddings but will be deleted
	insertInsight(t, db, "af-2", "Go goroutine concurrency patterns", "user", 3, nil, now.Add(-1*time.Hour))
	ins := insertInsight(t, db, "af-3", "Go uses SQLite for persistent memory storage engine", "user", 3, nil, now)

	// Soft-delete af-2 (simulates AutoPrune)
	if err := db.SoftDeleteInsight("af-2"); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}

	// Stale cache: af-2 (deleted) has similar embedding to af-3,
	// af-1 does NOT have an embedding in cache (no vector match).
	cache := EmbedCache{
		"af-2": {0.9, 0.85, 0.8, 0.7},
		"af-3": {0.88, 0.84, 0.82, 0.71},
	}

	candidates := FindSemanticCandidates(db, ins, cache)
	// af-2 is deleted → embedding path returns nil → should fall back to token overlap
	// af-1 should appear via token overlap (high content similarity with af-3)
	found := false
	for _, c := range candidates {
		if c.ID == "af-1" {
			found = true
		}
		if c.ID == "af-2" {
			t.Error("deleted insight af-2 should not appear")
		}
	}
	if !found {
		t.Error("want af-1 via token overlap fallback after all embedding candidates were deleted")
	}
}

// TestFindSemanticCandidates_EmptyCacheFallsBackToTokenOverlap verifies that
// an empty (non-nil) cache correctly falls back to token overlap.
func TestFindSemanticCandidates_EmptyCacheFallsBackToTokenOverlap(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	insertInsight(t, db, "fo-1", "Go uses SQLite for persistent graph storage engine", "user", 3, nil, now.Add(-1*time.Hour))
	ins := insertInsight(t, db, "fo-2", "Go uses SQLite for persistent memory storage engine", "user", 3, nil, now)

	// Empty cache (non-nil but no entries) — should fall back to token overlap
	emptyCache := make(EmbedCache)

	candidates := FindSemanticCandidates(db, ins, emptyCache)
	if len(candidates) == 0 {
		t.Fatal("want candidates via token overlap fallback")
	}
	if candidates[0].ID != "fo-1" {
		t.Errorf("top candidate: want fo-1, got %s", candidates[0].ID)
	}
}

// TestEngine_WithCache verifies the Engine passes its cache to CreateSemanticEdges.
func TestEngine_WithCache(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	insertInsight(t, db, "ec-1", "Go concurrency patterns", "user", 3, []string{"Go"}, now.Add(-1*time.Hour))
	ins := insertInsight(t, db, "ec-2", "Go goroutine patterns", "user", 3, []string{"Go"}, now)

	// Very similar vectors — only in cache, not DB
	cache := EmbedCache{
		"ec-1": {1.0, 0.9, 0.8, 0.7},
		"ec-2": {1.0, 0.85, 0.82, 0.71},
	}

	engine := NewEngine(db, cache)
	stats := engine.OnInsightCreated(ins)

	// Semantic edges should be created via cache
	if stats.Semantic == 0 {
		t.Error("want semantic edges via engine cache")
	}
}

// TestBuildEmbedCache verifies buildEmbedCache correctly loads from DB.
func TestBuildEmbedCache(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	insertInsight(t, db, "bc-1", "insight one", "user", 3, nil, now)
	insertInsight(t, db, "bc-2", "insight two", "user", 3, nil, now)

	vec1 := []float64{1.0, 0.0, 0.0}
	vec2 := []float64{0.0, 1.0, 0.0}
	db.UpdateEmbedding("bc-1", embed.SerializeVector(vec1))
	db.UpdateEmbedding("bc-2", embed.SerializeVector(vec2))

	cache := buildEmbedCache(db)
	if cache == nil {
		t.Fatal("want non-nil cache")
	}
	if len(cache) != 2 {
		t.Errorf("want 2 entries, got %d", len(cache))
	}
	if cache["bc-1"] == nil || cache["bc-2"] == nil {
		t.Error("want both entries in cache")
	}
}

// TestBuildEmbedCache_Empty verifies buildEmbedCache returns nil when no embeddings exist.
func TestBuildEmbedCache_Empty(t *testing.T) {
	db := testDB(t)
	insertInsight(t, db, "be-1", "no embedding", "user", 3, nil, time.Now().UTC())

	cache := buildEmbedCache(db)
	if cache != nil {
		t.Errorf("want nil cache when no embeddings, got %v", cache)
	}
}

// TestCreateSemanticEdges_NilCacheFallback verifies that passing nil cache
// falls back to loading from DB (same behavior as before cache was introduced).
func TestCreateSemanticEdges_NilCacheFallback(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	insertInsight(t, db, "nf-1", "Go concurrency", "user", 3, nil, now.Add(-1*time.Hour))
	ins2 := insertInsight(t, db, "nf-2", "Go goroutines", "user", 3, nil, now)

	vec1 := []float64{1.0, 0.9, 0.8, 0.7}
	vec2 := []float64{1.0, 0.85, 0.82, 0.71}
	db.UpdateEmbedding("nf-1", embed.SerializeVector(vec1))
	db.UpdateEmbedding("nf-2", embed.SerializeVector(vec2))

	// nil cache — should load from DB
	count := CreateSemanticEdges(db, ins2, nil)
	if count == 0 {
		t.Error("want semantic edges via DB fallback")
	}
}

// --- BFS direct tests ---

func TestBFS_EdgeFilter(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	// Graph: A --semantic--> B --temporal--> C
	insertInsight(t, db, "bf-A", "node A", "user", 3, nil, now)
	insertInsight(t, db, "bf-B", "node B", "user", 3, nil, now)
	insertInsight(t, db, "bf-C", "node C", "user", 3, nil, now)

	db.InsertEdge(&model.Edge{SourceID: "bf-A", TargetID: "bf-B", EdgeType: model.EdgeSemantic, Weight: 0.9, Metadata: map[string]string{}, CreatedAt: now})
	db.InsertEdge(&model.Edge{SourceID: "bf-B", TargetID: "bf-C", EdgeType: model.EdgeTemporal, Weight: 1.0, Metadata: map[string]string{}, CreatedAt: now})

	// Filter to semantic only: should reach B but NOT C (temporal edge blocked)
	nodes := BFS(db, "bf-A", BFSOptions{MaxDepth: 3, EdgeFilter: model.EdgeSemantic})
	ids := make(map[string]bool)
	for _, n := range nodes {
		ids[n.Insight.ID] = true
	}
	if !ids["bf-B"] {
		t.Error("EdgeFilter=semantic: should reach bf-B via semantic edge")
	}
	if ids["bf-C"] {
		t.Error("EdgeFilter=semantic: should NOT reach bf-C (only reachable via temporal edge)")
	}

	// Filter to temporal only: should NOT reach B from A (semantic edge blocked)
	nodes2 := BFS(db, "bf-A", BFSOptions{MaxDepth: 3, EdgeFilter: model.EdgeTemporal})
	if len(nodes2) != 0 {
		t.Errorf("EdgeFilter=temporal from bf-A: want 0 nodes, got %d", len(nodes2))
	}

	// No filter: should reach both B and C
	nodesAll := BFS(db, "bf-A", BFSOptions{MaxDepth: 3})
	if len(nodesAll) != 2 {
		t.Errorf("no EdgeFilter: want 2 nodes (B,C), got %d", len(nodesAll))
	}
}

func TestBFS_BidirectionalTraversal(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	// Edge direction: A → B, but BFS should traverse both directions
	insertInsight(t, db, "bd-A", "node A", "user", 3, nil, now)
	insertInsight(t, db, "bd-B", "node B", "user", 3, nil, now)

	db.InsertEdge(&model.Edge{SourceID: "bd-A", TargetID: "bd-B", EdgeType: model.EdgeCausal, Weight: 0.5, Metadata: map[string]string{}, CreatedAt: now})

	// Starting from B, should reach A via reverse traversal
	nodes := BFS(db, "bd-B", BFSOptions{MaxDepth: 2})
	if len(nodes) != 1 || nodes[0].Insight.ID != "bd-A" {
		t.Errorf("reverse traversal from bd-B: want [bd-A], got %v", nodes)
	}
}

func TestBFS_ExcludesStartNode(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	insertInsight(t, db, "ex-A", "start", "user", 3, nil, now)
	insertInsight(t, db, "ex-B", "neighbor", "user", 3, nil, now)
	db.InsertEdge(&model.Edge{SourceID: "ex-A", TargetID: "ex-B", EdgeType: model.EdgeSemantic, Weight: 1.0, Metadata: map[string]string{}, CreatedAt: now})

	nodes := BFS(db, "ex-A", BFSOptions{MaxDepth: 2})
	for _, n := range nodes {
		if n.Insight.ID == "ex-A" {
			t.Error("start node should be excluded from BFS results")
		}
	}
}

// --- CreateEntityEdges: maxTotalEntityEdges cap ---

func TestCreateEntityEdges_TotalEdgeCap(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	// Create many entities, each shared with multiple existing insights,
	// to trigger the maxTotalEntityEdges (50) global cap.
	// We need: enough entities × enough targets per entity > 50 edges.
	// Use 15 entities, each shared by 5 existing insights = potential 150 edges (bidirectional).
	entities := make([]string, 15)
	for i := range entities {
		entities[i] = "Entity" + string(rune('A'+i))
	}

	// Insert 5 existing insights, each having all 15 entities
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("cap-%d", i)
		insertInsight(t, db, id, "content "+id, "user", 3, entities, now.Add(-time.Duration(i+1)*time.Hour))
	}

	// New insight with all 15 entities
	ins := insertInsight(t, db, "cap-new", "new insight with many entities", "user", 3, entities, now)

	count := CreateEntityEdges(db, ins)
	if count > maxTotalEntityEdges {
		t.Errorf("total entity edges should be capped at %d, got %d", maxTotalEntityEdges, count)
	}
	// Should have created some edges (not zero)
	if count == 0 {
		t.Error("want at least some entity edges")
	}
}
