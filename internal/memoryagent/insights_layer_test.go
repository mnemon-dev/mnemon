package memoryagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/records"
)

func TestComposeInsights(t *testing.T) {
	m := timelineMemory{Insights: []insight{
		{Text: "First conclusion.", Date: "2024/01/01", Records: []string{"a", "b"}},
		{Text: sized("long", 1480), Date: "2024/01/02", Records: []string{"c"}},
		{Text: "Third conclusion.", Date: "2024/01/03", Records: []string{"b", "d"}},
		{Text: "Never chosen.", Date: "2024/01/04", Records: []string{"e"}},
	}}
	// Chosen order; a line that would pass 1,500 characters (the header not
	// counted) is skipped and the next tried.
	block, shown := composeInsights(m, []string{"2", "1", "0"})
	want := "Conclusions drawn from the memory:\n- (2024/01/03) Third conclusion.\n- (2024/01/01) First conclusion."
	if block.ID != "5-insights" || block.Kind != "5-insights" || block.Text != want || shown != 2 || !slices.Equal(linkedIDs(block), []string{"b", "d", "a"}) {
		t.Fatalf("%q %d %v", block.Text, shown, linkedIDs(block))
	}
	// A line of exactly the budget fits alone.
	m.Insights[1].Text = sized("long", 1500-jsLen("\n- (2024/01/02) "))
	if block, shown = composeInsights(m, []string{"1", "0"}); shown != 1 || jsLen(block.Text) != jsLen(insightsHead)+layerInsights {
		t.Fatal(jsLen(block.Text), shown)
	}
	if _, shown = composeInsights(m, nil); shown != 0 {
		t.Fatal(shown)
	}
}

// insightReadJudge is layerJudge, with the insights' needed answers by
// their first word.
func insightReadJudge(answers map[string]float64) *scriptedJudge {
	layer := layerJudge()
	return &scriptedJudge{answer: func(text string, need int) float64 {
		if _, rest, ok := strings.Cut(text, ") "); ok && strings.HasPrefix(text, "(") {
			if p, ok := answers[strings.Fields(rest)[0]]; ok {
				return p
			}
		}
		return layer.answer(text, need)
	}}
}

// withInsights stores insights in the scope's state.
func withInsights(t *testing.T, overlay *Overlay, insights []insight) {
	t.Helper()
	state, token, err := overlay.loadTimelines(context.Background(), records.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	state.Mode, state.Memory.Insights = "layered", insights
	if _, err := overlay.saveTimelines(context.Background(), records.Scope{}, token, state); err != nil {
		t.Fatal(err)
	}
}

func insightConfig(view ViewConfig, insights bool) Config {
	return Config{View: &view, Consolidation: &ConsolidationConfig{Mode: "layered", Insights: insights}}
}

// callsWith is the judge's calls whose candidates come from source.
func callsWith(judge *scriptedJudge, source string) []judgeCall {
	out := []judgeCall{}
	for _, call := range judge.calls {
		if len(call.State.Candidates) > 0 && call.State.Candidates[0].Source == source {
			out = append(out, call)
		}
	}
	return out
}

func rawCalls(judge *scriptedJudge) []string {
	out := []string{}
	for _, call := range judge.calls {
		questions, _ := json.Marshal(call.Questions)
		out = append(out, call.Raw+"|"+string(questions))
	}
	slices.Sort(out)
	return out
}

func TestReadSimpleWithInsights(t *testing.T) {
	ctx := context.Background()
	s, overlay := layerStore(t)
	question := ReadRequest{Question: "What is the codename?"}
	answers := map[string]float64{"Codename": 0.9, "Choice": 0.95, "Plans": 0.2, "Weekly": 0.3, "Budget": 0.95}
	read := func(insights bool) (View, *scriptedJudge, *limitPlanner) {
		judge, planner := insightReadJudge(answers), codenamePlanner("NEED: the codename")
		view, err := layerAgent(t, s, overlay, insightConfig(ViewConfig{MaxItems: 6}, insights), planner, judge).Read(ctx, question)
		if err != nil {
			t.Fatal(err)
		}
		return view, judge, planner
	}
	base, _, _ := read(false)
	shown := recordIDs(base.Records)
	if shown[0] != "orbit" || slices.Contains(shown, "launch") || len(shown) != 6 {
		t.Fatal(shown)
	}
	withInsights(t, overlay, []insight{
		{Text: "Codename ORBIT stayed across plans.", Date: "2024/02/01", Records: []string{"orbit"}},
		{Text: "Plans kept changing every week.", Date: "2024/03/05", Records: []string{shown[1], "gone-1", shown[2]}},
		{Text: "Budget never revisited after January.", Date: "2024/01/10", Records: []string{"gone-1"}},
		{Text: "Choice of codename preceded launch planning.", Date: "2024/03/01", Records: []string{"launch"}},
		{Text: "Weekly calls kept going.", Date: "2024/03/02", Records: []string{shown[3]}},
	})
	// Off, stored insights change nothing.
	if view, _, _ := read(false); pinnedView(t, view) != pinnedView(t, base) {
		t.Fatal("stored insights changed a read without the setting")
	}
	view, judge, planner := read(true)
	// The fifth block: the needed insights by their answer (the choice's,
	// the last candidate, then the codename's), then the lit ones below 0.5
	// by the View records they link (plans: 2, weekly: 1). The budget's is
	// neither lit nor near the message: not a candidate, though JEV would
	// call it needed.
	if !slices.Equal(blockIDs(view.Index), []string{"1-instructions", "2-topics", "3-timelines", "4-values", "5-insights"}) {
		t.Fatal(blockIDs(view.Index))
	}
	want := "Conclusions drawn from the memory:\n- (2024/03/01) Choice of codename preceded launch planning.\n- (2024/02/01) Codename ORBIT stayed across plans.\n- (2024/03/05) Plans kept changing every week.\n- (2024/03/02) Weekly calls kept going."
	if got := view.Index[4]; got.Text != want || view.Stats.LayerInsights != 4 {
		t.Fatalf("%q %d", got.Text, view.Stats.LayerInsights)
	}
	// Its records name the insights' records the View shows (launch and
	// gone-1 are not shown), with their hashes; they join nothing.
	refs := map[string]Reference{}
	for _, record := range view.Records {
		refs[record.ID] = record.Reference
	}
	if want := []Reference{refs["orbit"], refs[shown[1]], refs[shown[2]], refs[shown[3]]}; !reflect.DeepEqual(view.Index[4].Records, want) || refs["orbit"].Hash == "" {
		t.Fatal(view.Index[4].Records, want)
	}
	if !reflect.DeepEqual(view.Records, base.Records) || view.Truncated != base.Truncated || !reflect.DeepEqual(view.Needs, base.Needs) {
		t.Fatal(recordIDs(view.Records))
	}
	// One judgement of its own: the lit insights by the View records they
	// link (ties in memory order), then those nearest the message, each
	// once; the needed question only.
	calls := callsWith(judge, "insight")
	if len(calls) != 1 || len(calls[0].Questions) != 4 {
		t.Fatal(calls)
	}
	texts := []string{}
	for _, c := range calls[0].State.Candidates {
		texts = append(texts, c.Text)
	}
	if !slices.Equal(texts, []string{"(2024/03/05) Plans kept changing every week.", "(2024/02/01) Codename ORBIT stayed across plans.", "(2024/03/02) Weekly calls kept going.", "(2024/03/01) Choice of codename preceded launch planning."}) {
		t.Fatalf("%q", texts)
	}
	for i := range 4 {
		q := calls[0].Questions[fmt.Sprintf("n%d", i)]
		if q.Instructions != neededQuestion(byID(fmt.Sprintf("c%d", i))) || q.Criteria["true"] != neededYes {
			t.Fatal(calls[0].Questions)
		}
	}
	// Every other request is the one the read makes without insights,
	// byte for byte: the planner's, the screen's, the judgement's and the
	// topics' and values' call.
	_, offJudge, offPlanner := read(false)
	other := slices.DeleteFunc(rawCalls(judge), func(raw string) bool { return strings.Contains(raw, `"source":"insight"`) })
	if !slices.Equal(other, rawCalls(offJudge)) || len(other) != judge.count()-1 || view.Stats.DecisionCalls != judge.count() {
		t.Fatal("the insights changed another JEV request")
	}
	plannerCalls := func(p *limitPlanner) []string {
		out := []string{}
		for _, call := range p.calls {
			out = append(out, fmt.Sprintf("%d|%s|%s", call.maxTokens, call.system, call.prompt))
		}
		slices.Sort(out)
		return out
	}
	if !slices.Equal(plannerCalls(planner), plannerCalls(offPlanner)) || len(planner.calls) == 0 {
		t.Fatal("the insights changed a planner request")
	}
}

func TestInsightsJoinNoRecords(t *testing.T) {
	ctx := context.Background()
	s, overlay := layerStore(t)
	// A plan with no need shows ORBIT alone; the launch record, linked from
	// a shown timeline, is judged and joins. An insight's records never
	// join, nor change what the join judges.
	withInsights(t, overlay, []insight{{Text: "Codename ORBIT matters most.", Date: "2024/03/01", Records: []string{"codename-020", "orbit", "launch"}}})
	read := func(insights bool) (View, *scriptedJudge) {
		judge := insightReadJudge(map[string]float64{"Codename": 0.9})
		judge.answer = func(text string, need int) float64 {
			if text == "codename 20" || strings.HasPrefix(text, "(") || text == "codename ORBIT" || text == launchText || strings.HasPrefix(text, "Codename launch: ") {
				return 0.9
			}
			return 0.1
		}
		view, err := layerAgent(t, s, overlay, insightConfig(ViewConfig{MaxItems: 6}, insights), codenamePlanner("nothing stored is needed"), judge).Read(ctx, ReadRequest{Question: "What is the codename?"})
		if err != nil {
			t.Fatal(err)
		}
		return view, judge
	}
	off, offJudge := read(false)
	on, onJudge := read(true)
	if !slices.Equal(recordIDs(on.Records), []string{"orbit", "launch"}) || !slices.Equal(recordIDs(off.Records), recordIDs(on.Records)) {
		t.Fatal(recordIDs(on.Records), recordIDs(off.Records))
	}
	join := func(judge *scriptedJudge) string {
		for _, call := range judge.calls {
			if strings.Contains(call.Raw, launchText) && !strings.Contains(call.Raw, `"source":"insight"`) && len(call.State.Candidates) == 1 {
				return call.Raw
			}
		}
		return ""
	}
	if join(onJudge) == "" || join(onJudge) != join(offJudge) {
		t.Fatal("the join changed:", join(onJudge), join(offJudge))
	}
	if got := on.Index[len(on.Index)-1]; got.ID != "5-insights" || !slices.Equal(linkedIDs(got), []string{"orbit", "launch"}) {
		t.Fatal(got)
	}
}

func TestReadSimpleInsightsDegrade(t *testing.T) {
	ctx := context.Background()
	s, overlay := layerStore(t)
	question := ReadRequest{Question: "What is the codename?"}
	withInsights(t, overlay, []insight{{Text: "Codename ORBIT stayed.", Date: "2024/02/01", Records: []string{"orbit"}}})
	// A failed judgement shows no insights, lit or not.
	good := insightReadJudge(map[string]float64{"Codename": 0.9})
	failing := decisionFunc(func(ctx context.Context, state any, questions map[string]Question) (map[string]float64, Usage, error) {
		raw, _ := json.Marshal(state)
		if strings.Contains(string(raw), `"source":"insight"`) {
			return nil, Usage{}, errors.New("judge down")
		}
		return good.Decide(ctx, state, questions)
	})
	view, err := layerAgent(t, s, overlay, insightConfig(ViewConfig{MaxItems: 6}, true), codenamePlanner("NEED: the codename"), failing).Read(ctx, question)
	if err != nil || len(view.Index) != 4 || !slices.Equal(view.Stats.Degraded, []string{"layer-insights"}) || view.Stats.LayerInsights != 0 {
		t.Fatal(blockIDs(view.Index), view.Stats.Degraded, err)
	}
	// So does one the budget cannot pay for: it is not asked.
	judge := insightReadJudge(map[string]float64{"Codename": 0.9})
	a := layerAgent(t, s, overlay, insightConfig(ViewConfig{MaxItems: 6}, true), codenamePlanner("NEED: the codename"), judge)
	r := &simpleRun{agent: a, req: ReadRequest{MaxItems: 6}, dialogue: []Message{user("What is the codename?")}, items: map[string]*simpleItem{}}
	r.stats.DecisionCalls = simpleDecisionBudget - 1
	snapshot, _ := s.Get(ctx, records.GetRequest{IDs: []string{}})
	r.revision = snapshot.Revision
	orbit, _ := evidence(ctx, s, snapshot.Revision, records.Scope{}, []string{"orbit"})
	if view, err = r.layer(ctx, View{Records: orbit, Needs: []Need{}}); err != nil || slices.Contains(blockIDs(view.Index), "5-insights") || !slices.Contains(view.Stats.Degraded, "layer-insights") || len(callsWith(judge, "insight")) != 0 || len(callsWith(judge, "topic")) != 1 {
		t.Fatal(blockIDs(view.Index), view.Stats.Degraded, err)
	}
	// With more candidates than one call judges, the judgement is paid for
	// whole or not asked: 45 lit insights take two calls, and one is left.
	many := []insight{}
	for i := range 45 {
		many = append(many, insight{Text: fmt.Sprintf("Codename note %d about the plans of that week.", i), Date: "2024/02/01", Records: []string{"orbit"}})
	}
	withInsights(t, overlay, many)
	judge = insightReadJudge(map[string]float64{"Codename": 0.9})
	a = layerAgent(t, s, overlay, insightConfig(ViewConfig{MaxItems: 6}, true), codenamePlanner("NEED: the codename"), judge)
	r = &simpleRun{agent: a, req: ReadRequest{MaxItems: 6}, revision: snapshot.Revision, dialogue: []Message{user("What is the codename?")}, items: map[string]*simpleItem{}}
	r.stats.DecisionCalls = simpleDecisionBudget - 2
	if view, err = r.layer(ctx, View{Records: orbit, Needs: []Need{}}); err != nil || slices.Contains(blockIDs(view.Index), "5-insights") || !slices.Contains(view.Stats.Degraded, "layer-insights") || len(callsWith(judge, "insight")) != 0 {
		t.Fatal(blockIDs(view.Index), view.Stats.Degraded, err, len(callsWith(judge, "insight")))
	}
	// Paid for, they are judged in two calls, and the shown ones fit the
	// block's budget.
	judge = insightReadJudge(map[string]float64{"Codename": 0.9})
	if view, err = layerAgent(t, s, overlay, insightConfig(ViewConfig{MaxItems: 6}, true), codenamePlanner("NEED: the codename"), judge).Read(ctx, question); err != nil || len(callsWith(judge, "insight")) != 2 || view.Stats.LayerInsights == 0 || view.Stats.LayerInsights == 45 || jsLen(view.Index[4].Text) > jsLen(insightsHead)+layerInsights {
		t.Fatal(err, len(callsWith(judge, "insight")), view.Stats.LayerInsights)
	}
	withInsights(t, overlay, []insight{{Text: "Codename ORBIT stayed.", Date: "2024/02/01", Records: []string{"orbit"}}})
	// A canceled read fails.
	canceled, cancel := context.WithCancel(ctx)
	cancelling := decisionFunc(func(ctx context.Context, state any, questions map[string]Question) (map[string]float64, Usage, error) {
		raw, _ := json.Marshal(state)
		if strings.Contains(string(raw), `"source":"insight"`) {
			cancel()
			return nil, Usage{}, canceled.Err()
		}
		return good.Decide(ctx, state, questions)
	})
	if _, err = layerAgent(t, s, overlay, insightConfig(ViewConfig{MaxItems: 6}, true), codenamePlanner("NEED: the codename"), cancelling).Read(canceled, question); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// Without stored insights, or with none to judge, nothing is asked and
	// nothing degrades.
	for _, stored := range [][]insight{nil, {{Text: "Unrelated weather.", Date: "2024/02/01", Records: []string{"gone-1"}}}} {
		withInsights(t, overlay, stored)
		judge := insightReadJudge(nil)
		view, err := layerAgent(t, s, overlay, insightConfig(ViewConfig{MaxItems: 6}, true), codenamePlanner("NEED: the codename"), judge).Read(ctx, question)
		if err != nil || len(view.Index) != 4 || view.Stats.Degraded != nil || len(callsWith(judge, "insight")) != 0 {
			t.Fatal(stored, blockIDs(view.Index), view.Stats.Degraded, err)
		}
	}
}

func TestInsightsCountTowardTheView(t *testing.T) {
	ctx := context.Background()
	s, overlay := layerStore(t)
	withInsights(t, overlay, []insight{{Text: "Codename ORBIT stayed.", Date: "2024/02/01", Records: []string{"orbit"}}})
	judge := insightReadJudge(map[string]float64{"Codename": 0.9})
	view, err := layerAgent(t, s, overlay, insightConfig(ViewConfig{MaxItems: 6}, true), codenamePlanner("NEED: the codename"), judge).Read(ctx, ReadRequest{Question: "What is the codename?"})
	if err != nil || view.Stats.LayerInsights != 1 {
		t.Fatal(err, view.Stats)
	}
	// A View just big enough for every block and the records but one: the
	// insights block's characters count, so a record gives way.
	tight := viewCharacters(view) - 1
	judge = insightReadJudge(map[string]float64{"Codename": 0.9})
	small, err := layerAgent(t, s, overlay, insightConfig(ViewConfig{MaxItems: 6, MaxCharacters: tight}, true), codenamePlanner("NEED: the codename"), judge).Read(ctx, ReadRequest{Question: "What is the codename?"})
	if err != nil || viewCharacters(small) > tight || len(small.Records) != len(view.Records)-1 || !small.Truncated || small.Index[4].Text != view.Index[4].Text {
		t.Fatal(err, viewCharacters(small), tight, recordIDs(small.Records))
	}
}

func TestNearestInsights(t *testing.T) {
	ctx := context.Background()
	s, _ := agentStore(t)
	server := &embeddingServer{}
	url := server.start(t, func(text string) []float64 {
		text = strings.TrimPrefix(strings.TrimPrefix(text, "search_query: "), "search_document: ")
		switch {
		case strings.HasPrefix(text, "rocket?"), strings.HasPrefix(text, "Launch"):
			return []float64{1, 0}
		case strings.HasPrefix(text, "Budget"):
			return []float64{0.6, 0.8}
		}
		return []float64{0, 1}
	})
	embedding := &records.EmbeddingConfig{URL: url, Model: "test", QueryPrefix: "search_query: ", DocumentPrefix: "search_document: "}
	a, err := New(s, Config{Version: 1, Strategy: "simple", Embedding: embedding, Consolidation: &ConsolidationConfig{Mode: "layered", Insights: true}}, Models{Planner: completeFunc(nil), Judge: decisionFunc(fixtureJudge)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	texts := []string{"Weather stayed mild.", "Budget grew.", "Launch slipped twice."}
	// By meaning: the launch, then the budget; ties keep memory order.
	r := testRun(a, user("rocket?"))
	if got := r.nearestInsights(ctx, "rocket?", texts, false); !slices.Equal(got, []int{2, 1, 0}) || r.stats.Degraded != nil {
		t.Fatal(got, r.stats.Degraded)
	}
	// The texts are embedded and kept as the layer's documents.
	kept, err := s.DerivedVectors(ctx, texts, *embedding, false)
	if err != nil || slices.ContainsFunc(kept, func(v []float32) bool { return v == nil }) {
		t.Fatal("insight vectors not kept", err)
	}
	// When the topics fell back to shared words, so do the insights,
	// without embedding: shared words, positive scores only.
	server.mu.Lock()
	server.inputs = nil
	server.mu.Unlock()
	r = testRun(a, user("When did the launch slip?"))
	if got := r.nearestInsights(ctx, "When did the launch slip?", texts, true); !slices.Equal(got, []int{2}) || len(requests(server)) != 0 {
		t.Fatal(got, requests(server))
	}
	// A failing embedder falls back to shared words (layer-lexical).
	server.mu.Lock()
	server.fail = true
	server.mu.Unlock()
	r = testRun(a, user("rocket launch?"))
	if got := r.nearestInsights(ctx, "rocket launch?", []string{"Launch moved.", "Rain."}, false); !slices.Equal(got, []int{0}) || !slices.Equal(r.stats.Degraded, []string{"layer-lexical"}) {
		t.Fatal(got, r.stats.Degraded)
	}
	// At most 12.
	many := []string{}
	for i := range 20 {
		many = append(many, fmt.Sprintf("Launch %d.", i))
	}
	if got := testRun(a, user("launch")).nearestInsights(ctx, "launch", many, true); len(got) != 12 {
		t.Fatal(got)
	}
}

func TestWarmLayerEmbedsInsightTexts(t *testing.T) {
	ctx := context.Background()
	s, overlay := layerStore(t)
	withInsights(t, overlay, []insight{{Text: "Codename ORBIT stayed.", Date: "2024/02/01", Records: []string{"orbit"}}, {Text: "Launch slipped.", Date: "2024/03/05", Records: []string{"launch"}}})
	server := &embeddingServer{}
	embedding := &records.EmbeddingConfig{URL: server.start(t, func(string) []float64 { return []float64{1, 0} }), Model: "test", QueryPrefix: "search_query: ", DocumentPrefix: "search_document: "}
	off := layerAgent(t, s, overlay, Config{Embedding: embedding, Consolidation: &ConsolidationConfig{Mode: "layered"}}, completeFunc(nil), layerJudge())
	if err := off.WarmLayer(ctx, records.Scope{}); err != nil {
		t.Fatal(err)
	}
	layer := documents(server)
	if slices.Contains(layer, "search_document: Launch slipped.") {
		t.Fatal(layer)
	}
	// With insights, their texts too, after the layer's own.
	server.mu.Lock()
	server.inputs = nil
	server.mu.Unlock()
	fresh := layerAgent(t, s, overlay, Config{Embedding: embedding, Consolidation: &ConsolidationConfig{Mode: "layered", Insights: true}}, completeFunc(nil), layerJudge())
	if err := fresh.WarmLayer(ctx, records.Scope{}); err != nil {
		t.Fatal(err)
	}
	if got := documents(server); !slices.Equal(got, []string{"search_document: Codename ORBIT stayed.", "search_document: Launch slipped."}) {
		t.Fatal(got)
	}
}
