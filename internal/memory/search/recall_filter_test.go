package search

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mnemon-dev/mnemon/internal/memory/embed"
)

func TestRecallFilterScopesVectorCandidatesBeforeTopK(t *testing.T) {
	db := testDB(t)
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := old.AddDate(6, 0, 0)
	insertInsight(t, db, "vector-gold", "eligible older evidence", "prod", 3, nil, old)
	if err := db.UpdateEmbedding("vector-gold", embed.SerializeVector([]float64{0.5, 0.5})); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < anchorTopK+5; i++ {
		insertInsight(t, db, fmt.Sprintf("recent-%02d", i), "recent inventory", "prod", 3, nil, recent)
		id := fmt.Sprintf("excluded-%02d", i)
		insertInsight(t, db, id, "strong vector distractor", "sandbox", 5, nil, recent)
		if err := db.UpdateEmbedding(id, embed.SerializeVector([]float64{1, 0})); err != nil {
			t.Fatal(err)
		}
	}
	resp, err := IntentAwareRecallWithFilter(db, "unmatched query", []float64{1, 0}, nil, 100, nil, RecallFilter{Source: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, result := range resp.Results {
		if strings.HasPrefix(result.Insight.ID, "excluded-") || result.Insight.Source != "prod" {
			t.Errorf("vector anchor escaped scope: %s", result.Insight.ID)
		}
		found = found || result.Insight.ID == "vector-gold"
	}
	if !found {
		t.Fatal("eligible vector candidate was displaced before scope filtering")
	}
}
