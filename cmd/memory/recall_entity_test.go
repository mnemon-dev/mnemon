package memory

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"
)

func TestSmartRecallEntityAnchorSurvivesLongQuery(t *testing.T) {
	for _, entity := range []string{"ProjectVega", "项目维加"} {
		t.Run(entity, func(t *testing.T) {
			db := scopedRecallStore(t)
			insertTestInsight(t, db, "entity-gold", "Expiry is seven days", "prod", "2020-01-01T00:00:00Z")
			entities, err := json.Marshal([]string{entity})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Conn().Exec(`UPDATE insights SET entities=? WHERE id='entity-gold'`, string(entities)); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 30; i++ {
				id := fmt.Sprintf("noise-%02d", i)
				insertTestInsight(t, db, id, "approved archive retrieval policy for ProjectOrion", "prod", "2026-01-01T00:00:00Z")
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			recIntent = "ENTITY"
			query := "「" + entity + "」 approved archive retrieval policy"
			for _, mode := range []string{"compact", "verbose", "brief"} {
				recVerbose, recBrief = mode == "verbose", mode == "brief"
				if ids := scopedRecallIDs(t, query); !slices.Contains(ids, "entity-gold") {
					t.Errorf("%s dropped the exact entity before reranking: %v", mode, ids)
				}
			}
		})
	}
}
