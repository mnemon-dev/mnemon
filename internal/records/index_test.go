package records

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"sync"
	"testing"
)

func indexedRecord(id, text string, data map[string]any) Upsert {
	raw, _ := json.Marshal(map[string]any{"id": id, "kind": "note", "scope": "global", "state": "active", "version": 1, "title": id, "content": text, "data": data, "createdAt": "2026-10-19T00:00:00Z"})
	return Upsert{RecordJSON: string(raw), SearchText: text, VectorText: text}
}

func indexRequest(query string, policy *IndexPolicy) QueryRequest {
	input := map[string]json.RawMessage{"query": json.RawMessage(`""`), "match": json.RawMessage(`"words"`)}
	input["query"], _ = json.Marshal(query)
	return QueryRequest{Input: input, Index: policy}
}

func TestDerivedIndexIndependentRoutes(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	link := func(id, kind string) map[string]any {
		return map[string]any{"id": id, "version": 1, "type": kind, "weight": 1}
	}
	_, err := s.Put(ctx, PutRequest{Upsert: []Upsert{
		indexedRecord("entity", "unrelated body", map[string]any{"entities": []string{"Athena"}, "aliases": []string{"starmap"}}),
		indexedRecord("unicode", "Αθήνα πόλη", nil),
		indexedRecord("anchor", "Why outage happened", map[string]any{"links": []any{link("cause", "causal")}}),
		indexedRecord("cause", "Disk filled completely", nil),
		indexedRecord("dated", "release shipped", map[string]any{"eventAt": "2020-03-02T23:30:00-02:00"}),
		indexedRecord("old", "alpha endpoint", nil),
		indexedRecord("new", "beta endpoint", map[string]any{"mnemonSupersedes": map[string]any{"id": "old", "version": 1}}),
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, query, want string
		policy            IndexPolicy
	}{
		{"entities", "Athena", "entity", IndexPolicy{Version: 1, Entities: true}},
		{"alias", "starmap", "entity", IndexPolicy{Version: 1, Entities: true}},
		{"lexical", "Αθήνα", "unicode", IndexPolicy{Version: 1, Lexical: true}},
		{"relations", "Why outage", "cause", IndexPolicy{Version: 1, Relations: true}},
		{"supersedes", "alpha", "new", IndexPolicy{Version: 1, Supersedes: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			baseline, err := s.Query(ctx, indexRequest(tc.query, nil))
			if err != nil || slices.Contains(baseline.IDs, tc.want) {
				t.Fatalf("fixture must distinguish retrieval routes: %+v %v", baseline, err)
			}
			got, err := s.Query(ctx, indexRequest(tc.query, &tc.policy))
			if err != nil || !slices.Contains(got.IDs, tc.want) || got.Index == nil {
				t.Fatalf("missing %s: %+v %v", tc.want, got, err)
			}
		})
	}
	q := indexRequest("release", &IndexPolicy{Version: 1, Temporal: true})
	q.Input["since"], q.Input["until"] = json.RawMessage(`"2020-03-03"`), json.RawMessage(`"2020-03-03"`)
	got, err := s.Query(ctx, q)
	if err != nil || !reflect.DeepEqual(got.IDs, []string{"dated"}) {
		t.Fatal("event UTC date filter", got, err)
	}
	q.Input["until"] = json.RawMessage(`"2020-03-03T01:29:59Z"`)
	got, err = s.Query(ctx, q)
	if err != nil || len(got.IDs) != 0 {
		t.Fatal("timestamp upper bound", got, err)
	}
	q.Input["until"] = json.RawMessage(`"nonsense"`)
	if _, err = s.Query(ctx, q); err == nil {
		t.Fatal("invalid date accepted")
	}
}

func TestDerivedIndexHistoryScopeAndVersion(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	s.cache = newQueryCache()
	old := indexedRecord("old", "alpha", nil)
	newer := indexedRecord("new", "beta", map[string]any{"mnemonSupersedes": map[string]any{"id": "old", "version": 1}})
	secret := indexedRecord("private", "Athena", map[string]any{"entities": []string{"Athena"}})
	var secretValue map[string]any
	json.Unmarshal([]byte(secret.RecordJSON), &secretValue)
	secretValue["scope"], secretValue["workspaceId"] = "project", "/private"
	b, _ := json.Marshal(secretValue)
	secret.RecordJSON = string(b)
	first, err := s.Put(ctx, PutRequest{Upsert: []Upsert{old, newer, secret}})
	if err != nil {
		t.Fatal(err)
	}
	p := &IndexPolicy{Version: 1, Supersedes: true}
	q := indexRequest("alpha", p)
	got, err := s.Query(ctx, q)
	if err != nil || got.IDs[0] != "new" {
		t.Fatal("current correction", got, err)
	}
	q.Input["history"] = json.RawMessage(`true`)
	got, err = s.Query(ctx, q)
	if err != nil || got.IDs[0] != "old" {
		t.Fatal("history remains reachable", got, err)
	}
	changed := indexedRecord("old", "alpha changed", nil)
	var v map[string]any
	json.Unmarshal([]byte(changed.RecordJSON), &v)
	v["version"] = 2
	b, _ = json.Marshal(v)
	changed.RecordJSON = string(b)
	if _, err = s.Put(ctx, PutRequest{ExpectedRevision: &first, Upsert: []Upsert{changed}}); err != nil {
		t.Fatal(err)
	}
	delete(q.Input, "history")
	got, err = s.Query(ctx, q)
	if err != nil || slices.Contains(got.IDs, "new") {
		t.Fatal("stale target version admitted", got, err)
	}
	q.Revision = first
	got, err = s.Query(ctx, q)
	if err != nil || got.IDs[0] != "new" {
		t.Fatal("pinned history lost", got, err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			q := indexRequest("Athena", &IndexPolicy{Version: 1, Entities: true})
			if i%2 == 1 {
				w := "/private"
				q.Scope.WorkspaceID = &w
			}
			got, err := s.Query(ctx, q)
			if err != nil || slices.Contains(got.IDs, "private") != (i%2 == 1) {
				t.Errorf("scope cache leak %+v %v", got, err)
			}
		}(i)
	}
	wg.Wait()
	snapshot, _ := s.Get(ctx, GetRequest{Revision: first, IDs: []string{"old"}})
	if len(snapshot.Records) != 1 || snapshot.Records[0] != old.RecordJSON {
		t.Fatal("raw evidence mutated", snapshot)
	}
}

func TestIndexPolicyAndDefaultCompatibility(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	s.Put(ctx, PutRequest{Upsert: []Upsert{indexedRecord("x", "alpha", nil)}})
	base, _ := s.Query(ctx, indexRequest("alpha", nil))
	empty, _ := s.Query(ctx, indexRequest("alpha", &IndexPolicy{}))
	a, _ := json.Marshal(base)
	b, _ := json.Marshal(empty)
	if string(a) != string(b) || string(a) != `{"ids":["x"]}` {
		t.Fatal("default wire changed", string(a), string(b))
	}
	for _, raw := range []string{`{"index":{"version":1,"mystery":true}}`, `{"index":{"version":2,"entities":true}}`, `{"index":{"entities":true}}`} {
		var q QueryRequest
		err := json.Unmarshal([]byte(raw), &q)
		if err == nil {
			_, err = s.Query(ctx, q)
		}
		if err == nil {
			t.Fatal("invalid policy accepted", raw)
		}
	}
	q := indexRequest("alpha", &IndexPolicy{Version: 1, Lexical: true})
	q.CandidateLimit = 2001
	if _, err := s.Query(ctx, q); err == nil {
		t.Fatal("unbounded candidates accepted")
	}
}

func TestTemporalIndexKeepsRelevanceUnlessOrderIsExplicit(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, err := s.Put(ctx, PutRequest{Upsert: []Upsert{
		indexedRecord("a-old", "song song song", map[string]any{"eventAt": "2020-01-01"}),
		indexedRecord("z-new", "song", map[string]any{"eventAt": "2026-01-01"}),
	}})
	if err != nil {
		t.Fatal(err)
	}
	q := indexRequest("When song", nil)
	baseline, err := s.Query(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	q.Index = &IndexPolicy{Version: 1, Temporal: true}
	got, err := s.Query(ctx, q)
	if err != nil || !reflect.DeepEqual(got.IDs, baseline.IDs) {
		t.Fatal("WHEN intent silently displaced relevance", got, baseline, err)
	}
	q.Input["timeOrder"] = json.RawMessage(`"newest"`)
	got, err = s.Query(ctx, q)
	if err != nil || got.IDs[0] != "z-new" {
		t.Fatal("explicit newest order not honored", got, err)
	}
	q.Input["timeOrder"] = json.RawMessage(`"oldest"`)
	got, err = s.Query(ctx, q)
	if err != nil || got.IDs[0] != "a-old" {
		t.Fatal("explicit oldest order not honored", got, err)
	}
}

func TestAdvisoryLinksCannotBecomeCorrectionAuthority(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	old := indexedRecord("a-old", "alpha endpoint", nil)
	proposal := indexedRecord("z-proposal", "beta endpoint", map[string]any{"links": []Link{{ID: "a-old", Version: 1, Type: "supersedes", Weight: 1}}})
	if _, err := s.Put(ctx, PutRequest{Upsert: []Upsert{old, proposal}}); err != nil {
		t.Fatal(err)
	}
	q := indexRequest("alpha", &IndexPolicy{Version: 1, Supersedes: true})
	got, err := s.Query(ctx, q)
	if err != nil || got.IDs[0] != "a-old" {
		t.Fatal("advisory proposal demoted original evidence", got, err)
	}
	approved := indexedRecord("z-proposal", "beta endpoint", map[string]any{"mnemonSupersedes": map[string]any{"id": "a-old", "version": 1}})
	if _, err := s.Put(ctx, PutRequest{Upsert: []Upsert{approved}}); err != nil {
		t.Fatal(err)
	}
	got, err = s.Query(ctx, q)
	if err != nil || got.IDs[0] != "z-proposal" {
		t.Fatal("reserved correction claim was ignored", got, err)
	}
}
