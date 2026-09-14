package search

import (
	"container/heap"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/mnemon-dev/mnemon/internal/memory/model"
)

func TestKeywordSearchStableCutoff(t *testing.T) {
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	insights := []*model.Insight{
		{ID: "b", Content: "Vega", Importance: 3, CreatedAt: old},
		{ID: "a", Content: "Vega", Importance: 3, CreatedAt: old},
		{ID: "y", Content: "Vega", Importance: 3, CreatedAt: old.AddDate(1, 0, 0)},
		{ID: "z", Content: "Vega", Importance: 5, CreatedAt: old},
	}
	want := []string{"z", "y", "a", "b"}
	for rotation := range insights {
		input := append(slices.Clone(insights[rotation:]), insights[:rotation]...)
		for _, limit := range []int{1, 2, 3, 4, 0} {
			results := KeywordSearch(input, "Vega", limit)
			got := make([]string, len(results))
			for i, result := range results {
				got[i] = result.Insight.ID
			}
			end := len(want)
			if limit > 0 {
				end = limit
			}
			if !slices.Equal(got, want[:end]) {
				t.Errorf("rotation %d limit %d: got %v, want %v", rotation, limit, got, want[:end])
			}
		}
	}
}

func TestRecallHeapTieOrder(t *testing.T) {
	t.Run("beam", func(t *testing.T) {
		h := &beamHeap{{id: "z", score: 1}, {id: "b", score: 1}, {id: "a", score: 1}}
		heap.Init(h)
		for _, want := range []string{"a", "b", "z"} {
			if got := heap.Pop(h).(beamItem).id; got != want {
				t.Errorf("got %s, want %s", got, want)
			}
		}
	})
	t.Run("vector", func(t *testing.T) {
		h := &vectorHitMinHeap{{id: "a", similarity: 1}, {id: "b", similarity: 1}, {id: "z", similarity: 1}}
		heap.Init(h)
		for _, want := range []string{"z", "b", "a"} {
			if got := heap.Pop(h).(vectorHit).id; got != want {
				t.Errorf("evicted %s, want %s", got, want)
			}
		}
	})
}

func TestVectorSearchStableCutoff(t *testing.T) {
	cache := make(map[string][]float64)
	for i := 0; i < 32; i++ {
		cache[fmt.Sprintf("v-%02d", i)] = []float64{1, 0}
	}
	for run := 0; run < 16; run++ {
		got := vectorSearchFromCache(cache, []float64{1, 0}, 3)
		if len(got) != 3 || got[0].id != "v-00" || got[1].id != "v-01" || got[2].id != "v-02" {
			t.Fatalf("run %d returned map-dependent top-k: %v", run, got)
		}
	}
}

func TestCausalSortPreservesScoreTies(t *testing.T) {
	db := testDB(t)
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	results := []RecallResult{
		{Insight: &model.Insight{ID: "z", Importance: 5, CreatedAt: old}, Score: 1},
		{Insight: &model.Insight{ID: "y", Importance: 3, CreatedAt: old.AddDate(1, 0, 0)}, Score: 1},
		{Insight: &model.Insight{ID: "a", Importance: 3, CreatedAt: old}, Score: 1},
		{Insight: &model.Insight{ID: "b", Importance: 3, CreatedAt: old}, Score: 1},
	}
	for _, result := range results {
		insertInsight(t, db, result.Insight.ID, "evidence", "user", result.Insight.Importance, nil, result.Insight.CreatedAt)
	}
	got := causalTopologicalSort(db, results)
	for i := range results {
		if got[i].Insight.ID != results[i].Insight.ID {
			t.Errorf("unrelated result %d changed from %s to %s", i, results[i].Insight.ID, got[i].Insight.ID)
		}
	}
}

func TestBeamBudgetIndependentOfSQLiteScanOrder(t *testing.T) {
	db := testDB(t)
	db.Conn().SetMaxOpenConns(1)
	created := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, id := range []string{"start", "c", "b", "a"} {
		insertInsight(t, db, id, "evidence", "user", 3, nil, created)
		if id != "start" {
			if err := db.InsertEdge(&model.Edge{SourceID: "start", TargetID: id, EdgeType: model.EdgeSemantic, Weight: 1, CreatedAt: created}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, setting := range []string{"OFF", "ON"} {
		if _, err := db.Conn().Exec("PRAGMA reverse_unordered_selects=" + setting); err != nil {
			t.Fatal(err)
		}
		scores := map[string]float64{"start": 1}
		beamSearchFromAnchor(db, "start", 1, nil, GetWeights(IntentGeneral),
			TraversalParams{BeamWidth: 2, MaxDepth: 1, MaxVisited: 3}, scores,
			make(map[string]string), make(map[string]*model.Insight), nil, nil)
		if len(scores) != 3 || scores["a"] == 0 || scores["b"] == 0 || scores["c"] != 0 {
			t.Errorf("scan order %s changed budget selection: %v", setting, scores)
		}
	}
}

func TestRecallStableTiedGraphAndTimeAnchors(t *testing.T) {
	for _, graph := range []bool{false, true} {
		t.Run(fmt.Sprint(graph), func(t *testing.T) {
			db := testDB(t)
			created := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
			n := anchorTopK + 5
			if graph {
				n = 8
			}
			for i := n - 1; i >= 0; i-- {
				id := fmt.Sprintf("tie-%02d", i)
				insertInsight(t, db, id, "Vega evidence", "user", 3, []string{"Vega"}, created)
				if graph {
					for j := i + 1; j < n; j++ {
						if err := db.InsertEdge(&model.Edge{SourceID: id, TargetID: fmt.Sprintf("tie-%02d", j), EdgeType: model.EdgeSemantic, Weight: 1, CreatedAt: created}); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			query, entities := "unmatched", []string(nil)
			if graph {
				query, entities = "Vega evidence", []string{"Vega"}
			}
			for run := 0; run < 16; run++ {
				response, err := IntentAwareRecall(db, query, nil, entities, 1, nil)
				if err != nil {
					t.Fatal(err)
				}
				if len(response.Results) != 1 || response.Results[0].Insight.ID != "tie-00" {
					t.Fatalf("run %d: tied recall should retain tie-00, got %+v", run, response.Results)
				}
			}
		})
	}
}
