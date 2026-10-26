package memoryagent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/records"
)

// traceFixture is a store of notes with a consolidated memory over them,
// whose read takes need rounds: the codename is found at once, the launch day
// only by the planner's rewritten search.
type traceFixture struct {
	store   *records.Store
	overlay *Overlay
}

func newTraceFixture(t *testing.T) *traceFixture {
	t.Helper()
	s, dir := agentStore(t)
	seedNotes(t, s, "mission", 30)
	seedNote(t, s, "orbit", "the mission codename is ORBIT")
	seedNote(t, s, "liftoff", "lift-off happens on Tuesday")
	seedNote(t, s, "pref", "prefers terse replies") // no search finds it
	overlay, err := OpenOverlay(filepath.Join(dir, "memory-index.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { overlay.Close() })
	state := emptyTimelineState()
	state.Memory = traceMemory()
	if _, err := overlay.saveTimelines(context.Background(), records.Scope{}, "", state); err != nil {
		t.Fatal(err)
	}
	return &traceFixture{store: s, overlay: overlay}
}

func traceMemory() timelineMemory {
	return timelineMemory{
		Threads: []thread{{ID: "T1", Title: "Mission"}, {ID: "T2", Title: "Launch"}},
		Events: []event{
			{Thread: "T1", Date: "2024/01/01", Text: "The mission was named ORBIT.", Records: []string{"orbit"}},
			{Thread: "T2", Date: "2024/01/02", Text: "Lift-off is planned for Tuesday.", Records: []string{"liftoff"}},
		},
		Facts: map[string][]factValue{"mission · codename": {
			{Value: "ORBIT", Date: "2024/01/01", Records: []string{"orbit"}},
			{Value: "ORBIT-2", Date: "2024/01/05", Records: []string{"mission-001"}, Conflict: true},
		}},
		FactOrder:  []string{"mission · codename"},
		Directives: []directive{{Text: "Keep it short.", Date: "2024/01/01", Records: []string{"pref"}}},
	}
}

// read runs the fixture's read with new scripted models, traced or not.
func (f *traceFixture) read(t *testing.T, traced bool) (View, *ReadTrace, *limitPlanner, *scriptedJudge) {
	t.Helper()
	planner := replies("mission codename\nlaunch date\nNOTE: the mission codename is ORBIT", "NEED: the codename\nNEED: the launch day", "lift-off day")
	judge := &scriptedJudge{answer: func(text string, need int) float64 {
		switch {
		case need == 0 && strings.Contains(text, "ORBIT"),
			need == 1 && strings.Contains(text, "Tuesday"),
			need == -1 && (strings.Contains(text, "ORBIT") || strings.Contains(text, "Tuesday")):
			return 0.9
		}
		return 0.1
	}}
	// The View has room left after its records: the layer's linked records
	// are judged.
	config := Config{Version: 1, Strategy: "simple", View: &ViewConfig{MaxItems: 100, MaxCharacters: 12000, Order: "time"}, Consolidation: &ConsolidationConfig{Mode: "timelines"}}
	a, err := New(f.store, config, Models{Planner: planner, Judge: judge}, f.overlay)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var trace *ReadTrace
	if traced {
		trace = &ReadTrace{}
		ctx = WithReadTrace(ctx, trace)
	}
	view, err := a.Read(ctx, ReadRequest{Question: "What is the mission codename, and when is the launch?"})
	if err != nil {
		t.Fatal(err)
	}
	return view, trace, planner, judge
}

// viewBytes is the View as JSON without its timings, which no two reads share.
func viewBytes(t *testing.T, view View) string {
	t.Helper()
	view.Stats.QueryMS, view.Stats.ModelMS = 0, 0
	raw, err := json.Marshal(struct {
		View
		RankOrder []string
	}{view, view.RankOrder})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// calls are what the models were asked, in a stable order: the plan and the
// screen send their calls concurrently.
func calls(planner *limitPlanner, judge *scriptedJudge) []string {
	out := []string{}
	for _, call := range planner.calls {
		out = append(out, fmt.Sprintf("planner %s\x00%s\x00%d", call.system, call.prompt, call.maxTokens))
	}
	for _, call := range judge.calls {
		questions, _ := json.Marshal(call.Questions)
		out = append(out, "judge "+call.Raw+"\x00"+string(questions))
	}
	sort.Strings(out)
	return out
}

// The tracer only records: a traced read sends the models the same requests
// and composes the same View, byte for byte, as an untraced one.
func TestReadPathWithoutTracerIsUnchanged(t *testing.T) {
	f := newTraceFixture(t)
	plain, none, plainPlanner, plainJudge := f.read(t, false)
	if none != nil {
		t.Fatal("an untraced read has a trace")
	}
	traced, trace, tracedPlanner, tracedJudge := f.read(t, true)
	if len(trace.Stages) == 0 {
		t.Fatal("the traced read recorded nothing")
	}
	if a, b := viewBytes(t, plain), viewBytes(t, traced); a != b {
		t.Fatalf("the trace changed the View:\n%s\n%s", a, b)
	}
	if a, b := calls(plainPlanner, plainJudge), calls(tracedPlanner, tracedJudge); !slices.Equal(a, b) {
		t.Fatalf("the trace changed the model requests:\n%q\n%q", a, b)
	}
	if len(plain.Index) == 0 || plain.Stats.DecisionCalls == 0 || len(plainPlanner.calls) < 3 {
		t.Fatal("the fixture's read did not reach the layer and the need loop", plain.Index, plain.Stats)
	}
}

// The trace names the stages in the order the read first enters them, and
// their calls and use add up to the read's.
func TestReadTraceRecordsTheStages(t *testing.T) {
	f := newTraceFixture(t)
	view, trace, planner, _ := f.read(t, true)
	names := []string{}
	planned, decided := 0, 0
	var usage Usage
	for _, stage := range trace.Stages {
		names = append(names, stage.Name)
		planned += stage.PlannerCalls
		decided += stage.DecisionCalls
		usage.add(stage.Usage)
		if stage.Duration <= 0 {
			t.Errorf("stage %s took no time", stage.Name)
		}
	}
	if want := []string{"cues", "candidates", "judge", "need", "compose"}; !slices.Equal(names, want) {
		t.Fatal(names)
	}
	if planned != view.Stats.PlannerCalls || decided != view.Stats.DecisionCalls || usage != view.Stats.Usage || planned != len(planner.calls) {
		t.Fatal(planned, decided, usage, view.Stats)
	}
	if trace.Revision != view.Revision {
		t.Fatal(trace.Revision, view.Revision)
	}

	// The plan: the message, the note and the two searches; two needs.
	cueKinds := []string{}
	for _, c := range trace.Cues {
		cueKinds = append(cueKinds, c.Kind+":"+c.Text)
	}
	if !slices.Equal(cueKinds, []string{"message:What is the mission codename, and when is the launch?", "note:the mission codename is ORBIT", "search:mission codename", "search:launch date"}) {
		t.Fatal(cueKinds)
	}
	if !reflect.DeepEqual(trace.Needs, []Need{{Text: "the codename"}, {Text: "the launch day"}}) || trace.Needless {
		t.Fatal(trace.Needs, trace.Needless)
	}

	// Each cue's search, its first page, and the pool.
	if len(trace.Searches) < len(trace.Cues)+1 {
		t.Fatal("no rewritten search", trace.Searches)
	}
	for i, c := range trace.Cues {
		s := trace.Searches[i]
		if s.Text != c.Text || s.Kind != c.Kind || len(s.First) > simplePageSize || s.Ranked < len(s.First) {
			t.Fatal(i, s)
		}
	}
	if len(trace.Searches[1].First) == 0 || trace.Searches[1].First[0] != "orbit" {
		t.Fatal("the note's search", trace.Searches[1])
	}
	if len(trace.Pool) == 0 || len(trace.Pool) > simplePoolSize || !slices.Contains(trace.Pool, "orbit") || slices.Contains(trace.Pool, "liftoff") {
		t.Fatal(trace.Pool)
	}

	// The rounds: a page for the launch day, then a search, then met.
	if len(trace.Rounds) != 2 {
		t.Fatalf("%+v", trace.Rounds)
	}
	first, second := trace.Rounds[0], trace.Rounds[1]
	if first.Round != 1 || !slices.Equal(first.Steps, []string{"met", "page"}) || len(first.Pages) != 1 || len(first.Searches) != 0 {
		t.Fatalf("%+v", first)
	}
	if second.Round != 2 || !slices.Equal(second.Steps, []string{"none", "search"}) || len(second.Searches) != 1 || !slices.Contains(second.Fresh, "liftoff") {
		t.Fatalf("%+v", second)
	}
	rewritten := trace.Searches[second.Searches[0]]
	if rewritten.Text != "lift-off day" || rewritten.Kind != "search" || !slices.Contains(rewritten.First, "liftoff") {
		t.Fatalf("%+v", rewritten)
	}
	if !slices.Equal(trace.NeedStates, []string{"met", "met"}) || trace.Stop != "covered" {
		t.Fatal(trace.NeedStates, trace.Stop)
	}

	// JEV's answers per pooled record, in pool order.
	items := map[string]TraceItem{}
	for _, item := range trace.Items {
		items[item.ID] = item
	}
	if len(items) != len(trace.Items) || trace.Items[0].ID != trace.Pool[0] {
		t.Fatal(trace.Items)
	}
	orbit, liftoff := items["orbit"], items["liftoff"]
	if !orbit.HasScreen || orbit.Screen != 0.9 || !orbit.Judged || orbit.Needed != 0.9 || !slices.Equal(orbit.Row, []float64{0.9, 0.1}) || orbit.Text != "the mission codename is ORBIT" {
		t.Fatalf("%+v", orbit)
	}
	if !liftoff.Judged || liftoff.Needed != 0.9 || !slices.Equal(liftoff.Row, []float64{0.1, 0.9}) {
		t.Fatalf("%+v", liftoff)
	}

	// The layer's candidates, judged; the View's blocks name their items.
	layer := map[string]TraceLayerItem{}
	for _, item := range trace.Layer {
		layer[item.Kind+" "+item.ID] = item
	}
	if t1 := layer["topic T1"]; t1.Lit != 1 || t1.Needed != 0.9 || !strings.HasPrefix(t1.Text, "Mission: 1 events") || !slices.Equal(t1.Events, []string{"e0"}) {
		t.Fatalf("%+v", trace.Layer)
	}
	if value := layer["value mission · codename"]; value.Needed != 0.9 || value.Lit == 0 || value.Events != nil {
		t.Fatalf("%+v", trace.Layer)
	}
	blocks := map[string][]string{}
	for _, block := range view.Index {
		blocks[block.ID] = block.Items
	}
	if !slices.Equal(blocks["1-instructions"], []string{"d0"}) || !slices.Equal(blocks["2-topics"], []string{"T1", "T2"}) ||
		!slices.Equal(blocks["3-timelines"], []string{"e0", "e1"}) || !slices.Equal(blocks["4-values"], []string{"mission · codename"}) {
		t.Fatal(blocks)
	}
	if len(trace.Linked) != 1 || trace.Linked[0].ID != "pref" || trace.Linked[0].Needed != 0.1 || trace.Linked[0].Joined || trace.Linked[0].Text != "prefers terse replies" {
		t.Fatalf("%+v", trace.Linked)
	}
}

// A read that fails still leaves the stages it ran, with their calls.
func TestReadTraceOfAFailedRead(t *testing.T) {
	f := newTraceFixture(t)
	planner := replies("mission codename", "NEED: the codename", "")
	judge := decisionFunc(func(context.Context, any, map[string]Question) (map[string]float64, Usage, error) {
		return nil, Usage{InputTokens: 7}, errTraceTestJudge
	})
	a, err := New(f.store, Config{Version: 1, Strategy: "simple"}, Models{Planner: planner, Judge: judge}, nil)
	if err != nil {
		t.Fatal(err)
	}
	trace := &ReadTrace{}
	if _, err := a.Read(WithReadTrace(context.Background(), trace), ReadRequest{Question: "What is the codename?"}); err == nil {
		t.Fatal("the read did not fail")
	}
	names := []string{}
	for _, stage := range trace.Stages {
		names = append(names, stage.Name)
	}
	if !slices.Equal(names, []string{"cues", "candidates", "judge"}) || trace.Stages[0].PlannerCalls != 2 || trace.Stages[2].DecisionCalls == 0 {
		t.Fatalf("%+v", trace.Stages)
	}
	if len(trace.Items) == 0 || trace.Items[0].HasScreen {
		t.Fatalf("%+v", trace.Items)
	}
}

var errTraceTestJudge = &traceTestError{}

type traceTestError struct{}

func (*traceTestError) Error() string { return "judge unavailable" }
