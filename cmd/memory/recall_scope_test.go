package memory

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/mnemon-dev/mnemon/internal/memory/model"
	"github.com/mnemon-dev/mnemon/internal/memory/store"
)

func scopedRecallStore(t *testing.T) *store.DB {
	t.Helper()
	oldDir, oldStore, oldReadOnly := dataDir, storeName, readOnly
	oldBasic, oldBrief, oldVerbose, oldLimit := recBasic, recBrief, recVerbose, recLimit
	oldCategory, oldSource, oldIntent, oldExcerpt := recCategory, recSource, recIntent, recExcerpt
	t.Cleanup(func() {
		dataDir, storeName, readOnly = oldDir, oldStore, oldReadOnly
		recBasic, recBrief, recVerbose, recLimit = oldBasic, oldBrief, oldVerbose, oldLimit
		recCategory, recSource, recIntent, recExcerpt = oldCategory, oldSource, oldIntent, oldExcerpt
	})
	t.Setenv("MNEMON_EMBED_ENDPOINT", "http://127.0.0.1:1")
	t.Setenv("MNEMON_EMBED_PROTOCOL", "ollama")
	dataDir, storeName, readOnly = t.TempDir(), "recall-scope", true
	recBasic, recBrief, recVerbose, recLimit = false, false, false, 100
	recCategory, recSource, recIntent, recExcerpt = "", "", "GENERAL", 240
	db, err := store.Open(store.StoreDir(dataDir, storeName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func scopedRecallIDs(t *testing.T, query string) []string {
	t.Helper()
	var runErr error
	out := captureStdout(t, func() { runErr = recallCmd.RunE(recallCmd, []string{query}) })
	if runErr != nil {
		t.Fatal(runErr)
	}
	var response struct {
		Results []struct {
			ID      string
			Insight struct{ ID string }
		}
	}
	if err := json.Unmarshal([]byte(out), &response); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(response.Results))
	for idx, result := range response.Results {
		ids[idx] = result.ID
		if recVerbose {
			ids[idx] = result.Insight.ID
		}
	}
	return ids
}

func TestSmartRecallFiltersBeforeCandidateSelection(t *testing.T) {
	db := scopedRecallStore(t)
	insertTestInsight(t, db, "wanted", "Vega release token expires in sixty days", "prod", "2020-01-01T00:00:00Z")
	if _, err := db.Conn().Exec(`UPDATE insights SET category='decision' WHERE id='wanted'`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("noise-%02d", i)
		insertTestInsight(t, db, id, "Vega release token sandbox sample", "sandbox", "2026-01-01T00:00:00Z")
		if _, err := db.Conn().Exec(`UPDATE insights SET category='fact', importance=5 WHERE id=?`, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"compact", "verbose", "brief"} {
		t.Run(mode, func(t *testing.T) {
			recVerbose, recBrief, recLimit = mode == "verbose", mode == "brief", 1
			for _, filter := range []struct{ category, source string }{
				{"decision", "prod"}, {"decision", ""}, {"", "prod"},
			} {
				recCategory, recSource = filter.category, filter.source
				if ids := scopedRecallIDs(t, "Vega release token"); !slices.Equal(ids, []string{"wanted"}) {
					t.Errorf("filter %+v returned %v; eligible insight must survive candidate and result limits", filter, ids)
				}
			}
			for _, filter := range []struct{ category, source string }{
				{"decision", "sandbox"}, {"", "absent"}, {"preference", ""},
			} {
				recCategory, recSource = filter.category, filter.source
				if ids := scopedRecallIDs(t, "Vega release token"); len(ids) != 0 {
					t.Errorf("empty scope %+v returned %v", filter, ids)
				}
			}
		})
	}
}

func TestSmartRecallCannotTraverseOutsideScope(t *testing.T) {
	for _, boundary := range []string{"source", "category"} {
		t.Run(boundary, func(t *testing.T) {
			db := scopedRecallStore(t)
			insertTestInsight(t, db, "start", "unique scoped anchor", "prod", "2020-01-02T00:00:00Z")
			insertTestInsight(t, db, "bridge", "intermediate connector", "prod", "2020-01-01T00:00:00Z")
			insertTestInsight(t, db, "tail", "hidden payload beyond bridge", "prod", "2019-01-01T00:00:00Z")
			for i := 0; i < 25; i++ {
				insertTestInsight(t, db, fmt.Sprintf("recent-%02d", i), "unrelated inventory", "prod", "2026-01-01T00:00:00Z")
			}
			if boundary == "source" {
				if _, err := db.Conn().Exec(`UPDATE insights SET source='sandbox' WHERE id='bridge'`); err != nil {
					t.Fatal(err)
				}
			} else if _, err := db.Conn().Exec(`UPDATE insights SET category='fact' WHERE id='bridge'`); err != nil {
				t.Fatal(err)
			}
			for _, pair := range [][2]string{{"start", "bridge"}, {"bridge", "tail"}} {
				if err := db.InsertEdge(&model.Edge{SourceID: pair[0], TargetID: pair[1], EdgeType: model.EdgeCausal, Weight: 1, CreatedAt: time.Now().UTC()}); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if ids := scopedRecallIDs(t, "unique scoped anchor"); !slices.Contains(ids, "tail") {
				t.Fatalf("unfiltered control did not follow the graph: %v", ids)
			}
			recSource, recCategory = "prod", "context"
			ids := scopedRecallIDs(t, "unique scoped anchor")
			if !slices.Contains(ids, "start") || slices.Contains(ids, "bridge") || slices.Contains(ids, "tail") {
				t.Fatalf("scoped recall crossed the %s boundary: %v", boundary, ids)
			}
		})
	}
}
