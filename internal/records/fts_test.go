package records

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
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
			// Feedback is opt-in; omitting it and explicitly disabling it keep
			// the same ranking, including filtered and historical snapshots.
			q.Input = maps.Clone(q.Input)
			for _, expand := range []json.RawMessage{nil, json.RawMessage("false")} {
				delete(q.Input, "expand")
				if expand != nil {
					q.Input["expand"] = expand
				}
				plain, err := store.Query(ctx, q)
				if err != nil || plain.Expansion != nil || !reflect.DeepEqual(plain.IDs, want[i].IDs) {
					t.Fatalf("query %d expand=%s: got %+v want IDs %v: %v", i, expand, plain, want[i].IDs, err)
				}
			}
		}
	}
	s.cache = newQueryCache()
	check(s)
	if _, err := s.Put(ctx, PutRequest{ExpectedRevision: &first, Upsert: []Upsert{sample("a", "lake new")}, Delete: []string{"b"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.buildLexical(ctx); err != nil {
		t.Fatal(err)
	}
	check(s) // Historical postings/stats remain exact after replacement/deletion.
	// Alternate historical and current reads without another write/build between
	// them: no prepared membership, aggregate df or borrowed view may cross revisions.
	current, err := s.Query(ctx, QueryRequest{Input: map[string]json.RawMessage{
		"query": json.RawMessage(`"lake"`), "match": json.RawMessage(`"words"`), "expand": json.RawMessage(`true`),
	}})
	if err != nil || !reflect.DeepEqual(current.IDs, []string{"a", "c"}) {
		t.Fatal(current, err)
	}
	check(s)
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

func TestPreparedStatisticsCancellationAndConcurrentQueries(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	hash, err := s.Put(ctx, PutRequest{Upsert: []Upsert{
		sample("a", "dog dog lake local_cache"), sample("b", "dog cat lake"), sample("c", "cat cat"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	q := QueryRequest{Revision: hash, Input: map[string]json.RawMessage{
		"query": json.RawMessage(`"dog cat"`), "match": json.RawMessage(`"words"`), "expand": json.RawMessage(`true`),
	}}
	want, err := s.Query(ctx, q) // Independent original tokenizer/statistics path.
	if err != nil {
		t.Fatal(err)
	}
	if err = s.buildLexical(ctx); err != nil {
		t.Fatal(err)
	}
	docs, err := s.queryRecords(ctx, 1, hash)
	if err != nil {
		t.Fatal(err)
	}
	v := newSelection(docs)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = s.selectionDF(canceled, v); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled preparation: %v", err)
	}
	df, err := s.selectionDF(ctx, v)
	if err != nil || df["dog"] != 2 || df["cat"] != 2 {
		t.Fatal(df, err)
	}
	s.cache = newQueryCache()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := s.Query(ctx, q)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Errorf("concurrent query: %+v %v", got, err)
			}
		}()
	}
	wg.Wait()
}

func TestPreparedLexicalRemainderUsesWholeCandidateStatistics(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	rows := make([]Upsert, 3000)
	for i := range rows {
		rows[i] = sample(fmt.Sprintf("%04d", i), "dog common lake "+strings.Repeat("cat ", i%50))
	}
	rows[0] = sample("0000", strings.Repeat("dog ", 100)+"rare_unique")
	rows[2999] = sample("2999", "tail_rare")
	hash, err := s.Put(ctx, PutRequest{Upsert: rows})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.buildLexical(ctx); err != nil {
		t.Fatal(err)
	}
	s.cache = newQueryCache()
	all, err := s.queryRecords(ctx, 1, hash)
	if err != nil {
		t.Fatal(err)
	}
	// A small preparation budget exercises both partitions without making the
	// deterministic unit suite build a 150,001-document fixture.
	s.cache.allSelection.prefixLimit = 32
	if _, err = s.selectionDF(ctx, s.cache.allSelection); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"dog", "common lake", "rare_unique tail_rare", "anythingabsent", "cat dog rare_unique"} {
		for _, filtered := range []bool{false, true} {
			input := map[string]json.RawMessage{"query": json.RawMessage(strconvJSON(query)), "match": json.RawMessage(`"words"`), "expand": json.RawMessage(`true`)}
			if filtered {
				input["ids"] = json.RawMessage(`["0000","2999","0042"]`)
			}
			req := QueryRequest{Revision: hash, Input: input}
			oracle := newLexical(candidates(all, req, "", true))
			ids := oracle.search(query)
			expansion := oracle.feedback(ids[:min(10, len(ids))], query)
			want := QueryResult{IDs: ids, Expansion: &expansion}
			got, err := s.Query(ctx, req)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("query=%q filtered=%v: got=%+v want=%+v %v", query, filtered, got, want, err)
			}
		}
	}
}
