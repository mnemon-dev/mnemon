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

func TestCatchUpLexicalIndexesBeforeTheNextQuery(t *testing.T) {
	ctx := context.Background()
	query := func(s *Store, rev string) []string {
		var in map[string]json.RawMessage
		if err := json.Unmarshal([]byte(`{"query":"rome flights","match":"bm25"}`), &in); err != nil {
			t.Fatal(err)
		}
		result, err := s.Query(ctx, QueryRequest{Revision: rev, Input: in})
		if err != nil {
			t.Fatal(err)
		}
		return result.IDs
	}
	upserts := []Upsert{sample("a", "the weather was nice"), sample("b", "rome rome flights booked"), sample("c", "a hotel in rome")}
	indexed := func(s *Store) int {
		var n int
		if err := s.readDB().QueryRowContext(ctx, "SELECT count(*) FROM lexical_documents").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// Without a query runtime there is no background build to catch up.
	plain := testStore(t)
	if _, err := plain.Put(ctx, PutRequest{Upsert: upserts}); err != nil {
		t.Fatal(err)
	}
	if err := plain.CatchUpLexical(ctx); err != nil || indexed(plain) != 0 {
		t.Fatal(err, indexed(plain))
	}

	// With one, every version is indexed before the next query, whether or
	// not the worker has run yet, and SQLite's bm25() ranks it.
	served := testStore(t)
	stop, err := served.StartRuntime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	rev, err := served.Put(ctx, PutRequest{Upsert: upserts})
	if err != nil {
		t.Fatal(err)
	}
	if err := served.CatchUpLexical(ctx); err != nil || indexed(served) != 3 {
		t.Fatal(err, indexed(served))
	}
	if got := query(served, rev); !slices.Equal(got, []string{"b", "c"}) {
		t.Fatal(got)
	}
}
