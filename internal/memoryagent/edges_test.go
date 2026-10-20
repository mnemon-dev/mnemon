package memoryagent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/records"
)

// edgeMemory is a consolidated memory with two threads, two keys and four
// entity names. In date order thread T1 holds A (r3), B (r1) and C (r4, r1,
// r7); T2 holds X (r2) and D (r6), whose date is no date.
func edgeMemory() timelineMemory {
	return timelineMemory{
		Threads: []thread{{ID: "T1", Title: "Cabin trip"}, {ID: "T2", Title: "Job"}},
		Events: []event{
			{Thread: "T1", Date: "2024/01/05", Text: "Greg Hall booked the cabin.", Records: []string{"r1"}, Entities: []string{"Greg Hall", "Cabin Co"}},
			{Thread: "T2", Date: "2024/01/03", Text: "Started at Acme.", Records: []string{"r2"}, Entities: []string{"Acme"}},
			{Thread: "T1", Date: "2024/01/02", Text: "Greg suggested a cabin trip.", Records: []string{"r3"}, Entities: []string{"Greg"}},
			{Thread: "T1", Date: "2024/01/09", Text: "Paid the cabin deposit.", Records: []string{"r4", "r1", "r7"}},
			{Thread: "T2", Date: "sometime", Text: "Asked for a raise.", Records: []string{"r6"}},
		},
		Facts: map[string][]factValue{
			"cabin trip · date": {
				{Value: "May 3", Date: "2024/01/02", Records: []string{"r3"}},
				{Value: "May 3", Date: "2024/01/05", Records: []string{"r1"}},
				{Value: "May 10", Date: "2024/01/09", Records: []string{"r4"}},
			},
			"job · title": {
				{Value: "Engineer", Date: "2024/01/03", Records: []string{"r2"}},
				{Value: "Manager", Date: "2024/02/01", Records: []string{"r5"}, Entities: []string{"Acme"}},
			},
		},
		FactOrder:  []string{"cabin trip · date", "job · title"},
		Directives: []directive{},
	}
}

// edgeVersions are the records' versions: r4 was changed twice, r7 is gone.
func edgeVersions() map[string]int64 {
	return map[string]int64{"r1": 1, "r2": 1, "r3": 1, "r4": 3, "r5": 1, "r6": 1}
}

func edgeLink(id string, version int64, kind string, weight float64) records.Link {
	return records.Link{ID: id, Version: version, Type: kind, Weight: weight}
}

// gregClusters merges "Greg" into "Greg Hall".
func gregClusters() map[string]string {
	return map[string]string{"Greg Hall": "Greg Hall", "Cabin Co": "Cabin Co", "Acme": "Acme", "Greg": "Greg Hall"}
}

func TestBuildEdgesFromState(t *testing.T) {
	state := emptyTimelineState()
	state.Memory, state.EntityClusters = edgeMemory(), gregClusters()
	got := buildEdges([]timelineState{state}, edgeVersions(), nil)
	want := map[string]records.DerivedMetadata{
		// Entities of the events and values citing it, with the other names
		// of their clusters; links to the nearest events of its threads
		// (entity) and to the previous and next (temporal); the earliest
		// date citing it.
		// Then the capitalized words of its multi-word names ("Co" is too
		// short).
		"r1": {Version: 1, Entities: []string{"Greg Hall", "Cabin Co"}, Aliases: []string{"Greg", "Hall", "Cabin"}, EventAt: "2024-01-05",
			Links: []records.Link{edgeLink("r3", 1, "entity", 0.6), edgeLink("r3", 1, "temporal", 0.5), edgeLink("r4", 3, "temporal", 0.5)}},
		"r2": {Version: 1, Entities: []string{"Acme"}, EventAt: "2024-01-03", Links: []records.Link{edgeLink("r6", 1, "temporal", 0.5)}},
		"r3": {Version: 1, Entities: []string{"Greg"}, Aliases: []string{"Greg Hall", "Hall"}, EventAt: "2024-01-02", Links: []records.Link{edgeLink("r1", 1, "temporal", 0.5)}},
		// A later value supersedes the earlier one; its links bind the
		// targets' versions and its own row its own.
		"r4": {Version: 3, EventAt: "2024-01-09", Links: []records.Link{edgeLink("r1", 1, "supersedes", 1), edgeLink("r1", 1, "entity", 0.6), edgeLink("r1", 1, "temporal", 0.5), edgeLink("r3", 1, "entity", 0.6)}},
		"r5": {Version: 1, Entities: []string{"Acme"}, EventAt: "2024-02-01", Links: []records.Link{edgeLink("r2", 1, "supersedes", 1)}},
		// "sometime" is no date: nothing is inferred.
		"r6": {Version: 1, Links: []records.Link{edgeLink("r2", 1, "entity", 0.6), edgeLink("r2", 1, "temporal", 0.5)}},
	}
	if !reflect.DeepEqual(got, want) {
		for id := range want {
			if !reflect.DeepEqual(got[id], want[id]) {
				t.Errorf("%s:\n got %+v\nwant %+v", id, got[id], want[id])
			}
		}
		t.Fatalf("records %d, want %d", len(got), len(want))
	}
	records, links := edgeCounts(got)
	if records != 6 || !reflect.DeepEqual(links, map[string]int{"entity": 4, "temporal": 6, "supersedes": 2}) {
		t.Fatal(records, links)
	}
	// Without decisions every name is its own cluster: word aliases only.
	state.EntityClusters = nil
	for id, item := range buildEdges([]timelineState{state}, edgeVersions(), nil) {
		words := map[string][]string{"r1": {"Greg", "Hall", "Cabin"}}[id]
		if !slices.Equal(item.Aliases, words) || !slices.Equal(item.Entities, want[id].Entities) {
			t.Fatal(id, item)
		}
	}
	// A timelines state names no entities: links and dates only.
	for i := range state.Memory.Events {
		state.Memory.Events[i].Entities = nil
	}
	state.Memory.Facts["job · title"][1].Entities = nil
	for id, item := range buildEdges([]timelineState{state}, edgeVersions(), nil) {
		if item.Entities != nil || !reflect.DeepEqual(item.Links, want[id].Links) || item.EventAt != want[id].EventAt {
			t.Fatal(id, item)
		}
	}
}

func TestBuildEdgesNearestEvents(t *testing.T) {
	state := emptyTimelineState()
	versions := map[string]int64{}
	state.Memory.Threads = []thread{{ID: "T1", Title: "Long thread"}}
	// Stored out of date order.
	for _, p := range []int{3, 0, 6, 1, 5, 2, 4} {
		id := fmt.Sprintf("e%d", p)
		state.Memory.Events = append(state.Memory.Events, event{Thread: "T1", Date: fmt.Sprintf("2024/01/0%d", p+1), Text: id, Records: []string{id}})
		versions[id] = 1
	}
	got := buildEdges([]timelineState{state}, versions, nil)
	want := []records.Link{edgeLink("e5", 1, "entity", 0.6), edgeLink("e5", 1, "temporal", 0.5), edgeLink("e4", 1, "entity", 0.6), edgeLink("e3", 1, "entity", 0.6), edgeLink("e2", 1, "entity", 0.6)}
	if !reflect.DeepEqual(got["e6"].Links, want) {
		t.Fatal("the last event links its four nearest", got["e6"].Links)
	}
	// Entity links are written once per pair, on the later event's records
	// (the index reads them both ways): e0's four nearest link to it, the
	// rest do not.
	to := map[string][]string{}
	for id, item := range got {
		for _, link := range item.Links {
			if link.Type == "entity" {
				to[link.ID] = append(to[link.ID], id)
			}
		}
	}
	slices.Sort(to["e0"])
	if !slices.Equal(to["e0"], []string{"e1", "e2", "e3", "e4"}) || !reflect.DeepEqual(got["e0"].Links, []records.Link{edgeLink("e1", 1, "temporal", 0.5)}) {
		t.Fatal(to["e0"], got["e0"].Links)
	}
	for id, item := range got {
		seen := map[string]bool{}
		for _, link := range item.Links {
			key := link.ID + " " + link.Type
			if seen[key] || link.ID == id {
				t.Fatal("duplicate or self link", id, link)
			}
			seen[key] = true
		}
	}
	// Two records in one order in a thread and the other order in another
	// get one entity link, not one each way.
	state = emptyTimelineState()
	state.Memory.Threads = []thread{{ID: "T1", Title: "One"}, {ID: "T2", Title: "Two"}}
	state.Memory.Events = []event{
		{Thread: "T1", Date: "2024/01/01", Text: "a", Records: []string{"ra"}},
		{Thread: "T1", Date: "2024/01/02", Text: "b", Records: []string{"rb"}},
		{Thread: "T2", Date: "2024/01/03", Text: "c", Records: []string{"rb"}},
		{Thread: "T2", Date: "2024/01/04", Text: "d", Records: []string{"ra"}},
	}
	got = buildEdges([]timelineState{state}, map[string]int64{"ra": 1, "rb": 1}, nil)
	if !reflect.DeepEqual(got["rb"].Links, []records.Link{edgeLink("ra", 1, "entity", 0.6), edgeLink("ra", 1, "temporal", 0.5)}) ||
		!reflect.DeepEqual(got["ra"].Links, []records.Link{edgeLink("rb", 1, "temporal", 0.5)}) {
		t.Fatal(got)
	}
}

func TestBuildEdgesBounds(t *testing.T) {
	state := emptyTimelineState()
	state.Memory.Threads = []thread{{ID: "T1", Title: "Hub"}, {ID: "T2", Title: "Solo"}}
	versions := map[string]int64{"hub": 1, "solo": 1}
	names := []string{}
	state.EntityClusters = map[string]string{}
	for i := range 70 {
		name := fmt.Sprintf("N%d", i)
		names = append(names, name)
		state.EntityClusters[name] = "N0"
	}
	for i := range 60 {
		id := fmt.Sprintf("x%02d", i)
		versions[id] = 1
		e := event{Thread: "T1", Date: fmt.Sprintf("2024/02/%02d", i%28+1), Text: id, Records: []string{"hub", id}}
		if i == 0 {
			e.Entities = names
		}
		state.Memory.Events = append(state.Memory.Events, e)
	}
	state.Memory.Events = append(state.Memory.Events, event{Thread: "T2", Date: "2024/03/01", Text: "solo", Records: []string{"solo"}, Entities: []string{"N0"}})
	got := buildEdges([]timelineState{state}, versions, nil)
	hub, solo := got["hub"], got["solo"]
	if len(hub.Links) != 50 || len(hub.Entities) != 64 || !slices.Equal(hub.Aliases, names[64:]) || len(solo.Aliases) != 64 || solo.Aliases[0] != "N1" {
		t.Fatal(len(hub.Links), len(hub.Entities), hub.Aliases, len(solo.Aliases))
	}
	s, _ := agentStore(t)
	if err := s.ReplaceDerivedMetadata(context.Background(), edgesSource, got); err != nil {
		t.Fatal("the store refused the built rows", err)
	}
}

// aliasJudge says "Greg" and "G. Hall" are "Greg Hall", and no to the rest.
func aliasJudge() *linkJudge {
	return &linkJudge{answer: func(kind, name, candidate string) float64 {
		greg := map[string]bool{"Greg": true, "Greg Hall": true, "G. Hall": true}
		if kind == "alias" && greg[name] && greg[candidate] {
			return 0.9
		}
		return 0.1
	}}
}

func TestResolveAliases(t *testing.T) {
	judge := aliasJudge()
	a := linkAgent(t, judge, 8, nil)
	state, result := linkState0(edgeMemory()), &ConsolidateResult{}
	changed, err := a.newLinker().resolveAliases(context.Background(), state, result)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	if !reflect.DeepEqual(state.EntityClusters, gregClusters()) {
		t.Fatal(state.EntityClusters)
	}
	if result.AliasCalls != 1 || result.AliasMerges != 1 || result.AliasFallbacks != 0 || result.LinkCalls != 0 || result.LinkUsage == nil || result.LinkUsage.InputTokens != 100 || state.Usage.InputTokens != 100 {
		t.Fatalf("%+v %+v", result, state.Usage)
	}
	// Each name is compared with the names known before it that share
	// words with its context: Cabin Co with Greg Hall, Greg with both; Acme
	// shares none, Greg Hall comes first.
	call := judge.calls[0]
	asked := []string{}
	names := func(items []linkItem, id string) string {
		for _, item := range items {
			if item.ID == id {
				return item.Name
			}
		}
		t.Fatal("no item", id)
		return ""
	}
	for key, q := range call.Questions {
		refs := linkRef.FindAllStringSubmatch(q.Instructions, -1)
		asked = append(asked, names(call.State.New, refs[0][1])+" ~ "+names(call.State.Candidates, refs[1][1]))
		want := "Do the name in the new item whose `id` is \"" + refs[0][1] + "\" and the name in the candidate whose `id` is \"" + refs[1][1] + "\" refer to the same person, organization, place, product or document?"
		criteria := map[string]string{
			"true":  "The texts show they are the same one (a full name and a short name, a nickname, a renamed product, an abbreviation).",
			"false": "Different ones that only share a word, a role or a topic, or the texts do not show that they are the same.",
		}
		if q.Type != "noul" || q.Instructions != want || !reflect.DeepEqual(q.Criteria, criteria) || !strings.HasPrefix(key, "a") {
			t.Fatalf("question %s %+v", key, q)
		}
	}
	slices.Sort(asked)
	if !slices.Equal(asked, []string{"Cabin Co ~ Greg Hall", "Greg ~ Cabin Co", "Greg ~ Greg Hall"}) {
		t.Fatal(asked)
	}
	for _, item := range call.State.New {
		if item.Name == "Greg" && item.Text != "Greg suggested a cabin trip." {
			t.Fatalf("item %+v", item)
		}
	}
	// Decided names are not asked again; a new name meets the decided ones.
	judge.calls = nil
	*result = ConsolidateResult{}
	if changed, err = a.newLinker().resolveAliases(context.Background(), state, result); err != nil || changed || judge.count() != 0 || result.AliasCalls != 0 {
		t.Fatal(changed, err, judge.count())
	}
	state.Memory.Events = append(state.Memory.Events, event{Thread: "T1", Date: "2024/01/10", Text: "G. Hall paid for the cabin.", Records: []string{"r8"}, Entities: []string{"G. Hall"}})
	if changed, err = a.newLinker().resolveAliases(context.Background(), state, result); err != nil || !changed || state.EntityClusters["G. Hall"] != "Greg Hall" || result.AliasMerges != 1 {
		t.Fatal(changed, err, state.EntityClusters, result)
	}
	if call := judge.calls[0]; len(call.State.New) != 1 || call.State.New[0].Name != "G. Hall" {
		t.Fatal(call.State)
	}
}

func TestResolveAliasesCandidateLimitAndEmbedding(t *testing.T) {
	// One candidate each, the most similar by meaning: Greg's is Acme.
	server := &embeddingServer{}
	url := server.start(t, func(text string) []float64 {
		text = strings.TrimPrefix(text, "search_document: ")
		if strings.HasPrefix(text, "Greg:") || strings.HasPrefix(text, "Acme:") {
			return []float64{1, 0, 0}
		}
		return []float64{0, 0, 1}
	})
	embedding := &records.EmbeddingConfig{URL: url, Model: "test", QueryPrefix: "search_query: ", DocumentPrefix: "search_document: "}
	judge := &linkJudge{}
	a := linkAgent(t, judge, 1, embedding)
	state := linkState0(edgeMemory())
	if _, err := a.newLinker().resolveAliases(context.Background(), state, &ConsolidateResult{}); err != nil {
		t.Fatal(err)
	}
	pairs := []string{}
	for _, call := range judge.calls {
		for _, q := range call.Questions {
			refs := linkRef.FindAllStringSubmatch(q.Instructions, -1)
			for _, n := range call.State.New {
				for _, c := range call.State.Candidates {
					if n.ID == refs[0][1] && c.ID == refs[1][1] {
						pairs = append(pairs, n.Name+" ~ "+c.Name)
					}
				}
			}
		}
	}
	slices.Sort(pairs)
	if !slices.Equal(pairs, []string{"Cabin Co ~ Greg Hall", "Greg ~ Acme"}) {
		t.Fatal(pairs)
	}
	// The contexts are embedded as documents; the new names' are not
	// stored, the decided names' are, for later jobs.
	server.mu.Lock()
	inputs := slices.Clone(server.inputs)
	server.mu.Unlock()
	context0 := "Greg: Greg suggested a cabin trip."
	if !slices.Contains(inputs, "search_document: "+context0) {
		t.Fatal(inputs)
	}
	kept, err := a.store.DerivedVectors(context.Background(), []string{context0}, *embedding, false)
	if err != nil || kept[0] != nil {
		t.Fatal("new names' contexts were stored", err)
	}
	state.Memory.Events = append(state.Memory.Events, event{Thread: "T1", Date: "2024/01/10", Text: "G. Hall paid.", Records: []string{"r8"}, Entities: []string{"G. Hall"}})
	if _, err := a.newLinker().resolveAliases(context.Background(), state, &ConsolidateResult{}); err != nil {
		t.Fatal(err)
	}
	kept, err = a.store.DerivedVectors(context.Background(), []string{context0, "G. Hall: G. Hall paid."}, *embedding, false)
	if err != nil || kept[0] == nil || kept[1] != nil {
		t.Fatal("decided names' contexts", kept[0] != nil, kept[1] != nil, err)
	}
}

func TestResolveAliasesJEVFailure(t *testing.T) {
	judge := aliasJudge()
	judge.err = func(context.Context) error { return ErrBudgetCap }
	a := linkAgent(t, judge, 8, nil)
	state, result := linkState0(edgeMemory()), &ConsolidateResult{}
	changed, err := a.newLinker().resolveAliases(context.Background(), state, result)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	// The names JEV was to decide stay alone, counted; every name is decided.
	own := map[string]string{"Greg Hall": "Greg Hall", "Cabin Co": "Cabin Co", "Acme": "Acme", "Greg": "Greg"}
	if !reflect.DeepEqual(state.EntityClusters, own) || result.AliasFallbacks != 2 || result.AliasCalls != 1 || result.AliasMerges != 0 {
		t.Fatal(state.EntityClusters, result)
	}
	// A canceled job decides nothing.
	ctx, cancel := context.WithCancel(context.Background())
	judge = aliasJudge()
	judge.err = func(context.Context) error { cancel(); return context.Canceled }
	a = linkAgent(t, judge, 8, nil)
	state = linkState0(edgeMemory())
	if _, err = a.newLinker().resolveAliases(ctx, state, &ConsolidateResult{}); !errors.Is(err, context.Canceled) || state.EntityClusters != nil {
		t.Fatal(err, state.EntityClusters)
	}
}

// putEdgeRecords stores r1–r6 as notes without an eventAt, r4 at version 3.
func putEdgeRecords(t *testing.T, s *records.Store) {
	t.Helper()
	texts := map[string]string{
		"r1": "my uncle booked the lakeside cabin",
		"r2": "first day at the new office",
		"r3": "Greg suggested a trip",
		"r4": "paid the deposit and moved it to May 10",
		"r5": "promotion confirmed today",
		"r6": "asked about the raise",
	}
	upserts := []records.Upsert{}
	for _, id := range []string{"r1", "r2", "r3", "r4", "r5", "r6"} {
		version := 1
		if id == "r4" {
			version = 3
		}
		v := recordValue{ID: id, Kind: "note", Title: "Note", Content: texts[id], Scope: "global", State: "active", Version: version, Signals: 1, CreatedAt: "2026-10-19T00:00:00Z", UpdatedAt: "2026-10-19T00:00:00Z", History: []any{}, Data: map[string]any{}}
		upserts = append(upserts, encodeRecord(v, texts[id]))
	}
	if _, err := s.Put(context.Background(), records.PutRequest{Upsert: upserts}); err != nil {
		t.Fatal(err)
	}
}

// derivedOverlay counts the records' derived-metadata rows and reports
// whether an overlay was ever written.
func derivedOverlay(t *testing.T, dir string) (rows int, written bool) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "records.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var generations int
	if err := db.QueryRow(`SELECT (SELECT count(*) FROM derived_metadata),(SELECT count(*) FROM meta WHERE key='derived')`).Scan(&rows, &generations); err != nil {
		t.Fatal(err)
	}
	return rows, generations > 0
}

func queryIDs(t *testing.T, s *records.Store, text string, policy *records.IndexPolicy, extra map[string]string) []string {
	t.Helper()
	input := map[string]json.RawMessage{"match": json.RawMessage(`"words"`)}
	input["query"], _ = json.Marshal(text)
	for key, value := range extra {
		input[key], _ = json.Marshal(value)
	}
	got, err := s.Query(context.Background(), records.QueryRequest{Input: input, Index: policy})
	if err != nil {
		t.Fatal(err)
	}
	return got.IDs
}

func TestRebuildEdgesFromStoredState(t *testing.T) {
	s, dir := agentStore(t)
	putEdgeRecords(t, s)
	folds := &limitPlanner{reply: func(string, string) (string, error) { return "", errors.New("a rebuild folds nothing") }}
	judge := aliasJudge()
	a, overlay := layeredAgent(t, s, dir, ConsolidationConfig{NativeEdges: true}, folds, judge)
	stored := emptyTimelineState()
	stored.Mode, stored.Memory, stored.Batches = "layered", edgeMemory(), 2
	stored.Pending = []pendingRecord{{ID: "r9", Header: "h", Text: "unfolded"}}
	if _, err := overlay.saveTimelines(context.Background(), records.Scope{}, "", stored); err != nil {
		t.Fatal(err)
	}
	result, err := a.Consolidate(context.Background(), ConsolidateRequest{RebuildEdges: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(folds.calls) != 0 || result.Folded != 0 || result.Visited != 0 || result.Added != 0 {
		t.Fatalf("a rebuild folded: %+v", result)
	}
	if result.EdgeRecords != 6 || !reflect.DeepEqual(result.EdgeLinks, map[string]int{"entity": 4, "temporal": 6, "supersedes": 2}) || result.AliasCalls != 1 || result.AliasMerges != 1 || result.Revision == "" {
		t.Fatalf("result %+v", result)
	}
	state := loadState(t, overlay)
	stored.EntityClusters, stored.EdgesMemory = gregClusters(), edgesDigest(stored.Memory)
	stored.Usage = state.Usage
	if !reflect.DeepEqual(state, stored) || state.Usage.InputTokens != 100 {
		t.Fatalf("the rebuild changed more than the decisions:\n%+v\n%+v", state, stored)
	}
	if rows, written := derivedOverlay(t, dir); rows != 6 || !written {
		t.Fatal(rows, written)
	}
	// Each native route finds the linked records through the overlay.
	entities := &records.IndexPolicy{Version: 1, Entities: true}
	if got := queryIDs(t, s, "Greg", nil, nil); !slices.Equal(got, []string{"r3"}) {
		t.Fatal(got)
	}
	if got := queryIDs(t, s, "Greg", entities, nil); !slices.Contains(got, "r1") || !slices.Contains(got, "r3") {
		t.Fatal("the alias did not find Greg Hall's record", got)
	}
	relations := &records.IndexPolicy{Version: 1, Relations: true}
	if got := queryIDs(t, s, "suggested", relations, nil); !slices.Contains(got, "r3") || !slices.Contains(got, "r1") || !slices.Contains(got, "r4") {
		t.Fatal("thread neighbors", got)
	}
	supersedes := &records.IndexPolicy{Version: 1, Supersedes: true}
	if got := queryIDs(t, s, "uncle booked", supersedes, nil); !slices.Equal(got, []string{"r1", "r4"}) {
		t.Fatal("the newer value, never demoting", got)
	}
	temporal := &records.IndexPolicy{Version: 1, Temporal: true}
	if got := queryIDs(t, s, "Greg suggested", temporal, map[string]string{"since": "2024-01-02", "until": "2024-01-02"}); !slices.Equal(got, []string{"r3"}) {
		t.Fatal("event time", got)
	}
	// A second rebuild asks nothing and writes the same rows.
	again, err := a.Consolidate(context.Background(), ConsolidateRequest{RebuildEdges: true})
	if err != nil || again.AliasCalls != 0 || again.EdgeRecords != 6 || judge.count() != 1 {
		t.Fatal(again, err)
	}
	// Rebuilding never resets.
	if _, err := a.Consolidate(context.Background(), ConsolidateRequest{RebuildEdges: true, Reset: true}); err == nil {
		t.Fatal("a rebuild reset the state")
	}
}

func TestRebuildEdgesJEVFailureCounted(t *testing.T) {
	s, dir := agentStore(t)
	putEdgeRecords(t, s)
	judge := aliasJudge()
	judge.err = func(context.Context) error { return errors.New("jev down") }
	a, overlay := layeredAgent(t, s, dir, ConsolidationConfig{NativeEdges: true}, &limitPlanner{}, judge)
	stored := emptyTimelineState()
	stored.Memory = edgeMemory()
	if _, err := overlay.saveTimelines(context.Background(), records.Scope{}, "", stored); err != nil {
		t.Fatal(err)
	}
	result, err := a.Consolidate(context.Background(), ConsolidateRequest{RebuildEdges: true})
	if err != nil || result.AliasFallbacks != 2 || result.AliasMerges != 0 || result.EdgeRecords != 6 {
		t.Fatal(result, err)
	}
	// Undecided, Greg is not Greg Hall: Greg's record has no "Hall"; Greg
	// Hall's has its words.
	if got := queryIDs(t, s, "Hall", &records.IndexPolicy{Version: 1, Entities: true}, nil); !slices.Equal(got, []string{"r1"}) {
		t.Fatal("an undecided alias found a record", got)
	}
}

func TestNativeEdgesAfterLayeredJob(t *testing.T) {
	s, dir := agentStore(t)
	seedTopics(t, s, 3)
	model := &limitPlanner{reply: func(_, prompt string) (string, error) { return layeredReply(prompt), nil }}
	a, overlay := layeredAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100, NativeEdges: true}, model, topicJudge())
	result, err := a.Consolidate(context.Background(), ConsolidateRequest{})
	if err != nil || result.Folded != 3 {
		t.Fatal(result, err)
	}
	// Nine notes, each cited by its topic's event and value: three threads
	// of three events and three keys of three values.
	if result.EdgeRecords != 9 || !reflect.DeepEqual(result.EdgeLinks, map[string]int{"entity": 9, "temporal": 12, "supersedes": 6}) || result.AliasCalls != 1 || result.AliasMerges != 0 {
		t.Fatalf("result %+v", result)
	}
	state := loadState(t, overlay)
	if !reflect.DeepEqual(state.EntityClusters, map[string]string{"kayak": "kayak", "taxes": "taxes", "garden": "garden"}) {
		t.Fatal(state.EntityClusters)
	}
	if rows, written := derivedOverlay(t, dir); rows != 9 || !written {
		t.Fatal(rows, written)
	}
	if got := queryIDs(t, s, "00", nil, nil); !slices.Equal(got, []string{"n00"}) {
		t.Fatal(got)
	}
	relations := &records.IndexPolicy{Version: 1, Relations: true}
	if got := queryIDs(t, s, "00", relations, nil); !slices.Contains(got, "n03") || !slices.Contains(got, "n06") {
		t.Fatal("thread neighbors", got)
	}
	// A job that changes nothing leaves the overlay alone.
	result, err = a.Consolidate(context.Background(), ConsolidateRequest{})
	if err != nil || result.EdgeRecords != 0 || result.AliasCalls != 0 {
		t.Fatal(result, err)
	}
	// A reset clears the overlay with the memory.
	if result, err = a.Consolidate(context.Background(), ConsolidateRequest{Reset: true, MaxBatches: 1}); err != nil || result.EdgeRecords != 3 {
		t.Fatal(result, err)
	}
}

func TestNativeEdgesTimelinesMode(t *testing.T) {
	s, dir := agentStore(t)
	seedTopics(t, s, 2)
	// The author's fold: no entity names.
	model := &limitPlanner{reply: func(_, prompt string) (string, error) {
		reply := layeredReply(prompt)
		return strings.ReplaceAll(reply, `"entities":`, `"names":`), nil
	}}
	a, overlay := timelinesAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100, NativeEdges: true}, Models{Planner: model})
	result, err := a.Consolidate(context.Background(), ConsolidateRequest{})
	if err != nil || result.Folded != 2 || result.EdgeRecords != 6 || result.AliasCalls != 0 || result.EdgeLinks["entity"] == 0 {
		t.Fatal(result, err)
	}
	raw, _ := json.Marshal(loadState(t, overlay))
	if strings.Contains(string(raw), "entityClusters") {
		t.Fatal(string(raw))
	}
}

func TestNativeEdgesOffLeavesRecordsAlone(t *testing.T) {
	for _, mode := range []string{"layered", "timelines"} {
		t.Run(mode, func(t *testing.T) {
			s, dir := agentStore(t)
			seedTopics(t, s, 2)
			model := &limitPlanner{reply: func(_, prompt string) (string, error) { return layeredReply(prompt), nil }}
			var a *Agent
			var overlay *Overlay
			if mode == "layered" {
				a, overlay = layeredAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100}, model, topicJudge())
			} else {
				a, overlay = timelinesAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100}, Models{Planner: model})
			}
			result, err := a.Consolidate(context.Background(), ConsolidateRequest{})
			if err != nil || result.Folded != 2 {
				t.Fatal(result, err)
			}
			if rows, written := derivedOverlay(t, dir); rows != 0 || written {
				t.Fatal("derived metadata written", rows, written)
			}
			state, _ := json.Marshal(loadState(t, overlay))
			out, _ := json.Marshal(result)
			for _, field := range []string{"entityClusters", "edgesMemory", "edgeRecords", "edgeLinks", "alias"} {
				if strings.Contains(string(state), field) || strings.Contains(string(out), field) {
					t.Fatal(field, string(state), string(out))
				}
			}
			// Without nativeEdges a rebuild clears the consolidation's rows,
			// and here there are none.
			if result, err := a.Consolidate(context.Background(), ConsolidateRequest{RebuildEdges: true}); err != nil || result.EdgeRecords != 0 {
				t.Fatal(result, err)
			}
			if rows, written := derivedOverlay(t, dir); rows != 0 || written {
				t.Fatal("derived metadata written", rows, written)
			}
		})
	}
	// memory-agent/v1 consolidation has no edges either.
	s, dir := agentStore(t)
	overlay, err := OpenOverlay(filepath.Join(dir, "memory-index.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer overlay.Close()
	a, err := New(s, Config{Version: 1, Policy: &ReadPolicy{Consolidation: true}}, Models{Planner: completeFunc(nil), Judge: decisionFunc(fixtureJudge)}, overlay)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Consolidate(context.Background(), ConsolidateRequest{RebuildEdges: true}); err != nil {
		t.Fatal("v1 rebuild", err)
	}
	if rows, written := derivedOverlay(t, dir); rows != 0 || written {
		t.Fatal("derived metadata written", rows, written)
	}
}

func TestNativeEdgesConfig(t *testing.T) {
	s, _ := agentStore(t)
	models := Models{Planner: &limitPlanner{}, Judge: decisionFunc(fixtureJudge)}
	for _, mode := range []string{"timelines", "layered"} {
		a, err := New(s, Config{Version: 1, Policy: &ReadPolicy{}, Consolidation: &ConsolidationConfig{Mode: mode, NativeEdges: true}}, models, nil)
		if err != nil || !a.config.Consolidation.NativeEdges {
			t.Fatal(mode, err)
		}
	}
	if _, err := New(s, Config{Version: 1, Policy: &ReadPolicy{}, Consolidation: &ConsolidationConfig{NativeEdges: true}}, models, nil); err == nil {
		t.Fatal("nativeEdges accepted without a timelines mode")
	}
	raw, _ := json.Marshal(ConsolidationConfig{Mode: "layered", NativeEdges: true})
	if string(raw) != `{"mode":"layered","nativeEdges":true}` {
		t.Fatal(string(raw))
	}
	raw, _ = json.Marshal(ConsolidateRequest{RebuildEdges: true})
	if string(raw) != `{"scope":{},"rebuildEdges":true}` {
		t.Fatal(string(raw))
	}
	if raw, _ := json.Marshal(ConsolidationConfig{Mode: "layered"}); strings.Contains(string(raw), "nativeEdges") {
		t.Fatal(string(raw))
	}
}

func TestNativeEdgesAcrossScopes(t *testing.T) {
	s, dir := agentStore(t)
	putEdgeRecords(t, s)
	a, overlay := layeredAgent(t, s, dir, ConsolidationConfig{NativeEdges: true}, &limitPlanner{}, aliasJudge())
	other := emptyTimelineState()
	other.Memory = timelineMemory{Threads: []thread{{ID: "T1", Title: "Office"}}, Events: []event{
		{Thread: "T1", Date: "2023/12/01", Text: "Office party.", Records: []string{"r5"}, Entities: []string{"Dana"}},
		{Thread: "T1", Date: "2023/12/02", Text: "Office cleanup.", Records: []string{"r6"}},
	}, Facts: map[string][]factValue{}, FactOrder: []string{}, Directives: []directive{}}
	workspace := "/work"
	if _, err := overlay.saveTimelines(context.Background(), records.Scope{WorkspaceID: &workspace}, "", other); err != nil {
		t.Fatal(err)
	}
	mine := emptyTimelineState()
	mine.Memory = edgeMemory()
	if _, err := overlay.saveTimelines(context.Background(), records.Scope{}, "", mine); err != nil {
		t.Fatal(err)
	}
	// Rebuilding one scope keeps the other's edges: the overlay is every
	// stored state's.
	result, err := a.Consolidate(context.Background(), ConsolidateRequest{RebuildEdges: true})
	if err != nil || result.EdgeRecords != 6 {
		t.Fatal(result, err)
	}
	if got := queryIDs(t, s, "Dana", &records.IndexPolicy{Version: 1, Entities: true}, nil); !slices.Equal(got, []string{"r5"}) {
		t.Fatal(got)
	}
	// r5 is dated by the other scope's earlier event.
	if got := queryIDs(t, s, "promotion", &records.IndexPolicy{Version: 1, Temporal: true}, map[string]string{"since": "2023-12-01", "until": "2023-12-01"}); !slices.Equal(got, []string{"r5"}) {
		t.Fatal(got)
	}
}

func TestNativeEdgesEmptyScopeWritesNothing(t *testing.T) {
	s, dir := agentStore(t)
	a, overlay := layeredAgent(t, s, dir, ConsolidationConfig{NativeEdges: true}, &limitPlanner{}, aliasJudge())
	for _, req := range []ConsolidateRequest{{}, {RebuildEdges: true}} {
		if result, err := a.Consolidate(context.Background(), req); err != nil || result.EdgeRecords != 0 {
			t.Fatal(result, err)
		}
	}
	if _, token, err := overlay.loadTimelines(context.Background(), records.Scope{}); err != nil || token != "" {
		t.Fatal("an empty scope's state was written", token, err)
	}
}

func TestRebuildEdgesWithoutNativeEdgesClears(t *testing.T) {
	s, dir := agentStore(t)
	seedTopics(t, s, 1)
	model := &limitPlanner{reply: func(_, prompt string) (string, error) { return layeredReply(prompt), nil }}
	a, overlay := layeredAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100, NativeEdges: true}, model, topicJudge())
	if result, err := a.Consolidate(context.Background(), ConsolidateRequest{}); err != nil || result.EdgeRecords != 3 {
		t.Fatal(result, err)
	}
	// The same store, nativeEdges turned off: a rebuild clears the rows.
	off, err := New(s, Config{Version: 1, Policy: &ReadPolicy{Consolidation: true}, Consolidation: &ConsolidationConfig{Mode: "layered", BatchCharacters: 100}}, Models{Consolidator: model, Judge: topicJudge()}, overlay)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := off.Consolidate(context.Background(), ConsolidateRequest{RebuildEdges: true, Reset: true}); err == nil {
		t.Fatal("a rebuild reset the state")
	}
	if result, err := off.Consolidate(context.Background(), ConsolidateRequest{RebuildEdges: true}); err != nil || result.EdgeRecords != 0 {
		t.Fatal(result, err)
	}
	if rows, _ := derivedOverlay(t, dir); rows != 0 {
		t.Fatal("rows left", rows)
	}
}

func TestNativeEdgesResetClears(t *testing.T) {
	s, dir := agentStore(t)
	seedTopics(t, s, 1)
	var fail atomic.Bool
	model := &limitPlanner{reply: func(_, prompt string) (string, error) {
		if fail.Load() {
			return "", errors.New("refused")
		}
		return layeredReply(prompt), nil
	}}
	a, _ := layeredAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100, NativeEdges: true}, model, topicJudge())
	if result, err := a.Consolidate(context.Background(), ConsolidateRequest{}); err != nil || result.EdgeRecords != 3 {
		t.Fatal(result, err)
	}
	// A reset whose fold fails leaves an empty memory: its edges go too.
	fail.Store(true)
	result, err := a.Consolidate(context.Background(), ConsolidateRequest{Reset: true})
	if err == nil || result.EdgeRecords != 0 || result.EdgesPending || result.EdgeError != "" {
		t.Fatal(result, err)
	}
	if rows, _ := derivedOverlay(t, dir); rows != 0 {
		t.Fatal("a reset kept the old edges", rows)
	}
}

func TestNativeEdgesFailureKeepsTheFold(t *testing.T) {
	s, dir := agentStore(t)
	seedTopics(t, s, 1)
	model := &limitPlanner{reply: func(_, prompt string) (string, error) { return layeredReply(prompt), nil }}
	a, overlay := layeredAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100, NativeEdges: true}, model, topicJudge())
	// Another scope's state that will not load breaks the edge step.
	if _, err := overlay.db.Exec(`INSERT INTO timelines VALUES('broken','not its digest','{}')`); err != nil {
		t.Fatal(err)
	}
	result, err := a.Consolidate(context.Background(), ConsolidateRequest{})
	if err != nil || result.Folded != 1 || !result.EdgesPending || !strings.Contains(result.EdgeError, "digest") {
		t.Fatal("the edge step failed the fold", result, err)
	}
	if state := loadState(t, overlay); len(state.Memory.Events) != 3 || state.EdgesMemory != "" {
		t.Fatal("the fold was not kept, or the edges were marked built", state.EdgesMemory)
	}
	// The next job, with nothing new, retries the edges.
	if _, err := overlay.db.Exec(`DELETE FROM timelines WHERE scope='broken'`); err != nil {
		t.Fatal(err)
	}
	result, err = a.Consolidate(context.Background(), ConsolidateRequest{})
	if err != nil || result.Folded != 0 || result.EdgeRecords != 3 || result.EdgesPending || result.EdgeError != "" {
		t.Fatal(result, err)
	}
}

func TestNativeEdgesDeferred(t *testing.T) {
	s, dir := agentStore(t)
	seedTopics(t, s, 2)
	model := &limitPlanner{reply: func(_, prompt string) (string, error) { return layeredReply(prompt), nil }}
	judge := topicJudge()
	a, _ := layeredAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100, NativeEdges: true}, model, judge)
	// Deferred jobs fold and leave the edges pending, asking JEV nothing
	// about names.
	for i := range 2 {
		result, err := a.Consolidate(context.Background(), ConsolidateRequest{Hold: true, DeferEdges: true, MaxBatches: 1})
		if err != nil || result.Folded != 1 || !result.EdgesPending || result.EdgeRecords != 0 || result.AliasCalls != 0 {
			t.Fatal(i, result, err)
		}
	}
	if rows, _ := derivedOverlay(t, dir); rows != 0 {
		t.Fatal("a deferred job wrote edges", rows)
	}
	// A deferred job that folds nothing still reports the pending edges.
	if result, err := a.Consolidate(context.Background(), ConsolidateRequest{Hold: true, DeferEdges: true, MaxBatches: 1}); err != nil || result.Folded != 0 || !result.EdgesPending {
		t.Fatal(result, err)
	}
	// One rebuild catches up.
	result, err := a.Consolidate(context.Background(), ConsolidateRequest{RebuildEdges: true})
	if err != nil || result.EdgeRecords != 6 || result.EdgesPending {
		t.Fatal(result, err)
	}
	if result, err := a.Consolidate(context.Background(), ConsolidateRequest{Hold: true, DeferEdges: true, MaxBatches: 1}); err != nil || result.EdgesPending {
		t.Fatal(result, err)
	}
	raw, _ := json.Marshal(ConsolidateRequest{DeferEdges: true})
	if !strings.Contains(string(raw), `"deferEdges":true`) {
		t.Fatal(string(raw))
	}
}

func TestBuildEdgesWordAliases(t *testing.T) {
	state := emptyTimelineState()
	state.Memory.Threads = []thread{{ID: "T1", Title: "Names"}}
	names := []string{"Lake Tahoe", "The Cabin Co", "User Profile", "May Day Parade", "Monday Meeting", "Assistant Manager", "cabin rental", "G. Hall", "Dr. Anne-Marie O'Brien", "Acme", "Greg's Bakery", "Our Garden"}
	state.Memory.Events = []event{{Thread: "T1", Date: "2024/01/01", Text: "names", Records: []string{"r"}, Entities: names}}
	got := buildEdges([]timelineState{state}, map[string]int64{"r": 1}, nil)["r"]
	// The capitalized words of three letters or more of each multi-word
	// name, but no stopword, month, weekday, role or first-person word;
	// single-word names and lowercase words give none.
	want := []string{"Lake", "Tahoe", "Cabin", "Profile", "Day", "Parade", "Meeting", "Manager", "Hall", "Anne", "Marie", "Brien", "Greg", "Bakery", "Garden"}
	if !slices.Equal(got.Entities, names) || !slices.Equal(got.Aliases, want) {
		t.Fatalf("aliases %q", got.Aliases)
	}
	// A word that is itself one of the record's names is no alias.
	state.Memory.Events[0].Entities = []string{"Tahoe", "Lake Tahoe"}
	if got := buildEdges([]timelineState{state}, map[string]int64{"r": 1}, nil)["r"]; !slices.Equal(got.Aliases, []string{"Lake"}) {
		t.Fatalf("aliases %q", got.Aliases)
	}
}
