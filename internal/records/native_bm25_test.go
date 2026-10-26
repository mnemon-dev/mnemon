package records

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
)

func TestNativeBM25RanksBySQLite(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	rev, err := s.Put(ctx, PutRequest{Upsert: []Upsert{
		sample("a", "the weather was nice"), sample("b", "rome rome flights booked"), sample("c", "a hotel in rome"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	query := func(match string) []string {
		var in map[string]json.RawMessage
		if err := json.Unmarshal([]byte(`{"query":"rome flights","match":"`+match+`"}`), &in); err != nil {
			t.Fatal(err)
		}
		result, err := s.Query(ctx, QueryRequest{Revision: rev, Input: in})
		if err != nil {
			t.Fatal(err)
		}
		return result.IDs
	}
	// Before the index covers the records, "bm25" ranks as "words" does.
	if got, want := query("bm25"), query("words"); !slices.Equal(got, want) {
		t.Fatal(got, want)
	}
	if err := s.buildLexical(ctx); err != nil {
		t.Fatal(err)
	}
	// With the index, SQLite ranks: both words before one; no word, no rank.
	if got := query("bm25"); !slices.Equal(got, []string{"b", "c"}) {
		t.Fatal(got)
	}
}
