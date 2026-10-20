package memoryagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// insightMemory is a consolidated memory of three events on two threads,
// three values of two keys and a directive.
func insightMemory() timelineMemory {
	return timelineMemory{
		Threads: []thread{{ID: "T1", Title: "Kayak trip"}, {ID: "T2", Title: "Tax return"}},
		Events: []event{
			{Thread: "T1", Date: "2024/01/03", Text: "Booked a kayak for the lake trip.", Records: []string{"a"}},
			{Thread: "T2", Date: "2024/01/04", Text: "Filed the tax return.", Records: []string{"b", "a"}},
			{Thread: "T1", Date: "2024/02/01", Text: "Moved the kayak trip to May 10.", Records: []string{"d"}},
		},
		Facts: map[string][]factValue{
			"kayak trip · date": {{Value: "May 3", Date: "2024/01/03", Records: []string{"a"}}, {Value: "May 10", Date: "2024/02/01", Records: []string{"d"}, Conflict: true}},
			"tax · refund":      {{Value: "$300", Date: "2024/01/04", Records: []string{"b"}}},
		},
		FactOrder:  []string{"kayak trip · date", "tax · refund"},
		Directives: []directive{{Text: "Answer briefly.", Date: "2024/01/02", Records: []string{"a"}}},
	}
}

func TestInsightSystemPromptVerbatim(t *testing.T) {
	// The brief's prompt, its lines joined by spaces.
	sum := sha256.Sum256([]byte(insightSystem))
	if hex.EncodeToString(sum[:]) != "af3cf82bb9df3fc941d5627b1200aeaaf86fabe9121321f350c83fec6afa7f68" || jsLen(insightSystem) != 852 {
		t.Fatal("the L2 system prompt is not the brief's")
	}
	if !strings.HasSuffix(insightSystem, `Reply with JSON only: {"insights":[{"text":"...","date":"YYYY/MM/DD","items":["E12","V3.2"]}]}`) {
		t.Fatal(insightSystem)
	}
}

func TestInsightStream(t *testing.T) {
	m := insightMemory()
	// Every unread item in date order; a date's items are the events, then
	// the values (by key, then history), then the directives. Ids count from
	// 1 in the memory, a value's by key and place in its history.
	lines := map[string]string{
		"E1":   "E1 (2024/01/03) [Kayak trip] Booked a kayak for the lake trip.",
		"E2":   "E2 (2024/01/04) [Tax return] Filed the tax return.",
		"E3":   "E3 (2024/02/01) [Kayak trip] Moved the kayak trip to May 10.",
		"V1.1": "V1.1 (2024/01/03) kayak trip · date: May 3",
		"V1.2": "V1.2 (2024/02/01) kayak trip · date: May 10",
		"V2.1": "V2.1 (2024/01/04) tax · refund: $300",
		"D1":   "D1 (2024/01/02) Answer briefly.",
	}
	in := func(ids ...string) []string {
		out := []string{}
		for _, id := range ids {
			out = append(out, lines[id])
		}
		return out
	}
	stream := insightStream(m, insightRead{})
	if got := itemLines(stream); !slices.Equal(got, in("D1", "E1", "V1.1", "E2", "V2.1", "E3", "V1.2")) {
		t.Fatalf("%q", got)
	}
	if stream[3].id != "E2" || stream[6].id != "V1.2" || stream[0].id != "D1" || !slices.Equal(stream[3].records, []string{"b", "a"}) || stream[6].date != "2024/02/01" {
		t.Fatalf("%+v", stream)
	}
	if insightPrompt(stream[:2]) != lines["D1"]+"\n"+lines["E1"] {
		t.Fatal(insightPrompt(stream[:2]))
	}
	// What L2 has read is not read again; the others keep their ids.
	read := insightRead{Events: 2, Values: map[string]int{"kayak trip · date": 1}, Directives: 1}
	if got := itemLines(insightStream(m, read)); !slices.Equal(got, in("V2.1", "E3", "V1.2")) {
		t.Fatalf("%q", got)
	}
	// Reading moves each list's position past what was read.
	var position insightRead
	position.advance(stream[:4])
	if !reflect.DeepEqual(position, insightRead{Events: 2, Values: map[string]int{"kayak trip · date": 1}, Directives: 1}) {
		t.Fatalf("%+v", position)
	}
	position.advance(stream[4:])
	if !reflect.DeepEqual(position, insightRead{Events: 3, Values: map[string]int{"kayak trip · date": 2, "tax · refund": 1}, Directives: 1}) || len(insightStream(m, position)) != 0 {
		t.Fatalf("%+v", position)
	}
	// Items appended later (a new event, a value of a read key, a new key, a
	// directive) are the next stream, in date order.
	m.Events = append(m.Events, event{Thread: "T9", Date: "2024/03/04", Text: "A thread not stored.", Records: []string{"e"}})
	m.Facts["kayak trip · date"] = append(m.Facts["kayak trip · date"], factValue{Value: "May 17", Date: "2024/03/01", Records: []string{"e"}})
	m.Facts["garden · crop"] = []factValue{{Value: "tomatoes", Date: "2024/03/02"}}
	m.FactOrder = append(m.FactOrder, "garden · crop")
	m.Directives = append(m.Directives, directive{Text: "Use metric units.", Date: "2024/03/03"})
	want := []string{
		"V1.3 (2024/03/01) kayak trip · date: May 17",
		"V3.1 (2024/03/02) garden · crop: tomatoes",
		"D2 (2024/03/03) Use metric units.",
		"E4 (2024/03/04) [T9] A thread not stored.",
	}
	if got := itemLines(insightStream(m, position)); !slices.Equal(got, want) {
		t.Fatalf("%q", got)
	}
	// A position past a list's end reads nothing of it.
	if got := insightStream(m, insightRead{Events: 9, Values: map[string]int{"kayak trip · date": 7, "tax · refund": 1, "garden · crop": 1}, Directives: 5}); len(got) != 0 {
		t.Fatal(itemLines(got))
	}
}

func TestInsightStreamDates(t *testing.T) {
	// Dates that are no days (YYYY/MM/DD) come after every dated item, in
	// the same order among themselves: events, values, directives, memory
	// order.
	m := timelineMemory{
		Threads: []thread{{ID: "T1", Title: "Trip"}},
		Events: []event{
			{Thread: "T1", Date: "soon", Text: "e1"},
			{Thread: "T1", Date: "2024/05/02", Text: "e2"},
			{Thread: "T1", Date: "", Text: "e3"},
			{Thread: "T1", Date: "2024/04/30", Text: "e4"},
		},
		Facts:      map[string][]factValue{"b": {{Value: "v", Date: "2024/5/1"}, {Value: "w", Date: "2024/05/02"}}, "a": {{Value: "x", Date: "2024/04/30"}}},
		FactOrder:  []string{"b", "a"},
		Directives: []directive{{Text: "d1", Date: "2024/04/29"}, {Text: "d2", Date: "never"}},
	}
	ids := func(items []insightItem) []string {
		out := []string{}
		for _, item := range items {
			out = append(out, item.id)
		}
		return out
	}
	if got := ids(insightStream(m, insightRead{})); !slices.Equal(got, []string{"D1", "E4", "V2.1", "E2", "V1.2", "E1", "E3", "V1.1", "D2"}) {
		t.Fatal(got)
	}
}

func TestInsightReadOutOfListOrder(t *testing.T) {
	// The later event of the list is the earlier: date order reads it first,
	// and the position keeps it until the event before it is read.
	m := timelineMemory{
		Threads:    []thread{{ID: "T1", Title: "Trip"}},
		Events:     []event{{Thread: "T1", Date: "2024/05/01", Text: "May"}, {Thread: "T1", Date: "2024/04/01", Text: "April"}, {Thread: "T1", Date: "2024/04/02", Text: "April 2"}},
		Facts:      map[string][]factValue{"k": {{Value: "late", Date: "2024/06/01"}, {Value: "early", Date: "2024/01/01"}}},
		FactOrder:  []string{"k"},
		Directives: []directive{{Text: "later", Date: "2024/03/02"}, {Text: "sooner", Date: "2024/03/01"}},
	}
	stream := insightStream(m, insightRead{})
	if got := itemLines(stream); !slices.Equal(got, []string{"V1.2 (2024/01/01) k: early", "D2 (2024/03/01) sooner", "D1 (2024/03/02) later", "E2 (2024/04/01) [Trip] April", "E3 (2024/04/02) [Trip] April 2", "E1 (2024/05/01) [Trip] May", "V1.1 (2024/06/01) k: late"}) {
		t.Fatalf("%q", got)
	}
	var read insightRead
	read.advance(stream[:2])
	if !reflect.DeepEqual(read, insightRead{Values: map[string]int{"k": 0}, ValuesAfter: map[string][]int{"k": {1}}, DirectivesAfter: []int{1}}) {
		t.Fatalf("%+v", read)
	}
	read.advance(stream[2:5])
	if !reflect.DeepEqual(read, insightRead{EventsAfter: []int{1, 2}, Values: map[string]int{"k": 0}, ValuesAfter: map[string][]int{"k": {1}}, Directives: 2}) {
		t.Fatalf("%+v", read)
	}
	// Nothing read is read again, nor marked twice.
	read.advance(stream[3:4])
	if !reflect.DeepEqual(read.EventsAfter, []int{1, 2}) {
		t.Fatal(read.EventsAfter)
	}
	if got := itemLines(insightStream(m, read)); !slices.Equal(got, []string{"E1 (2024/05/01) [Trip] May", "V1.1 (2024/06/01) k: late"}) {
		t.Fatalf("%q", got)
	}
	raw, _ := json.Marshal(read)
	if string(raw) != `{"events":0,"eventsAfter":[1,2],"values":{"k":0},"valuesAfter":{"k":[1]},"directives":2}` {
		t.Fatal(string(raw))
	}
	// Once the earlier items are read, the positions take in the rest.
	read.advance(stream[5:])
	if !reflect.DeepEqual(read, insightRead{Events: 3, Values: map[string]int{"k": 2}, Directives: 2}) || len(insightStream(m, read)) != 0 {
		t.Fatalf("%+v", read)
	}
	// Reading an item twice changes nothing.
	read.advance(stream[:1])
	if !reflect.DeepEqual(read, insightRead{Events: 3, Values: map[string]int{"k": 2}, Directives: 2}) {
		t.Fatalf("%+v", read)
	}
	if raw, _ = json.Marshal(read); string(raw) != `{"events":3,"values":{"k":2},"directives":2}` {
		t.Fatal(string(raw))
	}
}

// sizedItems are items whose lines are n units long.
func sizedItems(lengths ...int) []insightItem {
	out := []insightItem{}
	for i, n := range lengths {
		out = append(out, insightItem{id: "D" + strings.Repeat("1", i+1), line: strings.Repeat("x", n)})
	}
	return out
}

func batchLengths(batches [][]insightItem) [][]int {
	out := [][]int{}
	for _, batch := range batches {
		lengths := []int{}
		for _, item := range batch {
			lengths = append(lengths, jsLen(item.line))
		}
		out = append(out, lengths)
	}
	return out
}

func TestInsightBatches(t *testing.T) {
	stream := sizedItems(40, 40, 30, 10, 60, 50, 5)
	// nextBatch's rule over the lines: a batch ends with the item that
	// brings it to the characters; the short rest only with flush.
	if got := batchLengths(insightBatches(stream, 100, 16, false)); !reflect.DeepEqual(got, [][]int{{40, 40, 30}, {10, 60, 50}}) {
		t.Fatal(got)
	}
	if got := batchLengths(insightBatches(stream, 100, 16, true)); !reflect.DeepEqual(got, [][]int{{40, 40, 30}, {10, 60, 50}, {5}}) {
		t.Fatal(got)
	}
	// At most max batches, in stream order.
	if got := batchLengths(insightBatches(stream, 100, 1, true)); !reflect.DeepEqual(got, [][]int{{40, 40, 30}}) {
		t.Fatal(got)
	}
	// An item longer than a batch at the head is a batch of its own.
	if got := batchLengths(insightBatches(sizedItems(250, 20), 100, 16, false)); !reflect.DeepEqual(got, [][]int{{250}}) {
		t.Fatal(got)
	}
	// Lengths are UTF-16 units: a line of 50 emoji is 100.
	emoji := []insightItem{{id: "D1", line: strings.Repeat("😀", 50)}, {id: "D2", line: "x"}}
	if got := insightBatches(emoji, 100, 16, false); len(got) != 1 || len(got[0]) != 1 {
		t.Fatal(got)
	}
	if got := insightBatches(nil, 100, 16, true); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestParseInsights(t *testing.T) {
	batch := insightStream(insightMemory(), insightRead{})
	reply := "```json\n" + `{"insights":[` +
		`{"text":" The kayak trip moved from May 3 to May 10. ","date":"2024/02/01","items":["V1.1","E3","V1.2","E3"]},` +
		`{"text":"Only one known item.","date":"2024/02/01","items":["E1","E99"]},` +
		`{"text":"Unknown ids only.","date":"2024/02/01","items":["X1","E9"]},` +
		`{"text":"  ","date":"2024/02/01","items":["E1","E2"]},` +
		`{"text":"Dated by its items.","date":"Feb 2024","items":["e2"," D1 "]},` +
		`{"text":"Not a day.","date":"2024/02/30","items":["E1","D1"]},` +
		`{"text":"No date at all.","items":["E1","E2"]},` +
		`"not an object",` +
		`{"text":"Items not a list.","items":"E1,E2"},` +
		`{"text":"Numbers are no ids.","items":[1,2]}` +
		`]}` + "\n```"
	got, ok := parseInsights(reply, batch)
	want := []insight{
		{Text: "The kayak trip moved from May 3 to May 10.", Date: "2024/02/01", Records: []string{"a", "d"}},
		{Text: "Dated by its items.", Date: "2024/01/04", Records: []string{"b", "a"}},
		{Text: "Not a day.", Date: "2024/01/03", Records: []string{"a"}},
		{Text: "No date at all.", Date: "2024/01/04", Records: []string{"a", "b"}},
	}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("%v %+v", ok, got)
	}
	// Only the batch's ids count: of the events alone, one insight cites two.
	events := slices.DeleteFunc(slices.Clone(batch), func(item insightItem) bool { return item.kind != itemEvent })
	got, ok = parseInsights(reply, events)
	if !ok || len(got) != 1 || got[0].Text != "No date at all." {
		t.Fatalf("%+v", got)
	}
	// A reply without a JSON object does not parse; one without insights
	// gives none.
	if _, ok := parseInsights("I could not find anything.", batch); ok {
		t.Fatal("parsed a reply without JSON")
	}
	for _, empty := range []string{`{"insights":[]}`, `{"events":[]}`, `{"insights":"none"}`} {
		if got, ok := parseInsights(empty, batch); !ok || got == nil || len(got) != 0 {
			t.Fatal(empty, got, ok)
		}
	}
	// Items whose dates are no days give their latest date; an insight whose
	// items have no date has none.
	odd := []insightItem{{id: "D1", date: "someday", records: []string{"x"}}, {id: "D2", date: "", records: []string{}}, {id: "D3", date: "2023/12/31"}}
	got, _ = parseInsights(`{"insights":[{"text":"a","items":["D1","D2"]},{"text":"b","items":["D1","D3"]},{"text":"c","items":["D2","D2","D3"]},{"text":"d","date":"","items":["D2","D1"]}]}`, odd)
	dates := []string{}
	for _, in := range got {
		dates = append(dates, in.Date)
	}
	if !slices.Equal(dates, []string{"someday", "2023/12/31", "2023/12/31", "someday"}) || got[0].Records == nil || len(got[2].Records) != 0 {
		t.Fatalf("%+v", got)
	}
	raw, _ := json.Marshal(got[2])
	if string(raw) != `{"text":"c","date":"2023/12/31","records":[]}` {
		t.Fatal(string(raw))
	}
}

// storedInsights are three conclusions L2 drew before.
func storedInsights() []insight {
	return []insight{
		{Text: "The kayak trip keeps moving later in May.", Date: "2024/02/01", Records: []string{"a", "d"}},
		{Text: "Taxes are filed early every year.", Date: "2024/01/04", Records: []string{"b"}},
		{Text: "The garden grows tomatoes each spring.", Date: "2024/03/02", Records: []string{"g"}},
	}
}

// sameConclusion says yes when the new insight and the candidate are about
// the same subject, their first word other than "The".
func sameConclusion() *linkJudge {
	subject := func(text string) string {
		words := strings.Fields(text)
		if words[0] == "The" {
			return words[1]
		}
		return words[0]
	}
	return &linkJudge{answer: func(kind, newText, candidate string) float64 {
		if kind == "insight" && subject(newText) == subject(candidate) {
			return 0.8
		}
		return 0.2
	}}
}

func TestLinkInsights(t *testing.T) {
	judge := sameConclusion()
	a := linkAgent(t, judge, 8, nil)
	state, result := linkState0(insightMemory()), &ConsolidateResult{}
	state.Memory.Insights = storedInsights()
	fresh := []insight{
		{Text: "The kayak trip moved twice, from May 3 to May 17.", Date: "2024/03/01", Records: []string{"e", "a", "f"}},
		{Text: "Rent rose every year since 2021.", Date: "2024/01/09", Records: []string{"r"}},
		{Text: "Taxes come with a refund each time.", Date: "2023/12/01", Records: []string{"b", "c"}},
	}
	linked, counts, err := a.newLinker().linkInsights(context.Background(), state, fresh, result)
	if err != nil || counts != (insightCounts{New: 1, Merged: 2}) {
		t.Fatal(counts, err)
	}
	// A merged insight keeps its text, takes the later date and appends the
	// new records it lacks; a new one is added after the stored ones.
	want := []insight{
		{Text: "The kayak trip keeps moving later in May.", Date: "2024/03/01", Records: []string{"a", "d", "e", "f"}},
		{Text: "Taxes are filed early every year.", Date: "2024/01/04", Records: []string{"b", "c"}},
		{Text: "The garden grows tomatoes each spring.", Date: "2024/03/02", Records: []string{"g"}},
		{Text: "Rent rose every year since 2021.", Date: "2024/01/09", Records: []string{"r"}},
	}
	if !reflect.DeepEqual(linked, want) {
		t.Fatalf("%+v", linked)
	}
	if !reflect.DeepEqual(state.Memory.Insights, storedInsights()) {
		t.Fatal("linking changed the stored insights in place")
	}
	// One call: every new insight against the stored ones that share words
	// with it, one question a pair, worded as the brief words it.
	if judge.count() != 1 || result.InsightLinkCalls != 1 || result.LinkCalls != 0 || result.Usage.InputTokens != 100 || state.Usage.InputTokens != 100 || result.LinkUsage == nil || result.LinkUsage.InputTokens != 100 {
		t.Fatalf("%d %+v %+v", judge.count(), result, state.Usage)
	}
	call := judge.calls[0]
	if !reflect.DeepEqual(call.State.New[0], linkItem{ID: "n1", Date: "2024/03/01", Text: fresh[0].Text}) || !slices.ContainsFunc(call.State.Candidates, func(c linkItem) bool {
		return reflect.DeepEqual(c, linkItem{ID: "I2", Date: "2024/01/04", Text: "Taxes are filed early every year."})
	}) {
		t.Fatalf("%+v", call.State)
	}
	for key, q := range call.Questions {
		refs := linkRef.FindAllStringSubmatch(q.Instructions, -1)
		want := Question{Type: "noul", Instructions: "Does the new insight whose `id` is \"" + refs[0][1] + "\" state the same conclusion as the candidate whose `id` is \"" + refs[1][1] + "\"?",
			Criteria: map[string]string{"true": "Both draw the same conclusion about the same thing, even if worded differently.", "false": "They concern different things, or draw different conclusions."}}
		if !strings.HasPrefix(key, "g") || !reflect.DeepEqual(q, want) {
			t.Fatalf("%s: %+v", key, q)
		}
	}
	// Each new insight is compared with at most LinkCandidates stored ones,
	// the nearest: with one, the kayak trip is asked about its own
	// conclusion only.
	judge = sameConclusion()
	if _, _, err = linkAgent(t, judge, 1, nil).newLinker().linkInsights(context.Background(), state, fresh[:1], &ConsolidateResult{}); err != nil || len(judge.calls[0].Questions) != 1 || judge.calls[0].State.Candidates[0].ID != "I1" {
		t.Fatal(err, judge.calls)
	}
	// A merge never writes into the room behind a stored insight's records.
	roomy := append(make([]string, 0, 8), "a", "d")
	state.Memory.Insights[0].Records = roomy
	if _, _, err = a.newLinker().linkInsights(context.Background(), state, fresh[:1], &ConsolidateResult{}); err != nil || roomy[:3][2] != "" {
		t.Fatal("linking wrote into a stored insight's records", roomy[:3], err)
	}
}

func TestLinkInsightsWithoutJudgement(t *testing.T) {
	fresh := []insight{
		{Text: "The kayak trip moved twice.", Date: "2024/03/01", Records: []string{"e"}},
		{Text: "Rent rose every year.", Date: "2024/01/09", Records: []string{"r"}},
	}
	// Nothing stored: every insight is new, and JEV is not asked.
	judge := sameConclusion()
	a := linkAgent(t, judge, 8, nil)
	state := linkState0(insightMemory())
	linked, counts, err := a.newLinker().linkInsights(context.Background(), state, fresh, &ConsolidateResult{})
	if err != nil || judge.count() != 0 || counts != (insightCounts{New: 2}) || !reflect.DeepEqual(linked, fresh) {
		t.Fatal(counts, err, linked)
	}
	// A failed or unaffordable judgement adds every new insight (fallback).
	judge.err = func(context.Context) error { return fmt.Errorf("jev: %w", ErrBudgetCap) }
	state.Memory.Insights = storedInsights()
	result := &ConsolidateResult{}
	linked, counts, err = a.newLinker().linkInsights(context.Background(), state, fresh, result)
	if err != nil || counts != (insightCounts{New: 2, Fallback: true}) || !reflect.DeepEqual(linked, append(storedInsights(), fresh...)) || result.InsightLinkCalls != 1 || state.Usage.InputTokens != 3 {
		t.Fatal(counts, err, linked, result.InsightLinkCalls)
	}
	// So does a judgement that fails after a call that said yes: 40 new
	// insights, each with three candidates, take two calls.
	many, stored := []insight{}, []insight{}
	for i := range 40 {
		many = append(many, insight{Text: fmt.Sprintf("The topic%d grew again.", i), Date: "2024/02/01"})
		stored = append(stored, insight{Text: fmt.Sprintf("The topic%d grew. topic%d grew. topic%d.", i, (i+1)%40, (i+2)%40), Date: "2024/01/01"})
	}
	state.Memory.Insights = stored
	calls := 0
	judge.err = func(context.Context) error {
		if calls++; calls == 2 {
			return errors.New("jev: malformed decision")
		}
		return nil
	}
	linked, counts, err = a.newLinker().linkInsights(context.Background(), state, many, &ConsolidateResult{})
	if err != nil || calls != 2 || counts != (insightCounts{New: 40, Fallback: true}) || len(linked) != 80 {
		t.Fatal(calls, counts, err, len(linked))
	}
	// A canceled job returns its cancellation.
	ctx, cancel := context.WithCancel(context.Background())
	judge.err = func(ctx context.Context) error { cancel(); return ctx.Err() }
	if _, _, err := a.newLinker().linkInsights(ctx, state, fresh, &ConsolidateResult{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// No new insight: the stored ones as they are.
	state.Memory.Insights = storedInsights()
	if linked, counts, err = a.newLinker().linkInsights(context.Background(), state, nil, &ConsolidateResult{}); err != nil || counts != (insightCounts{}) || !reflect.DeepEqual(linked, storedInsights()) {
		t.Fatal(linked, counts, err)
	}
}
