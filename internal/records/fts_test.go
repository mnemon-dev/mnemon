package records

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestPersistentLexicalCandidateStatsAndHistory(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	first, err := s.Put(ctx, PutRequest{Upsert: []Upsert{
		sample("a", "dog dog auth-token local_cache read:scope 汉字 猫 ⺀𰀀 İskender WİTH"),
		sample("b", "dog cat auth-token 汉字 ⺀𰀀"), sample("c", "dog lake cat cat"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	queries := []QueryRequest{}
	for _, query := range []string{"dog cat", "auth-token local_cache read:scope", "汉字 猫 ⺀𰀀", "İskender WİTH"} {
		for _, filter := range []string{"", `,"ids":["a","c"]`} {
			var in map[string]json.RawMessage
			if err := json.Unmarshal([]byte(`{"query":`+strconvJSON(query)+`,"match":"words","expand":true`+filter+`}`), &in); err != nil {
				t.Fatal(err)
			}
			queries = append(queries, QueryRequest{Revision: first, Input: in})
		}
	}
	want := make([]QueryResult, len(queries))
	for i, q := range queries {
		want[i], err = s.Query(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
	}
	// Concurrent, resumable builders cannot duplicate postings or lose rows.
	other, err := Open(filepath.Dir(sPath(t, s)), false)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var wg sync.WaitGroup
	for _, store := range []*Store{s, other} {
		wg.Add(1)
		go func(store *Store) {
			defer wg.Done()
			if err := store.buildLexical(ctx); err != nil {
				t.Error(err)
			}
		}(store)
	}
	wg.Wait()
	check := func(store *Store) {
		t.Helper()
		for i, q := range queries {
			got, err := store.Query(ctx, q)
			if err != nil || !reflect.DeepEqual(got, want[i]) {
				t.Fatalf("query %d: got %+v want %+v: %v", i, got, want[i], err)
			}
		}
	}
	check(s)
	if _, err := s.Put(ctx, PutRequest{ExpectedRevision: &first, Upsert: []Upsert{sample("a", "lake new")}, Delete: []string{"b"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.buildLexical(ctx); err != nil {
		t.Fatal(err)
	}
	check(s) // Historical postings/stats remain exact after replacement/deletion.
	ro, err := Open(filepath.Dir(sPath(t, s)), true)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	check(ro) // A new process/store can reuse the durable index without writes.
	_, hash, err := revision(ctx, ro.db, first)
	if err != nil {
		t.Fatal(err)
	}
	docs, err := ro.queryRecords(ctx, 1, hash)
	if err != nil {
		t.Fatal(err)
	}
	if _, ready, err := ro.precomputedLexical(ctx, docs, "dog"); err != nil || !ready {
		t.Fatalf("reopened store did not reuse FTS: ready=%v %v", ready, err)
	}
}

func strconvJSON(text string) string { raw, _ := json.Marshal(text); return string(raw) }

func TestLongTokenKeepsExactLexicalFallback(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	long := strings.Repeat("a", 33000)
	rev, err := s.Put(ctx, PutRequest{Upsert: []Upsert{sample("a", long), sample("b", "short")}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.buildLexical(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := s.Query(ctx, QueryRequest{Revision: rev, Input: map[string]json.RawMessage{
		"query": json.RawMessage(strconvJSON(long)), "match": json.RawMessage(`"words"`),
	}})
	if err != nil || !reflect.DeepEqual(got.IDs, []string{"a"}) {
		t.Fatal(got, err)
	}
}
