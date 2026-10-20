package records

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
)

// The native Smart Recall routes: recency (native's fourth anchor signal)
// and traversal (native's beam search over the links).

func TestIndexIdentityKeepsExistingPolicies(t *testing.T) {
	// Captured before traversal and recency existed.
	for _, tc := range []struct {
		policy *IndexPolicy
		want   string
	}{
		{nil, "74234e98afe7498fb5daf1f36ac2d78acc339464f950703b8c019892f982b90b"},
		{&IndexPolicy{}, "44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a"},
		{&IndexPolicy{Version: 1, Lexical: true, Entities: true}, "9585000d3bf74d10e3960ac9325fd82fdb68d5c7e78554c5eec89c63db6a7b41"},
		{&IndexPolicy{Version: 1, Lexical: true, Entities: true, Relations: true, Temporal: true, Supersedes: true}, "4ed76b7eab41b5fcda7555873d254aefe43cf61b27f654e85b9184f573a3dfdf"},
	} {
		if got := tc.policy.Identity(); got != "records-index/v1/"+tc.want {
			t.Errorf("%+v: %s", tc.policy, got)
		}
	}
	seen := map[string]bool{}
	for _, p := range []*IndexPolicy{{Version: 1, Lexical: true}, {Version: 1, Lexical: true, Recency: true}, {Version: 1, Recency: true}, {Version: 1, Traversal: true}, {Version: 1, Recency: true, Traversal: true}} {
		if seen[p.Identity()] {
			t.Errorf("identity collision %+v", p)
		}
		seen[p.Identity()] = true
		if !p.Enabled() || p.Validate() != nil {
			t.Errorf("policy %+v not enabled or invalid", p)
		}
	}
	for _, p := range []*IndexPolicy{{Recency: true}, {Traversal: true}} {
		if p.Validate() == nil {
			t.Errorf("unversioned policy %+v accepted", p)
		}
	}
	var decoded QueryRequest
	if err := json.Unmarshal([]byte(`{"index":{"version":1,"traversal":true,"recency":true}}`), &decoded); err != nil || !decoded.Index.Traversal || !decoded.Index.Recency {
		t.Fatal(decoded.Index, err)
	}
}

// recencyStore holds 25 records whose index times are distinct but for one
// tie: eventAt first, then the record date, then createdAt.
func recencyStore(t *testing.T) *Store {
	t.Helper()
	upserts := []Upsert{}
	for i := 0; i < 21; i++ {
		upserts = append(upserts, indexedRecord(fmt.Sprintf("r%02d", i), fmt.Sprintf("plain note %d", i), map[string]any{"eventAt": fmt.Sprintf("2020-01-%02dT00:00:00Z", i+1)}))
	}
	// The record date stands in without an eventAt, createdAt without either.
	day := indexedRecord("day", "plain day note", nil)
	var v map[string]any
	json.Unmarshal([]byte(day.RecordJSON), &v)
	v["date"] = "2021-05-01"
	raw, _ := json.Marshal(v)
	day.RecordJSON = string(raw)
	created := indexedRecord("created", "plain created note", nil)
	json.Unmarshal([]byte(created.RecordJSON), &v)
	v["date"], v["createdAt"] = nil, "2019-01-01T00:00:00Z"
	raw, _ = json.Marshal(v)
	created.RecordJSON = string(raw)
	upserts = append(upserts, day, created,
		indexedRecord("tie-b", "plain tie note", map[string]any{"eventAt": "2020-06-01"}),
		indexedRecord("tie-a", "plain tie note", map[string]any{"eventAt": "2020-06-01T00:00:00Z"}))
	s := testStore(t)
	if _, err := s.Put(context.Background(), PutRequest{Upsert: upserts}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRecencyRouteOrdersNewestFirst(t *testing.T) {
	ctx := context.Background()
	s := recencyStore(t)
	// No record matches the words: the recency route alone ranks.
	q := indexRequest("zzz", &IndexPolicy{Version: 1, Recency: true})
	got, err := s.Query(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"day", "tie-a", "tie-b"}
	for i := 20; len(want) < 20; i-- {
		want = append(want, fmt.Sprintf("r%02d", i))
	}
	if !slices.Equal(got.IDs, want) || got.Index.Routes["recency"] != 20 || got.Index.Routes["base"] != 0 || got.Index.Truncated {
		t.Fatal(got.IDs, got.Index)
	}
	// Fused like the other seed lists: a word match leads, the newest follow.
	q = indexRequest("created", &IndexPolicy{Version: 1, Recency: true})
	got, err = s.Query(ctx, q)
	if err != nil || len(got.IDs) != 21 || !slices.Equal(got.IDs[:3], []string{"created", "day", "tie-a"}) {
		t.Fatal(got.IDs, err)
	}
	// A candidate limit below twenty bounds the route.
	q = indexRequest("zzz", &IndexPolicy{Version: 1, Recency: true})
	q.CandidateLimit = 5
	got, err = s.Query(ctx, q)
	if err != nil || !slices.Equal(got.IDs, want[:5]) || !got.Index.Truncated {
		t.Fatal(got.IDs, got.Index, err)
	}
	// Literal and explicit-id reads take no route.
	idRead := indexRequest("zzz", &IndexPolicy{Version: 1, Recency: true})
	idRead.Input["ids"] = json.RawMessage(`["r01"]`)
	for _, q := range []QueryRequest{indexRequest("", &IndexPolicy{Version: 1, Recency: true}), idRead} {
		got, err = s.Query(ctx, q)
		if _, ok := got.Index.Routes["recency"]; err != nil || ok {
			t.Fatal(got.Index, err)
		}
	}
	// Without the flag there is no route.
	got, err = s.Query(ctx, indexRequest("zzz", &IndexPolicy{Version: 1, Lexical: true}))
	if _, ok := got.Index.Routes["recency"]; err != nil || ok || len(got.IDs) != 0 {
		t.Fatal(got, err)
	}
}
