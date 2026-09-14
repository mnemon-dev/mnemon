package search

import (
	"fmt"
	"testing"
	"time"

	"github.com/mnemon-dev/mnemon/internal/memory/model"
)

func TestRecallVisitBudgetKeepsStrongEdgesAtHighDegree(t *testing.T) {
	for _, intermediate := range []bool{false, true} {
		t.Run(fmt.Sprintf("intermediate_hub=%v", intermediate), func(t *testing.T) {
			db := testDB(t)
			old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
			anchorID, hubContent := "hub", "UniqueNeedle primary evidence"
			if intermediate {
				anchorID, hubContent = "entry", "intermediate connector"
				insertInsight(t, db, anchorID, "UniqueNeedle primary evidence", "prod", 3, nil, old)
			}
			insertInsight(t, db, "hub", hubContent, "prod", 3, nil, old)
			insertInsight(t, db, "z-gold", "Approval expires in seven days", "prod", 3, nil, old)
			if intermediate {
				if err := db.InsertEdge(&model.Edge{SourceID: anchorID, TargetID: "hub", EdgeType: model.EdgeSemantic, Weight: 1, CreatedAt: old}); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.InsertEdge(&model.Edge{SourceID: "hub", TargetID: "z-gold", EdgeType: model.EdgeSemantic, Weight: 1, CreatedAt: old}); err != nil {
				t.Fatal(err)
			}
			budget := getTraversalParams(IntentGeneral).MaxVisited
			for i := 0; i < budget+20; i++ {
				id := fmt.Sprintf("a-noise-%03d", i)
				insertInsight(t, db, id, "unrelated inventory", "prod", 3, nil, old.AddDate(6, 0, 0))
				if err := db.InsertEdge(&model.Edge{SourceID: "hub", TargetID: id, EdgeType: model.EdgeSemantic, Weight: 0.001, CreatedAt: old}); err != nil {
					t.Fatal(err)
				}
			}
			response, err := IntentAwareRecall(db, "UniqueNeedle", nil, nil, 1000, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, result := range response.Results {
				if result.Insight.ID == "z-gold" {
					return
				}
			}
			t.Fatalf("ID order discarded the strong transition behind %d weak edges (traversed %d)", budget+20, response.Meta.Traversed)
		})
	}
}

func TestBeamVisitBudgetUsesCompleteScopedTransitionScore(t *testing.T) {
	db := testDB(t)
	created := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	allowed := make(map[string]*model.Insight)
	for _, id := range []string{"start", "a-structural", "z-semantic", "x-excluded"} {
		insight := insertInsight(t, db, id, "evidence", "prod", 3, nil, created)
		if id != "x-excluded" {
			allowed[id] = insight
		}
		if id != "start" {
			weight := 1.0
			if id == "z-semantic" {
				weight = 0.01
			}
			// Incoming edges exercise the same neighbor and transition policy.
			if err := db.InsertEdge(&model.Edge{SourceID: id, TargetID: "start", EdgeType: model.EdgeSemantic, Weight: weight, CreatedAt: created}); err != nil {
				t.Fatal(err)
			}
		}
	}
	cache := map[string][]float64{"a-structural": {0, 1}, "z-semantic": {1, 0}, "x-excluded": {1, 0}}
	scores := map[string]float64{"start": 1}
	beamSearchFromAnchor(db, "start", 1, []float64{1, 0}, GetWeights(IntentGeneral),
		TraversalParams{BeamWidth: 1, MaxDepth: 2, MaxVisited: 2}, scores,
		make(map[string]string), make(map[string]*model.Insight), cache, allowed)
	if len(scores) != 2 || scores["z-semantic"] <= 1.4 || scores["a-structural"] != 0 || scores["x-excluded"] != 0 {
		t.Fatalf("the visit budget must use structural + semantic score inside the scope: %v", scores)
	}
}
