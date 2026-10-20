package memoryagent

import (
	"context"
	"encoding/json"
	"errors"
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
		// Same thread or a shared cluster (Cabin Co): the nearest day
		// first, the more similar of the 5th first, the undated last.
		"temporal": {3, 2, 0, 5},
		// The same thread first, then by similarity.
		"causal": {2, 5, 0, 3},
		// A shared cluster, by similarity.
		"entity": {3, 2},
	} {
		if got := edgeCandidates(kind, nodes, 6, deposit, 8); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %v, want %v", kind, got, want)
		}
	}
	if got := edgeCandidates("semantic", nodes, 6, deposit, 2); !reflect.DeepEqual(got, []int{3, 2}) {
		t.Fatal("limit", got)
	}
	if got := edgeCandidates("temporal", nodes, 6, deposit, 2); !reflect.DeepEqual(got, []int{3, 2}) {
		t.Fatal("limit", got)
	}
	// Greg Hall's booking (node 2) shares a cluster with Greg's suggestion
	// (0) by the alias decision, not by a word; nothing is similar.
	none := similar(2)
	if got := edgeCandidates("entity", nodes, 2, none, 8); !reflect.DeepEqual(got, []int{0}) {
		t.Fatal(got)
	}
	if got := edgeCandidates("semantic", nodes, 2, none, 8); len(got) != 0 {
		t.Fatal(got)
	}
	if got := edgeCandidates("causal", nodes, 2, none, 8); !reflect.DeepEqual(got, []int{0}) {
		t.Fatal(got)
	}
	// The undated check (node 5) is compared with the events stored
	// before it, the nearest in the memory first when nothing else tells.
	if got := edgeCandidates("temporal", nodes, 5, similar(5), 8); !reflect.DeepEqual(got, []int{4, 2, 0}) {
		t.Fatal(got)
	}
	// The packing (node 7) follows the deposit of the same day (6).
	if got := edgeCandidates("temporal", nodes, 7, similar(7), 8); !reflect.DeepEqual(got, []int{6, 2, 0, 5}) {
		t.Fatal(got)
	}
	// The first event has nothing before it.
	for _, kind := range []string{"semantic", "temporal", "causal", "entity"} {
		if got := edgeCandidates(kind, nodes, 0, similar(0, 1, 2, 3), 8); len(got) != 0 {
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
	if _, err := a.newLinker().decideEventEdges(context.Background(), state, allEdgeKinds, result); err != nil {
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
	if _, err := linkAgent(t, judge, 8, nil).newLinker().decideEventEdges(context.Background(), linkState0(m), []string{"temporal"}, &ConsolidateResult{}); err != nil {
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
	changed, err := a.newLinker().decideEventEdges(context.Background(), state, allEdgeKinds, result)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	first, second := eventID(state.Memory.Events[0]), eventID(state.Memory.Events[1])
	// At least 0.5 is an edge weighted by its probability, to two decimals;
	// a no is kept as 0. Both events are decided for every kind.
	want := &eventEdges{
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
	if changed, err := a.newLinker().decideEventEdges(context.Background(), state, allEdgeKinds, again); err != nil || changed || judge.count() != 1 {
		t.Fatal(changed, err, judge.count())
	}
	if raw, _ := json.Marshal(again); strings.Contains(string(raw), "edge") {
		t.Fatal(string(raw))
	}
	// The kinds asked are those listed.
	judge = edgeAnswers(nil)
	only := linkState0(cabinMemory())
	if _, err := linkAgent(t, judge, 8, nil).newLinker().decideEventEdges(context.Background(), only, []string{"causal"}, &ConsolidateResult{}); err != nil {
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
	if _, err := a.newLinker().decideEventEdges(context.Background(), state, allEdgeKinds, &ConsolidateResult{}); err != nil {
		t.Fatal(err)
	}
	// A new event is asked about against the events before it; the decided
	// ones are not asked about again.
	state.Memory.Events = append(state.Memory.Events, event{Thread: "T1", Date: "2024/01/09", Text: "Greg cancelled the cabin.", Records: []string{"r3"}, Entities: []string{"Greg"}})
	judge.calls = nil
	result := &ConsolidateResult{}
	if changed, err := a.newLinker().decideEventEdges(context.Background(), state, allEdgeKinds, result); err != nil || !changed {
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
	if _, err := a.newLinker().decideEventEdges(context.Background(), state, []string{"temporal", "causal"}, &ConsolidateResult{}); err != nil || judge.count() != 0 {
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
	changed, err := a.newLinker().decideEventEdges(context.Background(), state, allEdgeKinds, result)
	if err != nil || !changed {
		t.Fatal("a JEV failure failed the step", changed, err)
	}
	first, second := eventID(state.Memory.Events[0]), eventID(state.Memory.Events[1])
	// The answered pairs are kept; the others stay undecided, not "no".
	want := &eventEdges{
		Decided: map[string][]string{"semantic": {first, second}, "temporal": {first, second}, "causal": {first}, "entity": {first}},
		Answers: map[string]map[string]map[string]float64{"semantic": {second: {first: 0.9}}, "temporal": {second: {first: 0.9}}},
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
	if changed, err = a.newLinker().decideEventEdges(context.Background(), state, allEdgeKinds, result); err != nil || !changed || result.EdgeFallbacks != 0 {
		t.Fatal(changed, err, result)
	}
	if got := askedPairs(t, judge.calls); !slices.Equal(got, []string{"causal Greg paid the cabin deposit. ~ Greg booked the cabin.", "entity Greg paid the cabin deposit. ~ Greg booked the cabin."}) {
		t.Fatal(got)
	}
	if len(state.EventEdges.Decided["causal"]) != 2 || state.EventEdges.Answers["entity"][second][first] != 0.9 {
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
	if _, err := a.newLinker().decideEventEdges(context.Background(), state, []string{"temporal"}, &ConsolidateResult{}); err != nil {
		t.Fatal(err)
	}
	third := eventID(state.Memory.Events[2])
	if len(state.EventEdges.Decided["temporal"]) != 2 || len(state.EventEdges.Answers["temporal"][third]) != 1 {
		t.Fatalf("%+v", state.EventEdges)
	}
	down = false
	judge.calls = nil
	if _, err := a.newLinker().decideEventEdges(context.Background(), state, []string{"temporal"}, &ConsolidateResult{}); err != nil {
		t.Fatal(err)
	}
	if got := askedPairs(t, judge.calls); !slices.Equal(got, []string{"temporal Greg cancelled the cabin. ~ Greg booked the cabin."}) || len(state.EventEdges.Decided["temporal"]) != 3 {
		t.Fatal(got, state.EventEdges)
	}
	// Every call failing, or no decision model, decides no pair.
	judge = edgeAnswers(nil)
	judge.err = func(context.Context) error { return ErrBudgetCap }
	state, result = linkState0(cabinMemory()), &ConsolidateResult{}
	if _, err := linkAgent(t, judge, 8, nil).newLinker().decideEventEdges(context.Background(), state, allEdgeKinds, result); err != nil || result.EdgeFallbacks != 4 || result.EdgeCalls != 1 || state.EventEdges.Answers != nil {
		t.Fatal(err, result, state.EventEdges)
	}
	s, _ := agentStore(t)
	none, err := New(s, Config{Version: 1, Policy: &ReadPolicy{}, Consolidation: &ConsolidationConfig{Mode: "timelines"}}, Models{Planner: &limitPlanner{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	state, result = linkState0(cabinMemory()), &ConsolidateResult{}
	if _, err := none.newLinker().decideEventEdges(context.Background(), state, allEdgeKinds, result); err != nil || result.EdgeFallbacks != 4 || result.EdgeCalls != 0 {
		t.Fatal(err, result)
	}
	// A canceled job decides nothing.
	ctx, cancel := context.WithCancel(context.Background())
	judge = edgeAnswers(nil)
	judge.err = func(context.Context) error { cancel(); return context.Canceled }
	state = linkState0(cabinMemory())
	if _, err := linkAgent(t, judge, 8, nil).newLinker().decideEventEdges(ctx, state, allEdgeKinds, &ConsolidateResult{}); !errors.Is(err, context.Canceled) || state.EventEdges != nil {
		t.Fatal(err, state.EventEdges)
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
	if _, err := a.newLinker().decideEventEdges(context.Background(), state, []string{"semantic"}, &ConsolidateResult{}); err != nil {
		t.Fatal(err)
	}
	state.Memory.Events = append(state.Memory.Events, event{Thread: "T3", Date: "2024/01/05", Text: "Rented a boat for the lake.", Records: []string{"r3"}})
	judge.calls = nil
	if _, err := a.newLinker().decideEventEdges(context.Background(), state, []string{"semantic"}, &ConsolidateResult{}); err != nil {
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
	if _, err := linkAgent(t, judge, 1, embedding).newLinker().decideEventEdges(context.Background(), linkState0(state.Memory), []string{"semantic"}, &ConsolidateResult{}); err != nil {
		t.Fatal(err)
	}
	if got := askedPairs(t, judge.calls); !slices.Contains(got, "semantic Rented a boat for the lake. ~ Planted tomatoes by the lake.") || slices.Contains(got, "semantic Rented a boat for the lake. ~ Booked a kayak.") {
		t.Fatal(got)
	}
}
