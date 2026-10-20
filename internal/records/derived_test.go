package records

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// versioned is indexedRecord at the given record version.
func versioned(id, text string, version int, data map[string]any) Upsert {
	u := indexedRecord(id, text, data)
	var v map[string]any
	json.Unmarshal([]byte(u.RecordJSON), &v)
	v["version"] = version
	raw, _ := json.Marshal(v)
	u.RecordJSON = string(raw)
	return u
}

// mergedMetadata is the index metadata of every current record, overlay
// rows included.
func mergedMetadata(t *testing.T, s *Store) map[string]indexMetadata {
	t.Helper()
	ctx := context.Background()
	rev, hash, err := revision(ctx, s.readDB(), "")
	if err != nil {
		t.Fatal(err)
	}
	all, err := s.queryRecords(ctx, rev, hash)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := derivedGeneration(ctx, s.readDB())
	if err != nil {
		t.Fatal(err)
	}
	meta, seen, err := s.indexMetadata(ctx, rev, all, generation)
	if err != nil || seen != generation {
		t.Fatal(seen, generation, err)
	}
	return meta
}

func TestDerivedMetadataMerge(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	writerLink := map[string]any{"id": "cause", "version": 1, "type": "causal", "weight": 1}
	_, err := s.Put(ctx, PutRequest{Upsert: []Upsert{
		indexedRecord("writer", "owl notes", map[string]any{"entities": []string{"Athena"}, "aliases": []string{"owl"}, "links": []any{writerLink}, "eventAt": "2020-01-01"}),
		indexedRecord("plain", "plain notes", nil),
		indexedRecord("cause", "cause notes", nil),
		indexedRecord("target", "target notes", nil),
	}})
	if err != nil {
		t.Fatal(err)
	}
	err = s.ReplaceDerivedMetadata(ctx, "consolidation", map[string]DerivedMetadata{
		"writer": {Version: 1, Entities: []string{"Athena", "Pallas"}, Aliases: []string{"Minerva", "owl"},
			Links: []Link{{ID: "target", Version: 1, Type: "supersedes", Weight: 0.6}}, EventAt: "2021-05-05"},
		"plain": {Version: 1, EventAt: "2019-02-02", Entities: []string{"Plato"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// A second source merges after the first, by source name.
	if err = s.ReplaceDerivedMetadata(ctx, "zeta", map[string]DerivedMetadata{"plain": {Version: 1, Entities: []string{"Zeno", "Plato"}, EventAt: "2018-01-01"}}); err != nil {
		t.Fatal(err)
	}
	meta := mergedMetadata(t, s)
	// The writer's names stay as they are; the overlay's are kept apart,
	// for the index to take after them.
	w := meta["writer"]
	if !slices.Equal(w.Entities, []string{"Athena"}) || !slices.Equal(w.Aliases, []string{"owl"}) || w.EventAt != "2020-01-01" ||
		!slices.Equal(w.derivedEntities, []string{"Athena", "Pallas"}) || !slices.Equal(w.derivedAliases, []string{"Minerva", "owl"}) {
		t.Fatalf("writer metadata %+v", w)
	}
	// Overlay links follow the writer's and never carry authority, even when
	// typed supersedes.
	if len(w.Links) != 2 || w.Links[0].ID != "cause" || w.Links[1] != (Link{ID: "target", Version: 1, Type: "supersedes", Weight: 0.6}) {
		t.Fatalf("links %+v", w.Links)
	}
	p := meta["plain"]
	if p.Entities != nil || !slices.Equal(p.derivedEntities, []string{"Plato", "Zeno"}) || p.EventAt != "2019-02-02" || p.derivedAliases != nil || p.Links != nil {
		t.Fatalf("plain metadata %+v", p)
	}
	if c := meta["cause"]; c.Entities != nil || c.EventAt != "" || c.Version != 1 {
		t.Fatalf("cause metadata %+v", c)
	}

	// Through the routes: the overlay's entities and aliases are found, its
	// eventAt dates a record without one, and its link expands.
	for _, tc := range []struct {
		query, want string
		policy      IndexPolicy
	}{
		{"Pallas", "writer", IndexPolicy{Version: 1, Entities: true}},
		{"Minerva", "writer", IndexPolicy{Version: 1, Entities: true}},
		{"Zeno", "plain", IndexPolicy{Version: 1, Entities: true}},
		{"target", "writer", IndexPolicy{Version: 1, Supersedes: true}},
	} {
		got, err := s.Query(ctx, indexRequest(tc.query, &tc.policy))
		if err != nil || !slices.Contains(got.IDs, tc.want) {
			t.Fatalf("%s: %+v %v", tc.query, got, err)
		}
	}
	dated := func(day string) []string {
		q := indexRequest("notes", &IndexPolicy{Version: 1, Temporal: true})
		q.Input["since"], q.Input["until"] = json.RawMessage(`"`+day+`"`), json.RawMessage(`"`+day+`"`)
		got, err := s.Query(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		return got.IDs
	}
	if got := dated("2019-02-02"); !slices.Equal(got, []string{"plain"}) {
		t.Fatal("overlay eventAt", got)
	}
	if got := dated("2021-05-05"); len(got) != 0 {
		t.Fatal("overlay eventAt replaced the writer's", got)
	}
	if got := dated("2020-01-01"); !slices.Equal(got, []string{"writer"}) {
		t.Fatal("writer eventAt", got)
	}
}

func TestDerivedSupersedesExpandsWithoutDemoting(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	if _, err := s.Put(ctx, PutRequest{Upsert: []Upsert{indexedRecord("a-old", "alpha endpoint", nil), indexedRecord("z-new", "beta endpoint", nil)}}); err != nil {
		t.Fatal(err)
	}
	err := s.ReplaceDerivedMetadata(ctx, "consolidation", map[string]DerivedMetadata{"z-new": {Version: 1, Links: []Link{{ID: "a-old", Version: 1, Type: "supersedes", Weight: 1}}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Query(ctx, indexRequest("alpha", &IndexPolicy{Version: 1, Supersedes: true}))
	if err != nil || !slices.Equal(got.IDs, []string{"a-old", "z-new"}) || got.Index.Routes["supersedes"] != 1 {
		t.Fatal("derived supersedes must expand and never demote", got, err)
	}
}

func TestDerivedMetadataBindsRecordVersion(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	if _, err := s.Put(ctx, PutRequest{Upsert: []Upsert{indexedRecord("x", "xylophone", nil), indexedRecord("y", "yodel", nil), indexedRecord("t", "tuba", nil)}}); err != nil {
		t.Fatal(err)
	}
	err := s.ReplaceDerivedMetadata(ctx, "consolidation", map[string]DerivedMetadata{
		"x": {Version: 1, Entities: []string{"Pallas"}},
		"y": {Version: 2, Entities: []string{"Hermes"}},
		"t": {Version: 1, Links: []Link{{ID: "x", Version: 1, Type: "entity", Weight: 0.6}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	entities := func(query string) []string {
		got, err := s.Query(ctx, indexRequest(query, &IndexPolicy{Version: 1, Entities: true}))
		if err != nil {
			t.Fatal(err)
		}
		return got.IDs
	}
	related := func() []string {
		got, err := s.Query(ctx, indexRequest("tuba", &IndexPolicy{Version: 1, Relations: true}))
		if err != nil {
			t.Fatal(err)
		}
		return got.IDs
	}
	if !slices.Contains(entities("Pallas"), "x") || slices.Contains(entities("Hermes"), "y") || !slices.Contains(related(), "x") {
		t.Fatal("a row applies only at its record's version")
	}
	first, _ := s.Get(ctx, GetRequest{IDs: []string{}})
	if _, err = s.Put(ctx, PutRequest{Upsert: []Upsert{versioned("x", "xylophone changed", 2, nil)}}); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(entities("Pallas"), "x") || slices.Contains(related(), "x") {
		t.Fatal("a changed record kept its stale row or link")
	}
	// The stale row applies again to a read of the revision it described.
	q := indexRequest("Pallas", &IndexPolicy{Version: 1, Entities: true})
	q.Revision = first.Revision
	got, err := s.Query(ctx, q)
	if err != nil || !slices.Contains(got.IDs, "x") {
		t.Fatal("pinned revision", got, err)
	}
}

func TestDerivedMetadataInvalidatesCaches(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	stop, err := s.StartRuntime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if _, err := s.Put(ctx, PutRequest{Upsert: []Upsert{indexedRecord("x", "x notes", nil), indexedRecord("old", "x older notes", nil)}}); err != nil {
		t.Fatal(err)
	}
	query := func(text string, policy IndexPolicy) []string {
		got, err := s.Query(ctx, indexRequest(text, &policy))
		if err != nil {
			t.Fatal(err)
		}
		return got.IDs
	}
	entities := IndexPolicy{Version: 1, Entities: true}
	if slices.Contains(query("Pallas", entities), "x") {
		t.Fatal("entity before any overlay")
	}
	for i, tc := range []struct {
		items map[string]DerivedMetadata
		want  bool
	}{
		{map[string]DerivedMetadata{"x": {Version: 1, Entities: []string{"Pallas"}}}, true},
		{map[string]DerivedMetadata{}, false},
		{map[string]DerivedMetadata{"x": {Version: 1, Entities: []string{"Pallas"}}}, true},
	} {
		if err := s.ReplaceDerivedMetadata(ctx, "consolidation", tc.items); err != nil {
			t.Fatal(err)
		}
		// Twice: the second is served from the caches.
		for range 2 {
			if got := query("Pallas", entities); slices.Contains(got, "x") != tc.want {
				t.Fatal("stale overlay served", i, got)
			}
		}
	}
	// The selection a temporal policy builds is cached under the overlay too.
	temporal := IndexPolicy{Version: 1, Temporal: true}
	day := func() []string {
		q := indexRequest("notes", &temporal)
		q.Input["since"], q.Input["until"] = json.RawMessage(`"2001-01-01"`), json.RawMessage(`"2001-01-01"`)
		got, err := s.Query(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		return got.IDs
	}
	if got := day(); len(got) != 0 {
		t.Fatal(got)
	}
	if err := s.ReplaceDerivedMetadata(ctx, "consolidation", map[string]DerivedMetadata{"old": {Version: 1, EventAt: "2001-01-01"}}); err != nil {
		t.Fatal(err)
	}
	if got := day(); !slices.Equal(got, []string{"old"}) {
		t.Fatal("temporal selection served a stale overlay", got)
	}
}

func TestDerivedMetadataReadonlyAndSchema(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, PutRequest{Upsert: []Upsert{indexedRecord("x", "x notes", nil)}}); err != nil {
		t.Fatal(err)
	}
	// A store from before the overlay: no table and no generation.
	if _, err := s.db.Exec(`DROP TABLE derived_metadata; DELETE FROM meta WHERE key='derived'`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	ro, err := Open(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ro.Query(ctx, indexRequest("x", &IndexPolicy{Version: 1, Entities: true, Relations: true, Supersedes: true}))
	if err != nil || !slices.Equal(got.IDs, []string{"x"}) {
		t.Fatal("readonly store without the overlay table", got, err)
	}
	if err := ro.ReplaceDerivedMetadata(ctx, "consolidation", map[string]DerivedMetadata{}); err == nil || !strings.Contains(err.Error(), "readonly") {
		t.Fatal("readonly store accepted derived metadata", err)
	}
	ro.Close()
	// Opening it for writing adds the table.
	s, err = Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReplaceDerivedMetadata(ctx, "consolidation", map[string]DerivedMetadata{"x": {Version: 1, Entities: []string{"Pallas"}}}); err != nil {
		t.Fatal(err)
	}
	ro, err = Open(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	got, err = ro.Query(ctx, indexRequest("Pallas", &IndexPolicy{Version: 1, Entities: true}))
	if err != nil || !slices.Contains(got.IDs, "x") {
		t.Fatal("readonly store does not read the overlay", got, err)
	}
}

func TestDerivedMetadataBounds(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	if _, err := s.Put(ctx, PutRequest{Upsert: []Upsert{indexedRecord("x", "x notes", nil)}}); err != nil {
		t.Fatal(err)
	}
	names := func(n int) []string {
		out := []string{}
		for i := range n {
			out = append(out, fmt.Sprintf("N%d", i))
		}
		return out
	}
	links := func(n int) []Link {
		out := []Link{}
		for i := range n {
			out = append(out, Link{ID: fmt.Sprintf("t%d", i), Version: 1, Type: "entity", Weight: 0.5})
		}
		return out
	}
	good := DerivedMetadata{Version: 1, Entities: names(64), Aliases: names(64), Links: links(50), EventAt: "2020-01-02T03:04:05Z"}
	if err := s.ReplaceDerivedMetadata(ctx, "consolidation", map[string]DerivedMetadata{"x": good}); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string]func(*DerivedMetadata){
		"entities":     func(m *DerivedMetadata) { m.Entities = names(65) },
		"aliases":      func(m *DerivedMetadata) { m.Aliases = names(65) },
		"links":        func(m *DerivedMetadata) { m.Links = links(51) },
		"zero weight":  func(m *DerivedMetadata) { m.Links[3].Weight = 0 },
		"weight":       func(m *DerivedMetadata) { m.Links[3].Weight = 1.01 },
		"type":         func(m *DerivedMetadata) { m.Links[3].Type = "mystery" },
		"link version": func(m *DerivedMetadata) { m.Links[3].Version = 0 },
		"link id":      func(m *DerivedMetadata) { m.Links[3].ID = "" },
		"version":      func(m *DerivedMetadata) { m.Version = 0 },
		"eventAt":      func(m *DerivedMetadata) { m.EventAt = "yesterday" },
		"empty name":   func(m *DerivedMetadata) { m.Entities[0] = "" },
	} {
		m := good
		m.Entities, m.Aliases, m.Links = slices.Clone(good.Entities), slices.Clone(good.Aliases), slices.Clone(good.Links)
		bad(&m)
		if err := s.ReplaceDerivedMetadata(ctx, "consolidation", map[string]DerivedMetadata{"x": m}); err == nil {
			t.Fatal("accepted", name)
		}
	}
	for _, source := range []string{"", strings.Repeat("s", 65)} {
		if err := s.ReplaceDerivedMetadata(ctx, source, map[string]DerivedMetadata{}); err == nil {
			t.Fatalf("accepted source %q", source)
		}
	}
	if err := s.ReplaceDerivedMetadata(ctx, "consolidation", map[string]DerivedMetadata{"": {Version: 1}}); err == nil {
		t.Fatal("accepted an empty id")
	}
	// A refused replace changed nothing.
	if m := mergedMetadata(t, s)["x"]; len(m.derivedEntities) != 64 || len(m.Links) != 50 {
		t.Fatal("refused replace changed the overlay", len(m.derivedEntities), len(m.Links))
	}
}

func TestNoDerivedRowsKeepsResultsByteIdentical(t *testing.T) {
	ctx := context.Background()
	link := func(id, kind string) map[string]any {
		return map[string]any{"id": id, "version": 1, "type": kind, "weight": 1}
	}
	upserts := []Upsert{
		indexedRecord("entity", "unrelated body", map[string]any{"entities": []string{"Athena"}, "aliases": []string{"starmap"}}),
		indexedRecord("anchor", "Why outage happened", map[string]any{"links": []any{link("cause", "causal"), link("entity", "entity")}}),
		indexedRecord("cause", "Disk filled completely", nil),
		indexedRecord("dated", "release shipped", map[string]any{"eventAt": "2020-03-02T23:30:00-02:00"}),
		indexedRecord("old", "alpha endpoint", nil),
		indexedRecord("new", "beta endpoint alpha", map[string]any{"mnemonSupersedes": map[string]any{"id": "old", "version": 1}}),
	}
	queries := []QueryRequest{}
	for _, text := range []string{"Athena", "starmap", "Why outage", "alpha", "release", "When release", ""} {
		for _, policy := range []*IndexPolicy{nil, {Version: 1, Lexical: true}, {Version: 1, Entities: true}, {Version: 1, Relations: true}, {Version: 1, Supersedes: true}, {Version: 1, Temporal: true},
			{Version: 1, Lexical: true, Entities: true, Relations: true, Temporal: true, Supersedes: true}} {
			queries = append(queries, indexRequest(text, policy))
			q := indexRequest(text, policy)
			q.Input["since"], q.Input["timeOrder"] = json.RawMessage(`"2020-01-01"`), json.RawMessage(`"newest"`)
			if policy == nil {
				delete(q.Input, "timeOrder")
			}
			queries = append(queries, q)
		}
	}
	results := func(s *Store) []string {
		out := []string{}
		for _, q := range queries {
			got, err := s.Query(ctx, q)
			raw, _ := json.Marshal(got)
			out = append(out, fmt.Sprint(string(raw), err))
		}
		return out
	}
	plain, overlaid := testStore(t), testStore(t)
	for _, s := range []*Store{plain, overlaid} {
		if _, err := s.Put(ctx, PutRequest{Upsert: upserts}); err != nil {
			t.Fatal(err)
		}
	}
	want := results(plain)
	// No rows at all, then rows that do not apply (another version), and
	// rows replaced by none: every result keeps its bytes.
	steps := []func() error{
		func() error {
			return overlaid.ReplaceDerivedMetadata(ctx, "consolidation", map[string]DerivedMetadata{})
		},
		func() error {
			return overlaid.ReplaceDerivedMetadata(ctx, "consolidation", map[string]DerivedMetadata{"entity": {Version: 2, Entities: []string{"Pallas"}, EventAt: "2020-03-03"}})
		},
		func() error {
			return overlaid.ReplaceDerivedMetadata(ctx, "other", map[string]DerivedMetadata{"anchor": {Version: 1, Links: []Link{{ID: "dated", Version: 1, Type: "causal", Weight: 1}}}})
		},
		func() error { return overlaid.ReplaceDerivedMetadata(ctx, "other", nil) },
	}
	for i, step := range steps {
		if err := step(); err != nil {
			t.Fatal(err)
		}
		got := results(overlaid)
		if i == 2 {
			if reflect.DeepEqual(got, want) {
				t.Fatal("the applying row changed nothing; the comparison proves nothing")
			}
			continue
		}
		if !reflect.DeepEqual(got, want) {
			for j := range got {
				if got[j] != want[j] {
					t.Fatalf("step %d query %d:\n%s\n%s", i, j, got[j], want[j])
				}
			}
		}
	}
	// With the runtime's caches as well.
	for _, s := range []*Store{plain, overlaid} {
		stop, err := s.StartRuntime(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer stop()
	}
	if want, got := results(plain), results(overlaid); !reflect.DeepEqual(got, want) {
		t.Fatal("cached results differ")
	}
}

func TestDerivedCacheKeepsOneEntryPerGeneration(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	s.cache = newQueryCache()
	if _, err := s.Put(ctx, PutRequest{Upsert: []Upsert{indexedRecord("x", "x notes", nil)}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceDerivedMetadata(ctx, "consolidation", map[string]DerivedMetadata{"x": {Version: 1, Entities: []string{"Pallas"}}}); err != nil {
		t.Fatal(err)
	}
	stale, err := derivedGeneration(ctx, s.readDB())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceDerivedMetadata(ctx, "consolidation", map[string]DerivedMetadata{"x": {Version: 1, Entities: []string{"Hermes"}}}); err != nil {
		t.Fatal(err)
	}
	rev, hash, err := revision(ctx, s.readDB(), "")
	if err != nil {
		t.Fatal(err)
	}
	all, err := s.queryRecords(ctx, rev, hash)
	if err != nil {
		t.Fatal(err)
	}
	// Two queries that read the generation before the replace both build
	// from the newer overlay: one cache entry, under the newer generation.
	req := indexRequest("x", &IndexPolicy{Version: 1, Entities: true})
	req.derived = stale
	for range 2 {
		index, err := s.derived(ctx, rev, hash, all, req)
		if err != nil || index.generation == stale {
			t.Fatal(index.generation, err)
		}
	}
	if len(s.cache.derivedOrder) != 1 || len(s.cache.derived) != 1 || s.cache.derivedDocs != 1 {
		t.Fatal(s.cache.derivedOrder, len(s.cache.derived), s.cache.derivedDocs)
	}
	// Eviction past four policies finds every entry it removes.
	for _, policy := range []IndexPolicy{{Version: 1, Lexical: true}, {Version: 1, Relations: true}, {Version: 1, Temporal: true}, {Version: 1, Supersedes: true}, {Version: 1, Lexical: true, Entities: true}} {
		req := indexRequest("x", &policy)
		req.derived = stale
		if _, err := s.derived(ctx, rev, hash, all, req); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.cache.derivedOrder) != 4 || len(s.cache.derived) != 4 || s.cache.derivedDocs != 4 {
		t.Fatal(s.cache.derivedOrder, len(s.cache.derived), s.cache.derivedDocs)
	}
}

func TestReplaceDerivedMetadataWritesOnlyChanges(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	if _, err := s.db.Exec(`CREATE TABLE writes(kind TEXT, id TEXT);
CREATE TRIGGER derived_insert AFTER INSERT ON derived_metadata BEGIN INSERT INTO writes VALUES('insert',new.id); END;
CREATE TRIGGER derived_update AFTER UPDATE ON derived_metadata BEGIN INSERT INTO writes VALUES('update',new.id); END;
CREATE TRIGGER derived_delete AFTER DELETE ON derived_metadata BEGIN INSERT INTO writes VALUES('delete',old.id); END;`); err != nil {
		t.Fatal(err)
	}
	writes := func() []string {
		rows, err := s.db.Query(`SELECT kind||' '||id FROM writes ORDER BY rowid`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := []string{}
		for rows.Next() {
			var w string
			rows.Scan(&w)
			out = append(out, w)
		}
		if _, err := s.db.Exec(`DELETE FROM writes`); err != nil {
			t.Fatal(err)
		}
		return out
	}
	generation := func() string {
		g, err := derivedGeneration(ctx, s.readDB())
		if err != nil {
			t.Fatal(err)
		}
		return g
	}
	items := map[string]DerivedMetadata{
		"a": {Version: 1, Entities: []string{"Athena"}},
		"b": {Version: 1, EventAt: "2020-01-01"},
		"c": {Version: 1, Aliases: []string{"Owl"}},
	}
	if err := s.ReplaceDerivedMetadata(ctx, "consolidation", items); err != nil {
		t.Fatal(err)
	}
	if got := writes(); !slices.Equal(got, []string{"insert a", "insert b", "insert c"}) || generation() != "1" {
		t.Fatal(got, generation())
	}
	// The same rows again: nothing written, the caches keep their generation.
	if err := s.ReplaceDerivedMetadata(ctx, "consolidation", items); err != nil {
		t.Fatal(err)
	}
	if got := writes(); len(got) != 0 || generation() != "1" {
		t.Fatal(got, generation())
	}
	// One row changed (its version), one gone, one new; another source's
	// rows are not touched.
	if err := s.ReplaceDerivedMetadata(ctx, "other", map[string]DerivedMetadata{"a": {Version: 1, Entities: []string{"Zeus"}}}); err != nil {
		t.Fatal(err)
	}
	writes()
	if err := s.ReplaceDerivedMetadata(ctx, "consolidation", map[string]DerivedMetadata{
		"a": {Version: 2, Entities: []string{"Athena"}},
		"b": {Version: 1, EventAt: "2020-01-01"},
		"d": {Version: 1, EventAt: "2021-01-01"},
	}); err != nil {
		t.Fatal(err)
	}
	if got := writes(); !slices.Equal(got, []string{"update a", "insert d", "delete c"}) || generation() != "3" {
		t.Fatal(got, generation())
	}
	var rows int
	if err := s.db.QueryRow(`SELECT count(*) FROM derived_metadata`).Scan(&rows); err != nil || rows != 4 {
		t.Fatal(rows, err)
	}
	// No rows for a source: its rows go.
	if err := s.ReplaceDerivedMetadata(ctx, "consolidation", nil); err != nil {
		t.Fatal(err)
	}
	if got := writes(); !slices.Equal(got, []string{"delete a", "delete b", "delete d"}) || generation() != "4" {
		t.Fatal(got, generation())
	}
}

func TestDerivedNamesComeAfterWriterAndExtracted(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	numbered := func(prefix string, n int) []string {
		out := []string{}
		for i := 1; i <= n; i++ {
			out = append(out, fmt.Sprintf("%s%02d", prefix, i))
		}
		return out
	}
	writer := map[string]any{"entities": numbered("E", 10), "aliases": numbered("A", 10)}
	if _, err := s.Put(ctx, PutRequest{Upsert: []Upsert{indexedRecord("x", "HTTP", writer)}}); err != nil {
		t.Fatal(err)
	}
	err := s.ReplaceDerivedMetadata(ctx, "consolidation", map[string]DerivedMetadata{"x": {Version: 1, Entities: numbered("O", 64), Aliases: numbered("P", 64)}})
	if err != nil {
		t.Fatal(err)
	}
	rev, hash, err := revision(ctx, s.readDB(), "")
	if err != nil {
		t.Fatal(err)
	}
	all, err := s.queryRecords(ctx, rev, hash)
	if err != nil {
		t.Fatal(err)
	}
	req := indexRequest("x", &IndexPolicy{Version: 1, Entities: true})
	req.derived, _ = derivedGeneration(ctx, s.readDB())
	index, err := s.buildDerived(ctx, rev, all, req)
	if err != nil {
		t.Fatal(err)
	}
	// The writer's entities, its aliases and the extracted names keep
	// their places; the overlay's fill what is left of the 64.
	for _, name := range append(append(numbered("E", 10), numbered("A", 10)...), "HTTP") {
		if !slices.Equal(index.entities[name], []string{"x"}) {
			t.Fatal("pushed out:", name)
		}
	}
	indexed := 0
	for range index.entities {
		indexed++
	}
	if indexed != 64 || index.entities["O01"] == nil || index.entities["P01"] != nil {
		t.Fatal(indexed, index.entities["O01"], index.entities["P01"])
	}
}
