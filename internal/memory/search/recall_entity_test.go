package search

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mnemon-dev/mnemon/internal/memory/model"
)

func TestRecallEntityAnchorsBoundedAndExact(t *testing.T) {
	db := testDB(t)
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := anchorTopK + 4; i >= 0; i-- {
		insertInsight(t, db, fmt.Sprintf("gold-%02d", i), "Expiry is seven days", "prod", 3, []string{"ProjectVega"}, old)
		insertInsight(t, db, fmt.Sprintf("noise-%02d", i), "approved archive retrieval policy", "prod", 5, []string{"ProjectOrion"}, old.AddDate(6, 0, 0))
	}
	insertInsight(t, db, "near-name", "other entity evidence", "prod", 5, []string{"ProjectVegaPlus"}, old)
	query := "ProjectVega approved archive retrieval policy"
	for _, tc := range []struct {
		name     string
		entities []string
		wantGold int
	}{
		{"exact", []string{"ProjectVega"}, anchorTopK},
		{"case-folded", []string{"projectvega", "PROJECTVEGA"}, anchorTopK},
		{"no-entities", nil, 0},
		{"empty-entities", []string{""}, 0},
		{"partial-name", []string{"ProjectVeg"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response, err := IntentAwareRecall(db, query, nil, tc.entities, 100, nil)
			if err != nil {
				t.Fatal(err)
			}
			goldCount := 0
			for _, result := range response.Results {
				if result.Insight.ID == "near-name" {
					t.Error("partial entity name was admitted as an exact entity anchor")
				}
				if strings.HasPrefix(result.Insight.ID, "gold-") {
					goldCount++
					if result.Insight.ID >= fmt.Sprintf("gold-%02d", anchorTopK) {
						t.Errorf("entity top-k did not resolve its tied cutoff by ID: %s", result.Insight.ID)
					}
					if result.Signals.Entity != 1 || result.Via != "entity" {
						t.Errorf("entity-only anchor lost its signal: %+v", result)
					}
				}
			}
			if goldCount != tc.wantGold || response.Meta.AnchorCount != anchorTopK+tc.wantGold {
				t.Errorf("got %d entity matches and %d anchors; want %d and %d", goldCount, response.Meta.AnchorCount, tc.wantGold, anchorTopK+tc.wantGold)
			}
		})
	}
}

func TestRecallEntityAnchorsRespectScopeAndSupersession(t *testing.T) {
	db := testDB(t)
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	insertInsight(t, db, "old-fact", "Expiry is sixty days", "prod", 3, []string{"ProjectVega"}, old)
	insertInsight(t, db, "new-fact", "Expiry is seven days", "prod", 3, []string{"ProjectVega"}, old.AddDate(1, 0, 0))
	if err := db.InsertEdge(&model.Edge{SourceID: "new-fact", TargetID: "old-fact", EdgeType: model.EdgeSupersedes, Weight: 1, CreatedAt: old}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < anchorTopK+5; i++ {
		insertInsight(t, db, fmt.Sprintf("noise-%02d", i), "approved archive retrieval policy", "prod", 5, []string{"ProjectOrion"}, old.AddDate(6, 0, 0))
		insertInsight(t, db, fmt.Sprintf("excluded-%02d", i), "archived sample", "sandbox", 5, []string{"ProjectVega"}, old.AddDate(6, 0, 0))
	}
	response, err := IntentAwareRecallWithFilter(db, "ProjectVega approved archive retrieval policy", nil,
		[]string{"ProjectVega"}, 100, nil, RecallFilter{Category: "fact", Source: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]RecallResult)
	for _, result := range response.Results {
		byID[result.Insight.ID] = result
		if result.Insight.Source != "prod" {
			t.Errorf("entity anchor escaped the scope: %s", result.Insight.ID)
		}
	}
	stale, hasOld := byID["old-fact"]
	current, hasNew := byID["new-fact"]
	if !hasOld || !hasNew || !stale.Superseded || current.Superseded || stale.Score >= current.Score {
		t.Errorf("entity anchors must retain both facts and their explicit correction: old=%+v current=%+v", stale, current)
	}
}

func TestRecallEntitySignalCountsDistinctNonemptyMatches(t *testing.T) {
	db := testDB(t)
	insertInsight(t, db, "repeated", "expiry evidence", "prod", 3,
		[]string{"ProjectVega", "PROJECTVEGA", "ProjectVega", ""}, time.Now().UTC())
	response, err := IntentAwareRecall(db, "ProjectVega expiry", nil, []string{"ProjectVega", "projectvega", ""}, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results) != 1 || response.Results[0].Signals.Entity != 1 {
		t.Errorf("repeated aliases or empty entities inflated the bounded entity signal: %+v", response.Results)
	}
}
