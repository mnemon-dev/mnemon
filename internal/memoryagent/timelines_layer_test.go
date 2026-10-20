package memoryagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/records"
)

// sized is a text of exactly n UTF-16 units that starts with the ASCII
// label; the fill is three bytes a unit, so byte counts differ.
func sized(label string, n int) string {
	return label + strings.Repeat("日", n-len(label))
}

func blockIDs(items []IndexItem) []string {
	out := []string{}
	for _, item := range items {
		out = append(out, item.ID)
	}
	return out
}

func linkedIDs(item IndexItem) []string {
	out := []string{}
	for _, ref := range item.Records {
		out = append(out, ref.ID)
	}
	return out
}

func TestLayerTextsFormat(t *testing.T) {
	m := timelineMemory{
		Threads: []thread{{ID: "T1", Title: "Title"}, {ID: "T2", Title: "Quiet"}, {ID: "T3", Title: "Long"}},
		Events: []event{
			{Thread: "T1", Date: "2024/03/01", Text: "a"},
			{Thread: "T3", Date: "2024/01/01", Text: "one"},
			{Thread: "T1", Date: "2024/03/05", Text: "b"},
			{Thread: "T3", Date: "2024/01/02", Text: "two"},
			{Thread: "T3", Date: "2024/01/03", Text: "three"},
			{Thread: "T3", Date: "2024/01/04", Text: "four"},
		},
		Facts: map[string][]factValue{
			"rent": {{Value: "900", Date: "2024/01/01"}, {Value: "950", Date: "2024/06/01", Conflict: true}},
			"car":  {{Value: "red", Date: "2024/02/02"}},
		},
		FactOrder: []string{"rent", "car"},
	}
	threads, values := layerTexts(m)
	want := map[string]string{
		"T1": "Title: 2 events, 2024/03/01 to 2024/03/05. Latest: 2024/03/01 a / 2024/03/05 b",
		"T3": "Long: 4 events, 2024/01/01 to 2024/01/04. Latest: 2024/01/02 two / 2024/01/03 three / 2024/01/04 four",
	}
	if !reflect.DeepEqual(threads, want) {
		t.Fatalf("thread texts %q", threads)
	}
	wantValues := map[string]string{
		"rent": "rent: 2024/01/01 900; 2024/06/01 950 (contradicts the earlier value)",
		"car":  "car: 2024/02/02 red",
	}
	if !reflect.DeepEqual(values, wantValues) {
		t.Fatalf("value texts %q", values)
	}
	ids, by := eventThreads(m)
	if !slices.Equal(ids, []string{"T1", "T3"}) || !slices.Equal(by["T3"], []int{1, 3, 4, 5}) {
		t.Fatal(ids, by)
	}
}

func TestLightUp(t *testing.T) {
	m := timelineMemory{
		Threads: []thread{{ID: "T1", Title: "a"}, {ID: "T2", Title: "b"}},
		Events: []event{
			{Thread: "T2", Records: []string{"r1", "r1", "x"}},
			{Thread: "T1", Records: []string{"x"}},
			{Thread: "T1", Records: []string{"r2"}},
			{Thread: "T2", Records: []string{"r2"}},
		},
		Facts: map[string][]factValue{
			"k1": {{Records: []string{"x"}}},
			"k2": {{Records: []string{"x"}}, {Records: []string{"r1"}}},
			"k3": {{Records: []string{"r2"}}},
		},
		FactOrder: []string{"k3", "k1", "k2"},
	}
	threads, counts, events, values := lightUp(m, map[string]bool{"r1": true, "r2": true})
	if !slices.Equal(threads, []string{"T2", "T1"}) || !reflect.DeepEqual(counts, map[string]int{"T2": 3, "T1": 1}) {
		t.Fatal(threads, counts)
	}
	if !reflect.DeepEqual(events, map[int]bool{0: true, 2: true, 3: true}) || !slices.Equal(values, []string{"k3", "k2"}) {
		t.Fatal(events, values)
	}
}

func TestLayerOrder(t *testing.T) {
	lit := litSets{Threads: []string{"T2", "T1", "T3"}, Counts: map[string]int{"T2": 3, "T1": 1, "T3": 5}, Values: []string{"k1", "k2"}}
	topics := []string{"T2", "T1", "T3", "T5", "T6", "T7"}
	threadScore := map[string]float64{"T5": 0.9, "T1": 0.6, "T7": 0.6, "T6": 0.3, "T2": 0.2}
	values := []string{"k1", "k2", "k3", "k4"}
	valueScore := map[string]float64{"k3": 0.8, "k2": 0.5, "k1": 0.1, "k4": 0.4}
	threads, vals := layerOrder(topics, values, lit, threadScore, valueScore)
	// JEV's yes by score (ties in candidate order), then the lit threads
	// below 0.5 by anchor count; T6, near but unlit and below 0.5, is dropped.
	if !slices.Equal(threads, []string{"T5", "T1", "T7", "T3", "T2"}) {
		t.Fatal(threads)
	}
	if !slices.Equal(vals, []string{"k3", "k2", "k1"}) {
		t.Fatal(vals)
	}
}

func TestComposeLayerBudgets(t *testing.T) {
	m := timelineMemory{Facts: map[string][]factValue{}, FactOrder: []string{}}
	// Instructions: 14 lines of 96 fill 54 + 1344; the 15th would pass 1500
	// and ends the block, though the short 16th would still fit.
	for i := 0; i < 30; i++ {
		text := sized(fmt.Sprintf("d%02d", i), 80)
		if i == 14 {
			text = sized("d14", 200)
		}
		if i == 15 {
			text = "short note"
		}
		m.Directives = append(m.Directives, directive{Text: text, Date: "2024/03/01", Records: []string{fmt.Sprintf("dir-%d", i%3)}})
	}
	blocks := composeLayer(m, nil, nil, litSets{})
	if !slices.Equal(blockIDs(blocks), []string{"1-instructions"}) {
		t.Fatal(blockIDs(blocks))
	}
	instructions := blocks[0]
	if jsLen(instructions.Text) != 54+14*96 || !strings.HasPrefix(instructions.Text, "Standing instructions and preferences the user stated:\n- (2024/03/01) d00") ||
		strings.Contains(instructions.Text, "short note") || instructions.Kind != "1-instructions" || instructions.Title != "" || instructions.Date != "" {
		t.Fatalf("%d %q", jsLen(instructions.Text), instructions.Text)
	}
	if !slices.Equal(linkedIDs(instructions), []string{"dir-0", "dir-1", "dir-2"}) {
		t.Fatal(instructions.Records)
	}

	// Topics: the chosen first, then the largest; a line too long is
	// skipped and a later short one still enters; listed in memory order.
	m = timelineMemory{
		Threads: []thread{{ID: "T1", Title: "Alpha"}, {ID: "T2", Title: sized("Long", 1500)}, {ID: "T3", Title: "Gamma"}, {ID: "T4", Title: "Delta"}, {ID: "T5", Title: "Silent"}},
		Events: []event{
			{Thread: "T1", Date: "2024/01/01", Text: "a1"},
			{Thread: "T2", Date: "2024/01/02", Text: "b1"}, {Thread: "T2", Date: "2024/01/03", Text: "b2"}, {Thread: "T2", Date: "2024/01/04", Text: "b3"},
			{Thread: "T3", Date: "2024/01/05", Text: "c1"}, {Thread: "T3", Date: "2024/02/05", Text: "c2"},
			{Thread: "T4", Date: "2024/01/06", Text: "d1"},
		},
		Facts: map[string][]factValue{}, FactOrder: []string{},
	}
	blocks = composeLayer(m, []string{"T5", "T4"}, nil, litSets{})
	want := "Topics of the conversation (first and last date):\n- Alpha (2024/01/01)\n- Gamma (2024/01/05 to 2024/02/05)\n- Delta (2024/01/06)"
	if !slices.Equal(blockIDs(blocks), []string{"2-topics", "3-timelines"}) || blocks[0].Text != want || len(blocks[0].Records) != 0 {
		t.Fatalf("%q", blocks)
	}
	if blocks[1].Text != "Timelines of the topics this concerns:\nThread: Delta\n- 2024/01/06: d1" {
		t.Fatalf("%q", blocks[1].Text)
	}

	// Timelines: A and B are whole (9 + 2×615 = 1239 each); C (9 + 6×215 =
	// 1299) is over its room min(1500, 3500-2480) = 1020, so it keeps its
	// anchor c1 (224), then the newest that fit: c5, c4, c3 (869); c2 and c0
	// would pass 1020. Shown in date order. The 149 left is under 300: D,
	// however small, is not shown.
	m = timelineMemory{Threads: []thread{{ID: "A", Title: "A"}, {ID: "B", Title: "B"}, {ID: "C", Title: "C"}, {ID: "D", Title: "D"}}, Facts: map[string][]factValue{}, FactOrder: []string{}}
	add := func(id, date, text string, recs ...string) {
		m.Events = append(m.Events, event{Thread: id, Date: date, Text: text, Records: recs})
	}
	for i := 0; i < 2; i++ {
		add("A", "2024/01/01", sized(fmt.Sprintf("a%d", i), 600), "ra")
		add("B", "2024/02/01", sized(fmt.Sprintf("b%d", i), 600), "rb")
	}
	for i := 0; i < 6; i++ {
		add("C", fmt.Sprintf("2024/03/%02d", i+1), sized(fmt.Sprintf("c%d", i), 200), fmt.Sprintf("rc%d", i))
	}
	add("D", "2024/04/01", "tiny", "rd")
	blocks = composeLayer(m, []string{"A", "B", "C", "D"}, nil, litSets{Events: map[int]bool{5: true}})
	threadA := "Thread: A\n- 2024/01/01: " + sized("a0", 600) + "\n- 2024/01/01: " + sized("a1", 600)
	threadB := "Thread: B\n- 2024/02/01: " + sized("b0", 600) + "\n- 2024/02/01: " + sized("b1", 600)
	threadC := "Thread: C"
	for _, i := range []int{1, 3, 4, 5} {
		threadC += fmt.Sprintf("\n- 2024/03/%02d: %s", i+1, sized(fmt.Sprintf("c%d", i), 200))
	}
	if m.Events[5].Text != sized("c1", 200) || jsLen(threadA) != 1239 || jsLen(threadC) != 869 {
		t.Fatal("bad fixture", jsLen(threadA), jsLen(threadC))
	}
	if !slices.Equal(blockIDs(blocks), []string{"2-topics", "3-timelines"}) || blocks[1].Text != "Timelines of the topics this concerns:\n"+threadA+"\n\n"+threadB+"\n\n"+threadC {
		t.Fatalf("%q", blocks[1].Text)
	}
	if !slices.Equal(linkedIDs(blocks[1]), []string{"ra", "rb", "rc1", "rc3", "rc4", "rc5"}) {
		t.Fatal(linkedIDs(blocks[1]))
	}
	// Alone, C fits its room of 1500 and is shown whole.
	blocks = composeLayer(m, []string{"C"}, nil, litSets{Events: map[int]bool{5: true}})
	if !strings.Contains(blocks[1].Text, sized("c0", 200)) {
		t.Fatal("a thread that fits was cut")
	}

	// Values: each key's history in the given order; one that does not fit
	// is skipped and a later one still enters. The header is not counted.
	m = timelineMemory{
		Facts: map[string][]factValue{
			"big":   {{Value: sized("v", 1480), Date: "2024/01/01", Records: []string{"r-big"}}},
			"color": {{Value: "red", Date: "2024/01/01", Records: []string{"r1"}}, {Value: "blue", Date: "2024/02/01", Records: []string{"r2", "r1"}, Conflict: true}},
			"pet":   {{Value: "cat", Date: "2024/03/01", Records: []string{"r3"}}},
		},
		FactOrder: []string{"color", "big", "pet"},
	}
	blocks = composeLayer(m, nil, []string{"color", "big", "missing", "pet"}, litSets{})
	want = "History of values:\n- color: 2024/01/01 red; 2024/02/01 blue (contradicts the earlier value)\n- pet: 2024/03/01 cat"
	if !slices.Equal(blockIDs(blocks), []string{"4-values"}) || blocks[0].Text != want || !slices.Equal(linkedIDs(blocks[0]), []string{"r1", "r2", "r3"}) {
		t.Fatalf("%q", blocks)
	}
	// 1480 + "\n- big: 2024/01/01 " fits 1500 alone: the header is not counted.
	blocks = composeLayer(m, nil, []string{"big"}, litSets{})
	if len(blocks) != 1 || jsLen(blocks[0].Text) <= 1500 {
		t.Fatal(blocks)
	}
	if blocks := composeLayer(timelineMemory{Facts: map[string][]factValue{}}, []string{"T9"}, []string{"k"}, litSets{}); len(blocks) != 0 {
		t.Fatal(blocks)
	}
}

func TestComposeLayerEdges(t *testing.T) {
	// Instructions may fill their 1500 exactly: 54 + 14×96 + 102.
	m := timelineMemory{Facts: map[string][]factValue{}, FactOrder: []string{}}
	for i := 0; i < 15; i++ {
		m.Directives = append(m.Directives, directive{Text: sized(fmt.Sprintf("d%02d", i), 80+6*(i/14)), Date: "2024/03/01"})
	}
	if blocks := composeLayer(m, nil, nil, litSets{}); jsLen(blocks[0].Text) != 1500 {
		t.Fatal(jsLen(blocks[0].Text))
	}

	// The directory keeps the chosen, then the threads with the most events
	// (ties in memory order); a fifth line of 416 would pass 1500.
	m = timelineMemory{Facts: map[string][]factValue{}, FactOrder: []string{}}
	counts := []int{1, 3, 2, 4, 2}
	for i, n := range counts {
		id := fmt.Sprintf("T%d", i+1)
		m.Threads = append(m.Threads, thread{ID: id, Title: sized(id, 400)})
		for j := 0; j < n; j++ {
			m.Events = append(m.Events, event{Thread: id, Date: "2024/01/01", Text: "e"})
		}
	}
	blocks := composeLayer(m, []string{"T5"}, nil, litSets{})
	want := "Topics of the conversation (first and last date):"
	for _, id := range []string{"T2", "T4", "T5"} {
		want += "\n- " + sized(id, 400) + " (2024/01/01)"
	}
	if blocks[0].Text != want {
		t.Fatalf("%q", blocks[0].Text)
	}

	// A timeline is begun in a room of exactly 300 (A and B fill 1500 each,
	// C 196, with the separators 3200), not in 299.
	timelines := func(c int) string {
		m := timelineMemory{Facts: map[string][]factValue{}, FactOrder: []string{}}
		for id, size := range map[string]int{"A": 1476, "B": 1476, "C": c, "D": 10} {
			m.Threads = append(m.Threads, thread{ID: id, Title: id})
			m.Events = append(m.Events, event{Thread: id, Date: "2024/01/01", Text: sized(id, size)})
		}
		blocks := composeLayer(m, []string{"A", "B", "C", "D"}, nil, litSets{})
		return blocks[len(blocks)-1].Text
	}
	if text := timelines(172); !strings.HasSuffix(text, "\n\nThread: D\n- 2024/01/01: "+sized("D", 10)) || jsLen(text) != jsLen("Timelines of the topics this concerns:\n")+3200+2+34 {
		t.Fatalf("room 300: %d", jsLen(text))
	}
	if text := timelines(173); strings.Contains(text, "Thread: D") {
		t.Fatal("room 299 began a timeline")
	}

	// One thread over its own room of 1500 is cut though 3500 are left; an
	// event that does not fit is passed over for an older one that does.
	m = timelineMemory{Threads: []thread{{ID: "A", Title: "A"}}, Facts: map[string][]factValue{}, FactOrder: []string{}}
	for i, size := range []int{100, 800, 800} {
		m.Events = append(m.Events, event{Thread: "A", Date: fmt.Sprintf("2024/01/0%d", i+1), Text: sized(fmt.Sprintf("e%d", i), size), Records: []string{fmt.Sprintf("r%d", i)}})
	}
	blocks = composeLayer(m, []string{"A"}, nil, litSets{})
	want = "Timelines of the topics this concerns:\nThread: A\n- 2024/01/01: " + sized("e0", 100) + "\n- 2024/01/03: " + sized("e2", 800)
	if blocks[1].Text != want || !slices.Equal(linkedIDs(blocks[1]), []string{"r0", "r2"}) {
		t.Fatalf("%q", blocks[1].Text)
	}
}

func TestLayerCandidates(t *testing.T) {
	lit := litSets{Threads: []string{"T3", "T1"}, Values: []string{"k2"}}
	topics, values := layerCandidates(lit, []string{"T1", "T2", "T3", "T4"}, []string{"k1", "k2", "k3"}, []int{0, 3, 1}, []int{1, 0})
	if !slices.Equal(topics, []string{"T3", "T1", "T4", "T2"}) || !slices.Equal(values, []string{"k2", "k1"}) {
		t.Fatal(topics, values)
	}
}

// layerRun is a simple read over s at its current revision that has shown
// view and whose consolidated scope is memory, judged by judge.
func layerRun(t *testing.T, s *records.Store, memory timelineMemory, judge Decider, view ViewConfig, ids ...string) (*simpleRun, View) {
	t.Helper()
	dir := t.TempDir()
	overlay, err := OpenOverlay(filepath.Join(dir, "memory-index.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { overlay.Close() })
	state := emptyTimelineState()
	state.Memory = memory
	if _, err := overlay.saveTimelines(context.Background(), records.Scope{}, "", state); err != nil {
		t.Fatal(err)
	}
	a := layerAgent(t, s, overlay, Config{View: &view}, completeFunc(nil), judge)
	snapshot, err := s.Get(context.Background(), records.GetRequest{IDs: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	shown, err := evidence(context.Background(), s, snapshot.Revision, records.Scope{}, ids)
	if err != nil || len(shown) != len(ids) {
		t.Fatal(shown, err)
	}
	byID := map[string]Evidence{}
	for _, value := range shown {
		byID[value.ID] = value
	}
	base := View{Records: []Evidence{}, Needs: []Need{{Text: "the codename", HasEvidence: true}}}
	for _, id := range ids {
		base.Records = append(base.Records, byID[id])
	}
	r := &simpleRun{agent: a, req: ReadRequest{MaxItems: a.config.View.MaxItems}, revision: snapshot.Revision, dialogue: []Message{user("What is the codename?")},
		needs: []Need{{Text: "the codename"}}, items: map[string]*simpleItem{"orbit": {ID: "orbit", Row: []float64{0.9}}}}
	return r, base
}

func TestLayerTrimAndExpand(t *testing.T) {
	s, _ := agentStore(t)
	seedNote(t, s, "orbit", "codename ORBIT")
	seedNote(t, s, "big", sized("big", 300))
	seedNote(t, s, "tiny", "日本😀")
	scores := map[string]float64{"l1": 0.5, "l2": 0.4, "l3": 0.9, "l4": 0.7, "huge": 0.95}
	linked := []string{"l1", "l2", "orbit", "l3", "gone", "huge", "l4"}
	for _, id := range linked {
		if id == "huge" {
			seedNote(t, s, id, sized("huge", 11950))
		} else if id != "orbit" && id != "gone" {
			seedNote(t, s, id, "linked note "+id)
		}
	}
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("m%02d", i)
		seedNote(t, s, id, "linked note "+id)
		scores[id] = 0.8
	}
	judge := &scriptedJudge{answer: func(text string, _ int) float64 {
		if id, ok := strings.CutPrefix(text, "linked note "); ok {
			return scores[id]
		}
		if strings.HasPrefix(text, "huge") {
			return scores["huge"]
		}
		return 0.1
	}}
	memory := timelineMemory{Threads: []thread{}, Events: []event{}, Facts: map[string][]factValue{}, FactOrder: []string{},
		Directives: []directive{{Text: "Keep it short.", Date: "2024/01/01", Records: linked}}}
	instructions := jsLen("Standing instructions and preferences the user stated:\n- (2024/01/01) Keep it short.")

	// The records give way to the layer: big does not fit and is dropped, the
	// later tiny (4 units, 10 bytes) still fits; no room is left for linked
	// records, and with no topic or value JEV is not asked. The evidence the
	// View loses is no longer reported.
	r, base := layerRun(t, s, memory, judge, ViewConfig{MaxItems: 16, MaxCharacters: instructions + 14 + 4}, "orbit", "big", "tiny")
	view, err := r.layer(context.Background(), base)
	if err != nil || !slices.Equal(recordIDs(view.Records), []string{"orbit", "tiny"}) || !view.Truncated || judge.count() != 0 {
		t.Fatal(recordIDs(view.Records), view.Truncated, judge.count(), err)
	}
	if !slices.Equal(view.Needs, []Need{{Text: "the codename", HasEvidence: true}}) || !reflect.DeepEqual(view.Index[0].Records, []Reference{view.Records[0].Reference}) {
		t.Fatal(view.Needs, view.Index)
	}
	r, base = layerRun(t, s, memory, judge, ViewConfig{MaxItems: 16, MaxCharacters: instructions + 3}, "orbit")
	if view, err = r.layer(context.Background(), base); err != nil || len(view.Records) != 0 || view.Needs[0].HasEvidence || len(view.Index[0].Records) != 0 {
		t.Fatal(view.Records, view.Needs, err)
	}

	// Linked records JEV calls needed join by score while they fit: huge is
	// too long, l2 is not needed, gone is not stored.
	r, base = layerRun(t, s, memory, judge, ViewConfig{MaxItems: 5}, "orbit")
	if view, err = r.layer(context.Background(), base); err != nil || !slices.Equal(recordIDs(view.Records), []string{"orbit", "l3", "l4", "l1"}) || view.Truncated {
		t.Fatal(recordIDs(view.Records), err)
	}
	if view.Records[1].Score != 0.9 || view.Records[3].Score != 0.5 || view.Records[1].Hash == "" {
		t.Fatal(view.Records)
	}
	refs := []string{}
	for _, ref := range view.Index[0].Records {
		refs = append(refs, ref.ID)
	}
	if !slices.Equal(refs, []string{"l1", "orbit", "l3", "l4"}) {
		t.Fatal(refs)
	}
	// The View's items bound them.
	r, base = layerRun(t, s, memory, judge, ViewConfig{MaxItems: 3}, "orbit")
	if view, err = r.layer(context.Background(), base); err != nil || !slices.Equal(recordIDs(view.Records), []string{"orbit", "l3", "l4"}) {
		t.Fatal(recordIDs(view.Records), err)
	}
	// At most 24 linked records are judged and 8 join.
	many := []string{}
	for i := 0; i < 30; i++ {
		many = append(many, fmt.Sprintf("m%02d", i))
	}
	memory.Directives[0].Records = many
	calls := judge.count()
	r, base = layerRun(t, s, memory, judge, ViewConfig{MaxItems: 40}, "orbit")
	if view, err = r.layer(context.Background(), base); err != nil || !slices.Equal(recordIDs(view.Records), append([]string{"orbit"}, many[:8]...)) {
		t.Fatal(recordIDs(view.Records), err)
	}
	if judge.count() != calls+1 || len(judge.calls[calls].State.Candidates) != 24 {
		t.Fatal(judge.count() - calls)
	}
	// Linked records that cannot be read leave the View as it is.
	r, base = layerRun(t, s, memory, judge, ViewConfig{MaxItems: 40}, "orbit")
	r.revision = strings.Repeat("0", 64)
	if view, err = r.layer(context.Background(), base); err != nil || !slices.Equal(recordIDs(view.Records), []string{"orbit"}) {
		t.Fatal(recordIDs(view.Records), err)
	}
	// Linked records are read only while 400 characters are left.
	r, base = layerRun(t, s, memory, judge, ViewConfig{MaxItems: 40, MaxCharacters: instructions + 14 + 400}, "orbit")
	if view, err = r.layer(context.Background(), base); err != nil || len(view.Records) != 1 {
		t.Fatal(recordIDs(view.Records), err)
	}
	r, base = layerRun(t, s, memory, judge, ViewConfig{MaxItems: 40, MaxCharacters: instructions + 14 + 401}, "orbit")
	if view, err = r.layer(context.Background(), base); err != nil || len(view.Records) != 9 {
		t.Fatal(recordIDs(view.Records), err)
	}
}

// layerMemory is a consolidated scope over the codename notes: three topics,
// two values and one standing instruction.
func layerMemory() timelineMemory {
	return timelineMemory{
		Threads: []thread{{ID: "T1", Title: "Codename launch"}, {ID: "T2", Title: "Codename"}, {ID: "T3", Title: "Budget"}},
		Events: []event{
			{Thread: "T3", Date: "2024/01/10", Text: "The budget is 5 million.", Records: []string{"gone-1"}},
			{Thread: "T2", Date: "2024/02/01", Text: "The codename is ORBIT.", Records: []string{"orbit"}},
			{Thread: "T1", Date: "2024/03/01", Text: "Launch set for Thursday.", Records: []string{"launch"}},
			{Thread: "T1", Date: "2024/03/05", Text: "Launch moved to Friday.", Records: []string{"launch"}},
		},
		Facts: map[string][]factValue{
			"launch day": {{Value: "Thursday", Date: "2024/03/01", Records: []string{"launch"}}, {Value: "Friday", Date: "2024/03/05", Records: []string{"launch"}, Conflict: true}},
			"codename":   {{Value: "ORBIT", Date: "2024/02/01", Records: []string{"orbit"}}},
		},
		FactOrder:  []string{"launch day", "codename"},
		Directives: []directive{{Text: "Always answer in metric units.", Date: "2024/01/05", Records: []string{"orbit"}}},
	}
}

const launchText = "Launch moved to Friday at noon."

// layerJudge answers the screen, the table and the judgement by text: ORBIT
// and the launch record are needed; of the layer, the launch topic.
func layerJudge() *scriptedJudge {
	return &scriptedJudge{answer: func(text string, _ int) float64 {
		if text == "codename ORBIT" || text == launchText || strings.HasPrefix(text, "Codename launch: ") {
			return 0.9
		}
		return 0.1
	}}
}

func layerStore(t *testing.T) (*records.Store, *Overlay) {
	t.Helper()
	s, dir := agentStore(t)
	seedNotes(t, s, "codename", 40)
	seedNote(t, s, "orbit", "codename ORBIT")
	seedNote(t, s, "launch", launchText)
	overlay, err := OpenOverlay(filepath.Join(dir, "memory-index.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { overlay.Close() })
	state := emptyTimelineState()
	state.Memory = layerMemory()
	if _, err := overlay.saveTimelines(context.Background(), records.Scope{}, "", state); err != nil {
		t.Fatal(err)
	}
	return s, overlay
}

func layerAgent(t *testing.T, s *records.Store, overlay *Overlay, config Config, planner Completer, judge Decider) *Agent {
	t.Helper()
	config.Version, config.Strategy = 1, "simple"
	if config.Consolidation == nil {
		config.Consolidation = &ConsolidationConfig{Mode: "timelines"}
	}
	a, err := New(s, config, Models{Planner: planner, Judge: judge}, overlay)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func viewCharacters(view View) int {
	n := 0
	for _, item := range view.Index {
		n += jsLen(item.Text)
	}
	for _, record := range view.Records {
		n += jsLen(record.Text)
	}
	return n
}

// checkIndexRefs checks that every block names exactly its linked records
// the View shows, with their hashes.
func checkIndexRefs(t *testing.T, view View, linked map[string][]string) {
	t.Helper()
	shown := map[string]Reference{}
	for _, record := range view.Records {
		shown[record.ID] = record.Reference
	}
	for _, item := range view.Index {
		want := []Reference{}
		for _, id := range linked[item.ID] {
			if ref, ok := shown[id]; ok {
				want = append(want, ref)
			}
		}
		if !reflect.DeepEqual(item.Records, want) || item.Kind != item.ID || item.Title != "" || item.Date != "" {
			t.Fatalf("%s refs %v, want %v", item.ID, item.Records, want)
		}
	}
}

func TestReadSimpleWithLayer(t *testing.T) {
	s, overlay := layerStore(t)
	ctx := context.Background()
	judge := layerJudge()
	a := layerAgent(t, s, overlay, Config{View: &ViewConfig{MaxItems: 6}}, codenamePlanner("NEED: the codename"), judge)
	view, err := a.Read(ctx, ReadRequest{Question: "What is the codename?"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"Standing instructions and preferences the user stated:\n- (2024/01/05) Always answer in metric units.",
		"Topics of the conversation (first and last date):\n- Codename launch (2024/03/01 to 2024/03/05)\n- Codename (2024/02/01)\n- Budget (2024/01/10)",
		"Timelines of the topics this concerns:\nThread: Codename launch\n- 2024/03/01: Launch set for Thursday.\n- 2024/03/05: Launch moved to Friday.\n\nThread: Codename\n- 2024/02/01: The codename is ORBIT.",
		"History of values:\n- codename: 2024/02/01 ORBIT",
	}
	texts := []string{}
	for _, item := range view.Index {
		texts = append(texts, item.Text)
	}
	if !slices.Equal(blockIDs(view.Index), []string{"1-instructions", "2-topics", "3-timelines", "4-values"}) || !slices.Equal(texts, want) {
		t.Fatalf("%q", texts)
	}
	// The layer counts toward the characters, not the items.
	if len(view.Records) != 6 || view.Records[0].ID != "orbit" || slices.Contains(recordIDs(view.Records), "launch") || viewCharacters(view) > 12000 {
		t.Fatal(recordIDs(view.Records), viewCharacters(view))
	}
	linked := map[string][]string{"1-instructions": {"orbit"}, "3-timelines": {"launch", "orbit"}, "4-values": {"orbit"}}
	checkIndexRefs(t, view, linked)
	if view.Index[0].Records[0].Hash == "" || view.Index[1].Records == nil {
		t.Fatal(view.Index)
	}
	// One layer judgement: the lit topic, then the nearest by shared words,
	// then the lit value and the nearest; the needed question only. The
	// launch day is near but unlit and not needed: it is not shown.
	var layerCall *judgeCall
	for i := range judge.calls {
		if call := &judge.calls[i]; len(call.State.Candidates) > 0 && call.State.Candidates[0].Source == "topic" {
			if layerCall != nil {
				t.Fatal("two layer calls")
			}
			layerCall = call
		}
	}
	if layerCall == nil {
		t.Fatal("no layer judgement")
	}
	sources := []string{}
	for _, c := range layerCall.State.Candidates {
		sources = append(sources, c.Source)
	}
	if !slices.Equal(sources, []string{"topic", "topic", "topic", "value", "value"}) || !strings.HasPrefix(layerCall.State.Candidates[0].Text, "Codename: 1 events") ||
		layerCall.State.Candidates[3].Text != "codename: 2024/02/01 ORBIT" || !strings.HasPrefix(layerCall.State.Candidates[4].Text, "launch day: ") || len(layerCall.Questions) != 5 {
		t.Fatal(sources, layerCall.State.Candidates, layerCall.Questions)
	}
	for i := range 5 {
		q := layerCall.Questions[fmt.Sprintf("n%d", i)]
		if q.Instructions != neededQuestion(byID(fmt.Sprintf("c%d", i))) || q.Criteria["true"] != neededYes || q.Criteria["false"] != neededNo {
			t.Fatal(layerCall.Questions)
		}
	}
	// The screen, the judgement and the layer; the records are full, so the
	// linked launch record is not judged.
	if view.Stats.DecisionCalls != judge.count() || view.Stats.DecisionCalls != 3 {
		t.Fatal(view.Stats.DecisionCalls, judge.count())
	}

	// A plan with no need shows ORBIT alone; the launch record, linked from a
	// shown timeline and needed, joins after it.
	view, err = layerAgent(t, s, overlay, Config{View: &ViewConfig{MaxItems: 6}}, codenamePlanner("nothing stored is needed"), layerJudge()).Read(ctx, ReadRequest{Question: "What is the codename?"})
	if err != nil || !slices.Equal(recordIDs(view.Records), []string{"orbit", "launch"}) || view.Records[1].Score != 0.9 || view.Records[1].Hash == "" || view.Truncated {
		t.Fatal(recordIDs(view.Records), view.Truncated, err)
	}
	checkIndexRefs(t, view, linked)
	if len(view.Index[2].Records) != 2 {
		t.Fatal(view.Index[2].Records)
	}

	// Records give way from the end to the layer, skipping what does not fit.
	layerChars := 0
	for _, text := range want {
		layerChars += jsLen(text)
	}
	tight := layerChars + jsLen("codename ORBIT") + 11
	judge = layerJudge()
	view, err = layerAgent(t, s, overlay, Config{View: &ViewConfig{MaxItems: 6, MaxCharacters: tight}}, codenamePlanner("NEED: the codename"), judge).Read(ctx, ReadRequest{Question: "What is the codename?"})
	if err != nil || len(view.Index) != 4 || view.Records[0].ID != "orbit" || len(view.Records) != 2 || jsLen(view.Records[1].Text) > 11 || !view.Truncated || viewCharacters(view) > tight {
		t.Fatal(recordIDs(view.Records), view.Truncated, viewCharacters(view), err)
	}
	if !slices.Equal(view.Needs, []Need{{Text: "the codename", Covered: true, HasEvidence: true}}) {
		t.Fatal(view.Needs)
	}
	// No room for linked records: no expansion judgement.
	if judge.count() != 3 {
		t.Fatal(judge.count())
	}
}

func TestReadSimpleLayerDegrades(t *testing.T) {
	s, overlay := layerStore(t)
	ctx := context.Background()
	question := ReadRequest{Question: "What is the codename?"}
	failing := layerJudge()
	down := decisionFunc(func(ctx context.Context, state any, questions map[string]Question) (map[string]float64, Usage, error) {
		raw, _ := json.Marshal(state)
		if strings.Contains(string(raw), `"source":"topic"`) {
			return nil, Usage{}, errors.New("judge down")
		}
		return failing.Decide(ctx, state, questions)
	})
	// A failed layer judgement leaves what the records light up.
	view, err := layerAgent(t, s, overlay, Config{View: &ViewConfig{MaxItems: 6}}, codenamePlanner("NEED: the codename"), down).Read(ctx, question)
	if err != nil || len(view.Index) != 4 || view.Index[2].Text != "Timelines of the topics this concerns:\nThread: Codename\n- 2024/02/01: The codename is ORBIT." {
		t.Fatal(view.Index, err)
	}
	// A spent budget too: the layer is judged with no calls left.
	budget := layerJudge()
	spent := decisionFunc(func(ctx context.Context, state any, questions map[string]Question) (map[string]float64, Usage, error) {
		return budget.Decide(ctx, state, questions)
	})
	a := layerAgent(t, s, overlay, Config{View: &ViewConfig{MaxItems: 6}}, codenamePlanner("NEED: the codename"), spent)
	r := &simpleRun{agent: a, req: ReadRequest{MaxItems: 6}, dialogue: []Message{user("What is the codename?")}, items: map[string]*simpleItem{}}
	r.stats.DecisionCalls = simpleDecisionBudget
	base := View{Records: []Evidence{}, Needs: []Need{}}
	if view, err = r.layer(ctx, base); err != nil || len(view.Index) != 2 || view.Index[0].ID != "1-instructions" || view.Index[1].ID != "2-topics" || budget.count() != 0 {
		t.Fatal(view.Index, budget.count(), err)
	}

	// A canceled read fails.
	canceled, cancel := context.WithCancel(ctx)
	cancelling := decisionFunc(func(_ context.Context, state any, questions map[string]Question) (map[string]float64, Usage, error) {
		raw, _ := json.Marshal(state)
		if strings.Contains(string(raw), `"source":"topic"`) {
			cancel()
			return nil, Usage{}, canceled.Err()
		}
		return layerJudge().Decide(canceled, state, questions)
	})
	if _, err = layerAgent(t, s, overlay, Config{View: &ViewConfig{MaxItems: 6}}, codenamePlanner("NEED: the codename"), cancelling).Read(canceled, question); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}

	// No layer: consolidation off by policy, mode v1, an empty scope, or a
	// corrupt state.
	off := []Config{
		{View: &ViewConfig{MaxItems: 6}, Policy: &ReadPolicy{}},
		{View: &ViewConfig{MaxItems: 6}, Consolidation: &ConsolidationConfig{}},
	}
	for _, config := range off {
		if view, err = layerAgent(t, s, overlay, config, codenamePlanner("NEED: the codename"), layerJudge()).Read(ctx, question); err != nil || view.Index != nil || len(view.Records) != 6 {
			t.Fatal(config, view.Index, err)
		}
	}
	scoped := "/tmp/elsewhere"
	if view, err = layerAgent(t, s, overlay, Config{View: &ViewConfig{MaxItems: 6}}, codenamePlanner("NEED: the codename"), layerJudge()).Read(ctx, ReadRequest{Question: question.Question, Scope: records.Scope{WorkspaceID: &scoped}}); err != nil || view.Index != nil {
		t.Fatal(view.Index, err)
	}
	if _, err = overlay.db.Exec(`UPDATE timelines SET payload=payload||' '`); err != nil {
		t.Fatal(err)
	}
	if view, err = layerAgent(t, s, overlay, Config{View: &ViewConfig{MaxItems: 6}}, codenamePlanner("NEED: the codename"), layerJudge()).Read(ctx, question); err != nil || view.Index != nil || len(view.Records) != 6 {
		t.Fatal(view.Index, err)
	}
	// Without an overlay store there is no state either.
	if view, err = layerAgent(t, s, nil, Config{View: &ViewConfig{MaxItems: 6}}, codenamePlanner("NEED: the codename"), layerJudge()).Read(ctx, question); err != nil || view.Index != nil {
		t.Fatal(view.Index, err)
	}
}

func TestLayerJudgeNeededBatches(t *testing.T) {
	s, _ := agentStore(t)
	var active, overlap atomic.Int32
	judge := &scriptedJudge{answer: func(text string, _ int) float64 {
		if strings.HasSuffix(text, "7") {
			return 0.9
		}
		return 0.1
	}}
	judge.wait = func() error {
		if active.Add(1) > 1 {
			overlap.Add(1)
		}
		defer active.Add(-1)
		return nil
	}
	a := judgedAgent(t, s, completeFunc(func(context.Context, string, string) (string, Usage, error) { return "", Usage{}, nil }), judge)
	r := testRun(a, user("hello"), assistant(strings.Repeat("y", 700)), user("which?"))
	candidates := []judgeCandidate{}
	for i := 0; i < 45; i++ {
		candidates = append(candidates, judgeCandidate{ID: "ignored", Source: "topic", Text: fmt.Sprintf("topic %d", i)})
	}
	scores, err := r.judgeNeeded(context.Background(), candidates)
	if err != nil || len(scores) != 45 || scores[7] != 0.9 || scores[17] != 0.9 || scores[44] != 0.1 || overlap.Load() != 0 {
		t.Fatal(scores, err)
	}
	if judge.count() != 2 || len(judge.calls[0].Questions) != 40 || len(judge.calls[1].Questions) != 5 {
		t.Fatal(judge.count())
	}
	call := judge.calls[1]
	if call.State.Candidates[0].ID != "c0" || call.State.Candidates[0].Text != "topic 40" || call.State.Dialogue[1].Text != authorExcerpt(strings.Repeat("y", 700), 600) {
		t.Fatal(call.State)
	}
	for key := range call.Questions {
		if !strings.HasPrefix(key, "n") {
			t.Fatal(key)
		}
	}
	// The budget pays for the first batch only; the rest score 0.
	r = testRun(a, user("which?"))
	r.stats.DecisionCalls = simpleDecisionBudget - 1
	scores, err = r.judgeNeeded(context.Background(), candidates)
	if !errors.Is(err, errCallBudget) || scores[7] != 0.9 || scores[40] != 0 || scores[0] != 0.1 {
		t.Fatal(scores, err)
	}
	// A failed call stops the judging; what it did not judge scores 0.
	calls := 0
	failing := decisionFunc(func(ctx context.Context, state any, questions map[string]Question) (map[string]float64, Usage, error) {
		calls++
		if calls == 2 {
			return nil, Usage{}, errors.New("down")
		}
		return judge.Decide(ctx, state, questions)
	})
	r = testRun(judgedAgent(t, s, completeFunc(func(context.Context, string, string) (string, Usage, error) { return "", Usage{}, nil }), failing), user("which?"))
	more := append(slices.Clone(candidates), candidates...)
	scores, err = r.judgeNeeded(context.Background(), more)
	if err == nil || calls != 2 || scores[7] != 0.9 || scores[52] != 0 || scores[87] != 0 {
		t.Fatal(scores, err, calls)
	}
}

// embeddingServer embeds by the given table of vectors, keyed by the text
// after its prefix, and records every input it receives.
type embeddingServer struct {
	mu     sync.Mutex
	inputs []string
	fail   bool
}

func (e *embeddingServer) start(t *testing.T, vectors func(text string) []float64) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Input []string }
		json.NewDecoder(r.Body).Decode(&body)
		e.mu.Lock()
		e.inputs = append(e.inputs, body.Input...)
		fail := e.fail
		e.mu.Unlock()
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		out := [][]float64{}
		for _, text := range body.Input {
			out = append(out, vectors(text))
		}
		json.NewEncoder(w).Encode(map[string]any{"embeddings": out})
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func TestLayerNearest(t *testing.T) {
	s, _ := agentStore(t)
	server := &embeddingServer{}
	long := sized("gamma", 2000)
	url := server.start(t, func(text string) []float64 {
		text = strings.TrimPrefix(strings.TrimPrefix(text, "search_query: "), "search_document: ")
		switch {
		case strings.HasPrefix(text, "beta?"):
			return []float64{0, 1, 0}
		case strings.HasPrefix(text, "alpha"):
			return []float64{1, 0, 0}
		case strings.HasPrefix(text, "beta"):
			return []float64{0, 1, 0}
		case strings.HasPrefix(text, "gamma"):
			return []float64{0.6, 0.8, 0}
		}
		return []float64{0, 0, 1}
	})
	config := Config{Version: 1, Strategy: "simple", Embedding: &records.EmbeddingConfig{URL: url, Model: "test", QueryPrefix: "search_query: ", DocumentPrefix: "search_document: "}}
	a, err := New(s, config, Models{Planner: completeFunc(nil), Judge: decisionFunc(fixtureJudge)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := testRun(a, user("beta?"))
	threads := []string{"alpha thing", "beta thing", long, "delta"}
	values := []string{"gamma value", "alpha value"}
	nearThreads, nearValues, lexical := r.nearest(context.Background(), "beta?"+strings.Repeat("q", 1600), threads, values)
	// By meaning: dot products 0, 1, 0.8, 0, best first, ties in order.
	if lexical || !slices.Equal(nearThreads, []int{1, 2, 0, 3}) || !slices.Equal(nearValues, []int{0, 1}) {
		t.Fatal(nearThreads, nearValues, lexical)
	}
	server.mu.Lock()
	inputs := slices.Clone(server.inputs)
	server.mu.Unlock()
	if !slices.Contains(inputs, "search_query: "+jsSlice("beta?"+strings.Repeat("q", 1600), 0, 1500)) || !slices.Contains(inputs, "search_document: "+jsSlice(long, 0, 1500)) {
		t.Fatal(len(inputs))
	}
	// Twelve threads and sixteen values at most.
	many := []string{}
	for i := 0; i < 20; i++ {
		many = append(many, fmt.Sprintf("alpha %d", i))
	}
	nearThreads, nearValues, _ = r.nearest(context.Background(), "beta?", many, many)
	if len(nearThreads) != 12 || len(nearValues) != 16 || nearThreads[11] != 11 {
		t.Fatal(nearThreads, nearValues)
	}
	// A failing embedder leaves shared words, positive scores only.
	server.mu.Lock()
	server.fail = true
	server.mu.Unlock()
	r = testRun(a, user("beta?"))
	nearThreads, nearValues, lexical = r.nearest(context.Background(), "beta new?", []string{"alpha one", "beta two", "beta beta"}, []string{"nothing here"})
	if !lexical || !slices.Equal(nearThreads, []int{2, 1}) || len(nearValues) != 0 || !slices.Equal(r.stats.Degraded, []string{"layer-lexical"}) {
		t.Fatal(nearThreads, nearValues, lexical, r.stats.Degraded)
	}
	// Without an embedder, shared words too; that is no fallback.
	a = simpleAgent(t, s, completeFunc(nil))
	r = testRun(a, user("x"))
	nearThreads, _, lexical = r.nearest(context.Background(), "beta", []string{"alpha", "beta"}, nil)
	if !lexical || !slices.Equal(nearThreads, []int{1}) || r.stats.Degraded != nil {
		t.Fatal(nearThreads, lexical, r.stats.Degraded)
	}
}
