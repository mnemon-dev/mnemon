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

func TestEdgeKindsConfig(t *testing.T) {
	s, _ := agentStore(t)
	models := Models{Planner: &limitPlanner{}, Judge: decisionFunc(fixtureJudge)}
	for _, mode := range []string{"timelines", "layered"} {
		kinds := []string{"entity", "semantic", "temporal", "causal"}
		a, err := New(s, Config{Version: 1, Policy: &ReadPolicy{}, Consolidation: &ConsolidationConfig{Mode: mode, NativeEdges: true, EdgeKinds: kinds}}, models, nil)
		if err != nil || !reflect.DeepEqual(a.config.Consolidation.EdgeKinds, kinds) {
			t.Fatal(mode, err)
		}
	}
	for _, bad := range []struct {
		config ConsolidationConfig
		error  string
	}{
		{ConsolidationConfig{Mode: "layered", NativeEdges: true, EdgeKinds: []string{"similar"}}, `unknown edge kind "similar"`},
		{ConsolidationConfig{Mode: "layered", NativeEdges: true, EdgeKinds: []string{"Semantic"}}, `unknown edge kind "Semantic"`},
		{ConsolidationConfig{Mode: "layered", NativeEdges: true, EdgeKinds: []string{"semantic", "causal", "semantic"}}, `edge kind "semantic" is listed twice`},
		{ConsolidationConfig{Mode: "layered", EdgeKinds: []string{"semantic"}}, "edgeKinds requires nativeEdges"},
		{ConsolidationConfig{Mode: "timelines", EdgeKinds: []string{"entity"}}, "edgeKinds requires nativeEdges"},
		{ConsolidationConfig{EdgeKinds: []string{"entity"}}, `require mode "timelines" or "layered"`},
	} {
		if _, err := New(s, Config{Version: 1, Policy: &ReadPolicy{}, Consolidation: &bad.config}, models, nil); err == nil || !strings.Contains(err.Error(), bad.error) {
			t.Fatalf("%+v: %v", bad.config, err)
		}
	}
	// The edges are JEV's decisions: timelines mode needs a decision model
	// for them too.
	timelines := Config{Version: 1, Policy: &ReadPolicy{}, Consolidation: &ConsolidationConfig{Mode: "timelines", NativeEdges: true, EdgeKinds: []string{"temporal"}}}
	if _, err := New(s, timelines, Models{Planner: &limitPlanner{}}, nil); err == nil || !strings.Contains(err.Error(), "edgeKinds requires a decision model") {
		t.Fatal(err)
	}
	timelines.Consolidation.EdgeKinds = nil
	if _, err := New(s, timelines, Models{Planner: &limitPlanner{}}, nil); err != nil {
		t.Fatal(err)
	}
	// Empty, the field keeps the stage-2 bytes (and policy).
	raw, _ := json.Marshal(ConsolidationConfig{Mode: "layered", NativeEdges: true, EdgeKinds: []string{}})
	if string(raw) != `{"mode":"layered","nativeEdges":true}` {
		t.Fatal(string(raw))
	}
	raw, _ = json.Marshal(ConsolidationConfig{Mode: "layered", NativeEdges: true, EdgeKinds: []string{"semantic", "causal"}})
	if string(raw) != `{"mode":"layered","nativeEdges":true,"edgeKinds":["semantic","causal"]}` {
		t.Fatal(string(raw))
	}
	var config Config
	if err := json.Unmarshal([]byte(`{"version":1,"policy":{},"consolidation":{"mode":"layered","nativeEdges":true,"edgeKinds":[]}}`), &config); err != nil {
		t.Fatal(err)
	}
	a, err := New(s, config, models, nil)
	if err != nil || a.config.Consolidation.EdgeKinds != nil {
		t.Fatal(err, a.config.Consolidation.EdgeKinds)
	}
}

func TestEventIDs(t *testing.T) {
	e := event{Thread: "T1", Date: "2024/01/05", Text: "Greg Hall booked the cabin.", Records: []string{"r1", "r2"}, Entities: []string{"Greg Hall"}}
	id := eventID(e)
	if len(id) != 16 || strings.Trim(id, "0123456789abcdef") != "" {
		t.Fatal(id)
	}
	// Its thread (re-linked) and its names do not change it.
	same := e
	same.Thread, same.Entities = "T9", nil
	if eventID(same) != id {
		t.Fatal("the id follows the thread or the names")
	}
	// Its date, text and records do.
	for _, other := range []event{
		{Thread: "T1", Date: "2024/01/06", Text: e.Text, Records: e.Records},
		{Thread: "T1", Date: e.Date, Text: e.Text + " ", Records: e.Records},
		{Thread: "T1", Date: e.Date, Text: e.Text, Records: []string{"r2", "r1"}},
		{Thread: "T1", Date: e.Date, Text: e.Text, Records: []string{"r1"}},
	} {
		if eventID(other) == id {
			t.Fatal("same id", other)
		}
	}
	// No separator makes two events one.
	if eventID(event{Date: "a", Text: "b"}) == eventID(event{Date: "a\x00b"}) {
		t.Fatal("ambiguous id")
	}
	// The ids survive a rebuild of the state from its JSON.
	state := emptyTimelineState()
	state.Memory = candidateMemory()
	raw, _ := json.Marshal(state)
	var again timelineState
	if err := json.Unmarshal(raw, &again); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(nodeIDs(eventNodes(state)), nodeIDs(eventNodes(again))) {
		t.Fatal("ids changed in a rebuild")
	}
}

// candidateMemory is eight events with records on two threads, an event
// without records and a repeat of the second event on another thread.
// "Greg" and "Greg Hall" are one cluster (candidateClusters).
func candidateMemory() timelineMemory {
	return timelineMemory{
		Threads: []thread{{ID: "T1", Title: "Cabin trip"}, {ID: "T2", Title: "Job"}},
		Events: []event{
			{Thread: "T1", Date: "2024/01/02", Text: "Greg suggested a cabin trip.", Records: []string{"r1"}, Entities: []string{"Greg"}},
			{Thread: "T2", Date: "2024/01/03", Text: "Started at Acme.", Records: []string{"r2"}, Entities: []string{"Acme"}},
			{Thread: "T1", Date: "2024/01/05", Text: "Greg Hall booked the cabin.", Records: []string{"r3"}, Entities: []string{"Greg Hall", "Cabin Co"}},
			{Thread: "T2", Date: "2024-01-05", Text: "Acme lent a car for the cabin.", Records: []string{"r4"}, Entities: []string{"Acme", "Cabin Co"}},
			{Thread: "T1", Date: "2024/01/20", Text: "The cabin trip happened.", Records: []string{"r5"}, Entities: []string{"Cabin Co"}},
			{Thread: "T1", Date: "sometime", Text: "Checked the weather.", Records: []string{"r6"}},
			{Thread: "T1", Date: "2024/01/09", Text: "Paid the cabin deposit.", Records: []string{"r7"}, Entities: []string{"Cabin Co"}},
			{Thread: "T1", Date: "2024/01/09", Text: "Packed for the cabin.", Records: []string{"r8"}},
			{Thread: "T2", Date: "2024/01/01", Text: "Unrecorded.", Records: []string{}, Entities: []string{"Acme"}},
			{Thread: "T1", Date: "2024/01/03", Text: "Started at Acme.", Records: []string{"r2"}, Entities: []string{"Acme"}},
		},
		Facts: map[string][]factValue{}, FactOrder: []string{}, Directives: []directive{},
	}
}

func candidateClusters() map[string]string {
	return map[string]string{"Greg": "Greg Hall", "Greg Hall": "Greg Hall", "Cabin Co": "Cabin Co", "Acme": "Acme"}
}

func nodeIDs(nodes []eventNode) []string {
	out := []string{}
	for _, n := range nodes {
		out = append(out, n.id)
	}
	return out
}

func TestEventNodes(t *testing.T) {
	state := emptyTimelineState()
	state.Memory, state.EntityClusters = candidateMemory(), candidateClusters()
	nodes := eventNodes(state)
	// The events with records, each id once, in memory order.
	if len(nodes) != 8 {
		t.Fatal(len(nodes))
	}
	for i, n := range nodes {
		if n.at != i || n.id != eventID(state.Memory.Events[i]) || n.event.Text != state.Memory.Events[i].Text {
			t.Fatal(i, n)
		}
	}
	// Session days are ISO dates, "" for no date; clusters each once.
	if nodes[0].day != "2024-01-02" || nodes[3].day != "2024-01-05" || nodes[5].day != "" {
		t.Fatal(nodes[0].day, nodes[3].day, nodes[5].day)
	}
	if !reflect.DeepEqual(nodes[0].clusters, []string{"Greg Hall"}) || !reflect.DeepEqual(nodes[2].clusters, []string{"Greg Hall", "Cabin Co"}) || nodes[5].clusters != nil {
		t.Fatal(nodes[0].clusters, nodes[2].clusters, nodes[5].clusters)
	}
	// An undecided name is a cluster of its own.
	state.EntityClusters = nil
	if nodes = eventNodes(state); !reflect.DeepEqual(nodes[0].clusters, []string{"Greg"}) {
		t.Fatal(nodes[0].clusters)
	}
}

func TestEventEdgeCandidates(t *testing.T) {
	state := emptyTimelineState()
	state.Memory, state.EntityClusters = candidateMemory(), candidateClusters()
	nodes := eventNodes(state)
	hot := hotClusters(nodes)
	if len(hot) != 0 {
		t.Fatal(hot)
	}
	// similar ranks the nodes by similarity to node i: order[0] most.
	similar := func(i int, order ...int) []int {
		out := make([]int, len(nodes))
		for j := range out {
			out[j] = -1
		}
		for rank, j := range order {
			out[j] = rank
		}
		return out
	}
	// The deposit (node 6, January 9th) is compared with the events before
	// it: earlier days, or the same day earlier in the memory, or, when
	// either has no date, earlier in the memory. Not the trip of the 20th
	// (4), nor the packing of the same day stored after it (7).
	deposit := similar(6, 4, 3, 7, 2, 5, 0)
	for kind, want := range map[string][]int{
		// By similarity, any thread; the start at Acme (1) is not similar.
		"semantic": {3, 2, 5, 0},
		// Its thread first, the nearest day first, the undated last; then
		// the other events sharing a cluster (Cabin Co).
		"temporal": {2, 0, 5, 3},
		// The same thread first, then by similarity.
		"causal": {2, 5, 0, 3},
		// A shared cluster, by similarity.
		"entity": {3, 2},
	} {
		if got := edgeCandidates(kind, nodes, 6, deposit, hot, 8); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %v, want %v", kind, got, want)
		}
	}
	if got := edgeCandidates("semantic", nodes, 6, deposit, hot, 2); !reflect.DeepEqual(got, []int{3, 2}) {
		t.Fatal("limit", got)
	}
	if got := edgeCandidates("temporal", nodes, 6, deposit, hot, 2); !reflect.DeepEqual(got, []int{2, 0}) {
		t.Fatal("limit", got)
	}
	// Greg Hall's booking (node 2) shares a cluster with Greg's suggestion
	// (0) by the alias decision, not by a word; nothing is similar.
	none := similar(2)
	if got := edgeCandidates("entity", nodes, 2, none, hot, 8); !reflect.DeepEqual(got, []int{0}) {
		t.Fatal(got)
	}
	if got := edgeCandidates("semantic", nodes, 2, none, hot, 8); len(got) != 0 {
		t.Fatal(got)
	}
	if got := edgeCandidates("causal", nodes, 2, none, hot, 8); !reflect.DeepEqual(got, []int{0}) {
		t.Fatal(got)
	}
	// The undated check (node 5) is compared with the events stored
	// before it, the nearest in the memory first when nothing else tells.
	if got := edgeCandidates("temporal", nodes, 5, similar(5), hot, 8); !reflect.DeepEqual(got, []int{4, 2, 0}) {
		t.Fatal(got)
	}
	// The packing (node 7) follows the deposit of the same day (6).
	if got := edgeCandidates("temporal", nodes, 7, similar(7), hot, 8); !reflect.DeepEqual(got, []int{6, 2, 0, 5}) {
		t.Fatal(got)
	}
	// The first event has nothing before it.
	for _, kind := range []string{"semantic", "temporal", "causal", "entity"} {
		if got := edgeCandidates(kind, nodes, 0, similar(0, 1, 2, 3), hot, 8); len(got) != 0 {
			t.Fatal(kind, got)
		}
	}
}

var allEdgeKinds = []string{"semantic", "temporal", "causal", "entity"}

// cabinMemory is two events of one thread, both naming Greg.
func cabinMemory() timelineMemory {
	return timelineMemory{
		Threads: []thread{{ID: "T1", Title: "Cabin trip"}},
		Events: []event{
			{Thread: "T1", Date: "2024/01/02", Text: "Greg booked the cabin.", Records: []string{"r1"}, Entities: []string{"Greg"}},
			{Thread: "T1", Date: "2024/01/05", Text: "Greg paid the cabin deposit.", Records: []string{"r2"}, Entities: []string{"Greg"}},
		},
		Facts: map[string][]factValue{}, FactOrder: []string{}, Directives: []directive{},
	}
}

// edgeAnswers answers the event edge questions by kind.
func edgeAnswers(answers map[string]float64) *linkJudge {
	return &linkJudge{answer: func(kind, _, _ string) float64 { return answers[kind] }}
}

// askedPairs lists the questions of the calls as "<kind> <new text> ~
// <candidate text>", sorted.
func askedPairs(t *testing.T, calls []linkCall) []string {
	t.Helper()
	out := []string{}
	text := func(items []linkItem, id string) string {
		for _, item := range items {
			if item.ID == id {
				return item.Text
			}
		}
		t.Fatal("no item", id)
		return ""
	}
	for _, call := range calls {
		for _, q := range call.Questions {
			refs := linkRef.FindAllStringSubmatch(q.Instructions, -1)
			n, c := refs[0][1], refs[1][1]
			kind := map[string]string{"Are": "semantic", "Is ": "temporal", "Did": "causal"}[q.Instructions[:3]]
			if kind == "causal" {
				n, c = c, n
			}
			if kind == "semantic" && !strings.Contains(q.Instructions, "same matter") {
				kind = "entity"
			}
			out = append(out, kind+" "+text(call.State.New, n)+" ~ "+text(call.State.Candidates, c))
		}
	}
	slices.Sort(out)
	return out
}

func TestEventEdgeQuestionsVerbatim(t *testing.T) {
	judge := edgeAnswers(nil)
	a := linkAgent(t, judge, 8, nil)
	state, result := linkState0(cabinMemory()), &ConsolidateResult{}
	if _, err := a.newLinker().decideEventEdges(context.Background(), state, allEdgeKinds, result, nil); err != nil {
		t.Fatal(err)
	}
	// The first event has nothing before it; the second is asked about the
	// first once per kind, in one call.
	if judge.count() != 1 {
		t.Fatal(judge.count())
	}
	call := judge.calls[0]
	if call.Raw != `{"new":[{"id":"n1","date":"2024/01/05","thread":"Cabin trip","text":"Greg paid the cabin deposit.","entities":["Greg"]}],"candidates":[{"id":"c1","date":"2024/01/02","thread":"Cabin trip","text":"Greg booked the cabin.","entities":["Greg"]}]}` {
		t.Fatal(call.Raw)
	}
	want := map[string]Question{
		"es0": {Type: "noul", Instructions: "Are the new event whose `id` is \"n1\" and the candidate whose `id` is \"c1\" about the same matter, so that one helps to answer a question about the other?", Criteria: map[string]string{
			"true":  "They concern the same specific matter (the same plan, problem, decision, activity, object or relationship), even in different words.",
			"false": "They only share a general topic, a person or a time, or concern different matters.",
		}},
		"et1": {Type: "noul", Instructions: "Is the new event whose `id` is \"n1\" a later development of the same matter as the candidate whose `id` is \"c1\"?", Criteria: map[string]string{
			"true":  "It continues, follows up, changes, completes or reports on the same matter after the candidate.",
			"false": "It concerns a different matter, or only happens to be later.",
		}},
		"ec2": {Type: "noul", Instructions: "Did the candidate event whose `id` is \"c1\" cause, enable or prevent the new event whose `id` is \"n1\"?", Criteria: map[string]string{
			"true":  "The records state or clearly imply that the candidate led to, made possible or stopped the new event.",
			"false": "No such link is stated or clearly implied; being earlier or related is not a cause.",
		}},
		"en3": {Type: "noul", Instructions: "Are the new event whose `id` is \"n1\" and the candidate whose `id` is \"c1\" about the same particular person, organization, place, product or document?", Criteria: map[string]string{
			"true":  "Both concern that same particular one, named or clearly referred to.",
			"false": "They only share a common name or a general kind of thing, or concern different ones.",
		}},
	}
	if !reflect.DeepEqual(call.Questions, want) {
		t.Fatalf("%+v", call.Questions)
	}
	// An event without names has no entities field.
	m := cabinMemory()
	m.Events[1].Entities = nil
	judge = edgeAnswers(nil)
	if _, err := linkAgent(t, judge, 8, nil).newLinker().decideEventEdges(context.Background(), linkState0(m), []string{"temporal"}, &ConsolidateResult{}, nil); err != nil {
		t.Fatal(err)
	}
	if raw := judge.calls[0].Raw; !strings.Contains(raw, `"text":"Greg paid the cabin deposit."}`) || len(judge.calls[0].Questions) != 1 {
		t.Fatal(raw, judge.calls[0].Questions)
	}
}

func TestEventEdgeDecisions(t *testing.T) {
	judge := edgeAnswers(map[string]float64{"semantic": 0.876, "temporal": 0.5, "causal": 0.3, "entity": 0.499})
	a := linkAgent(t, judge, 8, nil)
	state, result := linkState0(cabinMemory()), &ConsolidateResult{}
	changed, err := a.newLinker().decideEventEdges(context.Background(), state, allEdgeKinds, result, nil)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	first, second := eventID(state.Memory.Events[0]), eventID(state.Memory.Events[1])
	// At least 0.5 is an edge weighted by its probability, to two decimals;
	// a no is kept as 0. Both events are decided for every kind.
	want := &eventEdges{
		Rules:   eventEdgeRules,
		Decided: map[string][]string{"semantic": {first, second}, "temporal": {first, second}, "causal": {first, second}, "entity": {first, second}},
		Answers: map[string]map[string]map[string]float64{
			"semantic": {second: {first: 0.88}},
			"temporal": {second: {first: 0.5}},
			"causal":   {second: {first: 0}},
			"entity":   {second: {first: 0}},
		},
	}
	if !reflect.DeepEqual(state.EventEdges, want) {
		t.Fatalf("%+v", state.EventEdges)
	}
	if result.EdgeCalls != 1 || !reflect.DeepEqual(result.EdgeAsked, map[string]int{"semantic": 1, "temporal": 1, "causal": 1, "entity": 1}) ||
		!reflect.DeepEqual(result.EdgeYes, map[string]int{"semantic": 1, "temporal": 1}) || result.EdgeFallbacks != 0 {
		t.Fatalf("%+v", result)
	}
	if result.LinkCalls != 0 || result.AliasCalls != 0 || result.LinkUsage == nil || result.LinkUsage.InputTokens != 100 || result.Usage.InputTokens != 100 || state.Usage.InputTokens != 100 {
		t.Fatalf("%+v %+v", result, state.Usage)
	}
	raw, _ := json.Marshal(result)
	for _, field := range []string{`"edgeCalls":1`, `"edgeAsked":{"causal":1,"entity":1,"semantic":1,"temporal":1}`, `"edgeYes":{"semantic":1,"temporal":1}`} {
		if !strings.Contains(string(raw), field) {
			t.Fatal(field, string(raw))
		}
	}
	if strings.Contains(string(raw), "edgeFallbacks") {
		t.Fatal(string(raw))
	}
	// Decided, yes or no, nothing is asked again.
	again := &ConsolidateResult{}
	if changed, err := a.newLinker().decideEventEdges(context.Background(), state, allEdgeKinds, again, nil); err != nil || changed || judge.count() != 1 {
		t.Fatal(changed, err, judge.count())
	}
	if raw, _ := json.Marshal(again); strings.Contains(string(raw), "edge") {
		t.Fatal(string(raw))
	}
	// The kinds asked are those listed.
	judge = edgeAnswers(nil)
	only := linkState0(cabinMemory())
	if _, err := linkAgent(t, judge, 8, nil).newLinker().decideEventEdges(context.Background(), only, []string{"causal"}, &ConsolidateResult{}, nil); err != nil {
		t.Fatal(err)
	}
	if got := askedPairs(t, judge.calls); !slices.Equal(got, []string{"causal Greg paid the cabin deposit. ~ Greg booked the cabin."}) || len(only.EventEdges.Decided) != 1 {
		t.Fatal(got, only.EventEdges)
	}
}

func TestEventEdgesIncremental(t *testing.T) {
	judge := edgeAnswers(map[string]float64{"temporal": 0.9})
	a := linkAgent(t, judge, 8, nil)
	state := linkState0(cabinMemory())
	if _, err := a.newLinker().decideEventEdges(context.Background(), state, allEdgeKinds, &ConsolidateResult{}, nil); err != nil {
		t.Fatal(err)
	}
	// A new event is asked about against the events before it; the decided
	// ones are not asked about again.
	state.Memory.Events = append(state.Memory.Events, event{Thread: "T1", Date: "2024/01/09", Text: "Greg cancelled the cabin.", Records: []string{"r3"}, Entities: []string{"Greg"}})
	judge.calls = nil
	result := &ConsolidateResult{}
	if changed, err := a.newLinker().decideEventEdges(context.Background(), state, allEdgeKinds, result, nil); err != nil || !changed {
		t.Fatal(changed, err)
	}
	if judge.count() != 1 || len(judge.calls[0].State.New) != 1 || judge.calls[0].State.New[0].Text != "Greg cancelled the cabin." {
		t.Fatal(judge.calls)
	}
	var want []string
	for _, kind := range allEdgeKinds {
		want = append(want, kind+" Greg cancelled the cabin. ~ Greg booked the cabin.", kind+" Greg cancelled the cabin. ~ Greg paid the cabin deposit.")
	}
	slices.Sort(want)
	if got := askedPairs(t, judge.calls); !slices.Equal(got, want) {
		t.Fatal(got)
	}
	if !reflect.DeepEqual(result.EdgeAsked, map[string]int{"semantic": 2, "temporal": 2, "causal": 2, "entity": 2}) || !reflect.DeepEqual(result.EdgeYes, map[string]int{"temporal": 2}) {
		t.Fatalf("%+v", result)
	}
	third := eventID(state.Memory.Events[2])
	if got := state.EventEdges.Answers["temporal"][third]; len(got) != 2 || len(state.EventEdges.Decided["entity"]) != 3 {
		t.Fatal(got, state.EventEdges.Decided)
	}
	// A kind listed later asks about every event, once.
	judge.calls = nil
	if _, err := a.newLinker().decideEventEdges(context.Background(), state, []string{"temporal", "causal"}, &ConsolidateResult{}, nil); err != nil || judge.count() != 0 {
		t.Fatal(err, judge.count())
	}
}

// limitedJudge is a linkJudge that takes at most max questions a call.
type limitedJudge struct {
	*linkJudge
	max int
}

func (j limitedJudge) MaxQuestions() int { return j.max }

func TestEventEdgeFailureRetried(t *testing.T) {
	// Two questions a call: the second call fails.
	judge := edgeAnswers(map[string]float64{"semantic": 0.9, "temporal": 0.9, "causal": 0.9, "entity": 0.9})
	down := true
	judge.err = func(context.Context) error {
		if down && judge.count() > 1 {
			return errors.New("jev down")
		}
		return nil
	}
	a := linkAgent(t, limitedJudge{judge, 2}, 8, nil)
	state, result := linkState0(cabinMemory()), &ConsolidateResult{}
	changed, err := a.newLinker().decideEventEdges(context.Background(), state, allEdgeKinds, result, nil)
	if err != nil || !changed {
		t.Fatal("a JEV failure failed the step", changed, err)
	}
	first, second := eventID(state.Memory.Events[0]), eventID(state.Memory.Events[1])
	// The answered pairs are kept; the others stay undecided, not "no",
	// their failed attempt counted.
	want := &eventEdges{
		Rules:    eventEdgeRules,
		Decided:  map[string][]string{"semantic": {first, second}, "temporal": {first, second}, "causal": {first}, "entity": {first}},
		Answers:  map[string]map[string]map[string]float64{"semantic": {second: {first: 0.9}}, "temporal": {second: {first: 0.9}}},
		Failures: map[string]map[string]map[string]int{"causal": {second: {first: 1}}, "entity": {second: {first: 1}}},
	}
	if !reflect.DeepEqual(state.EventEdges, want) {
		t.Fatalf("%+v", state.EventEdges)
	}
	if result.EdgeCalls != 2 || result.EdgeFallbacks != 2 || !reflect.DeepEqual(result.EdgeAsked, map[string]int{"semantic": 1, "temporal": 1}) || result.Usage.InputTokens != 103 {
		t.Fatalf("%+v", result)
	}
	// The next step asks the undecided pairs again, only them.
	down = false
	judge.calls = nil
	result = &ConsolidateResult{}
	if changed, err = a.newLinker().decideEventEdges(context.Background(), state, allEdgeKinds, result, nil); err != nil || !changed || result.EdgeFallbacks != 0 {
		t.Fatal(changed, err, result)
	}
	if got := askedPairs(t, judge.calls); !slices.Equal(got, []string{"causal Greg paid the cabin deposit. ~ Greg booked the cabin.", "entity Greg paid the cabin deposit. ~ Greg booked the cabin."}) {
		t.Fatal(got)
	}
	if len(state.EventEdges.Decided["causal"]) != 2 || state.EventEdges.Answers["entity"][second][first] != 0.9 || state.EventEdges.Failures != nil {
		t.Fatalf("%+v", state.EventEdges)
	}
	// An event whose pairs were answered in part asks only the rest.
	judge = edgeAnswers(map[string]float64{"temporal": 0.9})
	down = true
	judge.err = func(context.Context) error {
		if down && judge.count() > 1 {
			return errors.New("jev down")
		}
		return nil
	}
	a = linkAgent(t, limitedJudge{judge, 2}, 8, nil)
	state = linkState0(cabinMemory())
	state.Memory.Events = append(state.Memory.Events, event{Thread: "T1", Date: "2024/01/09", Text: "Greg cancelled the cabin.", Records: []string{"r3"}})
	if _, err := a.newLinker().decideEventEdges(context.Background(), state, []string{"temporal"}, &ConsolidateResult{}, nil); err != nil {
		t.Fatal(err)
	}
	third := eventID(state.Memory.Events[2])
	if len(state.EventEdges.Decided["temporal"]) != 2 || len(state.EventEdges.Answers["temporal"][third]) != 1 {
		t.Fatalf("%+v", state.EventEdges)
	}
	down = false
	judge.calls = nil
	if _, err := a.newLinker().decideEventEdges(context.Background(), state, []string{"temporal"}, &ConsolidateResult{}, nil); err != nil {
		t.Fatal(err)
	}
	if got := askedPairs(t, judge.calls); !slices.Equal(got, []string{"temporal Greg cancelled the cabin. ~ Greg booked the cabin."}) || len(state.EventEdges.Decided["temporal"]) != 3 {
		t.Fatal(got, state.EventEdges)
	}
	// A spent budget or an unavailable provider ends the asking, counting
	// no failure; so does no decision model.
	for _, fatal := range []error{ErrBudgetCap, ErrModelUnavailable} {
		judge = edgeAnswers(nil)
		judge.err = func(context.Context) error { return fatal }
		state, result = linkState0(cabinMemory()), &ConsolidateResult{}
		if _, err := linkAgent(t, limitedJudge{judge, 1}, 8, nil).newLinker().decideEventEdges(context.Background(), state, allEdgeKinds, result, nil); err != nil || result.EdgeFallbacks != 4 || result.EdgeCalls != 1 || state.EventEdges.Answers != nil || state.EventEdges.Failures != nil {
			t.Fatal(fatal, err, result, state.EventEdges)
		}
	}
	s, _ := agentStore(t)
	none, err := New(s, Config{Version: 1, Policy: &ReadPolicy{}, Consolidation: &ConsolidationConfig{Mode: "timelines"}}, Models{Planner: &limitPlanner{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	state, result = linkState0(cabinMemory()), &ConsolidateResult{}
	if _, err := none.newLinker().decideEventEdges(context.Background(), state, allEdgeKinds, result, nil); err != nil || result.EdgeFallbacks != 4 || result.EdgeCalls != 0 {
		t.Fatal(err, result)
	}
	// A job canceled in its first call answers nothing.
	ctx, cancel := context.WithCancel(context.Background())
	judge = edgeAnswers(nil)
	judge.err = func(context.Context) error { cancel(); return context.Canceled }
	state = linkState0(cabinMemory())
	if _, err := linkAgent(t, judge, 8, nil).newLinker().decideEventEdges(ctx, state, allEdgeKinds, &ConsolidateResult{}, nil); !errors.Is(err, context.Canceled) || state.EventEdges.Answers != nil || state.EventEdges.Failures != nil {
		t.Fatal(err, state.EventEdges)
	}
}

func TestEventEdgeFailedPairsGiveUp(t *testing.T) {
	// One call a pair. The judge cannot answer whether the deposit is a
	// later development of the booking (an invalid probability); it
	// answers the rest, and each step brings a new event to ask about. The
	// calls after the failed one are still asked, and since others
	// succeed, the failure counts.
	m := cabinMemory()
	m.Events = append(m.Events, event{Thread: "T1", Date: "2024/01/09", Text: "Greg cancelled the cabin.", Records: []string{"r3"}, Entities: []string{"Greg"}})
	judge := &linkJudge{answer: func(kind, newText, candidate string) float64 {
		if newText == "Greg paid the cabin deposit." {
			return 2
		}
		return 0.9
	}}
	a := linkAgent(t, limitedJudge{judge, 1}, 8, nil)
	state := linkState0(m)
	first, second := eventID(m.Events[0]), eventID(m.Events[1])
	failing := "temporal Greg paid the cabin deposit. ~ Greg booked the cabin."
	for attempt := 1; attempt <= 4; attempt++ {
		state.Memory.Events = append(state.Memory.Events, event{Thread: "T1", Date: fmt.Sprintf("2024/01/1%d", attempt), Text: fmt.Sprintf("Greg called the owner %d.", attempt), Records: []string{fmt.Sprintf("r%d", attempt+3)}})
		judge.calls = nil
		result := &ConsolidateResult{}
		if _, err := a.newLinker().decideEventEdges(context.Background(), state, []string{"temporal"}, result, nil); err != nil {
			t.Fatal(attempt, err)
		}
		asked := askedPairs(t, judge.calls)
		if slices.Contains(asked, failing) != (attempt <= 3) || len(asked) != len(judge.calls) {
			t.Fatal(attempt, asked)
		}
		// Undecided until it fails a third time: then given up, not "no".
		fallbacks, givenUp := map[bool]int{true: 1}[attempt < 3], map[bool]int{true: 1}[attempt == 3]
		if result.EdgeFallbacks != fallbacks || result.EdgeGivenUp != givenUp || result.EdgeCalls != len(asked) {
			t.Fatal(attempt, result)
		}
		if got := state.EventEdges.Failures["temporal"][second][first]; got != min(attempt, 3) {
			t.Fatal(attempt, got)
		}
		if decided := slices.Contains(state.EventEdges.Decided["temporal"], second); decided != (attempt >= 3) || state.EventEdges.answered("temporal", second, first) {
			t.Fatal(attempt, state.EventEdges)
		}
	}
	// A given-up pair is no edge.
	versions := map[string]int64{}
	for i := 1; i <= 7; i++ {
		versions[fmt.Sprintf("r%d", i)] = 1
	}
	built := buildEdges([]timelineState{*state}, versions, []string{"temporal"})
	for _, pair := range [][2]string{{"r1", "r2"}, {"r2", "r1"}} {
		if slices.ContainsFunc(built[pair[0]].Links, func(l records.Link) bool { return l.ID == pair[1] }) {
			t.Fatal(pair, built[pair[0]].Links)
		}
	}
	if !slices.Contains(built["r2"].Links, edgeLink("r3", 1, "temporal", 0.9)) {
		t.Fatal(built["r2"].Links)
	}
	raw, _ := json.Marshal(ConsolidateResult{EdgeGivenUp: 2})
	if !strings.Contains(string(raw), `"edgeGivenUp":2`) {
		t.Fatal(string(raw))
	}
}

func TestEventEdgeSystemicFailuresGiveUpNothing(t *testing.T) {
	// Four events of a thread: six temporal pairs, one call each. A judge
	// whose every call fails (a request error, a timeout, a missing
	// credential) ends each step after three failed calls in a row and
	// counts no failure: however many steps, no pair is given up.
	m := cabinMemory()
	m.Events = append(m.Events,
		event{Thread: "T1", Date: "2024/01/09", Text: "Greg cancelled the cabin.", Records: []string{"r3"}},
		event{Thread: "T1", Date: "2024/01/10", Text: "Greg rebooked the cabin.", Records: []string{"r4"}})
	judge := edgeAnswers(map[string]float64{"temporal": 0.9})
	down := true
	judge.err = func(context.Context) error {
		if down {
			return errors.New("400 bad request")
		}
		return nil
	}
	a := linkAgent(t, limitedJudge{judge, 1}, 8, nil)
	state := linkState0(m)
	for step := range 5 {
		result := &ConsolidateResult{}
		if _, err := a.newLinker().decideEventEdges(context.Background(), state, []string{"temporal"}, result, nil); err != nil {
			t.Fatal(step, err)
		}
		if result.EdgeCalls != 3 || result.EdgeFallbacks != 6 || result.EdgeGivenUp != 0 || state.EventEdges.Failures != nil || state.EventEdges.Answers != nil || len(state.EventEdges.Decided["temporal"]) != 1 {
			t.Fatal(step, result, state.EventEdges)
		}
	}
	// A healthy judge then decides them all.
	down = false
	result := &ConsolidateResult{}
	if _, err := a.newLinker().decideEventEdges(context.Background(), state, []string{"temporal"}, result, nil); err != nil || result.EdgeCalls != 6 || result.EdgeAsked["temporal"] != 6 || result.EdgeFallbacks != 0 || len(state.EventEdges.Decided["temporal"]) != 4 {
		t.Fatal(err, result, state.EventEdges)
	}
	// Failed calls that are not three in a row do not end the step, and
	// with calls succeeding around them, they count.
	judge = edgeAnswers(map[string]float64{"temporal": 0.9})
	judge.err = func(context.Context) error {
		if n := judge.count(); n == 1 || n == 3 || n == 4 {
			return errors.New("400 bad request")
		}
		return nil
	}
	state, result = linkState0(m), &ConsolidateResult{}
	if _, err := linkAgent(t, limitedJudge{judge, 1}, 8, nil).newLinker().decideEventEdges(context.Background(), state, []string{"temporal"}, result, nil); err != nil {
		t.Fatal(err)
	}
	failed := 0
	for _, to := range state.EventEdges.Failures["temporal"] {
		failed += len(to)
	}
	if result.EdgeCalls != 6 || result.EdgeFallbacks != 3 || failed != 3 || result.EdgeAsked["temporal"] != 3 {
		t.Fatal(result, state.EventEdges)
	}
}

func TestEventEdgeCancelKeepsAnswers(t *testing.T) {
	// One call a pair; the job is canceled in the third. The answers of
	// the first two are kept, and committed with a context that still
	// runs.
	m := cabinMemory()
	m.Events = append(m.Events, event{Thread: "T1", Date: "2024/01/09", Text: "Greg cancelled the cabin.", Records: []string{"r3"}, Entities: []string{"Greg"}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	judge := edgeAnswers(map[string]float64{"temporal": 0.9})
	judge.err = func(context.Context) error {
		if judge.count() == 3 {
			cancel()
			return context.Canceled
		}
		return nil
	}
	state := linkState0(m)
	commits := []int{}
	commit := func(saveCtx context.Context) error {
		if saveCtx.Err() != nil {
			t.Fatal("committed with a canceled context")
		}
		commits = append(commits, len(state.EventEdges.Answers["temporal"]))
		return nil
	}
	_, err := linkAgent(t, limitedJudge{judge, 1}, 8, nil).newLinker().decideEventEdges(ctx, state, []string{"temporal"}, &ConsolidateResult{}, commit)
	if !errors.Is(err, context.Canceled) || !slices.Equal(commits, []int{2}) {
		t.Fatal(err, commits)
	}
	first, second, third := eventID(m.Events[0]), eventID(m.Events[1]), eventID(m.Events[2])
	if !reflect.DeepEqual(state.EventEdges.Answers["temporal"], map[string]map[string]float64{second: {first: 0.9}, third: {second: 0.9}}) || !slices.Equal(state.EventEdges.Decided["temporal"], []string{first, second}) || state.EventEdges.Failures != nil {
		t.Fatalf("%+v", state.EventEdges)
	}
}

func TestEventEdgeChunkCommits(t *testing.T) {
	// Ten similar events, one call a pair: 45 calls, committed after the
	// first 32 and after the rest.
	m := timelineMemory{Threads: []thread{{ID: "T1", Title: "Cabin trip"}}, Facts: map[string][]factValue{}, FactOrder: []string{}, Directives: []directive{}}
	for i := range 10 {
		m.Events = append(m.Events, event{Thread: "T1", Date: fmt.Sprintf("2024/01/%02d", i+1), Text: fmt.Sprintf("cabin %d", i), Records: []string{fmt.Sprintf("r%d", i)}})
	}
	judge := edgeAnswers(map[string]float64{"semantic": 0.9})
	state := linkState0(m)
	commits := []int{}
	commit := func(context.Context) error {
		n := 0
		for _, answers := range state.EventEdges.Answers["semantic"] {
			n += len(answers)
		}
		commits = append(commits, n)
		return nil
	}
	result := &ConsolidateResult{}
	if _, err := linkAgent(t, limitedJudge{judge, 1}, 32, nil).newLinker().decideEventEdges(context.Background(), state, []string{"semantic"}, result, commit); err != nil {
		t.Fatal(err)
	}
	if result.EdgeCalls != 45 || !slices.Equal(commits, []int{32, 45}) || len(state.EventEdges.Decided["semantic"]) != 10 {
		t.Fatal(result.EdgeCalls, commits, state.EventEdges.Decided)
	}
	// A failing commit fails the step.
	state = linkState0(m)
	if _, err := linkAgent(t, limitedJudge{edgeAnswers(nil), 1}, 32, nil).newLinker().decideEventEdges(context.Background(), state, []string{"semantic"}, &ConsolidateResult{}, func(context.Context) error { return errors.New("disk full") }); err == nil || err.Error() != "disk full" {
		t.Fatal(err)
	}
}

func TestEventEdgeRulesReevaluate(t *testing.T) {
	// Decisions made under the first candidate rules (before the hot
	// stop-list): the last event was decided against Caroline's events.
	state := linkState0(speakerMemory())
	ids := nodeIDs(eventNodes(*state))
	state.EventEdges = &eventEdges{
		Decided: map[string][]string{"temporal": ids, "entity": ids},
		Answers: map[string]map[string]map[string]float64{
			"temporal": {ids[11]: {ids[0]: 0.8, ids[1]: 0}},
			"entity":   {ids[11]: {ids[0]: 0.9, ids[5]: 0.7}},
		},
	}
	judge := edgeAnswers(map[string]float64{"temporal": 0.6, "entity": 0.6})
	result := &ConsolidateResult{}
	changed, err := linkAgent(t, judge, 8, nil).newLinker().decideEventEdges(context.Background(), state, []string{"temporal", "entity"}, result, nil)
	if err != nil || !changed || state.EventEdges.Rules != eventEdgeRules {
		t.Fatal(changed, err, state.EventEdges.Rules)
	}
	// Every event is decided again under the current rules, asking only
	// the pairs never answered; the old answers stay.
	asked := []string{}
	for _, pair := range askedPairs(t, judge.calls) {
		if strings.Contains(pair, " event 11 ~") {
			asked = append(asked, pair)
		}
	}
	if !slices.Equal(asked, []string{"entity event 11 ~ event 2", "temporal event 11 ~ event 10", "temporal event 11 ~ event 2", "temporal event 11 ~ event 5", "temporal event 11 ~ event 8", "temporal event 11 ~ event 9"}) {
		t.Fatal(asked)
	}
	if got := state.EventEdges.Answers["temporal"][ids[11]]; got[ids[0]] != 0.8 || got[ids[10]] != 0.6 || len(state.EventEdges.Decided["temporal"]) != 12 || len(state.EventEdges.Decided["entity"]) != 12 {
		t.Fatalf("%+v", state.EventEdges)
	}
	judge.calls = nil
	if changed, err := linkAgent(t, judge, 8, nil).newLinker().decideEventEdges(context.Background(), state, []string{"temporal", "entity"}, &ConsolidateResult{}, nil); err != nil || changed || judge.count() != 0 {
		t.Fatal(changed, err, judge.count())
	}
	// The overlay drops the old entity edge that only Caroline joins (the
	// events share no other cluster) and keeps the temporal one: hotness
	// is recomputed when the edges are built.
	versions := map[string]int64{}
	for i := range 12 {
		versions[fmt.Sprintf("r%d", i)] = 1
	}
	links := buildEdges([]timelineState{*state}, versions, []string{"temporal", "entity"})
	has := func(a, b, kind string) bool {
		return slices.ContainsFunc(links[a].Links, func(l records.Link) bool { return l.ID == b && l.Type == kind }) ||
			slices.ContainsFunc(links[b].Links, func(l records.Link) bool { return l.ID == a && l.Type == kind })
	}
	if has("r11", "r0", "entity") || !has("r11", "r5", "entity") || !has("r11", "r2", "entity") || !has("r11", "r0", "temporal") {
		t.Fatal(links["r11"], links["r0"], links["r5"])
	}
	if state.EventEdges.Answers["entity"][ids[11]][ids[0]] != 0.9 {
		t.Fatal("the decision was dropped from the state")
	}
}

func TestEventEdgeLexicalFallback(t *testing.T) {
	server := &embeddingServer{fail: true}
	url := server.start(t, func(string) []float64 { return []float64{1, 0} })
	embedding := &records.EmbeddingConfig{URL: url, Model: "test", QueryPrefix: "search_query: ", DocumentPrefix: "search_document: "}
	result := &ConsolidateResult{}
	if _, err := linkAgent(t, edgeAnswers(nil), 8, embedding).newLinker().decideEventEdges(context.Background(), linkState0(cabinMemory()), []string{"semantic"}, result, nil); err != nil || result.EdgeLexical != 1 {
		t.Fatal(err, result.EdgeLexical)
	}
	// Shared words without an embedder are no fallback.
	result = &ConsolidateResult{}
	if _, err := linkAgent(t, edgeAnswers(nil), 8, nil).newLinker().decideEventEdges(context.Background(), linkState0(cabinMemory()), []string{"semantic"}, result, nil); err != nil || result.EdgeLexical != 0 {
		t.Fatal(err, result.EdgeLexical)
	}
	if raw, _ := json.Marshal(ConsolidateResult{EdgeLexical: 1}); !strings.Contains(string(raw), `"edgeLexical":1`) {
		t.Fatal(string(raw))
	}
}

func TestEventEdgeCandidatesBySimilarity(t *testing.T) {
	// One candidate: by meaning the kayak, by shared words the garden.
	server := &embeddingServer{}
	url := server.start(t, func(text string) []float64 {
		switch strings.TrimPrefix(text, "search_document: ") {
		case "Booked a kayak.", "Rented a boat for the lake.":
			return []float64{1, 0, 0}
		}
		return []float64{0, 0, 1}
	})
	embedding := &records.EmbeddingConfig{URL: url, Model: "test", QueryPrefix: "search_query: ", DocumentPrefix: "search_document: "}
	m := timelineMemory{
		Threads: []thread{{ID: "T1", Title: "Kayak"}, {ID: "T2", Title: "Garden"}, {ID: "T3", Title: "Boat"}},
		Events: []event{
			{Thread: "T1", Date: "2024/01/02", Text: "Booked a kayak.", Records: []string{"r1"}},
			{Thread: "T2", Date: "2024/01/03", Text: "Planted tomatoes by the lake.", Records: []string{"r2"}},
		},
		Facts: map[string][]factValue{}, FactOrder: []string{}, Directives: []directive{},
	}
	judge := edgeAnswers(nil)
	a := linkAgent(t, judge, 1, embedding)
	state := linkState0(m)
	if _, err := a.newLinker().decideEventEdges(context.Background(), state, []string{"semantic"}, &ConsolidateResult{}, nil); err != nil {
		t.Fatal(err)
	}
	state.Memory.Events = append(state.Memory.Events, event{Thread: "T3", Date: "2024/01/05", Text: "Rented a boat for the lake.", Records: []string{"r3"}})
	judge.calls = nil
	if _, err := a.newLinker().decideEventEdges(context.Background(), state, []string{"semantic"}, &ConsolidateResult{}, nil); err != nil {
		t.Fatal(err)
	}
	if got := askedPairs(t, judge.calls); !slices.Equal(got, []string{"semantic Rented a boat for the lake. ~ Booked a kayak."}) {
		t.Fatal(got)
	}
	// The texts are embedded as documents; the decided events' are kept,
	// the new event's are not.
	kept, err := a.store.DerivedVectors(context.Background(), []string{"Booked a kayak.", "Planted tomatoes by the lake.", "Rented a boat for the lake."}, *embedding, false)
	if err != nil || kept[0] == nil || kept[1] == nil || kept[2] != nil {
		t.Fatal(kept[0] != nil, kept[1] != nil, kept[2] != nil, err)
	}
	// A failing embedder leaves shared words.
	server.mu.Lock()
	server.fail = true
	server.mu.Unlock()
	judge = edgeAnswers(nil)
	if _, err := linkAgent(t, judge, 1, embedding).newLinker().decideEventEdges(context.Background(), linkState0(state.Memory), []string{"semantic"}, &ConsolidateResult{}, nil); err != nil {
		t.Fatal(err)
	}
	if got := askedPairs(t, judge.calls); !slices.Contains(got, "semantic Rented a boat for the lake. ~ Planted tomatoes by the lake.") || slices.Contains(got, "semantic Rented a boat for the lake. ~ Booked a kayak.") {
		t.Fatal(got)
	}
}

// judgedState is three events of a thread, a key whose second value
// supersedes the first, and JEV's answers about the events of every kind.
// A cites ra1 and ra2, B rb, C rc and ra1.
func judgedState() timelineState {
	state := emptyTimelineState()
	state.Memory = timelineMemory{
		Threads: []thread{{ID: "T1", Title: "Cabin trip"}},
		Events: []event{
			{Thread: "T1", Date: "2024/01/02", Text: "A", Records: []string{"ra1", "ra2"}, Entities: []string{"Greg"}},
			{Thread: "T1", Date: "2024/01/05", Text: "B", Records: []string{"rb"}, Entities: []string{"Greg Hall"}},
			{Thread: "T1", Date: "2024/01/09", Text: "C", Records: []string{"rc", "ra1"}},
		},
		Facts: map[string][]factValue{"cabin · date": {
			{Value: "May 3", Date: "2024/01/02", Records: []string{"ra2"}},
			{Value: "May 10", Date: "2024/01/05", Records: []string{"rb"}},
		}},
		FactOrder: []string{"cabin · date"}, Directives: []directive{},
	}
	state.EntityClusters = map[string]string{"Greg": "Greg Hall", "Greg Hall": "Greg Hall"}
	a, b, c := eventID(state.Memory.Events[0]), eventID(state.Memory.Events[1]), eventID(state.Memory.Events[2])
	state.EventEdges = &eventEdges{Answers: map[string]map[string]map[string]float64{
		"temporal": {b: {a: 0.8}, c: {b: 0.6}},
		"semantic": {b: {a: 0}, c: {a: 0.9}},
		"causal":   {c: {b: 0.7}},
		"entity":   {b: {a: 0.55}},
	}}
	return state
}

func TestBuildEdgesFromEventEdges(t *testing.T) {
	versions := map[string]int64{"ra1": 1, "ra2": 1, "rb": 2, "rc": 1}
	got := buildEdges([]timelineState{judgedState()}, versions, allEdgeKinds)
	// Each edge links every record of one event to every record of the
	// other, with its kind and weight: temporal and causal both ways;
	// semantic and entity, which the records' index reads both ways, once
	// per pair, on the record with fewer links. A pair of records two edges
	// of a kind join keeps the higher weight. Per record: supersedes first,
	// then the highest weights. The thread's rule links are gone; the
	// names, the supersedes link and the dates stay.
	want := map[string]records.DerivedMetadata{
		"ra1": {Version: 1, Entities: []string{"Greg"}, Aliases: []string{"Greg Hall", "Hall"}, EventAt: "2024-01-02", Links: []records.Link{
			edgeLink("ra2", 1, "semantic", 0.9), edgeLink("rb", 2, "temporal", 0.8), edgeLink("rb", 2, "causal", 0.7), edgeLink("rb", 2, "entity", 0.55)}},
		"ra2": {Version: 1, Entities: []string{"Greg"}, Aliases: []string{"Greg Hall", "Hall"}, EventAt: "2024-01-02", Links: []records.Link{
			edgeLink("rc", 1, "semantic", 0.9), edgeLink("rb", 2, "temporal", 0.8), edgeLink("rb", 2, "entity", 0.55)}},
		"rb": {Version: 2, Entities: []string{"Greg Hall"}, Aliases: []string{"Greg", "Hall"}, EventAt: "2024-01-05", Links: []records.Link{
			edgeLink("ra2", 1, "supersedes", 1), edgeLink("ra1", 1, "temporal", 0.8), edgeLink("ra2", 1, "temporal", 0.8),
			edgeLink("ra1", 1, "causal", 0.7), edgeLink("rc", 1, "causal", 0.7), edgeLink("rc", 1, "temporal", 0.6)}},
		"rc": {Version: 1, EventAt: "2024-01-09", Links: []records.Link{
			edgeLink("ra1", 1, "semantic", 0.9), edgeLink("rb", 2, "causal", 0.7), edgeLink("rb", 2, "temporal", 0.6)}},
	}
	if !reflect.DeepEqual(got, want) {
		for id := range want {
			if !reflect.DeepEqual(got[id], want[id]) {
				t.Errorf("%s:\n got %+v\nwant %+v", id, got[id], want[id])
			}
		}
		t.Fatalf("records %d, want %d", len(got), len(want))
	}
	// Only the listed kinds are projected.
	got = buildEdges([]timelineState{judgedState()}, versions, []string{"causal"})
	if !reflect.DeepEqual(got["rc"].Links, []records.Link{edgeLink("rb", 2, "causal", 0.7)}) ||
		!reflect.DeepEqual(got["rb"].Links, []records.Link{edgeLink("ra2", 1, "supersedes", 1), edgeLink("ra1", 1, "causal", 0.7), edgeLink("rc", 1, "causal", 0.7)}) {
		t.Fatal(got["rc"].Links, got["rb"].Links)
	}
	// A record missing from the store gets no row and no link.
	delete(versions, "rc")
	got = buildEdges([]timelineState{judgedState()}, versions, allEdgeKinds)
	if _, ok := got["rc"]; ok {
		t.Fatal(got["rc"])
	}
	for id, item := range got {
		for _, link := range item.Links {
			if link.ID == "rc" {
				t.Fatal(id, link)
			}
		}
	}
	// Without edgeKinds the decisions are ignored: stage 2 exactly.
	plain := judgedState()
	plain.EventEdges = nil
	versions["rc"] = 1
	if stage2, judged := buildEdges([]timelineState{plain}, versions, nil), buildEdges([]timelineState{judgedState()}, versions, nil); !reflect.DeepEqual(stage2, judged) {
		t.Fatal("decisions changed the stage-2 edges", judged)
	} else if links := stage2["rc"].Links; len(links) == 0 || links[0].Type != "entity" || links[0].Weight != 0.6 {
		t.Fatal("stage 2 has no thread links", links)
	}
}

func TestBuildEdgesFromEventEdgesCap(t *testing.T) {
	// A hub event with 51 temporal edges of weights 0.50 … 1.00, and a
	// semantic edge (0.6) to the event of the weakest: the hub keeps its
	// 50 strongest; the semantic edge lives on the other record. A second
	// hub as full: a semantic edge between the hubs, weaker than all their
	// links, is dropped.
	state := emptyTimelineState()
	state.Memory.Threads = []thread{{ID: "T1", Title: "Hub"}}
	versions := map[string]int64{"hub": 1, "hub2": 1}
	state.Memory.Events = []event{{Thread: "T1", Date: "2024/03/01", Text: "hub", Records: []string{"hub"}}, {Thread: "T1", Date: "2024/03/01", Text: "hub2", Records: []string{"hub2"}}}
	hub, hub2 := eventID(state.Memory.Events[0]), eventID(state.Memory.Events[1])
	answers := map[string]map[string]float64{hub: {}, hub2: {}}
	for i := range 51 {
		for h, prefix := range []string{"x", "y"} {
			id := fmt.Sprintf("%s%02d", prefix, i)
			versions[id] = 1
			e := event{Thread: "T1", Date: "2024/02/01", Text: id, Records: []string{id}}
			state.Memory.Events = append(state.Memory.Events, e)
			answers[[]string{hub, hub2}[h]][eventID(e)] = float64(50+i) / 100
		}
	}
	x00 := eventID(state.Memory.Events[2])
	state.EventEdges = &eventEdges{Answers: map[string]map[string]map[string]float64{
		"temporal": answers,
		"semantic": {hub: {x00: 0.6, hub2: 0.5}},
	}}
	got := buildEdges([]timelineState{state}, versions, allEdgeKinds)
	links := got["hub"].Links
	if len(links) != 50 || links[0] != edgeLink("x50", 1, "temporal", 1) || links[49] != edgeLink("x01", 1, "temporal", 0.51) {
		t.Fatal(len(links), links[0], links[len(links)-1])
	}
	if !reflect.DeepEqual(got["x00"].Links, []records.Link{edgeLink("hub", 1, "semantic", 0.6), edgeLink("hub", 1, "temporal", 0.5)}) {
		t.Fatal(got["x00"].Links)
	}
	for _, id := range []string{"hub", "hub2"} {
		for _, link := range got[id].Links {
			if link.Type == "semantic" {
				t.Fatal("a weaker link outlived the cap", id, link)
			}
		}
	}
	s, _ := agentStore(t)
	if err := s.ReplaceDerivedMetadata(context.Background(), edgesSource, got); err != nil {
		t.Fatal("the store refused the built rows", err)
	}
}

// topicEdgeJudge is topicJudge that also says two events of one topic are
// joined by a semantic (0.9), temporal (0.8) and entity (0.7) edge, never
// a causal one.
func topicEdgeJudge() *linkJudge {
	topics := topicJudge()
	return &linkJudge{answer: func(kind, newText, candidate string) float64 {
		weights := map[string]float64{"semantic": 0.9, "temporal": 0.8, "entity": 0.7, "causal": 0.2}
		if weight, ok := weights[kind]; ok {
			if strings.Fields(newText)[0] == strings.Fields(candidate)[0] {
				return weight
			}
			return 0.1
		}
		return topics.answer(kind, newText, candidate)
	}}
}

func TestEdgeKindsLayeredJob(t *testing.T) {
	s, dir := agentStore(t)
	seedTopics(t, s, 3)
	model := &limitPlanner{reply: func(_, prompt string) (string, error) { return layeredReply(prompt), nil }}
	judge := topicEdgeJudge()
	a, overlay := layeredAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100, NativeEdges: true, EdgeKinds: allEdgeKinds}, model, judge)
	result, err := a.Consolidate(context.Background(), ConsolidateRequest{})
	if err != nil || result.Folded != 3 {
		t.Fatal(result, err)
	}
	// Nine notes, three of each topic, all of one day: each event is asked
	// about the events before it (all share a word: semantic and causal),
	// those of its thread or names (temporal, entity). JEV joins the
	// events of a topic: three pairs a topic.
	if result.EdgeCalls != 1 || result.EdgeFallbacks != 0 ||
		!reflect.DeepEqual(result.EdgeAsked, map[string]int{"semantic": 36, "temporal": 9, "causal": 36, "entity": 9}) ||
		!reflect.DeepEqual(result.EdgeYes, map[string]int{"semantic": 9, "temporal": 9, "entity": 9}) {
		t.Fatalf("%+v", result)
	}
	// Record links: semantic and entity once a pair, temporal both ways, and
	// the values' supersedes; no thread links.
	if result.EdgeRecords != 9 || !reflect.DeepEqual(result.EdgeLinks, map[string]int{"semantic": 9, "temporal": 18, "entity": 9, "supersedes": 6}) {
		t.Fatalf("%+v", result)
	}
	rows := buildEdges([]timelineState{loadState(t, overlay)}, map[string]int64{"n00": 1, "n01": 1, "n02": 1, "n03": 1, "n04": 1, "n05": 1, "n06": 1, "n07": 1, "n08": 1}, allEdgeKinds)
	for id, row := range rows {
		for _, link := range row.Links {
			want := map[string]float64{"semantic": 0.9, "temporal": 0.8, "entity": 0.7, "supersedes": 1}[link.Type]
			if link.Weight != want || id[:2] != link.ID[:2] || (id[2]-'0')%3 != (link.ID[2]-'0')%3 {
				t.Fatal(id, link)
			}
		}
	}
	state := loadState(t, overlay)
	if state.EventEdges == nil || len(state.EventEdges.Decided["causal"]) != 9 || state.EdgesMemory != edgesDigest(state.Memory) {
		t.Fatalf("%+v", state.EventEdges)
	}
	// The records' index reads the links: a topic's notes reach each other
	// through an entity link from either end, and through temporal links
	// both ways.
	relations := &records.IndexPolicy{Version: 1, Relations: true}
	for query, want := range map[string][]string{"00": {"n03", "n06"}, "06": {"n00", "n03"}} {
		got := queryIDs(t, s, query, relations, nil)
		when := queryIDs(t, s, query, relations, map[string]string{"intent": "WHEN"})
		for _, id := range want {
			if !slices.Contains(got, id) || !slices.Contains(when, id) {
				t.Fatal(query, got, when)
			}
		}
		if slices.Contains(got, "n01") || slices.Contains(when, "n01") {
			t.Fatal("another topic", got, when)
		}
	}
	// A rebuild asks nothing again and writes the same rows.
	again, err := a.Consolidate(context.Background(), ConsolidateRequest{RebuildEdges: true})
	if err != nil || again.EdgeCalls != 0 || again.EdgeRecords != 9 || !reflect.DeepEqual(again.EdgeLinks, result.EdgeLinks) || judge.count() != result.LinkCalls+result.AliasCalls+1 {
		t.Fatal(again, err, judge.count())
	}
	// A reset forgets the decisions with the memory.
	if _, err := a.Consolidate(context.Background(), ConsolidateRequest{Reset: true, MaxBatches: 1}); err != nil {
		t.Fatal(err)
	}
	if state := loadState(t, overlay); len(state.EventEdges.Decided["semantic"]) != 3 {
		t.Fatalf("%+v", state.EventEdges)
	}
}

func TestEdgeKindsOffKeepsTheState(t *testing.T) {
	s, dir := agentStore(t)
	seedTopics(t, s, 2)
	model := &limitPlanner{reply: func(_, prompt string) (string, error) { return layeredReply(prompt), nil }}
	a, overlay := layeredAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100, NativeEdges: true}, model, topicEdgeJudge())
	result, err := a.Consolidate(context.Background(), ConsolidateRequest{})
	if err != nil || result.EdgeRecords != 6 || result.EdgeLinks["entity"] == 0 || result.EdgeLinks["semantic"] != 0 {
		t.Fatal(result, err)
	}
	state, _ := json.Marshal(loadState(t, overlay))
	out, _ := json.Marshal(result)
	for _, field := range []string{"eventEdges", "edgeCalls", "edgeAsked", "edgeYes", "edgeFallbacks"} {
		if strings.Contains(string(state), field) || strings.Contains(string(out), field) {
			t.Fatal(field, string(state), string(out))
		}
	}
}

func TestEventEdgePackingBounds(t *testing.T) {
	// Forty long events of one thread, every kind: each event after the
	// eighth has eight candidates for semantic, temporal and causal, and
	// those of its group of five for entity (Greg and the rest name every
	// event: hot). The questions take many calls, each within 128 questions
	// and 96,000 units, each question naming items its call holds, each
	// event naming at most 16 names.
	m := timelineMemory{Threads: []thread{{ID: "T1", Title: "Cabin trip"}}, Facts: map[string][]factValue{}, FactOrder: []string{}, Directives: []directive{}}
	long := strings.Repeat("渡し舟の予定 cabin ", 100)
	for i := range 40 {
		names := []string{"Greg", strings.Repeat("Name ", 200), fmt.Sprintf("Group %d", i/5)}
		for n := range 17 {
			names = append(names, fmt.Sprintf("Name %d", n))
		}
		m.Events = append(m.Events, event{Thread: "T1", Date: fmt.Sprintf("2024/%02d/%02d", i/28+1, i%28+1), Text: fmt.Sprintf("event %d %s", i, long), Records: []string{fmt.Sprintf("r%d", i)}, Entities: names})
	}
	judge := edgeAnswers(nil)
	result := &ConsolidateResult{}
	if _, err := linkAgent(t, judge, 8, nil).newLinker().decideEventEdges(context.Background(), linkState0(m), allEdgeKinds, result, nil); err != nil || result.EdgeFallbacks != 0 {
		t.Fatal(err, result)
	}
	questions := 0
	for _, call := range judge.calls {
		if len(call.Questions) > 128 || call.Size > maxJEVRequest {
			t.Fatal("call over the limits", len(call.Questions), call.Size)
		}
		for _, item := range append(slices.Clone(call.State.New), call.State.Candidates...) {
			if jsLen(item.Text) > 600 || jsLen(item.Entities[1]) > 600 || len(item.Entities) != 16 || item.Entities[15] != "Name 12" {
				t.Fatal("a text was not cut", jsLen(item.Text), item.Entities)
			}
		}
		questions += len(call.Questions)
	}
	asked := 0
	for _, n := range result.EdgeAsked {
		asked += n
	}
	// 3 kinds × (0+1+…+8 + 31×8) + 8 groups × (0+1+2+3+4).
	if questions != 3*284+80 || asked != questions || judge.count() != result.EdgeCalls || judge.count() <= questions/128 {
		t.Fatal(questions, asked, judge.count(), result.EdgeCalls)
	}
	askedPairs(t, judge.calls)
}

// speakerMemory is twelve events, a day apart, on three threads of four:
// Caroline names nine of them (all but the fourth), Melanie the third, the
// sixth and the last.
func speakerMemory() timelineMemory {
	m := timelineMemory{Threads: []thread{{ID: "T1", Title: "Group"}, {ID: "T2", Title: "Painting"}, {ID: "T3", Title: "Pottery"}}, Facts: map[string][]factValue{}, FactOrder: []string{}, Directives: []directive{}}
	for i := range 12 {
		names := []string{"Caroline"}
		if i == 3 {
			names = nil
		}
		if i == 2 || i == 5 || i == 11 {
			names = append(names, "Melanie")
		}
		m.Events = append(m.Events, event{Thread: fmt.Sprintf("T%d", i/4+1), Date: fmt.Sprintf("2024/01/%02d", i+1), Text: fmt.Sprintf("event %d", i), Records: []string{fmt.Sprintf("r%d", i)}, Entities: names})
	}
	return m
}

func TestHotClusters(t *testing.T) {
	state := emptyTimelineState()
	state.Memory = speakerMemory()
	nodes := eventNodes(state)
	// Caroline names 9 of 12 events: more than a quarter, and at least 8.
	if got := hotClusters(nodes); !reflect.DeepEqual(got, map[string]bool{"Caroline": true}) {
		t.Fatal(got)
	}
	// Fewer than 8 events naming it is never hot; a quarter is not more
	// than a quarter.
	if got := hotClusters(nodes[:8]); len(got) != 0 {
		t.Fatal(got)
	}
	quarter := []eventNode{}
	for i := range 32 {
		n := eventNode{id: fmt.Sprint(i)}
		if i < 8 {
			n.clusters = []string{"X"}
		}
		if i < 9 {
			n.clusters = append(n.clusters, "Y")
		}
		quarter = append(quarter, n)
	}
	if got := hotClusters(quarter); !reflect.DeepEqual(got, map[string]bool{"Y": true}) {
		t.Fatal(got)
	}
	// A hot cluster never counts as shared: the last event (Caroline,
	// Melanie) shares Melanie with the third and the sixth only.
	none := make([]int, len(nodes))
	for i := range none {
		none[i] = -1
	}
	hot := hotClusters(nodes)
	if got := edgeCandidates("entity", nodes, 11, none, hot, 8); !reflect.DeepEqual(got, []int{5, 2}) {
		t.Fatal(got)
	}
	// Temporal: its thread first, nearest day first, then the events of a
	// shared cluster that is not hot.
	if got := edgeCandidates("temporal", nodes, 11, none, hot, 8); !reflect.DeepEqual(got, []int{10, 9, 8, 5, 2}) {
		t.Fatal(got)
	}
	// Without the stop-list Caroline would bring every event.
	if got := edgeCandidates("entity", nodes, 11, none, nil, 20); len(got) != 10 {
		t.Fatal(got)
	}
}

func TestRebuildEdgesCanceledKeepsAnswers(t *testing.T) {
	s, dir := agentStore(t)
	m := cabinMemory()
	m.Events = append(m.Events, event{Thread: "T1", Date: "2024/01/09", Text: "Greg cancelled the cabin.", Records: []string{"r3"}, Entities: []string{"Greg"}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	judge := edgeAnswers(map[string]float64{"temporal": 0.9})
	judge.err = func(context.Context) error {
		if judge.count() == 2 {
			cancel()
			return context.Canceled
		}
		return nil
	}
	a, overlay := layeredAgent(t, s, dir, ConsolidationConfig{NativeEdges: true, EdgeKinds: []string{"temporal"}}, &limitPlanner{}, limitedJudge{judge, 1})
	stored := emptyTimelineState()
	stored.Mode, stored.Memory = "layered", m
	if _, err := overlay.saveTimelines(context.Background(), records.Scope{}, "", stored); err != nil {
		t.Fatal(err)
	}
	// Canceled in its second call, the rebuild keeps the first answer in
	// the stored state; the records are not replaced, nor the state marked
	// built.
	if _, err := a.Consolidate(ctx, ConsolidateRequest{RebuildEdges: true}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	state := loadState(t, overlay)
	if state.EventEdges == nil || !state.EventEdges.answered("temporal", eventID(m.Events[1]), eventID(m.Events[0])) || state.EdgesMemory != "" {
		t.Fatalf("%+v %q", state.EventEdges, state.EdgesMemory)
	}
	if rows, _ := derivedOverlay(t, dir); rows != 0 {
		t.Fatal(rows)
	}
	// The next rebuild asks only the rest.
	judge.err, judge.calls = nil, nil
	result, err := a.Consolidate(context.Background(), ConsolidateRequest{RebuildEdges: true})
	if err != nil || result.EdgeCalls != 2 || result.EdgeAsked["temporal"] != 2 {
		t.Fatal(result, err)
	}
	if state := loadState(t, overlay); state.EdgesMemory != edgesDigest(state.Memory) || len(state.EventEdges.Decided["temporal"]) != 3 {
		t.Fatalf("%+v", state)
	}
}
