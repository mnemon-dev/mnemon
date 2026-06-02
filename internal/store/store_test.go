package store

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mnemon-dev/mnemon/internal/model"
)

// testDB creates a fresh SQLite database in a temp directory, auto-closed on test cleanup.
func testDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open test DB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func makeInsight(id, content string, importance int) *model.Insight {
	now := time.Now().UTC()
	return &model.Insight{
		ID:         id,
		Content:    content,
		Category:   model.CategoryFact,
		Importance: importance,
		Tags:       []string{},
		Entities:   []string{},
		Source:     "test",
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

// --- Insight CRUD ---

func TestInsertAndGetInsight(t *testing.T) {
	db := testDB(t)
	ins := makeInsight("ins-1", "Go uses SQLite for storage", 3)
	ins.Tags = []string{"go", "sqlite"}
	ins.Entities = []string{"Go", "SQLite"}

	if err := db.InsertInsight(ins); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := db.GetInsightByID("ins-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Content != ins.Content {
		t.Errorf("content: want %q, got %q", ins.Content, got.Content)
	}
	if got.Importance != 3 {
		t.Errorf("importance: want 3, got %d", got.Importance)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "go" {
		t.Errorf("tags: want [go sqlite], got %v", got.Tags)
	}
	if len(got.Entities) != 2 || got.Entities[0] != "Go" {
		t.Errorf("entities: want [Go SQLite], got %v", got.Entities)
	}
}

func TestGetInsightByID_NotFound(t *testing.T) {
	db := testDB(t)
	_, err := db.GetInsightByID("nonexistent")
	if err == nil {
		t.Error("want error for nonexistent ID, got nil")
	}
}

func TestSoftDeleteInsight(t *testing.T) {
	db := testDB(t)
	ins := makeInsight("del-1", "to be deleted", 2)
	db.InsertInsight(ins)

	// Add an edge to verify it gets cleaned up
	db.InsertEdge(&model.Edge{
		SourceID: "del-1", TargetID: "del-1",
		EdgeType: model.EdgeTemporal, Weight: 1.0,
		Metadata: map[string]string{}, CreatedAt: time.Now().UTC(),
	})

	if err := db.SoftDeleteInsight("del-1"); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	// Should not be found via GetInsightByID (excludes deleted)
	_, err := db.GetInsightByID("del-1")
	if err == nil {
		t.Error("deleted insight should not be found via GetInsightByID")
	}

	// But should be found via GetInsightByIDIncludeDeleted
	got, err := db.GetInsightByIDIncludeDeleted("del-1")
	if err != nil {
		t.Fatalf("get include deleted: %v", err)
	}
	if got.DeletedAt == nil {
		t.Error("deleted_at should be set")
	}

	// Edges should be deleted
	edges, _ := db.GetEdgesByNode("del-1")
	if len(edges) != 0 {
		t.Errorf("edges should be deleted, got %d", len(edges))
	}
}

func TestSoftDeleteInsight_AlreadyDeleted(t *testing.T) {
	db := testDB(t)
	ins := makeInsight("del-2", "already deleted", 2)
	db.InsertInsight(ins)
	db.SoftDeleteInsight("del-2")

	err := db.SoftDeleteInsight("del-2")
	if err == nil {
		t.Error("double delete should return error")
	}
}

// --- Store names ---

func TestReadActiveRejectsInvalidStoreName(t *testing.T) {
	baseDir := t.TempDir()
	if err := os.WriteFile(ActiveFile(baseDir), []byte("../outside\n"), 0o644); err != nil {
		t.Fatalf("write active file: %v", err)
	}

	if got := ReadActive(baseDir); got != DefaultStoreName {
		t.Fatalf("invalid active store should fall back to %q, got %q", DefaultStoreName, got)
	}
}

func TestWriteActiveRejectsInvalidStoreName(t *testing.T) {
	if err := WriteActive(t.TempDir(), "../outside"); err == nil {
		t.Fatal("expected invalid store name error")
	}
}

func TestListStoresFiltersInvalidDirectoryNames(t *testing.T) {
	baseDir := t.TempDir()
	for _, name := range []string{"default", "work_1", ".hidden"} {
		dir := filepath.Join(baseDir, "data", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create store dir %q: %v", name, err)
		}
	}

	names, err := ListStores(baseDir)
	if err != nil {
		t.Fatalf("list stores: %v", err)
	}
	want := []string{"default", "work_1"}
	if len(names) != len(want) {
		t.Fatalf("store count mismatch: want %v, got %v", want, names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("store names mismatch: want %v, got %v", want, names)
		}
	}
}

func TestStoreExistsRejectsInvalidStoreName(t *testing.T) {
	baseDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(baseDir, "outside"), 0o755); err != nil {
		t.Fatalf("create outside dir: %v", err)
	}

	if StoreExists(baseDir, "../outside") {
		t.Fatal("invalid store name must not resolve outside data directory")
	}
}

// --- Query ---

func TestQueryInsights_Filters(t *testing.T) {
	db := testDB(t)
	ins1 := makeInsight("q-1", "Go language features", 5)
	ins1.Category = model.CategoryFact
	ins2 := makeInsight("q-2", "Python web framework", 2)
	ins2.Category = model.CategoryDecision
	ins3 := makeInsight("q-3", "Go concurrency patterns", 4)
	ins3.Category = model.CategoryFact
	db.InsertInsight(ins1)
	db.InsertInsight(ins2)
	db.InsertInsight(ins3)

	// Keyword filter
	results, err := db.QueryInsights(QueryFilter{Keyword: "Go"})
	if err != nil {
		t.Fatalf("query keyword: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("keyword 'Go': want 2 results, got %d", len(results))
	}

	// Category filter
	results, err = db.QueryInsights(QueryFilter{Category: "decision"})
	if err != nil {
		t.Fatalf("query category: %v", err)
	}
	if len(results) != 1 || results[0].ID != "q-2" {
		t.Errorf("category decision: want [q-2], got %v", results)
	}

	// MinImportance filter
	results, err = db.QueryInsights(QueryFilter{MinImportance: 4})
	if err != nil {
		t.Fatalf("query importance: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("importance>=4: want 2 results, got %d", len(results))
	}
}

// --- Edges ---

func TestInsertAndGetEdges(t *testing.T) {
	db := testDB(t)
	db.InsertInsight(makeInsight("e-1", "source", 3))
	db.InsertInsight(makeInsight("e-2", "target", 3))

	edge := &model.Edge{
		SourceID: "e-1", TargetID: "e-2",
		EdgeType: model.EdgeSemantic, Weight: 0.85,
		Metadata:  map[string]string{"cosine": "0.8500"},
		CreatedAt: time.Now().UTC(),
	}
	if err := db.InsertEdge(edge); err != nil {
		t.Fatalf("insert edge: %v", err)
	}

	// GetEdgesByNode
	edges, err := db.GetEdgesByNode("e-1")
	if err != nil {
		t.Fatalf("get edges by node: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("want 1 edge, got %d", len(edges))
	}
	if edges[0].EdgeType != model.EdgeSemantic {
		t.Errorf("edge type: want semantic, got %s", edges[0].EdgeType)
	}
	if edges[0].Metadata["cosine"] != "0.8500" {
		t.Errorf("metadata cosine: want 0.8500, got %s", edges[0].Metadata["cosine"])
	}

	// Also visible from target side
	edges, err = db.GetEdgesByNode("e-2")
	if err != nil {
		t.Fatalf("get edges by target: %v", err)
	}
	if len(edges) != 1 {
		t.Error("edge should be visible from target node too")
	}
}

func TestGetEdgesBySourceAndType(t *testing.T) {
	db := testDB(t)
	db.InsertInsight(makeInsight("st-1", "a", 3))
	db.InsertInsight(makeInsight("st-2", "b", 3))
	db.InsertInsight(makeInsight("st-3", "c", 3))

	db.InsertEdge(&model.Edge{
		SourceID: "st-1", TargetID: "st-2",
		EdgeType: model.EdgeTemporal, Weight: 1.0,
		Metadata: map[string]string{}, CreatedAt: time.Now().UTC(),
	})
	db.InsertEdge(&model.Edge{
		SourceID: "st-1", TargetID: "st-3",
		EdgeType: model.EdgeSemantic, Weight: 0.9,
		Metadata: map[string]string{}, CreatedAt: time.Now().UTC(),
	})

	edges, err := db.GetEdgesBySourceAndType("st-1", model.EdgeTemporal)
	if err != nil {
		t.Fatalf("get by source and type: %v", err)
	}
	if len(edges) != 1 || edges[0].TargetID != "st-2" {
		t.Errorf("want 1 temporal edge to st-2, got %v", edges)
	}
}

func TestFindInsightsWithEntity(t *testing.T) {
	db := testDB(t)
	ins1 := makeInsight("fe-1", "uses Go", 3)
	ins1.Entities = []string{"Go", "SQLite"}
	ins2 := makeInsight("fe-2", "uses Python", 3)
	ins2.Entities = []string{"Python"}
	ins3 := makeInsight("fe-3", "also uses Go", 3)
	ins3.Entities = []string{"Go"}
	db.InsertInsight(ins1)
	db.InsertInsight(ins2)
	db.InsertInsight(ins3)

	ids, err := db.FindInsightsWithEntity("Go", "fe-3", 10)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(ids) != 1 || ids[0] != "fe-1" {
		t.Errorf("want [fe-1], got %v", ids)
	}
}

// --- Transactions ---

func TestInTransaction_Commit(t *testing.T) {
	db := testDB(t)
	err := db.InTransaction(func() error {
		db.InsertInsight(makeInsight("tx-1", "in transaction", 3))
		return nil
	})
	if err != nil {
		t.Fatalf("transaction: %v", err)
	}
	got, err := db.GetInsightByID("tx-1")
	if err != nil || got == nil {
		t.Error("committed insight should be readable")
	}
}

func TestInTransaction_Rollback(t *testing.T) {
	db := testDB(t)
	err := db.InTransaction(func() error {
		db.InsertInsight(makeInsight("tx-2", "will be rolled back", 3))
		return errRollback
	})
	if err == nil {
		t.Error("transaction should return error on rollback")
	}
	_, err = db.GetInsightByID("tx-2")
	if err == nil {
		t.Error("rolled back insight should not be readable")
	}
}

var errRollback = &rollbackError{}

type rollbackError struct{}

func (e *rollbackError) Error() string { return "rollback" }

func TestInTransaction_Nested(t *testing.T) {
	db := testDB(t)
	err := db.InTransaction(func() error {
		return db.InTransaction(func() error { return nil })
	})
	if err == nil {
		t.Error("nested transactions should return error")
	}
}

// --- Lifecycle ---

func TestComputeEffectiveImportance_NewInsight(t *testing.T) {
	// Brand new: importance=3, 0 accesses, 0 days, 0 edges
	ei := ComputeEffectiveImportance(3, 0, 0, 0)
	// base=0.5, accessFactor=max(1.0, log(1))=1.0, decay=1.0, edge=1.0
	want := 0.5
	if math.Abs(ei-want) > 0.01 {
		t.Errorf("new insight: want ~%f, got %f", want, ei)
	}
}

func TestComputeEffectiveImportance_MaxImportance(t *testing.T) {
	ei := ComputeEffectiveImportance(5, 0, 0, 0)
	// base=1.0
	if ei != 1.0 {
		t.Errorf("max importance new: want 1.0, got %f", ei)
	}
}

func TestComputeEffectiveImportance_Decay(t *testing.T) {
	// After 30 days (1 half-life), should be ~half
	fresh := ComputeEffectiveImportance(3, 0, 0, 0)
	decayed := ComputeEffectiveImportance(3, 0, 30, 0)
	ratio := decayed / fresh
	if math.Abs(ratio-0.5) > 0.01 {
		t.Errorf("30-day decay ratio: want ~0.5, got %f", ratio)
	}
}

func TestComputeEffectiveImportance_HighAccess(t *testing.T) {
	low := ComputeEffectiveImportance(3, 0, 0, 0)
	high := ComputeEffectiveImportance(3, 10, 0, 0)
	if high <= low {
		t.Errorf("high access should increase EI: low=%f, high=%f", low, high)
	}
}

func TestComputeEffectiveImportance_EdgeBonus(t *testing.T) {
	noEdge := ComputeEffectiveImportance(3, 0, 0, 0)
	withEdge := ComputeEffectiveImportance(3, 0, 0, 5)
	if withEdge <= noEdge {
		t.Errorf("edges should increase EI: no=%f, with=%f", noEdge, withEdge)
	}
}

func TestComputeEffectiveImportance_EdgeCapped(t *testing.T) {
	e5 := ComputeEffectiveImportance(3, 0, 0, 5)
	e10 := ComputeEffectiveImportance(3, 0, 0, 10)
	// Edge count capped at 5, so e5 == e10
	if e5 != e10 {
		t.Errorf("edge cap at 5: e5=%f, e10=%f (should be equal)", e5, e10)
	}
}

func TestIsImmune(t *testing.T) {
	if !IsImmune(4, 0) {
		t.Error("importance>=4 should be immune")
	}
	if !IsImmune(5, 0) {
		t.Error("importance=5 should be immune")
	}
	if !IsImmune(1, 3) {
		t.Error("accessCount>=3 should be immune")
	}
	if IsImmune(3, 2) {
		t.Error("importance=3, access=2 should NOT be immune")
	}
	if IsImmune(1, 0) {
		t.Error("importance=1, access=0 should NOT be immune")
	}
}

func TestBaseWeight(t *testing.T) {
	tests := []struct {
		importance int
		want       float64
	}{
		{5, 1.0},
		{4, 0.8},
		{3, 0.5},
		{2, 0.3},
		{1, 0.15},
	}
	for _, tt := range tests {
		got := baseWeight(tt.importance)
		if got != tt.want {
			t.Errorf("baseWeight(%d): want %f, got %f", tt.importance, tt.want, got)
		}
	}
}

// --- AutoPrune ---

func TestAutoPrune_PrunesLowestEI(t *testing.T) {
	db := testDB(t)

	// Insert more than max
	for i := 0; i < 5; i++ {
		ins := makeInsight("prune-"+string(rune('a'+i)), "content", 2)
		db.InsertInsight(ins)
	}

	// Set max to 3 — should prune 2 (capped by PruneBatchSize)
	pruned, err := db.AutoPrune(3, nil)
	if err != nil {
		t.Fatalf("auto prune: %v", err)
	}
	if pruned != 2 {
		t.Errorf("want 2 pruned, got %d", pruned)
	}

	all, _ := db.GetAllActiveInsights()
	if len(all) != 3 {
		t.Errorf("want 3 remaining, got %d", len(all))
	}
}

func TestAutoPrune_RespectsImmune(t *testing.T) {
	db := testDB(t)

	// Insert 3 insights: 2 immune (importance=4), 1 not
	immune1 := makeInsight("immune-1", "important", 4)
	immune2 := makeInsight("immune-2", "also important", 5)
	weak := makeInsight("weak-1", "low importance", 1)
	db.InsertInsight(immune1)
	db.InsertInsight(immune2)
	db.InsertInsight(weak)

	// Max=1 — only non-immune should be pruned
	pruned, err := db.AutoPrune(1, nil)
	if err != nil {
		t.Fatalf("auto prune: %v", err)
	}
	if pruned != 1 {
		t.Errorf("want 1 pruned (only the weak one), got %d", pruned)
	}

	// Verify the weak one was pruned
	_, err = db.GetInsightByID("weak-1")
	if err == nil {
		t.Error("weak insight should be pruned")
	}
}

func TestAutoPrune_RespectsExcludeIDs(t *testing.T) {
	db := testDB(t)
	ins1 := makeInsight("ex-1", "content a", 1)
	ins2 := makeInsight("ex-2", "content b", 1)
	db.InsertInsight(ins1)
	db.InsertInsight(ins2)

	// Max=0, exclude ex-1
	pruned, _ := db.AutoPrune(0, []string{"ex-1"})
	if pruned != 1 {
		t.Errorf("want 1 pruned (only ex-2), got %d", pruned)
	}
	_, err := db.GetInsightByID("ex-1")
	if err != nil {
		t.Error("excluded insight should survive")
	}
}

func TestAutoPrune_NothingToPrune(t *testing.T) {
	db := testDB(t)
	db.InsertInsight(makeInsight("ok-1", "content", 3))

	pruned, err := db.AutoPrune(10, nil)
	if err != nil {
		t.Fatalf("auto prune: %v", err)
	}
	if pruned != 0 {
		t.Errorf("nothing to prune: want 0, got %d", pruned)
	}
}

// --- Oplog ---

func TestOplog(t *testing.T) {
	db := testDB(t)
	db.LogOp("remember", "ins-1", "test detail")
	db.LogOp("recall", "", "query: test")

	entries, err := db.GetOplog(10)
	if err != nil {
		t.Fatalf("get oplog: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	// Most recent first
	if entries[0].Operation != "recall" {
		t.Errorf("first entry: want recall, got %s", entries[0].Operation)
	}
	if entries[1].Operation != "remember" {
		t.Errorf("second entry: want remember, got %s", entries[1].Operation)
	}
}

// --- Embedding ---

func TestUpdateAndGetEmbedding(t *testing.T) {
	db := testDB(t)
	db.InsertInsight(makeInsight("emb-1", "content", 3))

	blob := []byte{1, 2, 3, 4, 5, 6, 7, 8} // 1 float64
	if err := db.UpdateEmbedding("emb-1", blob); err != nil {
		t.Fatalf("update embedding: %v", err)
	}

	got, err := db.GetEmbedding("emb-1")
	if err != nil {
		t.Fatalf("get embedding: %v", err)
	}
	if len(got) != 8 {
		t.Errorf("want 8 bytes, got %d", len(got))
	}
}

func TestGetAllActiveInsights(t *testing.T) {
	db := testDB(t)
	db.InsertInsight(makeInsight("all-1", "a", 3))
	db.InsertInsight(makeInsight("all-2", "b", 3))
	db.InsertInsight(makeInsight("all-3", "c", 3))
	db.SoftDeleteInsight("all-2")

	all, err := db.GetAllActiveInsights()
	if err != nil {
		t.Fatalf("get all: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("want 2 active, got %d", len(all))
	}
}

func TestIncrementAccessCount(t *testing.T) {
	db := testDB(t)
	db.InsertInsight(makeInsight("acc-1", "content", 3))

	db.IncrementAccessCount("acc-1")
	db.IncrementAccessCount("acc-1")

	got, _ := db.GetInsightByID("acc-1")
	if got.AccessCount != 2 {
		t.Errorf("want access_count=2, got %d", got.AccessCount)
	}
}

// --- Store management ---

func TestValidStoreName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"default", true},
		{"my-store", true},
		{"work_2024", true},
		{"A", true},
		{"a1", true},
		{"", false},
		{"-bad", false},
		{"_bad", false},
		{"has space", false},
		{"has/slash", false},
		{"has.dot", false},
		{".hidden", false},
	}
	for _, tt := range tests {
		got := ValidStoreName(tt.name)
		if got != tt.want {
			t.Errorf("ValidStoreName(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestReadWriteActive(t *testing.T) {
	base := t.TempDir()

	// No active file → default
	if got := ReadActive(base); got != DefaultStoreName {
		t.Errorf("ReadActive empty: got %q, want %q", got, DefaultStoreName)
	}

	// Write and read back
	if err := WriteActive(base, "work"); err != nil {
		t.Fatalf("WriteActive: %v", err)
	}
	if got := ReadActive(base); got != "work" {
		t.Errorf("ReadActive after write: got %q, want %q", got, "work")
	}
}

func TestListStores(t *testing.T) {
	base := t.TempDir()

	// Empty → nil
	names, err := ListStores(base)
	if err != nil {
		t.Fatalf("ListStores empty: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("want empty, got %v", names)
	}

	// Create stores
	db1, _ := Open(StoreDir(base, "alpha"))
	db1.Close()
	db2, _ := Open(StoreDir(base, "beta"))
	db2.Close()

	names, err = ListStores(base)
	if err != nil {
		t.Fatalf("ListStores: %v", err)
	}
	if len(names) != 2 || names[0] != "alpha" || names[1] != "beta" {
		t.Errorf("want [alpha beta], got %v", names)
	}
}

func TestStoreExists(t *testing.T) {
	base := t.TempDir()

	if StoreExists(base, "nope") {
		t.Error("should not exist")
	}

	db, _ := Open(StoreDir(base, "yes"))
	db.Close()
	if !StoreExists(base, "yes") {
		t.Error("should exist after Open")
	}
}

func TestMigrateIfNeeded(t *testing.T) {
	base := t.TempDir()

	// Create a legacy DB
	db, err := Open(base)
	if err != nil {
		t.Fatalf("open legacy: %v", err)
	}
	db.InsertInsight(makeInsight("mig-1", "legacy data", 3))
	db.Close()
	if err := os.WriteFile(filepath.Join(base, "mnemon.db-wal"), []byte("wal"), 0o644); err != nil {
		t.Fatalf("write legacy wal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "mnemon.db-shm"), []byte("shm"), 0o644); err != nil {
		t.Fatalf("write legacy shm: %v", err)
	}

	// Run migration
	if err := MigrateIfNeeded(base); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Old file should be gone
	if _, err := os.Stat(filepath.Join(base, "mnemon.db")); !os.IsNotExist(err) {
		t.Error("old mnemon.db should be moved")
	}
	if _, err := os.Stat(filepath.Join(base, "mnemon.db-wal")); !os.IsNotExist(err) {
		t.Error("old WAL should be moved")
	}
	if _, err := os.Stat(filepath.Join(base, "mnemon.db-shm")); !os.IsNotExist(err) {
		t.Error("old SHM should be moved")
	}
	if got, err := os.ReadFile(filepath.Join(StoreDir(base, DefaultStoreName), "mnemon.db-wal")); err != nil || string(got) != "wal" {
		t.Fatalf("migrated WAL mismatch: got=%q err=%v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(StoreDir(base, DefaultStoreName), "mnemon.db-shm")); err != nil || string(got) != "shm" {
		t.Fatalf("migrated SHM mismatch: got=%q err=%v", got, err)
	}
	if err := os.Remove(filepath.Join(StoreDir(base, DefaultStoreName), "mnemon.db-wal")); err != nil {
		t.Fatalf("remove fake migrated WAL: %v", err)
	}
	if err := os.Remove(filepath.Join(StoreDir(base, DefaultStoreName), "mnemon.db-shm")); err != nil {
		t.Fatalf("remove fake migrated SHM: %v", err)
	}

	// New location should work
	db2, err := Open(StoreDir(base, DefaultStoreName))
	if err != nil {
		t.Fatalf("open migrated: %v", err)
	}
	defer db2.Close()
	ins, err := db2.GetInsightByID("mig-1")
	if err != nil || ins.Content != "legacy data" {
		t.Errorf("migrated data lost: err=%v", err)
	}
}

func TestMigrateIfNeeded_AlreadyMigrated(t *testing.T) {
	base := t.TempDir()

	// Create new-layout DB directly
	db, _ := Open(StoreDir(base, DefaultStoreName))
	db.Close()

	// Should be a no-op
	if err := MigrateIfNeeded(base); err != nil {
		t.Fatalf("migrate no-op: %v", err)
	}
}

func TestMigrateIfNeeded_NoLegacy(t *testing.T) {
	base := t.TempDir()
	// Nothing to migrate
	if err := MigrateIfNeeded(base); err != nil {
		t.Fatalf("migrate empty: %v", err)
	}
}

// --- LoadKnownEntities ---

func TestLoadKnownEntities_Empty(t *testing.T) {
	db := testDB(t)
	known, err := db.LoadKnownEntities()
	if err != nil {
		t.Fatalf("load known entities on empty DB: %v", err)
	}
	if known == nil {
		t.Error("want non-nil empty map, got nil")
	}
	if len(known) != 0 {
		t.Errorf("want 0 known entities, got %d: %v", len(known), known)
	}
}

func TestLoadKnownEntities_DistinctUnion(t *testing.T) {
	db := testDB(t)
	a := makeInsight("ent-a", "first", 3)
	a.Entities = []string{"Athena", "Hestia"}
	b := makeInsight("ent-b", "second", 3)
	b.Entities = []string{"Hestia", "Mnemon"} // Hestia overlaps with a
	for _, ins := range []*model.Insight{a, b} {
		if err := db.InsertInsight(ins); err != nil {
			t.Fatalf("insert %s: %v", ins.ID, err)
		}
	}

	known, err := db.LoadKnownEntities()
	if err != nil {
		t.Fatalf("load known entities: %v", err)
	}
	for _, want := range []string{"Athena", "Hestia", "Mnemon"} {
		if !known[want] {
			t.Errorf("want %q in known set, got %v", want, known)
		}
	}
	if len(known) != 3 {
		t.Errorf("want 3 distinct entities (Hestia deduped), got %d: %v", len(known), known)
	}
}

func TestLoadKnownEntities_SkipsDeleted(t *testing.T) {
	db := testDB(t)
	live := makeInsight("ent-live", "kept", 3)
	live.Entities = []string{"Athena"}
	dead := makeInsight("ent-dead", "dropped", 3)
	dead.Entities = []string{"Banana"}
	for _, ins := range []*model.Insight{live, dead} {
		if err := db.InsertInsight(ins); err != nil {
			t.Fatalf("insert %s: %v", ins.ID, err)
		}
	}
	if err := db.SoftDeleteInsight("ent-dead"); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	known, err := db.LoadKnownEntities()
	if err != nil {
		t.Fatalf("load known entities: %v", err)
	}
	if !known["Athena"] {
		t.Errorf("want 'Athena' from live insight, got %v", known)
	}
	if known["Banana"] {
		t.Errorf("'Banana' is on a soft-deleted insight — must not appear, got %v", known)
	}
}
