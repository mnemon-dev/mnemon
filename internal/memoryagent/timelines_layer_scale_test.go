package memoryagent

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/records"
)

// view.layer "scaled" multiplies the budgets of the consolidated layer's
// blocks by the View's scale; what the layer selects, and so every model
// request it makes, is unchanged.

func TestViewLayerConfig(t *testing.T) {
	s, _ := agentStore(t)
	both := Models{Planner: codenamePlanner("NEED: the codename"), Judge: layerJudge()}
	timelines := &ConsolidationConfig{Mode: "timelines"}
	for _, good := range []ViewConfig{{}, {Layer: "scaled"}, {Layer: "scaled", MaxCharacters: 240000}} {
		a, err := New(s, Config{Version: 1, Strategy: "simple", Consolidation: timelines, View: &good}, both, nil)
		if err != nil || a.config.View.Layer != good.Layer {
			t.Error("valid layer refused", good, err)
		}
	}
	for _, bad := range []string{"Scaled", "scale", " scaled", "reference", "none"} {
		_, err := New(s, Config{Version: 1, Strategy: "simple", Consolidation: timelines, View: &ViewConfig{Layer: bad}}, both, nil)
		if err == nil || !strings.Contains(err.Error(), `layer "" or "scaled"`) {
			t.Error("invalid layer accepted", bad, err)
		}
	}
	// Without the timelines layer the setting could only be inert.
	for _, without := range []Config{{}, {Consolidation: &ConsolidationConfig{}}, {Consolidation: timelines, Policy: &ReadPolicy{}}} {
		without.Version, without.Strategy, without.View = 1, "simple", &ViewConfig{Layer: "scaled"}
		if _, err := New(s, without, both, nil); err == nil || !strings.Contains(err.Error(), "timelines or layered consolidation") {
			t.Error("layer without the timelines layer accepted", without.Consolidation, without.Policy, err)
		}
	}
	// Absent, it stays out of the config's JSON and so out of the policy
	// hash (TestReadSimpleWithoutExpandUnchanged pins the policies).
	if raw, _ := json.Marshal(ViewConfig{MaxItems: 16, MaxCharacters: 12000}); strings.Contains(string(raw), "layer") {
		t.Fatal(string(raw))
	}
	var decoded Config
	if err := json.Unmarshal([]byte(`{"view":{"layer":"scaled"}}`), &decoded); err != nil || decoded.View.Layer != "scaled" {
		t.Fatal(decoded.View, err)
	}
}

// scaleAgent is a simple agent with the timelines layer and view.
func scaleAgent(t *testing.T, view ViewConfig) *Agent {
	t.Helper()
	s, _ := agentStore(t)
	return layerAgent(t, s, nil, Config{View: &view}, completeFunc(nil), layerJudge())
}

func TestLayerScale(t *testing.T) {
	for _, tc := range []struct {
		layer        string
		chars, scale int
	}{
		// s is the View's characters over 12,000, rounded down, at least 1;
		// without "scaled" always 1.
		{"", 240000, 1}, {"", 12000, 1},
		{"scaled", 240000, 20}, {"scaled", 12000, 1}, {"scaled", 0, 1}, {"scaled", 11999, 1}, {"scaled", 1000, 1}, {"scaled", 1, 1},
		{"scaled", 23999, 1}, {"scaled", 24000, 2}, {"scaled", 35999, 2}, {"scaled", 1000000, 83},
	} {
		a := scaleAgent(t, ViewConfig{MaxCharacters: tc.chars, Layer: tc.layer})
		s := a.layerScale()
		budgets := a.layerBudgets()
		want := layerBudgets{instructions: 1500 * s, directory: 1500 * s, timelines: 3500 * s, thread: 1500 * s, values: 1500 * s, profiles: 3500 * s}
		if s != tc.scale || budgets != want {
			t.Error(tc, s, budgets)
		}
		// The bound gate and target read follows the budgets: the four
		// budgets, the timelines' 2 over, and the two headers.
		if bound := a.layerBound(); bound != 8000*s+59 || bound != budgets.blocksMax(false) {
			t.Error(tc, bound)
		}
	}
	// The reference budgets and their bound are the author's.
	if referenceBudgets != (layerBudgets{1500, 1500, 3500, 1500, 1500, 3500}) || layerBlocksMax != 8059 || referenceBudgets.blocksMax(false) != layerBlocksMax {
		t.Fatal(referenceBudgets, layerBlocksMax)
	}
}

// scaledLayerMemory is a consolidated scope whose every block passes its
// reference budget: 16 directives of 100 units (lines of 116), 40 topics
// with titles of 60 (directory lines of 76, 90 for a span), a topic of 16
// events of 120 and two more of 9 (lines of 135: the first over a thread's
// 1500, all three over the timelines' 3500), and 12 keys with a value of 130
// (lines of 152).
func scaledLayerMemory() timelineMemory {
	m := timelineMemory{Facts: map[string][]factValue{}, FactOrder: []string{}}
	for i := range 16 {
		m.Directives = append(m.Directives, directive{Text: sized(fmt.Sprintf("d%02d", i), 100), Date: "2024/01/01", Records: []string{fmt.Sprintf("d%02d", i)}})
	}
	for i := range 40 {
		id := fmt.Sprintf("T%d", i+1)
		m.Threads = append(m.Threads, thread{ID: id, Title: sized(id, 60)})
	}
	add := func(thread string, count int) {
		for k := range count {
			m.Events = append(m.Events, event{Thread: thread, Date: fmt.Sprintf("2024/02/%02d", k+1), Text: sized(fmt.Sprintf("%s-e%02d", thread, k), 120), Records: []string{fmt.Sprintf("%s-r%02d", thread, k)}})
		}
	}
	add("T1", 16)
	add("T2", 9)
	add("T3", 9)
	for i := 3; i < 40; i++ {
		add(fmt.Sprintf("T%d", i+1), 1)
	}
	for i := range 12 {
		key := fmt.Sprintf("key %02d", i)
		m.FactOrder = append(m.FactOrder, key)
		m.Facts[key] = []factValue{{Value: sized(fmt.Sprintf("v%02d", i), 130), Date: "2024/03/01", Records: []string{fmt.Sprintf("k%02d", i)}}}
	}
	return m
}

func TestScaledLayerShowsMore(t *testing.T) {
	m := scaledLayerMemory()
	threads, keys := []string{"T1", "T2", "T3"}, slices.Clone(m.FactOrder)
	byID := func(blocks []IndexItem) map[string]IndexItem {
		out := map[string]IndexItem{}
		for _, block := range blocks {
			out[block.ID] = block
		}
		return out
	}
	reference := byID(composeLayer(m, threads, keys, litSets{}, referenceBudgets))
	scaled := byID(composeLayer(m, threads, keys, litSets{}, referenceBudgets.times(3)))
	for id, limit := range map[string]int{"1-instructions": 1500, "2-topics": 1500, "3-timelines": 3500 + 2 + jsLen(timelinesHead), "4-values": 1500 + jsLen(valuesHead)} {
		if jsLen(reference[id].Text) > limit {
			t.Fatal(id, jsLen(reference[id].Text))
		}
	}
	// Under the reference budgets every block is cut.
	if strings.Contains(reference["1-instructions"].Text, "d15") || strings.Contains(reference["2-topics"].Text, sized("T40", 60)) ||
		strings.Contains(reference["3-timelines"].Text, "T3-e00") || strings.Contains(reference["3-timelines"].Text, "T1-e00") || strings.Contains(reference["4-values"].Text, "key 11") {
		t.Fatal("a reference block was not cut")
	}
	// Three times the budgets show everything the same selection chose,
	// in the same order and format.
	want := "Standing instructions and preferences the user stated:"
	for _, d := range m.Directives {
		want += "\n- (2024/01/01) " + d.Text
	}
	if scaled["1-instructions"].Text != want {
		t.Fatalf("%q", scaled["1-instructions"].Text)
	}
	for _, th := range m.Threads {
		if !strings.Contains(scaled["2-topics"].Text, "\n- "+th.Title+" (") {
			t.Fatal("topic left out", th.ID)
		}
	}
	timelines := timelinesHead
	for n, id := range threads {
		if n > 0 {
			timelines += "\n\n"
		}
		timelines += "Thread: " + sized(id, 60)
		for _, e := range m.Events {
			if e.Thread == id {
				timelines += "\n- " + e.Date + ": " + e.Text
			}
		}
	}
	if scaled["3-timelines"].Text != timelines {
		t.Fatalf("%q", scaled["3-timelines"].Text)
	}
	for _, key := range keys {
		if !strings.Contains(scaled["4-values"].Text, "\n- "+valueText(key, m.Facts[key])) {
			t.Fatal("value left out", key)
		}
	}
	// The block order and the linked records follow what is shown.
	if !slices.Equal(blockIDs(composeLayer(m, threads, keys, litSets{}, referenceBudgets.times(3))), []string{"1-instructions", "2-topics", "3-timelines", "4-values"}) ||
		len(linkedIDs(scaled["3-timelines"])) != 34 || len(linkedIDs(reference["3-timelines"])) >= 34 {
		t.Fatal(len(linkedIDs(scaled["3-timelines"])), len(linkedIDs(reference["3-timelines"])))
	}
}

// The thread floor does not scale: under twice the budgets a timeline is
// begun in a room of exactly 300 (A and B fill 3000 each, C 696, with the
// separators 6700 of 7000), not in 299.
func TestScaledLayerKeepsTheThreadFloor(t *testing.T) {
	timelines := func(c int) string {
		m := timelineMemory{Facts: map[string][]factValue{}, FactOrder: []string{}}
		for _, th := range []struct {
			id   string
			size int
		}{{"A", 3000 - 24}, {"B", 3000 - 24}, {"C", c}, {"D", 10}} {
			m.Threads = append(m.Threads, thread{ID: th.id, Title: th.id})
			m.Events = append(m.Events, event{Thread: th.id, Date: "2024/01/01", Text: sized(th.id, th.size)})
		}
		blocks := composeLayer(m, []string{"A", "B", "C", "D"}, nil, litSets{}, referenceBudgets.times(2))
		return blocks[len(blocks)-1].Text
	}
	if text := timelines(696 - 24); !strings.HasSuffix(text, "\n\nThread: D\n- 2024/01/01: "+sized("D", 10)) || jsLen(text) != jsLen(timelinesHead)+6700+2+34 {
		t.Fatalf("room 300: %d", jsLen(text))
	}
	if text := timelines(697 - 24); strings.Contains(text, "Thread: D") {
		t.Fatal("room 299 began a timeline")
	}
}

// Gate and target count only the leading records the layer's trim can never
// drop: within the View's characters less the most the scaled blocks take.
func TestScaledLayerBoundsTheWouldShowView(t *testing.T) {
	ctx := context.Background()
	scored := []*simpleItem{scoredItem("a", 0.9, 0, sized("a", 4000)), scoredItem("b", 0.8, 0, sized("b", 3941)), scoredItem("c", 0.7, 0, "c")}
	shown := func(view ViewConfig) []string {
		a := scaleAgent(t, view)
		r := &simpleRun{agent: a, req: ReadRequest{MaxItems: 16}, items: map[string]*simpleItem{}}
		got, err := r.wouldShow(ctx, scored)
		if err != nil {
			t.Fatal(err)
		}
		return itemIDs(got)
	}
	// 24,000 less 16,059 leaves 7,941: a and b, not c.
	if got := shown(ViewConfig{MaxCharacters: 24000, Layer: "scaled"}); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatal(got)
	}
	// The reference bound, 8,059, leaves 15,941.
	if got := shown(ViewConfig{MaxCharacters: 24000}); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatal(got)
	}
}

// recordingJudge records every decision request it passes on: its state
// and questions as JSON.
type recordingJudge struct {
	mu     sync.Mutex
	inner  Decider
	inputs []string
}

func (j *recordingJudge) Decide(ctx context.Context, state any, questions map[string]Question) (map[string]float64, Usage, error) {
	raw, _ := json.Marshal(state)
	asked, _ := json.Marshal(questions)
	j.mu.Lock()
	j.inputs = append(j.inputs, string(raw)+"\n"+string(asked))
	j.mu.Unlock()
	return j.inner.Decide(ctx, state, questions)
}

// requests is the judge's requests, sorted (judgements run in parallel).
func (j *recordingJudge) requests() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Sorted(slices.Values(j.inputs))
}

// plannerRequests is a scripted planner's calls, sorted.
func plannerRequests(p *limitPlanner) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := []string{}
	for _, call := range p.calls {
		out = append(out, fmt.Sprintf("%s\n%s\n%d", call.system, call.prompt, call.maxTokens))
	}
	return slices.Sorted(slices.Values(out))
}

// longLaunchMemory is layerMemory with twenty older launch events of 120
// units, citing nothing, before the launch topic's own: its timeline passes
// a thread's 1500.
func longLaunchMemory() timelineMemory {
	m := layerMemory()
	older := []event{}
	for i := range 20 {
		older = append(older, event{Thread: "T1", Date: fmt.Sprintf("2024/02/%02d", i+1), Text: sized(fmt.Sprintf("Launch prep %02d", i), 120)})
	}
	m.Events = append(older, m.Events...)
	return m
}

// layerReads reads "What is the codename?" from a store with memory under
// each view, with a recording judge around a new judge from judge, and
// returns the Views and the requests each read made of the judge and the
// planner.
func layerReads(t *testing.T, memory timelineMemory, consolidation string, needs string, judge func() *scriptedJudge, views ...ViewConfig) (out []View, judged, planned [][]string) {
	t.Helper()
	s, overlay := layerStore(t)
	ctx := context.Background()
	state, token, err := overlay.loadTimelines(ctx, records.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	state.Memory = memory
	if _, err := overlay.saveTimelines(ctx, records.Scope{}, token, state); err != nil {
		t.Fatal(err)
	}
	for _, view := range views {
		recording, planner := &recordingJudge{inner: judge()}, codenamePlanner(needs)
		a := layerAgent(t, s, overlay, Config{View: &view, Consolidation: &ConsolidationConfig{Mode: consolidation}}, planner, recording)
		got, err := a.Read(ctx, ReadRequest{Question: "What is the codename?"})
		if err != nil {
			t.Fatal(err)
		}
		out, judged, planned = append(out, got), append(judged, recording.requests()), append(planned, plannerRequests(planner))
	}
	return out, judged, planned
}

func TestReadSimpleScaledLayerMakesTheSameRequests(t *testing.T) {
	for _, needs := range []string{
		// The records fill the View: the linked records are not judged.
		"NEED: the codename",
		// A plan with no need shows ORBIT alone: the linked launch record is
		// judged, and it is linked from what both Views show.
		"nothing stored is needed",
	} {
		views, judged, planned := layerReads(t, longLaunchMemory(), "timelines", needs, layerJudge, ViewConfig{MaxItems: 6, MaxCharacters: 24000}, ViewConfig{MaxItems: 6, MaxCharacters: 24000, Layer: "scaled"})
		reference, scaled := views[0], views[1]
		if !slices.Equal(judged[0], judged[1]) || !slices.Equal(planned[0], planned[1]) || len(judged[0]) < 3 {
			t.Fatal(needs, len(judged[0]), len(judged[1]))
		}
		// The same records; the scaled timeline shows the launch topic whole.
		if !slices.Equal(recordIDs(reference.Records), recordIDs(scaled.Records)) || reference.Policy == scaled.Policy {
			t.Fatal(needs, recordIDs(reference.Records), recordIDs(scaled.Records))
		}
		timelines := func(view View) string {
			return view.Index[slices.IndexFunc(view.Index, func(b IndexItem) bool { return b.ID == "3-timelines" })].Text
		}
		if strings.Contains(timelines(reference), "Launch prep 00") || !strings.Contains(timelines(scaled), "Launch prep 00") || !strings.Contains(timelines(reference), "Launch moved to Friday.") {
			t.Fatal(needs, timelines(reference))
		}
		if !slices.Equal(blockIDs(reference.Index), blockIDs(scaled.Index)) || reference.Stats.DecisionCalls != scaled.Stats.DecisionCalls {
			t.Fatal(needs, blockIDs(scaled.Index), reference.Stats, scaled.Stats)
		}
	}
}

// The linked-record join reads what the blocks show: when the scaled
// timeline shows events the reference cut, the records they cite are among
// those judged (24 at most, as before).
func TestScaledLayerJoinsWhatItShows(t *testing.T) {
	s, _ := agentStore(t)
	seedNote(t, s, "orbit", "codename ORBIT")
	memory := timelineMemory{Threads: []thread{{ID: "T1", Title: "Codename"}}, Facts: map[string][]factValue{}, FactOrder: []string{}}
	for i := range 30 {
		id := fmt.Sprintf("l%02d", i)
		seedNote(t, s, id, "linked note "+id)
		memory.Events = append(memory.Events, event{Thread: "T1", Date: "2024/01/01", Text: sized(id, 100), Records: []string{id}})
	}
	judged := func(view ViewConfig) []string {
		judge := &scriptedJudge{answer: func(text string, _ int) float64 {
			if strings.HasPrefix(text, "Codename: ") {
				return 0.9
			}
			return 0.1
		}}
		r, base := layerRun(t, s, memory, judge, view, "orbit")
		if _, err := r.layer(context.Background(), base); err != nil {
			t.Fatal(err)
		}
		if judge.count() != 2 {
			t.Fatal(judge.count())
		}
		ids := []string{}
		for _, c := range judge.calls[1].State.Candidates {
			ids = append(ids, strings.TrimPrefix(c.Text, "linked note "))
		}
		return ids
	}
	// A thread's 1500 hold the title (16) and the newest 12 events (115 a
	// line); twice that holds 25, of which the first 24 are judged.
	if got := judged(ViewConfig{MaxItems: 40, MaxCharacters: 24000}); !slices.Equal(got, noteRange("l", 18, 30)) {
		t.Fatal(got)
	}
	if got := judged(ViewConfig{MaxItems: 40, MaxCharacters: 24000, Layer: "scaled"}); !slices.Equal(got, noteRange("l", 5, 29)) {
		t.Fatal(got)
	}
}

// noteRange is the ids <prefix><from> to <prefix><to-1>, two digits each.
func noteRange(prefix string, from, to int) []string {
	out := []string{}
	for i := from; i < to; i++ {
		out = append(out, fmt.Sprintf("%s%02d", prefix, i))
	}
	return out
}
