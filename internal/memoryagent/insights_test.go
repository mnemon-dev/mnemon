package memoryagent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	// Events, then each key's values in key order, then the directives; ids
	// count from 1 in the memory, a value's by key and place in its history.
	want := []string{
		"E1 (2024/01/03) [Kayak trip] Booked a kayak for the lake trip.",
		"E2 (2024/01/04) [Tax return] Filed the tax return.",
		"E3 (2024/02/01) [Kayak trip] Moved the kayak trip to May 10.",
		"V1.1 (2024/01/03) kayak trip · date: May 3",
		"V1.2 (2024/02/01) kayak trip · date: May 10",
		"V2.1 (2024/01/04) tax · refund: $300",
		"D1 (2024/01/02) Answer briefly.",
	}
	stream := insightStream(m, insightRead{})
	if got := itemLines(stream); !slices.Equal(got, want) {
		t.Fatalf("%q", got)
	}
	if stream[1].id != "E2" || stream[4].id != "V1.2" || stream[6].id != "D1" || !slices.Equal(stream[1].records, []string{"b", "a"}) || stream[4].date != "2024/02/01" {
		t.Fatalf("%+v", stream)
	}
	if insightPrompt(stream[:2]) != want[0]+"\n"+want[1] {
		t.Fatal(insightPrompt(stream[:2]))
	}
	// What L2 has read is not read again; the others keep their ids.
	read := insightRead{Events: 2, Values: map[string]int{"kayak trip · date": 1}, Directives: 1}
	if got := itemLines(insightStream(m, read)); !slices.Equal(got, []string{want[2], want[4], want[5]}) {
		t.Fatalf("%q", got)
	}
	// Reading moves the position past what was read, each list on its own.
	var position insightRead
	position.advance(stream[:4])
	if !reflect.DeepEqual(position, insightRead{Events: 3, Values: map[string]int{"kayak trip · date": 1}}) {
		t.Fatalf("%+v", position)
	}
	position.advance(stream[4:])
	if !reflect.DeepEqual(position, insightRead{Events: 3, Values: map[string]int{"kayak trip · date": 2, "tax · refund": 1}, Directives: 1}) || len(insightStream(m, position)) != 0 {
		t.Fatalf("%+v", position)
	}
	// Items appended later (a new event, a value of a read key, a new key, a
	// directive) are the next stream, in memory order.
	m.Events = append(m.Events, event{Thread: "T9", Date: "2024/03/01", Text: "A thread not stored.", Records: []string{"e"}})
	m.Facts["kayak trip · date"] = append(m.Facts["kayak trip · date"], factValue{Value: "May 17", Date: "2024/03/01", Records: []string{"e"}})
	m.Facts["garden · crop"] = []factValue{{Value: "tomatoes", Date: "2024/03/02"}}
	m.FactOrder = append(m.FactOrder, "garden · crop")
	m.Directives = append(m.Directives, directive{Text: "Use metric units.", Date: "2024/03/03"})
	want = []string{
		"E4 (2024/03/01) [T9] A thread not stored.",
		"V1.3 (2024/03/01) kayak trip · date: May 17",
		"V3.1 (2024/03/02) garden · crop: tomatoes",
		"D2 (2024/03/03) Use metric units.",
	}
	if got := itemLines(insightStream(m, position)); !slices.Equal(got, want) {
		t.Fatalf("%q", got)
	}
	// A position past a list's end reads nothing of it.
	if got := insightStream(m, insightRead{Events: 9, Values: map[string]int{"kayak trip · date": 7, "tax · refund": 1, "garden · crop": 1}, Directives: 5}); len(got) != 0 {
		t.Fatal(itemLines(got))
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
	// Only the batch's ids count.
	got, ok = parseInsights(reply, batch[:3])
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
