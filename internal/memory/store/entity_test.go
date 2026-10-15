package store

import (
	"fmt"
	"reflect"
	"testing"
)

func TestKnownEntities_FrequencyGateParity(t *testing.T) {
	for _, tt := range []struct {
		name        string
		active      int
		occurrences int
		deleted     int
		wantWord    bool
	}{
		{"empty", 0, 0, 0, false},
		{"small store floor", 20, 20, 0, true},
		{"above floor", 30, 21, 0, false},
		{"share boundary", 3000, 30, 0, true},
		{"above share", 3000, 31, 0, false},
		{"fractional share", 2050, 21, 0, false},
		{"deleted occurrences ignored", 20, 20, 2, true},
		{"deleted denominator ignored", 30, 21, 3000, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Given active and deleted insights, including duplicate entity values.
			db := testDB(t)
			err := db.InTransaction(func() error {
				for n := 0; n < tt.active+tt.deleted; n++ {
					ins := makeInsight(fmt.Sprintf("seed-%d", n), "synthetic deployment note", 3)
					ins.Entities = nil
					if n < tt.occurrences || n >= tt.active {
						ins.Entities = []string{"make", "make", ""}
					}
					if n < 2 && n < tt.active {
						ins.Entities = append(ins.Entities, "zebra")
					}
					if err := db.InsertInsight(ins); err != nil {
						return fmt.Errorf("insert seed: %w", err)
					}
					if n >= tt.active {
						if err := db.SoftDeleteInsight(ins.ID); err != nil {
							return fmt.Errorf("delete seed: %w", err)
						}
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			active, err := db.GetAllActiveInsights()
			if err != nil {
				t.Fatal(err)
			}

			// When write and recall build their wide-cast admission sets.
			loaded, err := db.LoadKnownEntities()
			if err != nil {
				t.Fatal(err)
			}
			known := KnownEntities(active)

			// Then both sets match the independently specified admission policy.
			want := map[string]bool{}
			if tt.wantWord {
				want["make"] = true
			}
			if tt.active > 0 {
				want["zebra"] = true
			}
			for name, got := range map[string]map[string]bool{"SQL": loaded, "Go": known} {
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s entities = %v, want %v", name, got, want)
				}
			}
		})
	}
}
