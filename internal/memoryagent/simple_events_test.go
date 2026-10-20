package memoryagent

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/records"
)

// eventsMemory is a consolidated memory over expandFixture's records. Event
// 5 repeats event 3 and event 6 cites nothing: both are left out.
func eventsMemory() timelineMemory {
	return timelineMemory{
		Threads: []thread{{ID: "T1", Title: "Codename launch"}, {ID: "T2", Title: "Rocket budget"}, {ID: "T3", Title: "Codename"}, {ID: "T4", Title: "Weekend"}, {ID: "T5", Title: "Codename team travel plans"}},
		Events: []event{
			{Thread: "T4", Date: "2023/05/08", Text: "Caroline asked about a weekend trip.", Records: []string{"a0"}},
			{Thread: "T3", Date: "2023/05/08", Text: "Codename ORBIT picked.", Records: []string{"a2"}},
			{Thread: "T1", Date: "2023/05/08", Text: "Rocket with that codename lifts off Friday at dawn.", Records: []string{"a3"}},
			{Thread: "T1", Date: "2023/05/09", Text: "Launch moved to Monday.", Records: []string{"b1", "doc-00001"}},
			{Thread: "T2", Date: "2023/05/09", Text: "Budget review redone.", Records: []string{"b2", "b0"}},
			{Thread: "T1", Date: "2023/05/09", Text: "Launch moved to Monday.", Records: []string{"b1", "doc-00001"}},
			{Thread: "T2", Date: "2023/05/09", Text: "An unsourced remark."},
			{Thread: "T5", Date: "2023/05/08", Text: "Codename team flies home after a long review meeting.", Records: []string{"doc-00002"}},
		},
		Facts: map[string][]factValue{}, FactOrder: []string{}, Directives: []directive{},
	}
}

// eventsStore is expandFixture with eventsMemory consolidated for the
// default scope.
func eventsStore(t *testing.T) (*records.Store, *Overlay) {
	t.Helper()
	s, dir := expandFixtureIn(t)
	overlay, err := OpenOverlay(filepath.Join(dir, "memory-index.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { overlay.Close() })
	state := emptyTimelineState()
	state.Memory = eventsMemory()
	if _, err := overlay.saveTimelines(context.Background(), records.Scope{}, "", state); err != nil {
		t.Fatal(err)
	}
	return s, overlay
}

// eventVectors embeds by subject: launch talk on the first axis (the
// Friday event nearest the question), the budget on the second, anything
// else on the third.
func eventVectors(text string) []float64 {
	text = strings.TrimPrefix(strings.TrimPrefix(text, "search_query: "), "search_document: ")
	switch {
	case strings.HasPrefix(text, "When is the launch"):
		return []float64{1, 0, 0}
	case strings.Contains(text, "lifts off Friday"):
		return []float64{1, 0, 0}
	case strings.Contains(text, "Launch moved"):
		return []float64{0.8, 0.6, 0}
	case strings.Contains(text, "Budget"):
		return []float64{0, 1, 0}
	}
	return []float64{0, 0, 1}
}

// eventsRun is a run of question over eventsStore with view expand events,
// a2 pooled.
func eventsRun(t *testing.T, s *records.Store, overlay *Overlay, embedding *records.EmbeddingConfig, question string) *simpleRun {
	t.Helper()
	config := Config{Embedding: embedding, View: &ViewConfig{Expand: &ExpandConfig{Kinds: []string{"events"}}}}
	r := testRun(layerAgent(t, s, overlay, config, completeFunc(nil), expandJudge()), user(question))
	r.items["a2"] = &simpleItem{ID: "a2"}
	return r
}

// documents is the document texts an embedding server was asked for.
func documents(server *embeddingServer) []string {
	return slices.DeleteFunc(requests(server), func(input string) bool { return !strings.HasPrefix(input, "search_document: ") })
}

// requests is every text an embedding server was asked for.
func requests(server *embeddingServer) []string {
	server.mu.Lock()
	defer server.mu.Unlock()
	return slices.Clone(server.inputs)
}

func TestEventCandidatesRankByMeaning(t *testing.T) {
	ctx := context.Background()
	s, overlay := eventsStore(t)
	server := &embeddingServer{}
	embedding := &records.EmbeddingConfig{URL: server.start(t, eventVectors), Model: "test", QueryPrefix: "search_query: ", DocumentPrefix: "search_document: "}
	r := eventsRun(t, s, overlay, embedding, "Current date: 2023/05/10\nWhen is the launch?")
	// The Friday event (cosine 1), then the Monday one (0.8, its records in
	// their order); the others (0) in memory order, a2 pooled, the repeated
	// and the unsourced events left out.
	got, err := r.eventCandidates(ctx, 10, r.pooled)
	if err != nil || !slices.Equal(candidateIDs(got), []string{"a3", "b1", "doc-00001", "a0", "b2", "b0", "doc-00002"}) || !slices.Equal(got[0].kinds, []string{"events"}) {
		t.Fatalf("%+v %v", got, err)
	}
	// The event texts were embedded once each, as documents, and stored;
	// the query is the message without its date line.
	texts := []string{
		"search_document: Weekend: 2023/05/08 Caroline asked about a weekend trip.",
		"search_document: Codename: 2023/05/08 Codename ORBIT picked.",
		"search_document: Codename launch: 2023/05/08 Rocket with that codename lifts off Friday at dawn.",
		"search_document: Codename launch: 2023/05/09 Launch moved to Monday.",
		"search_document: Rocket budget: 2023/05/09 Budget review redone.",
		"search_document: Codename team travel plans: 2023/05/08 Codename team flies home after a long review meeting.",
	}
	if docs := documents(server); !slices.Equal(docs, texts) || !slices.Contains(requests(server), "search_query: When is the launch?") {
		t.Fatal(requests(server))
	}
	if r.stats.ExpandEventsEmbedded != 6 || r.stats.ExpandEventsLexical || r.stats.Degraded != nil {
		t.Fatalf("%+v", r.stats)
	}
	// eventsTop bounds the events; the next read embeds nothing.
	r = eventsRun(t, s, overlay, embedding, "When is the launch?")
	if got, err = r.eventCandidates(ctx, 2, r.pooled); err != nil || !slices.Equal(candidateIDs(got), []string{"a3", "b1", "doc-00001"}) {
		t.Fatalf("%+v %v", got, err)
	}
	if len(documents(server)) != len(texts) || r.stats.ExpandEventsEmbedded != 0 {
		t.Fatal(documents(server), r.stats)
	}
	// A message that is only a date line asks nothing, as it gives no
	// message search.
	r = eventsRun(t, s, overlay, embedding, "Current date: 2023/05/10\n  ")
	if got, err = r.eventCandidates(ctx, 10, r.pooled); err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
}

func TestEventCandidatesFallBackToSharedWords(t *testing.T) {
	ctx := context.Background()
	s, overlay := eventsStore(t)
	// Without an embedder shared words rank them (BM25): only the launch
	// events share "launch", the Monday one the shorter, so first.
	r := eventsRun(t, s, overlay, nil, "When is the launch?")
	got, err := r.eventCandidates(ctx, 10, r.pooled)
	if err != nil || !slices.Equal(candidateIDs(got), []string{"b1", "doc-00001", "a3"}) || !r.stats.ExpandEventsLexical || r.stats.Degraded != nil {
		t.Fatalf("%+v %v %+v", got, err, r.stats)
	}
	// A failing embedder falls back the same way, and the read is degraded.
	server := &embeddingServer{fail: true}
	embedding := &records.EmbeddingConfig{URL: server.start(t, eventVectors), Model: "test", QueryPrefix: "search_query: ", DocumentPrefix: "search_document: "}
	r = eventsRun(t, s, overlay, embedding, "When is the launch?")
	got, err = r.eventCandidates(ctx, 10, r.pooled)
	if err != nil || !slices.Equal(candidateIDs(got), []string{"b1", "doc-00001", "a3"}) || !r.stats.ExpandEventsLexical || !slices.Equal(r.stats.Degraded, []string{"expansion-lexical"}) {
		t.Fatalf("%+v %v %+v", got, err, r.stats)
	}
	// A canceled read fails.
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	r = eventsRun(t, s, overlay, embedding, "When is the launch?")
	if _, err = r.eventCandidates(canceled, 10, r.pooled); err == nil {
		t.Fatal("canceled read went on")
	}
}

func TestEventCandidatesFillVectorsWithinABound(t *testing.T) {
	ctx := context.Background()
	s, overlay := eventsStore(t)
	server := &embeddingServer{}
	embedding := &records.EmbeddingConfig{URL: server.start(t, eventVectors), Model: "test", QueryPrefix: "search_query: ", DocumentPrefix: "search_document: "}
	bound := expandEventsEmbed
	expandEventsEmbed = 4
	defer func() { expandEventsEmbed = bound }()
	// Six texts lack vectors: the first read stores four and ranks by
	// shared words, degraded; the second stores the other two and ranks by
	// meaning.
	r := eventsRun(t, s, overlay, embedding, "When is the launch?")
	got, err := r.eventCandidates(ctx, 10, r.pooled)
	if err != nil || !slices.Equal(candidateIDs(got), []string{"b1", "doc-00001", "a3"}) || r.stats.ExpandEventsEmbedded != 4 || !r.stats.ExpandEventsLexical || !slices.Equal(r.stats.Degraded, []string{"expansion-lexical"}) {
		t.Fatalf("%+v %v %+v", got, err, r.stats)
	}
	r = eventsRun(t, s, overlay, embedding, "When is the launch?")
	got, err = r.eventCandidates(ctx, 10, r.pooled)
	if err != nil || candidateIDs(got)[0] != "a3" || r.stats.ExpandEventsEmbedded != 2 || r.stats.ExpandEventsLexical || r.stats.Degraded != nil || len(documents(server)) != 6 {
		t.Fatalf("%+v %v %+v", got, err, r.stats)
	}
}

func TestEventCandidatesNeedAState(t *testing.T) {
	ctx := context.Background()
	server := &embeddingServer{}
	embedding := &records.EmbeddingConfig{URL: server.start(t, eventVectors), Model: "test", QueryPrefix: "search_query: ", DocumentPrefix: "search_document: "}
	// No consolidated state: nothing, quietly.
	s, dir := expandFixtureIn(t)
	overlay, err := OpenOverlay(filepath.Join(dir, "memory-index.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer overlay.Close()
	for _, o := range []*Overlay{overlay, nil} {
		r := eventsRun(t, s, o, embedding, "When is the launch?")
		if got, err := r.eventCandidates(ctx, 10, r.pooled); err != nil || len(got) != 0 || r.stats.Degraded != nil || r.stats.ExpandEventsLexical {
			t.Fatal(got, err, r.stats)
		}
	}
	// Without the timelines layer (no timelines consolidation, or the
	// policy's consolidation off) the state is not read.
	s, overlay = eventsStore(t)
	for _, config := range []Config{
		{Consolidation: &ConsolidationConfig{}, View: &ViewConfig{Expand: &ExpandConfig{Kinds: []string{"events"}}}},
		{Policy: &ReadPolicy{}, View: &ViewConfig{Expand: &ExpandConfig{Kinds: []string{"events"}}}},
	} {
		config.Embedding = embedding
		r := testRun(layerAgent(t, s, overlay, config, completeFunc(nil), expandJudge()), user("When is the launch?"))
		if got, err := r.eventCandidates(ctx, 10, r.pooled); err != nil || len(got) != 0 {
			t.Fatal(got, err)
		}
	}
	if got := requests(server); len(got) != 0 {
		t.Fatal(got)
	}
	// A state that does not load degrades the expansion.
	if _, err := overlay.db.Exec(`UPDATE timelines SET payload=payload||' '`); err != nil {
		t.Fatal(err)
	}
	r := eventsRun(t, s, overlay, embedding, "When is the launch?")
	if got, err := r.eventCandidates(ctx, 10, r.pooled); err != nil || len(got) != 0 || !slices.Equal(r.stats.Degraded, []string{"expansion"}) {
		t.Fatal(got, err, r.stats)
	}
}

func TestWarmLayerEmbedsEventTexts(t *testing.T) {
	ctx := context.Background()
	s, overlay := eventsStore(t)
	server := &embeddingServer{}
	embedding := &records.EmbeddingConfig{URL: server.start(t, eventVectors), Model: "test", QueryPrefix: "search_query: ", DocumentPrefix: "search_document: "}
	// Without the events kind the warm-up embeds the layer's texts alone.
	plain := layerAgent(t, s, overlay, Config{Embedding: embedding, View: &ViewConfig{Expand: &ExpandConfig{Kinds: []string{"adjacent"}}}}, completeFunc(nil), expandJudge())
	if err := plain.WarmLayer(ctx, records.Scope{}); err != nil {
		t.Fatal(err)
	}
	layer := len(documents(server))
	if layer == 0 || slices.Contains(documents(server), "search_document: Weekend: 2023/05/08 Caroline asked about a weekend trip.") {
		t.Fatal(documents(server))
	}
	// With it, the event texts too; a read then embeds none.
	a := layerAgent(t, s, overlay, Config{Embedding: embedding, View: &ViewConfig{Expand: &ExpandConfig{Kinds: []string{"events"}}}}, completeFunc(nil), expandJudge())
	if err := a.WarmLayer(ctx, records.Scope{}); err != nil {
		t.Fatal(err)
	}
	if got := len(documents(server)); got != layer+6 {
		t.Fatal(got, layer, documents(server))
	}
	r := testRun(a, user("When is the launch?"))
	if got, err := r.eventCandidates(ctx, 1, r.pooled); err != nil || !slices.Equal(candidateIDs(got), []string{"a3"}) || r.stats.ExpandEventsEmbedded != 0 || len(documents(server)) != layer+6 {
		t.Fatal(got, err, r.stats)
	}
}

func TestReadSimpleExpandsEvents(t *testing.T) {
	s, overlay := eventsStore(t)
	read := func(expand *ExpandConfig) (View, *scriptedJudge) {
		t.Helper()
		judge := expandJudge()
		planner := replies("codename project\nNOTE: the codename is ORBIT", "NEED: the answer", "unrelated words")
		a := layerAgent(t, s, overlay, Config{View: &ViewConfig{Expand: expand}}, planner, judge)
		view, err := a.Read(context.Background(), ReadRequest{Question: "What is the codename?"})
		if err != nil {
			t.Fatal(err)
		}
		return view, judge
	}
	// The expansion's judgement is the second call with superseded
	// questions (the first judges the shortlist); its candidates are the
	// items it asks about (its peers are asked nothing).
	expansion := func(judge *scriptedJudge) []string {
		calls := slices.DeleteFunc(slices.Clone(judge.calls), func(call judgeCall) bool {
			return !slices.ContainsFunc(slices.Collect(maps.Keys(call.Questions)), func(key string) bool { return strings.HasPrefix(key, "s") })
		})
		if len(calls) != 2 {
			t.Fatal(len(calls))
		}
		texts := []string{}
		for i, candidate := range calls[1].State.Candidates {
			if _, asked := calls[1].Questions[fmt.Sprintf("n%d", i)]; asked {
				texts = append(texts, candidate.Text)
			}
		}
		return texts
	}
	launch, travel := "Melanie: launch window opens Friday at dawn", "Notes: wrap up and travel"
	// Shared words rank the events that name the codename: the shortest
	// (a2's, pooled), then a3's, then doc-00002's (then b1's, past the
	// three asked for).
	view, judge := read(&ExpandConfig{Kinds: []string{"events"}, EventsTop: 3})
	if got := expansion(judge); !slices.Equal(got, []string{launch, travel}) {
		t.Fatal(got)
	}
	// a3 is needed; doc-00002 (0.2) fills after the pooled records at the
	// same answer. (b1 and doc-00001 are the layer's linked records.)
	if got := recordIDs(view.Records); !slices.Equal(got[:5], []string{"a2", "a3", "a1", "doc-00000", "doc-00002"}) || view.Stats.Expanded != 2 || view.Stats.ExpandedNeeded != 1 || view.Stats.ExpandShown != 2 || !reflect.DeepEqual(view.Stats.ExpandKinds, map[string]int{"events": 2}) || !view.Stats.ExpandEventsLexical {
		t.Fatal(got, view.Stats)
	}
	// With seeds too, the sources take turns, seed first: a3 is a2's next
	// record and an event's; doc-00002 comes from the events.
	view, judge = read(&ExpandConfig{Kinds: []string{"adjacent", "events"}, EventsTop: 3})
	if got := expansion(judge); !slices.Equal(got, []string{launch, travel}) || !reflect.DeepEqual(view.Stats.ExpandKinds, map[string]int{"adjacent": 1, "events": 2}) {
		t.Fatal(got, view.Stats)
	}
	// eventsTop 1 takes a2's event alone: nothing new.
	view, _ = read(&ExpandConfig{Kinds: []string{"events"}, EventsTop: 1})
	if view.Stats.Expanded != 0 || view.Stats.ExpandKinds != nil {
		t.Fatal(view.Stats)
	}
}
