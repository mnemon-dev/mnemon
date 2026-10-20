package records

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"
)

// placed is indexedRecord with a scope, workspace and session.
func placed(record Upsert, scope, workspace, session string) Upsert {
	var value map[string]any
	json.Unmarshal([]byte(record.RecordJSON), &value)
	value["scope"] = scope
	if workspace != "" {
		value["workspaceId"] = workspace
	}
	if session != "" {
		value["sessionId"] = session
	}
	raw, _ := json.Marshal(value)
	record.RecordJSON = string(raw)
	return record
}

func neighborLink(id, kind string, weight float64) map[string]any {
	return map[string]any{"id": id, "version": 1, "type": kind, "weight": weight}
}

func TestLinksAreTheDerivedIndexLinks(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	first, err := s.Put(ctx, PutRequest{Upsert: []Upsert{
		indexedRecord("a", "alpha", map[string]any{"links": []any{
			neighborLink("b", "semantic", 0.5), neighborLink("c", "temporal", 0.7), neighborLink("d", "causal", 1),
			neighborLink("e", "entity", 0.25), neighborLink("stale", "temporal", 1), neighborLink("hidden", "semantic", 1),
		}}),
		indexedRecord("b", "beta", nil),
		indexedRecord("c", "gamma", nil),
		indexedRecord("d", "delta", nil),
		indexedRecord("e", "epsilon", nil),
		indexedRecord("stale", "stale", nil),
		indexedRecord("old", "old claim", nil),
		indexedRecord("new", "new claim", map[string]any{"mnemonSupersedes": map[string]any{"id": "old", "version": 1}}),
		placed(indexedRecord("hidden", "private", nil), "project", "/private", ""),
	}})
	if err != nil {
		t.Fatal(err)
	}
	// Now "stale" is at version 2: the link names version 1.
	stale := indexedRecord("stale", "stale", nil)
	var value map[string]any
	json.Unmarshal([]byte(stale.RecordJSON), &value)
	value["version"] = 2
	raw, _ := json.Marshal(value)
	stale.RecordJSON = string(raw)
	if _, err = s.Put(ctx, PutRequest{Upsert: []Upsert{stale}}); err != nil {
		t.Fatal(err)
	}
	// The overlay adds a temporal link from c to d, and one from a to b.
	if err = s.ReplaceDerivedMetadata(ctx, "consolidation", map[string]DerivedMetadata{
		"c": {Version: 1, Links: []Link{{ID: "d", Version: 1, Type: "temporal", Weight: 0.9}}},
		"a": {Version: 1, Links: []Link{{ID: "b", Version: 1, Type: "semantic", Weight: 1}}},
	}); err != nil {
		t.Fatal(err)
	}
	want := map[string][]Link{
		// The writer's links, then the overlay's; the stale and the invisible
		// targets are left out.
		"a": {{ID: "b", Version: 1, Type: "semantic", Weight: 0.5}, {ID: "c", Version: 1, Type: "temporal", Weight: 0.7}, {ID: "d", Version: 1, Type: "causal", Weight: 1}, {ID: "e", Version: 1, Type: "entity", Weight: 0.25}, {ID: "b", Version: 1, Type: "semantic", Weight: 1}},
		// Semantic and entity links are mutual; temporal and causal go the
		// way they were written.
		"b": {{ID: "a", Version: 1, Type: "semantic", Weight: 0.5}, {ID: "a", Version: 1, Type: "semantic", Weight: 1}},
		"c": {{ID: "d", Version: 1, Type: "temporal", Weight: 0.9}},
		"e": {{ID: "a", Version: 1, Type: "entity", Weight: 0.25}},
		// A correction links both ways.
		"new": {{ID: "old", Version: 1, Type: "supersedes", Weight: 1}},
		"old": {{ID: "new", Version: 1, Type: "supersedes", Weight: 1}},
	}
	ids := []string{"a", "b", "c", "d", "e", "old", "new", "missing"}
	for _, cached := range []bool{false, true} {
		if cached {
			s.cache = newQueryCache()
		}
		got, err := s.Links(ctx, "", Scope{}, ids)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("cached %v: %+v %v", cached, got, err)
		}
	}
	// Even the scope that sees the private record follows no link across
	// scopes; a pinned revision reads the links of then, with the current
	// overlay.
	private := "/private"
	got, err := s.Links(ctx, "", Scope{WorkspaceID: &private}, []string{"hidden", "a"})
	if err != nil || len(got) != 1 || len(got["a"]) != 5 {
		t.Fatal(got, err)
	}
	got, err = s.Links(ctx, first, Scope{}, []string{"a"})
	if err != nil || len(got["a"]) != 6 || got["a"][4] != (Link{ID: "stale", Version: 1, Type: "temporal", Weight: 1}) {
		t.Fatal(got, err)
	}
	if _, err = s.Links(ctx, "nonsense", Scope{}, []string{"a"}); err == nil {
		t.Fatal("unknown revision accepted")
	}
}

func TestAdjacentFollowsSessionsInStoreOrder(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	day := func(id, eventAt string) Upsert {
		return indexedRecord(id, id, map[string]any{"eventAt": eventAt})
	}
	chunk := func(id, document string) Upsert {
		return indexedRecord(id, id, map[string]any{"eventAt": "2023-05-08T18:00:00Z", "documentID": document})
	}
	dated := func(id, date string) Upsert {
		var value map[string]any
		record := indexedRecord(id, id, nil)
		json.Unmarshal([]byte(record.RecordJSON), &value)
		value["date"] = date
		raw, _ := json.Marshal(value)
		record.RecordJSON = string(raw)
		return record
	}
	_, err := s.Put(ctx, PutRequest{Upsert: []Upsert{
		// Two sessions written interleaved: ids sort apart from store order.
		day("z1", "2023-05-08T13:56:00Z"),
		day("b1", "2023-05-09T10:00:00Z"),
		day("m2", "2023-05-08T14:10:00Z"),
		chunk("doc-0", "doc"),
		day("a3", "2023-05-08T23:59:00Z"),
		day("b2", "2023-05-09T11:00:00Z"),
		chunk("doc-1", "doc"),
		// Another place on the same day; a session-scoped record.
		placed(day("p1", "2023-05-08T15:00:00Z"), "project", "/w", ""),
		placed(day("s1", "2023-05-08T15:00:00Z"), "session", "", "chat"),
		// A day given by the record's date; an undated record.
		dated("d1", "2023-05-08"),
		indexedRecord("undated", "undated", nil),
		// An offset keeps the writer's own day: 2023-05-08 there.
		day("tz", "2023-05-08T23:30:00-02:00"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{"z1", "m2", "a3", "b1", "b2", "doc-0", "doc-1", "p1", "s1", "d1", "undated", "tz", "missing"}
	got, err := nearbyIDs(s.Adjacent(ctx, "", Scope{}, ids, 1))
	want := map[string][]string{
		"z1": {"m2"}, "m2": {"z1", "a3"}, "a3": {"m2", "d1"}, "d1": {"a3", "tz"}, "tz": {"d1"},
		"b1": {"b2"}, "b2": {"b1"},
		"doc-0": {"doc-1"}, "doc-1": {"doc-0"},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, err)
	}
	// The scope that sees them: a project record has its own sessions.
	w, chat := "/w", "chat"
	got, err = nearbyIDs(s.Adjacent(ctx, "", Scope{WorkspaceID: &w, SessionID: &chat}, []string{"p1", "s1"}, 1))
	if err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
	if _, err = s.Put(ctx, PutRequest{Upsert: []Upsert{placed(day("p2", "2023-05-08T16:00:00Z"), "project", "/w", "")}}); err != nil {
		t.Fatal(err)
	}
	got, err = nearbyIDs(s.Adjacent(ctx, "", Scope{WorkspaceID: &w}, []string{"p1", "p2", "z1"}, 1))
	if err != nil || !reflect.DeepEqual(got, map[string][]string{"p1": {"p2"}, "p2": {"p1"}, "z1": {"m2"}}) {
		t.Fatal(got, err)
	}
	// A wider window: the k nearest on each side, nearest first, earlier
	// before later, within the session (z1 m2 a3 d1 tz on 2023-05-08).
	wide, err := s.Adjacent(ctx, "", Scope{}, []string{"a3", "z1", "tz", "b1"}, 2)
	if err != nil || !reflect.DeepEqual(wide, map[string][]Nearby{
		"a3": {{"m2", -1}, {"d1", 1}, {"z1", -2}, {"tz", 2}},
		"z1": {{"m2", 1}, {"a3", 2}},
		"tz": {{"d1", -1}, {"a3", -2}},
		"b1": {{"b2", 1}},
	}) {
		t.Fatal(wide, err)
	}
	if wide, err = s.Adjacent(ctx, "", Scope{}, []string{"z1"}, 5); err != nil || !reflect.DeepEqual(wide["z1"], []Nearby{{"m2", 1}, {"a3", 2}, {"d1", 3}, {"tz", 4}}) {
		t.Fatal(wide, err)
	}
	if _, err = s.Adjacent(ctx, "", Scope{}, ids, 0); err == nil {
		t.Fatal("empty window accepted")
	}
	if _, err = s.Adjacent(ctx, "nonsense", Scope{}, ids, 1); err == nil {
		t.Fatal("unknown revision accepted")
	}
}

// nearbyIDs is Adjacent's ids, offsets left out.
func nearbyIDs(near map[string][]Nearby, err error) (map[string][]string, error) {
	out := map[string][]string{}
	for id, list := range near {
		for _, n := range list {
			out[id] = append(out[id], n.ID)
		}
	}
	return out, err
}

// Concurrent readers of the cached index never write into its slices: x has
// three links (a slice with room for a fourth) and a correction.
func TestLinksConcurrentReadersShareTheIndex(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	s.cache = newQueryCache()
	_, err := s.Put(ctx, PutRequest{Upsert: []Upsert{
		indexedRecord("x", "x", map[string]any{"links": []any{neighborLink("p", "temporal", 1), neighborLink("q", "temporal", 1), neighborLink("r", "temporal", 1)}}),
		indexedRecord("p", "p", nil), indexedRecord("q", "q", nil), indexedRecord("r", "r", nil),
		indexedRecord("y", "y", map[string]any{"mnemonSupersedes": map[string]any{"id": "x", "version": 1}}),
	}})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := s.Links(ctx, "", Scope{}, []string{"x"})
			if err != nil || len(got["x"]) != 4 || got["x"][3] != (Link{ID: "y", Version: 1, Type: "supersedes", Weight: 1}) {
				t.Error(got, err)
			}
		}()
	}
	wg.Wait()
}

// A conversation's session (data.session) spans its documents: the memory
// service writes each Add as its own document of one session. Sessions are
// cached per revision and scope.
func TestAdjacentSessionsSpanDocumentsAndAreCached(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	chunk := func(id, document, session string) Upsert {
		data := map[string]any{"eventAt": "2023-05-08T13:00:00Z", "documentID": document}
		if session != "" {
			data["session"] = session
		}
		return indexedRecord(id, id, data)
	}
	first, err := s.Put(ctx, PutRequest{Upsert: []Upsert{
		chunk("add0-0", "add0", "chat-1"), chunk("add0-1", "add0", "chat-1"),
		chunk("other-0", "other", "chat-2"),
		chunk("add1-0", "add1", "chat-1"),
		chunk("plain-0", "plain", ""), chunk("plain-1", "plain", ""),
	}})
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{"add0-0", "add0-1", "add1-0", "other-0", "plain-0"}
	got, err := nearbyIDs(s.Adjacent(ctx, "", Scope{}, ids, 1))
	want := map[string][]string{"add0-0": {"add0-1"}, "add0-1": {"add0-0", "add1-0"}, "add1-0": {"add0-1"}, "plain-0": {"plain-1"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, err)
	}
	// A later revision has its own sessions; the earlier one keeps its.
	if _, err = s.Put(ctx, PutRequest{Upsert: []Upsert{chunk("add2-0", "add2", "chat-1")}}); err != nil {
		t.Fatal(err)
	}
	if got, err = nearbyIDs(s.Adjacent(ctx, "", Scope{}, []string{"add1-0"}, 1)); err != nil || !reflect.DeepEqual(got["add1-0"], []string{"add0-1", "add2-0"}) {
		t.Fatal(got, err)
	}
	if got, err = nearbyIDs(s.Adjacent(ctx, first, Scope{}, []string{"add1-0"}, 1)); err != nil || !reflect.DeepEqual(got["add1-0"], []string{"add0-1"}) {
		t.Fatal(got, err)
	}
	// Each revision and scope is built once, then served from the cache;
	// concurrent reads agree.
	builds := s.sessionBuilds.Load()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got, err := nearbyIDs(s.Adjacent(ctx, "", Scope{}, []string{"add1-0"}, 1)); err != nil || !reflect.DeepEqual(got["add1-0"], []string{"add0-1", "add2-0"}) {
				t.Error(got, err)
			}
		}()
	}
	wg.Wait()
	if s.sessionBuilds.Load() != builds {
		t.Fatal("the cached revision was rebuilt", s.sessionBuilds.Load(), builds)
	}
	w := "/w"
	if _, err = s.Adjacent(ctx, "", Scope{WorkspaceID: &w}, []string{"add1-0"}, 1); err != nil || s.sessionBuilds.Load() != builds+1 {
		t.Fatal("another scope shares an entry", err, s.sessionBuilds.Load(), builds)
	}
}
