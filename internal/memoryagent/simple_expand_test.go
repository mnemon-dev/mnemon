package memoryagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mnemon-dev/mnemon/internal/records"
)

// expandNote is a dated note with fixed bytes: its eventAt day is its date.
func expandNote(id, text, eventAt string, data map[string]any) records.Upsert {
	day := eventAt[:10]
	values := map[string]any{"eventAt": eventAt}
	maps.Copy(values, data)
	v := recordValue{ID: id, Kind: "note", Title: "Note", Content: text, Scope: "global", Date: &day, State: "active", Version: 1, Signals: 1, CreatedAt: "2026-10-19T00:00:00Z", UpdatedAt: "2026-10-19T00:00:00Z", History: []any{}, Data: values}
	return encodeRecord(v, text)
}

// expandDocument is an archived original document and one memory-record per
// chunk, as buildDocument writes them, with fixed bytes.
func expandDocument(id, eventAt string, chunks []string) []records.Upsert {
	day := eventAt[:10]
	content := strings.Join(chunks, "\n\n")
	parent := recordValue{ID: id, Kind: "memory-document", Title: "Original document", Content: content, Scope: "global", Date: &day, State: "archived", Signals: 1, Version: 1, CreatedAt: "2026-10-19T00:00:00Z", UpdatedAt: "2026-10-19T00:00:00Z", History: []any{}, Data: map[string]any{"source": "test", "eventAt": eventAt}}
	upserts := []records.Upsert{encodeRecord(parent, "")}
	offset := 0
	for i, chunk := range chunks {
		child := parent
		child.ID, child.Kind, child.Title, child.Content, child.State = id+"-0000"+string(rune('0'+i)), "memory-record", "Original record", chunk, "active"
		child.Data = map[string]any{"source": "test", "eventAt": eventAt, "documentID": id, "start": offset, "end": offset + len(chunk)}
		upserts = append(upserts, encodeRecord(child, chunk))
		offset += len(chunk) + len("\n\n")
	}
	return upserts
}

func expandLink(id, kind string, weight float64) map[string]any {
	return map[string]any{"id": id, "version": 1, "type": kind, "weight": weight}
}

// expandFixture stores two dated sessions of notes and a document written on
// the first session's day, in that order, with a writer link, a writer
// correction and overlay links. Searches for the codename find a2 and its
// session's talk of the codename; the launch notes share no word with them.
//
//	session A (2023-05-08): a0 a1 a2 a3 a4    a1 → b0 semantic (writer)
//	session B (2023-05-09): b0 b1 b2           b2 supersedes b0 (writer)
//	document doc (2023-05-08): doc-00000 doc-00001 doc-00002
//	overlay: a2 → b1 temporal 0.8, a2 → doc-00001 causal 0.6
func expandFixture(t *testing.T) *records.Store {
	t.Helper()
	s, _ := expandFixtureIn(t)
	return s
}

// expandFixtureIn is expandFixture and the store's directory.
func expandFixtureIn(t *testing.T) (*records.Store, string) {
	t.Helper()
	s, dir := agentStore(t)
	a, b := "2023-05-08T13:56:00Z", "2023-05-09T10:00:00Z"
	upserts := []records.Upsert{
		expandNote("a0", "Caroline: good morning, how was your weekend", a, nil),
		expandNote("a1", "Melanie: we picked a codename for the rocket project", a, map[string]any{"links": []any{expandLink("b0", "semantic", 0.5)}}),
		expandNote("a2", "Caroline: the codename is ORBIT", a, nil),
		expandNote("a3", "Melanie: launch window opens Friday at dawn", a, nil),
		expandNote("a4", "Caroline: sounds great, see you there", a, nil),
		expandNote("b0", "Melanie: rocket budget review went fine", b, nil),
		expandNote("b1", "Caroline: launch moved to Monday", b, nil),
		expandNote("b2", "Melanie: rocket budget review was redone", b, map[string]any{"mnemonSupersedes": map[string]any{"id": "b0", "version": 1}}),
	}
	upserts = append(upserts, expandDocument("doc", "2023-05-08T18:00:00Z", []string{"Notes: codename review meeting", "Notes: launch checklist signed", "Notes: wrap up and travel"})...)
	if _, err := s.Put(context.Background(), records.PutRequest{Upsert: upserts}); err != nil {
		t.Fatal(err)
	}
	overlay := map[string]records.DerivedMetadata{"a2": {Version: 1, Links: []records.Link{{ID: "b1", Version: 1, Type: "temporal", Weight: 0.8}, {ID: "doc-00001", Version: 1, Type: "causal", Weight: 0.6}}}}
	if err := s.ReplaceDerivedMetadata(context.Background(), "consolidation", overlay); err != nil {
		t.Fatal(err)
	}
	return s, dir
}

// expandJudge calls needed what names ORBIT or a launch, and no longer
// current what was redone.
func expandJudge() *scriptedJudge {
	return &scriptedJudge{
		answer: func(text string, _ int) float64 {
			if strings.Contains(text, "ORBIT") || strings.Contains(text, "launch") {
				return 0.9
			}
			return 0.2
		},
		superseded: func(text string) float64 {
			if strings.Contains(text, "redone") {
				return 0.7
			}
			return 0
		},
	}
}

func expandPlanner() *limitPlanner {
	return replies("codename project\nNOTE: the codename is ORBIT", "NEED: the codename\nNEED: the launch day", "codename rocket")
}

// pinnedView is a View's JSON with its clock readings zeroed.
func pinnedView(t *testing.T, view View) string {
	t.Helper()
	view.Stats.QueryMS, view.Stats.ModelMS = 0, 0
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Without view.expand a read is what it was before evidence expansion
// existed: these hashes were taken at 1f0c9a26, before any expansion code.
func TestReadSimpleWithoutExpandUnchanged(t *testing.T) {
	s := expandFixture(t)
	for name, tc := range map[string]struct {
		config Config
		want   string
	}{
		"judged": {Config{}, "3152098fef8497187f4ece79e44f4ce162672daebe33d6c133a10fdd89a499f7"},
		"fusion": {Config{View: &ViewConfig{MaxItems: 8, Fusion: "rrf"}}, "e56741bc64f56a5a5cc56e0b5a34ebaf76145b69690cdb0de069b6a4bd5120a9"},
		"index":  {Config{Index: &records.IndexPolicy{Version: 1, Lexical: true, Relations: true, Supersedes: true}, View: &ViewConfig{Intent: "question"}}, "c5f600208ddd868356ffc40dd14b54a0b49271ee288d7ce1824a730a361399c3"},
	} {
		config := tc.config
		config.Version, config.Strategy = 1, "simple"
		a, err := New(s, config, Models{Planner: expandPlanner(), Judge: expandJudge()}, nil)
		if err != nil {
			t.Fatal(err)
		}
		view, err := a.Read(context.Background(), ReadRequest{Question: "What is the codename and when is the launch?"})
		if err != nil {
			t.Fatal(name, err)
		}
		if got := pinnedView(t, view); got != tc.want {
			raw, _ := json.Marshal(view)
			t.Errorf("%s: %s\n%s", name, got, raw)
		}
	}
}

func TestExpandConfig(t *testing.T) {
	s, _ := agentStore(t)
	both := Models{Planner: expandPlanner(), Judge: expandJudge()}
	// Defaults fill in the agent's copy, not the caller's.
	expand := &ExpandConfig{Kinds: []string{"adjacent", "supersedes"}}
	a, err := New(s, Config{Version: 1, Strategy: "simple", View: &ViewConfig{Expand: expand}}, both, nil)
	if err != nil || !reflect.DeepEqual(*a.config.View.Expand, ExpandConfig{Kinds: []string{"adjacent", "supersedes"}, Limit: 24, Seeds: 16, AdjacentWindow: 1}) || expand.Limit != 0 || expand.Seeds != 0 || expand.AdjacentWindow != 0 {
		t.Fatal(a, err, expand)
	}
	// A window and a count of events are settings of their kinds.
	a, err = New(s, Config{Version: 1, Strategy: "simple", View: &ViewConfig{Expand: &ExpandConfig{Kinds: []string{"events", "causal"}}}}, both, nil)
	if err != nil || !reflect.DeepEqual(*a.config.View.Expand, ExpandConfig{Kinds: []string{"events", "causal"}, Limit: 24, Seeds: 16, EventsTop: 10}) {
		t.Fatal(a, err)
	}
	every := []string{"adjacent", "temporal", "semantic", "causal", "entity", "supersedes", "events"}
	for _, good := range []ExpandConfig{{Kinds: every, Limit: 64, Seeds: 32, AdjacentWindow: 5, EventsTop: 40}, {Kinds: []string{"entity"}, Limit: 1, Seeds: 1}, {Kinds: []string{"events", "adjacent"}, AdjacentWindow: 1, EventsTop: 1}} {
		if _, err = New(s, Config{Version: 1, Strategy: "simple", View: &ViewConfig{Expand: &good}}, both, nil); err != nil {
			t.Error("valid expansion refused", good, err)
		}
	}
	for _, bad := range []ExpandConfig{
		{}, {Kinds: []string{}}, {Kinds: []string{"Adjacent"}}, {Kinds: []string{"related"}}, {Kinds: []string{"temporal", "temporal"}},
		{Kinds: []string{"adjacent"}, Limit: 65}, {Kinds: []string{"adjacent"}, Limit: -1},
		{Kinds: []string{"adjacent"}, Seeds: 33}, {Kinds: []string{"adjacent"}, Seeds: -1},
		{Kinds: []string{"adjacent"}, AdjacentWindow: 6}, {Kinds: []string{"adjacent"}, AdjacentWindow: -1}, {Kinds: []string{"causal"}, AdjacentWindow: 2},
		{Kinds: []string{"events"}, EventsTop: 41}, {Kinds: []string{"events"}, EventsTop: -1}, {Kinds: []string{"adjacent"}, EventsTop: 5},
		{Kinds: []string{"events", "events"}}, {Kinds: []string{"Events"}},
	} {
		if _, err = New(s, Config{Version: 1, Strategy: "simple", View: &ViewConfig{Expand: &bad}}, both, nil); err == nil {
			t.Error("invalid expansion accepted", bad)
		}
	}
	// Absent, it stays out of the config's JSON and so out of the policy
	// hash (TestReadSimpleWithoutExpandUnchanged pins the policies).
	if raw, _ := json.Marshal(ViewConfig{MaxItems: 16, MaxCharacters: 12000}); strings.Contains(string(raw), "expand") {
		t.Fatal(string(raw))
	}
	decoder := json.NewDecoder(strings.NewReader(`{"version":1,"strategy":"simple","view":{"expand":{"kinds":["adjacent","temporal","events"],"limit":8,"seeds":4,"adjacentWindow":2,"eventsTop":12}}}`))
	decoder.DisallowUnknownFields()
	var decoded Config
	if err = decoder.Decode(&decoded); err != nil || !reflect.DeepEqual(*decoded.View.Expand, ExpandConfig{Kinds: []string{"adjacent", "temporal", "events"}, Limit: 8, Seeds: 4, AdjacentWindow: 2, EventsTop: 12}) {
		t.Fatal(decoded, err)
	}
	decoder = json.NewDecoder(strings.NewReader(`{"version":1,"strategy":"simple","view":{"expand":{"kinds":["adjacent"],"depth":2}}}`))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&decoded); err == nil {
		t.Fatal("unknown expansion field accepted")
	}
	// A set expansion is part of the policy.
	plain, _ := New(s, Config{Version: 1, Strategy: "simple"}, both, nil)
	expanded, _ := New(s, Config{Version: 1, Strategy: "simple", View: &ViewConfig{Expand: &ExpandConfig{Kinds: []string{"adjacent"}}}}, both, nil)
	if (&simpleRun{agent: plain}).policy() == (&simpleRun{agent: expanded}).policy() {
		t.Fatal("expansion left out of the policy")
	}
}

// expandRead reads "What is the codename?" from the fixture, whose cues
// pool a1, a2 and doc-00000 only, with view settings and a judge.
func expandRead(t *testing.T, s *records.Store, view *ViewConfig, judge Decider) View {
	t.Helper()
	return expandAsk(t, s, "What is the codename?", "codename project\nNOTE: the codename is ORBIT", view, judge)
}

// expandAsk reads question from the fixture with the planner's cues.
func expandAsk(t *testing.T, s *records.Store, question, cues string, view *ViewConfig, judge Decider) View {
	t.Helper()
	planner := replies(cues, "NEED: the answer", "unrelated words")
	a, err := New(s, Config{Version: 1, Strategy: "simple", View: view}, Models{Planner: planner, Judge: judge}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.Read(context.Background(), ReadRequest{Question: question})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestExpansionSeedsAreNeededItems(t *testing.T) {
	scored := []*simpleItem{scoredItem("a", 0.9, 0, "a"), scoredItem("b", 0.4, 0, "b"), scoredItem("c", 0.5, 0.8, "c"), scoredItem("d", 0.9, 0, "d"), scoredItem("e", 0.95, 0, "e")}
	// Best needed first, ties in shortlist order; a superseded seed counts.
	if got := itemIDs(expansionSeeds(scored, 16)); !slices.Equal(got, []string{"e", "a", "d", "c"}) {
		t.Fatal(got)
	}
	if got := itemIDs(expansionSeeds(scored, 2)); !slices.Equal(got, []string{"e", "a"}) {
		t.Fatal(got)
	}
	if got := expansionSeeds([]*simpleItem{scoredItem("b", 0.49, 0, "b")}, 16); len(got) != 0 {
		t.Fatal(itemIDs(got))
	}
}

func TestRankExpansion(t *testing.T) {
	seeds := []*simpleItem{scoredItem("s0", 0.9, 0, ""), scoredItem("s1", 0.8, 0, ""), scoredItem("s2", 0.8, 0, "")}
	neighbors := map[string][]neighbor{
		"s0": {{"x", "temporal", 0.5, 1}, {"pooled", "adjacent", 1, 1}, {"y", "adjacent", 1, 1}, {"x", "semantic", 0.25, 1}},
		"s1": {{"x", "causal", 1, 1}, {"z", "entity", 1, 1}, {"w", "supersedes", 0.5, 1}},
		"s2": {{"v", "semantic", 1, 1}, {"w", "temporal", 0.5, 1}},
	}
	known := func(id string) bool { return id == "pooled" || id == "s1" }
	got := rankExpansion(seeds, neighbors, known, 64)
	// y 0.9 (s0); x max(0.45, 0.225, 0.8) = 0.8 by s1; v, z 0.8 (z's seed
	// ranks first); w 0.4 (s1 before s2). Kinds collect over every seed.
	want := []expansionCandidate{
		{id: "y", score: 0.9, seed: 0, distance: 1, kinds: []string{"adjacent"}},
		{id: "x", score: 0.8, seed: 1, distance: 1, kinds: []string{"temporal", "semantic", "causal"}},
		{id: "z", score: 0.8, seed: 1, distance: 1, kinds: []string{"entity"}},
		{id: "v", score: 0.8, seed: 2, distance: 1, kinds: []string{"semantic"}},
		{id: "w", score: 0.4, seed: 1, distance: 1, kinds: []string{"supersedes", "temporal"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
	if got := rankExpansion(seeds, neighbors, known, 2); !reflect.DeepEqual(got, want[:2]) {
		t.Fatalf("%+v", got)
	}
	// A wider adjacent window: at one score, the nearer first, then by id;
	// a link counts as one step. Reached twice by one seed, a record keeps
	// its nearer place.
	window := map[string][]neighbor{"s0": {{"p2", "adjacent", 1, 2}, {"p1", "adjacent", 1, 1}, {"n1", "adjacent", 1, 1}, {"n2", "adjacent", 1, 2}, {"l", "temporal", 1, 1}, {"p2", "semantic", 1, 1}}}
	got = rankExpansion(seeds[:1], window, known, 64)
	if !slices.Equal(candidateIDs(got), []string{"l", "n1", "p1", "p2", "n2"}) || got[3].distance != 1 || !slices.Equal(got[3].kinds, []string{"adjacent", "semantic"}) {
		t.Fatalf("%+v", got)
	}
}

func candidateIDs(list []expansionCandidate) []string {
	out := []string{}
	for _, c := range list {
		out = append(out, c.id)
	}
	return out
}

func TestAlternateExpansionSources(t *testing.T) {
	c := func(id string, kinds ...string) expansionCandidate { return expansionCandidate{id: id, kinds: kinds} }
	ids := candidateIDs
	seed := []expansionCandidate{c("s1", "adjacent"), c("s2", "causal"), c("x", "adjacent"), c("s3", "causal")}
	events := []expansionCandidate{c("e1", "events"), c("x", "events"), c("e2", "events")}
	// Seed first, then turn by turn; a record both sources propose is
	// picked once, with both sources' kinds; a source's turn takes its
	// next record not picked yet.
	got := alternate(seed, events, 64)
	if !slices.Equal(ids(got), []string{"s1", "e1", "s2", "x", "s3", "e2"}) || !slices.Equal(got[3].kinds, []string{"adjacent", "events"}) {
		t.Fatalf("%+v", got)
	}
	if got = alternate(seed, events, 3); !slices.Equal(ids(got), []string{"s1", "e1", "s2"}) {
		t.Fatalf("%+v", got)
	}
	// An exhausted source leaves the rest to the other.
	if got = alternate(seed[:1], events, 64); !slices.Equal(ids(got), []string{"s1", "e1", "x", "e2"}) {
		t.Fatalf("%+v", got)
	}
	if got = alternate(seed, events[:1], 64); !slices.Equal(ids(got), []string{"s1", "e1", "s2", "x", "s3"}) {
		t.Fatalf("%+v", got)
	}
	// One source alone is that source, cut to the limit.
	if got = alternate(seed, nil, 2); !reflect.DeepEqual(got, seed[:2]) {
		t.Fatalf("%+v", got)
	}
	if got = alternate(nil, events, 2); !reflect.DeepEqual(got, events[:2]) {
		t.Fatalf("%+v", got)
	}
}

func TestReadSimpleExpandsFromNeededEvidence(t *testing.T) {
	s := expandFixture(t)
	every := &ViewConfig{Expand: &ExpandConfig{Kinds: []string{"adjacent", "temporal", "semantic", "causal", "entity", "supersedes"}}}
	// Without expansion the launch notes stay out: no cue reaches them.
	plain := expandRead(t, s, nil, expandJudge())
	if got := recordIDs(plain.Records); !slices.Equal(got, []string{"a2", "a1", "doc-00000"}) {
		t.Fatal(got)
	}
	judge := expandJudge()
	view := expandRead(t, s, every, judge)
	// a2 is the one seed: its next record a3 (0.9 × 1), its overlay links
	// b1 (temporal, 0.9 × 0.8) and doc-00001 (causal, 0.9 × 0.6); a1, the
	// record before it, is pooled. JEV calls all three needed.
	if got := recordIDs(view.Records); !slices.Equal(got, []string{"a2", "a3", "b1", "doc-00001", "a1", "doc-00000"}) {
		t.Fatal(got)
	}
	stats := view.Stats
	if stats.Expanded != 3 || stats.ExpandedNeeded != 3 || !reflect.DeepEqual(stats.ExpandKinds, map[string]int{"adjacent": 1, "temporal": 1, "causal": 1}) || stats.DecisionCalls != plain.Stats.DecisionCalls+1 || stats.Candidates != plain.Stats.Candidates || stats.Degraded != nil {
		t.Fatalf("%+v %+v", stats, plain.Stats)
	}
	if view.Records[1].Score != 0.9 || view.Records[1].Parent != nil || view.Records[3].Parent == nil || view.Records[3].Parent.ID != "doc" {
		t.Fatal(view.Records)
	}
	// The expansion is judged with judgeAll's questions, in candidate order.
	last := judge.calls[len(judge.calls)-1]
	texts := []judgeCandidate{}
	for _, text := range []string{"Melanie: launch window opens Friday at dawn", "Caroline: launch moved to Monday", "Notes: launch checklist signed"} {
		texts = append(texts, judgeCandidate{Source: judgeSource, Text: text})
	}
	state, questions := batchRequest([]Message{user("What is the codename?")}, texts)
	if raw, _ := json.Marshal(state); last.Raw != string(raw) || !reflect.DeepEqual(last.Questions, questions) {
		t.Fatal(last.Raw)
	}
	// Fusion places them as judged items by their needed answer.
	fusion := expandRead(t, s, &ViewConfig{Fusion: "rrf", Expand: every.Expand}, expandJudge())
	if got := recordIDs(fusion.Records); !slices.Contains(got, "a3") || !slices.Contains(got, "b1") || !slices.Contains(got, "doc-00001") || len(got) != 6 {
		t.Fatal(got)
	}
	// Only the kinds asked for are followed.
	temporal := expandRead(t, s, &ViewConfig{Expand: &ExpandConfig{Kinds: []string{"temporal"}}}, expandJudge())
	if got := recordIDs(temporal.Records); !slices.Equal(got, []string{"a2", "b1", "a1", "doc-00000"}) || !reflect.DeepEqual(temporal.Stats.ExpandKinds, map[string]int{"temporal": 1}) {
		t.Fatal(got, temporal.Stats)
	}
	// Adjacent alone: a2's session neighbors are a1 (pooled) and a3; the
	// document written the same day is another session.
	adjacent := expandRead(t, s, &ViewConfig{Expand: &ExpandConfig{Kinds: []string{"adjacent"}}}, expandJudge())
	if got := recordIDs(adjacent.Records); !slices.Equal(got, []string{"a2", "a3", "a1", "doc-00000"}) || adjacent.Stats.Expanded != 1 {
		t.Fatal(got, adjacent.Stats)
	}
	// The stats are absent without expansion.
	if raw, _ := json.Marshal(plain.Stats); strings.Contains(string(raw), "expand") {
		t.Fatal(string(raw))
	}
}

func TestReadSimpleComposesExpansionsOnMerit(t *testing.T) {
	s := expandFixture(t)
	// JEV needs ORBIT alone, the rest at 0.2: the launch notes are judged
	// and fill the View like the shortlist's records, after the pooled ones
	// at the same answer (ties keep the judged list's order).
	orbit := func(launch float64) *scriptedJudge {
		judge := expandJudge()
		judge.answer = func(text string, _ int) float64 {
			switch {
			case strings.Contains(text, "ORBIT"):
				return 0.9
			case strings.Contains(text, "launch"):
				return launch
			}
			return 0.05
		}
		return judge
	}
	expand := &ExpandConfig{Kinds: []string{"adjacent", "temporal", "causal"}}
	view := expandRead(t, s, &ViewConfig{Expand: expand}, orbit(0.05))
	if got := recordIDs(view.Records); !slices.Equal(got, []string{"a2", "a1", "doc-00000", "a3", "b1", "doc-00001"}) || view.Stats.Expanded != 3 || view.Stats.ExpandedNeeded != 0 || view.Stats.ExpandShown != 3 {
		t.Fatal(got, view.Stats)
	}
	// An expansion JEV answers 0.3 goes before a pooled record at 0.05:
	// with three slots the fill is theirs.
	plain := expandRead(t, s, &ViewConfig{MaxItems: 3}, orbit(0.3))
	view = expandRead(t, s, &ViewConfig{MaxItems: 3, Expand: expand}, orbit(0.3))
	if got := recordIDs(plain.Records); !slices.Equal(got, []string{"a2", "a1", "doc-00000"}) {
		t.Fatal(got)
	}
	if got := recordIDs(view.Records); !slices.Equal(got, []string{"a2", "a3", "b1"}) || view.Records[1].Score != 0.3 || view.Stats.ExpandShown != 2 {
		t.Fatal(got, view.Stats)
	}
	// With fusion the judged order holds them too.
	fused := expandRead(t, s, &ViewConfig{MaxItems: 16, Fusion: "rrf", Expand: expand}, orbit(0.3))
	if got := recordIDs(fused.Records); !slices.Contains(got, "a3") || !slices.Contains(got, "b1") || !slices.Contains(got, "doc-00001") || fused.Stats.ExpandShown != 3 {
		t.Fatal(got, fused.Stats)
	}
}

func TestReadSimpleExpandsWriterAndOverlayLinks(t *testing.T) {
	s := expandFixture(t)
	// JEV also needs the rocket: a1 is a seed after a2, and its writer link
	// reaches b0 (semantic, 0.9 × 0.5); a2's overlay links reach b1 (0.72)
	// and doc-00001 (0.54).
	rocket := expandJudge()
	rocket.answer = func(text string, _ int) float64 {
		if strings.Contains(text, "ORBIT") || strings.Contains(text, "launch") || strings.Contains(text, "rocket") {
			return 0.9
		}
		return 0.2
	}
	links := &ExpandConfig{Kinds: []string{"temporal", "semantic", "causal"}}
	view := expandRead(t, s, &ViewConfig{Expand: links}, rocket)
	if got := recordIDs(view.Records); !slices.Equal(got, []string{"a2", "a1", "b1", "doc-00001", "b0", "doc-00000"}) || !reflect.DeepEqual(view.Stats.ExpandKinds, map[string]int{"temporal": 1, "semantic": 1, "causal": 1}) {
		t.Fatal(got, view.Stats)
	}
	// Seeds bounds the records expanded from: a2, ranked first, alone.
	view = expandRead(t, s, &ViewConfig{Expand: &ExpandConfig{Kinds: links.Kinds, Seeds: 1}}, rocket)
	if got := recordIDs(view.Records); !slices.Equal(got, []string{"a2", "a1", "b1", "doc-00001", "doc-00000"}) || view.Stats.Expanded != 2 {
		t.Fatal(got, view.Stats)
	}
	// Limit bounds the neighbors judged: b1 is the best.
	view = expandRead(t, s, &ViewConfig{Expand: &ExpandConfig{Kinds: links.Kinds, Limit: 1}}, rocket)
	if got := recordIDs(view.Records); !slices.Equal(got, []string{"a2", "a1", "b1", "doc-00000"}) || view.Stats.Expanded != 1 {
		t.Fatal(got, view.Stats)
	}
	// A correction is followed both ways. "What was redone?" pools b2 (and
	// a0, "was"): from b2, which corrects b0, to b0, which JEV needs.
	corrections := &ViewConfig{Expand: &ExpandConfig{Kinds: []string{"supersedes"}}}
	view = expandAsk(t, s, "What was redone?", "redone\nNOTE: it was redone", corrections, rocket)
	if got := recordIDs(view.Records); !slices.Equal(got, []string{"b0", "a0"}) || !reflect.DeepEqual(view.Stats.ExpandKinds, map[string]int{"supersedes": 1}) || view.Stats.ExpandedNeeded != 1 {
		t.Fatal(got, view.Stats)
	}
	// From b0 back to b2, which JEV needs but calls no longer current: it
	// is judged and withdrawn.
	view = expandAsk(t, s, "What went fine?", "went fine\nNOTE: it went fine", corrections, rocket)
	if got := recordIDs(view.Records); !slices.Equal(got, []string{"b0"}) || view.Stats.Expanded != 1 || view.Stats.ExpandedNeeded != 1 {
		t.Fatal(got, view.Stats)
	}
}

func TestExpansionDegradesWithoutFailing(t *testing.T) {
	s := expandFixture(t)
	every := &ViewConfig{Expand: &ExpandConfig{Kinds: []string{"adjacent", "temporal", "causal"}}}
	plain := expandRead(t, s, nil, expandJudge())
	// The expansion's judgement fails (only its call shows a launch note):
	// the read goes on without it.
	launchDown := func(cancel context.CancelFunc) Decider {
		judge := expandJudge()
		return decisionFunc(func(ctx context.Context, state any, questions map[string]Question) (map[string]float64, Usage, error) {
			if raw, _ := json.Marshal(state); strings.Contains(string(raw), "launch window") {
				if cancel != nil {
					cancel()
					return nil, Usage{}, ctx.Err()
				}
				return nil, Usage{}, errors.New("judge down")
			}
			return judge.Decide(ctx, state, questions)
		})
	}
	view := expandRead(t, s, every, launchDown(nil))
	if !slices.Equal(recordIDs(view.Records), recordIDs(plain.Records)) || !slices.Equal(view.Stats.Degraded, []string{"expansion"}) || view.Stats.Expanded != 0 {
		t.Fatal(recordIDs(view.Records), view.Stats)
	}
	if raw, _ := json.Marshal(view.Stats); strings.Contains(string(raw), "expandKinds") {
		t.Fatal(string(raw))
	}
	// A canceled read fails.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, err := New(s, Config{Version: 1, Strategy: "simple", View: every}, Models{Planner: replies("codename project\nNOTE: the codename is ORBIT", "NEED: the answer", "unrelated words"), Judge: launchDown(cancel)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Read(ctx, ReadRequest{Question: "What is the codename?"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

// expansionRun is a judged run over a store whose seed note links to 50
// launch notes (weights falling with their number), the seed alone pooled
// and judged needed.
func expansionRun(t *testing.T, judge Decider) (*simpleRun, []*simpleItem) {
	t.Helper()
	return expansionRunWith(t, judge, &ExpandConfig{Kinds: []string{"temporal"}, Limit: 64})
}

// expansionRunWith is expansionRun with expand settings.
func expansionRunWith(t *testing.T, judge Decider, expand *ExpandConfig) (*simpleRun, []*simpleItem) {
	t.Helper()
	s, _ := agentStore(t)
	ctx := context.Background()
	upserts := []records.Upsert{expandNote("seed", "the codename is ORBIT", "2023-05-08T13:56:00Z", nil)}
	links := []records.Link{}
	for i := 0; i < 50; i++ {
		id := fmt.Sprintf("n%02d", i)
		upserts = append(upserts, expandNote(id, fmt.Sprintf("launch note %02d", i), "2023-06-01T10:00:00Z", nil))
		links = append(links, records.Link{ID: id, Version: 1, Type: "temporal", Weight: float64(50-i) / 50})
	}
	revision, err := s.Put(ctx, records.PutRequest{Upsert: upserts})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ReplaceDerivedMetadata(ctx, "consolidation", map[string]records.DerivedMetadata{"seed": {Version: 1, Links: links}}); err != nil {
		t.Fatal(err)
	}
	a, err := New(s, Config{Version: 1, Strategy: "simple", View: &ViewConfig{Expand: expand}}, Models{Planner: expandPlanner(), Judge: judge}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := testRun(a, user("What is the codename?"))
	r.revision = revision
	values, err := evidence(ctx, s, revision, records.Scope{}, []string{"seed"})
	if err != nil || len(values) != 1 {
		t.Fatal(values, err)
	}
	seed := &simpleItem{ID: "seed", Evidence: values[0], Needed: 0.9, Judged: true}
	r.items["seed"], r.order = seed, []string{"seed"}
	return r, []*simpleItem{seed}
}

func TestExpansionWithinTheDecisionBudget(t *testing.T) {
	ctx := context.Background()
	// Fifty neighbors are two judgement batches (40 and 10).
	judge := expandJudge()
	r, scored := expansionRun(t, judge)
	got, err := r.expandEvidence(ctx, scored, stopRounds)
	if err != nil || len(got) != 51 || judge.count() != 2 || r.stats.Expanded != 50 || r.stats.ExpandedNeeded != 50 || r.stats.Degraded != nil {
		t.Fatal(len(got), err, judge.count(), r.stats)
	}
	if got[1].ID != "n00" || got[50].ID != "n49" || r.items["n49"] != got[50] || slices.Contains(r.order, "n00") {
		t.Fatal(itemIDs(got))
	}
	// One call left judges the best 40; the rest are dropped.
	judge = expandJudge()
	r, scored = expansionRun(t, judge)
	r.stats.DecisionCalls = simpleDecisionBudget - 1
	got, err = r.expandEvidence(ctx, scored, stopRounds)
	if err != nil || len(got) != 41 || got[40].ID != "n39" || judge.count() != 1 || r.stats.Expanded != 40 || r.items["n40"] != nil || !slices.Equal(r.stats.Degraded, []string{"expansion"}) {
		t.Fatal(len(got), err, judge.count(), r.stats)
	}
	// None left: nothing is read or judged.
	judge = expandJudge()
	r, scored = expansionRun(t, judge)
	r.stats.DecisionCalls = simpleDecisionBudget
	got, err = r.expandEvidence(ctx, scored, stopRounds)
	if err != nil || len(got) != 1 || judge.count() != 0 || r.stats.Expanded != 0 || r.stats.QueryMS != 0 || !slices.Equal(r.stats.Degraded, []string{"expansion"}) {
		t.Fatal(len(got), err, judge.count(), r.stats)
	}
	// Neighbors that cannot be read degrade too.
	judge = expandJudge()
	r, scored = expansionRun(t, judge)
	r.revision = "nonsense"
	got, err = r.expandEvidence(ctx, scored, stopRounds)
	if err != nil || len(got) != 1 || judge.count() != 0 || !slices.Equal(r.stats.Degraded, []string{"expansion"}) {
		t.Fatal(len(got), err, judge.count(), r.stats)
	}
	// No needed item, no expansion.
	judge = expandJudge()
	r, scored = expansionRun(t, judge)
	scored[0].Needed = 0.4
	if got, err = r.expandEvidence(ctx, scored, stopRounds); err != nil || len(got) != 1 || judge.count() != 0 || r.stats.Degraded != nil || r.stats.QueryMS != 0 {
		t.Fatal(len(got), err, judge.count(), r.stats)
	}
}

func TestReadSimpleExpandsAdjacentWindow(t *testing.T) {
	s := expandFixture(t)
	judge := expandJudge()
	// a2's session is a0 a1 a2 a3 a4, a1 pooled: a3 is one step away, a0
	// and a4 two (by id at the same distance). JEV needs a3 alone.
	view := expandRead(t, s, &ViewConfig{Expand: &ExpandConfig{Kinds: []string{"adjacent"}, AdjacentWindow: 2}}, judge)
	last, texts := judge.calls[len(judge.calls)-1], []string{}
	for _, candidate := range last.State.Candidates {
		texts = append(texts, candidate.Text)
	}
	if !slices.Equal(texts, []string{"Melanie: launch window opens Friday at dawn", "Caroline: good morning, how was your weekend", "Caroline: sounds great, see you there"}) {
		t.Fatal(texts)
	}
	// a0 and a4 (0.2) fill after the pooled records at the same answer.
	if got := recordIDs(view.Records); !slices.Equal(got, []string{"a2", "a3", "a1", "doc-00000", "a0", "a4"}) || view.Stats.Expanded != 3 || view.Stats.ExpandedNeeded != 1 || view.Stats.ExpandShown != 3 || !reflect.DeepEqual(view.Stats.ExpandKinds, map[string]int{"adjacent": 3}) {
		t.Fatal(got, view.Stats)
	}
}

// Reads with view.expand but none of stage 4b's settings (gate, target,
// reserve), pinned by hash: taken at f330d564 and re-pinned only where a
// change was meant (every judged expansion composes on merit, and
// stats.expandShown), so nothing else moves them.
func TestReadSimpleExpandWithoutStage4bUnchanged(t *testing.T) {
	s := expandFixture(t)
	events, overlay := eventsStore(t)
	two := replies("codename project\nNOTE: the codename is ORBIT", "NEED: the codename\nNEED: the launch day", "codename rocket")
	for name, tc := range map[string]struct {
		store   *records.Store
		overlay *Overlay
		view    *ViewConfig
		want    string
	}{
		"judged":  {s, nil, &ViewConfig{Expand: &ExpandConfig{Kinds: []string{"adjacent", "temporal", "causal"}, AdjacentWindow: 2}}, "b2be452b616b7b8d1281089ed574aa8538abd30374faf16142507de11bf60006"},
		"fusion":  {s, nil, &ViewConfig{MaxItems: 5, Fusion: "rrf", Expand: &ExpandConfig{Kinds: []string{"adjacent", "temporal", "causal"}}}, "d29cb344f04e74ab2e54b459a28d2a6c6d899c99897188747349203818cf4b28"},
		"events":  {events, overlay, &ViewConfig{Expand: &ExpandConfig{Kinds: []string{"adjacent", "events"}, EventsTop: 3}}, "2157e4c5e704353741e663e1e27cbf82933dc4fadbb43da099b552c234f187ef"},
		"limited": {s, nil, &ViewConfig{MaxItems: 3, Expand: &ExpandConfig{Kinds: []string{"adjacent", "temporal", "causal"}, Limit: 2}}, "1cec11e29ff55e1dfb0140f0bdd49abbc2391864062c6d16d6a542eda00bad78"},
	} {
		config := Config{View: tc.view}
		var a *Agent
		if tc.overlay != nil {
			a = layerAgent(t, tc.store, tc.overlay, config, two, expandJudge())
		} else {
			config.Version, config.Strategy = 1, "simple"
			var err error
			if a, err = New(tc.store, config, Models{Planner: two, Judge: expandJudge()}, nil); err != nil {
				t.Fatal(err)
			}
		}
		view, err := a.Read(context.Background(), ReadRequest{Question: "What is the codename?"})
		if err != nil {
			t.Fatal(name, err)
		}
		if got := pinnedView(t, view); got != tc.want {
			raw, _ := json.Marshal(view)
			t.Errorf("%s: %s\n%s", name, got, raw)
		}
	}
}

// The memory service writes each Add of a conversation as its own document
// of one session: adjacency follows the session across them.
func TestAdjacencySpansConversationAdds(t *testing.T) {
	s, _ := agentStore(t)
	ctx := context.Background()
	at := time.Date(2023, 5, 8, 13, 0, 0, 0, time.UTC)
	ids := []string{}
	for i := 0; i < 3; i++ {
		when := at.Add(time.Duration(i) * time.Minute)
		result, err := WriteConversation(ctx, s, ConversationRequest{ID: fmt.Sprintf("add%d", i), SessionID: "session-1", Messages: []ConversationMessage{{Role: "user", Content: fmt.Sprintf("turn %d about the launch plan", i), At: &when}}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, result.Records[0].ID)
	}
	near, err := s.Adjacent(ctx, "", records.Scope{}, ids[1:2], 1)
	if err != nil || len(near[ids[1]]) != 2 || near[ids[1]][0].ID != ids[0] || near[ids[1]][1].ID != ids[2] {
		t.Fatal(near, err)
	}
}
