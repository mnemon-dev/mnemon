package graph

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/mnemon-dev/mnemon/internal/memory/store"
)

func TestExtractEntitiesIndexed_HotWordAdmission(t *testing.T) {
	// Given a polluted index and a rare lowercase project name.
	db := testDB(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for n := 0; n < 30; n++ {
		entities := []string{"make", "Open"}
		if n < 2 {
			entities = append(entities, "zebra")
		}
		insertInsight(t, db, fmt.Sprintf("seed-%d", n), "synthetic deployment note", "test", 3, entities, now)
	}
	all, err := db.GetAllActiveInsights()
	if err != nil {
		t.Fatal(err)
	}
	known := store.KnownEntities(all)

	for _, tt := range []struct {
		name  string
		query string
	}{
		{"lowercase", "can you make zebra better"},
		{"capitalized", "Open zebra deployment"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// When recall extracts entities from its active-insight snapshot.
			got := ExtractEntitiesIndexed(tt.query, known)

			// Then only the rare project contributes an entity signal.
			if !reflect.DeepEqual(got, []string{"zebra"}) {
				t.Errorf("query entities = %v, want [zebra]", got)
			}
		})
	}
}

func TestEngine_HotWordAdmission(t *testing.T) {
	for _, tt := range []struct {
		name     string
		provided []string
		want     []string
	}{
		{"automatic", nil, []string{"Go", "API", "zebra"}},
		{"explicit", []string{"make"}, []string{"make", "Go", "API", "zebra"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Given hot ordinary, dictionary, and regex entities.
			db := testDB(t)
			now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			for n := 0; n < 30; n++ {
				entities := []string{"make", "Go", "API"}
				if n < 2 {
					entities = append(entities, "zebra")
				}
				insertInsight(t, db, fmt.Sprintf("seed-%d", n), "synthetic deployment note", "test", 3, entities, now)
			}
			engine := NewEngineWithOptions(db, nil, EngineOptions{TemporalMode: TemporalDisabled})

			// When a write resolves and persists its extracted entities.
			err := db.InTransaction(func() error {
				ins := insertInsight(t, db, "new", "make zebra use Go API", "test", 3, tt.provided, now)
				engine.OnInsightCreated(ins)
				return db.UpdateEntities(ins.ID, ins.Entities)
			})
			if err != nil {
				t.Fatal(err)
			}

			// Then hot words require explicit provision, while strict paths survive.
			got, err := db.GetInsightByID("new")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(toSet(got.Entities), toSet(tt.want)) {
				t.Errorf("stored entities = %v, want %v", got.Entities, tt.want)
			}
		})
	}
}
