package cmd

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mnemon-dev/mnemon/internal/model"
	"github.com/mnemon-dev/mnemon/internal/store"
)

func TestImportRepairsBackdatedTemporalBackbone(t *testing.T) {
	t.Setenv("MNEMON_EMBED_ENDPOINT", "http://127.0.0.1:1")

	oldDataDir, oldStoreName, oldReadOnly := dataDir, storeName, readOnly
	oldImportNoDiff, oldImportDryRun := importNoDiff, importDryRun
	t.Cleanup(func() {
		dataDir, storeName, readOnly = oldDataDir, oldStoreName, oldReadOnly
		importNoDiff, importDryRun = oldImportNoDiff, oldImportDryRun
	})

	dataDir = t.TempDir()
	storeName = ""
	readOnly = false
	importNoDiff = true
	importDryRun = false

	db, err := store.Open(store.StoreDir(dataDir, store.DefaultStoreName))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	insertTestInsight(t, db, "old-2023", "older context", "chat", "2023-01-01T00:00:00Z")
	insertTestInsight(t, db, "old-2025", "newer context", "chat", "2025-01-01T00:00:00Z")
	now := time.Now().UTC()
	if err := db.InsertEdge(&model.Edge{
		SourceID:  "old-2023",
		TargetID:  "old-2025",
		EdgeType:  model.EdgeTemporal,
		Weight:    1.0,
		Metadata:  map[string]string{"sub_type": "backbone", "direction": "precedes"},
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("insert old forward edge: %v", err)
	}
	if err := db.InsertEdge(&model.Edge{
		SourceID:  "old-2025",
		TargetID:  "old-2023",
		EdgeType:  model.EdgeTemporal,
		Weight:    1.0,
		Metadata:  map[string]string{"sub_type": "backbone", "direction": "succeeds"},
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("insert old reverse edge: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}

	draftPath := filepath.Join(t.TempDir(), "memory_draft.json")
	draft := `{
  "schema_version": "1",
  "source": "chat",
  "insights": [
    {
      "content": "imported middle context",
      "category": "context",
      "importance": 3,
      "created_at": "2024-01-01T00:00:00Z"
    }
  ]
}`
	if err := os.WriteFile(draftPath, []byte(draft), 0o644); err != nil {
		t.Fatalf("write draft: %v", err)
	}

	output := captureStdout(t, func() {
		if err := importCmd.RunE(importCmd, []string{draftPath}); err != nil {
			t.Fatalf("import RunE: %v", err)
		}
	})
	if output == "" {
		t.Fatal("expected import summary output")
	}

	db, err = store.Open(store.StoreDir(dataDir, store.DefaultStoreName))
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer db.Close()

	imported, err := db.QueryInsights(store.QueryFilter{Keyword: "imported middle", Limit: 1})
	if err != nil {
		t.Fatalf("query imported insight: %v", err)
	}
	if len(imported) != 1 {
		t.Fatalf("imported insight count = %d, want 1", len(imported))
	}
	importedID := imported[0].ID

	if hasTemporalEdge(t, db, "old-2023", "old-2025") {
		t.Fatal("old 2023->2025 temporal edge should have been removed")
	}
	if hasTemporalEdge(t, db, "old-2025", "old-2023") {
		t.Fatal("old 2025->2023 temporal edge should have been removed")
	}
	if !hasTemporalEdge(t, db, "old-2023", importedID) {
		t.Fatal("missing repaired 2023->imported temporal edge")
	}
	if !hasTemporalEdge(t, db, importedID, "old-2023") {
		t.Fatal("missing repaired imported->2023 temporal edge")
	}
	if !hasTemporalEdge(t, db, importedID, "old-2025") {
		t.Fatal("missing repaired imported->2025 temporal edge")
	}
	if !hasTemporalEdge(t, db, "old-2025", importedID) {
		t.Fatal("missing repaired 2025->imported temporal edge")
	}
}

func TestImportRefreshesEffectiveImportanceAfterExplicitEdges(t *testing.T) {
	t.Setenv("MNEMON_EMBED_ENDPOINT", "http://127.0.0.1:1")

	oldDataDir, oldStoreName, oldReadOnly := dataDir, storeName, readOnly
	oldImportNoDiff, oldImportDryRun := importNoDiff, importDryRun
	t.Cleanup(func() {
		dataDir, storeName, readOnly = oldDataDir, oldStoreName, oldReadOnly
		importNoDiff, importDryRun = oldImportNoDiff, oldImportDryRun
	})

	dataDir = t.TempDir()
	storeName = ""
	readOnly = false
	importNoDiff = true
	importDryRun = false

	draftPath := filepath.Join(t.TempDir(), "memory_draft.json")
	draft := `{
  "schema_version": "1",
  "insights": [
    {
      "content": "alpha lowercase memory",
      "category": "context",
      "importance": 3,
      "source": "source-a"
    },
    {
      "content": "beta lowercase memory",
      "category": "context",
      "importance": 3,
      "source": "source-b"
    }
  ],
  "edges": [
    {
      "source_index": 0,
      "target_index": 1,
      "edge_type": "semantic",
      "weight": 0.8,
      "reason": "test explicit edge"
    }
  ]
}`
	if err := os.WriteFile(draftPath, []byte(draft), 0o644); err != nil {
		t.Fatalf("write draft: %v", err)
	}

	captureStdout(t, func() {
		if err := importCmd.RunE(importCmd, []string{draftPath}); err != nil {
			t.Fatalf("import RunE: %v", err)
		}
	})

	db, err := store.Open(store.StoreDir(dataDir, store.DefaultStoreName))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	var lowEI float64
	if err := db.Conn().QueryRow(
		`SELECT MIN(effective_importance) FROM insights WHERE deleted_at IS NULL`,
	).Scan(&lowEI); err != nil {
		t.Fatalf("query effective importance: %v", err)
	}
	if lowEI <= 0.5 {
		t.Fatalf("effective_importance = %f, want refreshed value above no-edge baseline", lowEI)
	}
}

func insertTestInsight(t *testing.T, db *store.DB, id, content, source, createdAt string) {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		t.Fatalf("parse time: %v", err)
	}
	if err := db.InsertInsight(&model.Insight{
		ID:         id,
		Content:    content,
		Category:   model.CategoryContext,
		Importance: 3,
		Tags:       []string{},
		Entities:   []string{},
		Source:     source,
		CreatedAt:  ts,
		UpdatedAt:  ts,
	}); err != nil {
		t.Fatalf("insert insight %s: %v", id, err)
	}
}

func hasTemporalEdge(t *testing.T, db *store.DB, sourceID, targetID string) bool {
	t.Helper()
	edges, err := db.GetEdgesBySourceAndType(sourceID, model.EdgeTemporal)
	if err != nil {
		t.Fatalf("get temporal edges for %s: %v", sourceID, err)
	}
	for _, edge := range edges {
		if edge.TargetID == targetID {
			return true
		}
	}
	return false
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close stdout writer: %v", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	return string(data)
}
