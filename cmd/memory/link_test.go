package memory

import (
	"slices"
	"sort"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/memory/store"
)

// A supersedes edge asserts something about one of the two insights. Recorded
// both ways it marks the correction as superseded too, so recall demotes both
// and the stale insight keeps its lead over the correction -- the ordering the
// type exists to fix. Mutual types must still be recorded in both directions.
func TestLinkRecordsDirectedEdgeOnceAndMutualEdgesBothWays(t *testing.T) {
	oldDataDir, oldStoreName, oldReadOnly := dataDir, storeName, readOnly
	oldType, oldWeight, oldMeta := linkType, linkWeight, linkMeta
	t.Cleanup(func() {
		dataDir, storeName, readOnly = oldDataDir, oldStoreName, oldReadOnly
		linkType, linkWeight, linkMeta = oldType, oldWeight, oldMeta
	})
	dataDir, storeName, readOnly = t.TempDir(), "", false
	linkWeight, linkMeta = 0.5, ""

	db, err := store.Open(store.StoreDir(dataDir, store.DefaultStoreName))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	insertTestInsight(t, db, "fresh", "the correction", "test", "2026-01-02T00:00:00Z")
	insertTestInsight(t, db, "stale", "the corrected claim", "test", "2026-01-01T00:00:00Z")
	if err := db.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}

	cases := []struct {
		edgeType string
		want     []string // source id of every edge of this type
	}{
		{edgeType: "supersedes", want: []string{"fresh"}},
		{edgeType: "semantic", want: []string{"fresh", "stale"}},
	}
	for _, tc := range cases {
		t.Run(tc.edgeType, func(t *testing.T) {
			linkType = tc.edgeType
			captureStdout(t, func() {
				if err := linkCmd.RunE(linkCmd, []string{"fresh", "stale"}); err != nil {
					t.Fatalf("link RunE: %v", err)
				}
			})

			db, err := store.Open(store.StoreDir(dataDir, store.DefaultStoreName))
			if err != nil {
				t.Fatalf("reopen store: %v", err)
			}
			defer db.Close()
			edges, err := db.GetAllEdges()
			if err != nil {
				t.Fatalf("read edges: %v", err)
			}

			var got []string
			for _, e := range edges {
				if string(e.EdgeType) == tc.edgeType {
					got = append(got, e.SourceID)
				}
			}
			sort.Strings(got)
			if !slices.Equal(got, tc.want) {
				t.Errorf("%s edges by source = %v, want %v", tc.edgeType, got, tc.want)
			}
		})
	}
}
