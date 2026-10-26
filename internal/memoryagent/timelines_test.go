package memoryagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mnemon-dev/mnemon/internal/records"
)

// The cases port the reference system's tests/consolidation.spec.ts 1-179
// (readable, nextBatch, applyReply and the background job).

func TestReadableCases(t *testing.T) {
	heading := "Conversation #2 on 2024/03/15 (Fri) 00:00:\n"
	long := "assistant: " + strings.Repeat("x", 300)
	for _, tc := range []struct {
		name, title, content string
		assistant            int
		header, body         string
		ok                   bool
	}{
		{"user chunk is read whole", "2024/03/15 (Fri) 00:00 · conversation 1", heading + "user: I moved the demo to Friday.\n{\"branch\":\"bench\"}", 10,
			"2024/03/15 (session 2)", "user: I moved the demo to Friday.", true},
		{"assistant start is cut", "t", heading + "assistant: Sure, here is a long plan for Friday.\n{\"branch\":\"bench\"}", 10,
			"2024/03/15 (session 2)", "assistant: …", true},
		{"assistant start cut at 240", "t", heading + long, 240,
			"2024/03/15 (session 2)", long[:240] + " …", true},
		{"short assistant start is whole", "t", heading + "assistant: ok", 240,
			"2024/03/15 (session 2)", "assistant: ok", true},
		{"assistant continuation is not read", "t", heading + "the rest of an assistant message", 10, "", "", false},
		{"end of an answer then the user is whole", "t", heading + "long answer.\nuser: next question", 10,
			"2024/03/15 (session 2)", "long answer.\nuser: next question", true},
		{"our conversation heading", "Original record", "Conversation s-1 on 2024/03/16 (Sat) 09:30:\nuser: hello", 240,
			"2024/03/16 (session s-1)", "user: hello", true},
		{"other record whole", "Shopping", "Buy oat milk on Friday.\n{\"branch\":\"bench\"}", 10, "Shopping", "Buy oat milk on Friday.", true},
		{"other record cut", strings.Repeat("T", 50), strings.Repeat("c", 2500), 240, strings.Repeat("T", 40), strings.Repeat("c", 2000), true},
		{"cuts count UTF-16 units", strings.Repeat("😀", 30), strings.Repeat("原", 2100), 240, strings.Repeat("😀", 20), strings.Repeat("原", 2000), true},
		{"only a data line", "Note", `  {"a":1}  `, 240, "", "", false},
		{"blank record", "Note", " \n\t", 240, "", "", false},
		{"a data line is only the last line", "Note", "{\"a\":1}\nkept", 240, "Note", "{\"a\":1}\nkept", true},
		{"conversation without a date is another record", "Original record", "Conversation s-1:\nuser: hi", 240, "Original record", "Conversation s-1:\nuser: hi", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			header, body, ok := readable(tc.title, tc.content, tc.assistant)
			if header != tc.header || body != tc.body || ok != tc.ok {
				t.Fatalf("readable = (%q, %q, %v), want (%q, %q, %v)", header, body, ok, tc.header, tc.body, tc.ok)
			}
		})
	}
}

func pendingOf(lengths ...int) []pendingRecord {
	out := []pendingRecord{}
	for i, n := range lengths {
		out = append(out, pendingRecord{ID: fmt.Sprintf("p%d", i), Header: "h", Text: strings.Repeat("a", n)})
	}
	return out
}

func ids(values []pendingRecord) []string {
	out := []string{}
	for _, value := range values {
		out = append(out, value.ID)
	}
	return out
}

func TestNextBatchThreshold(t *testing.T) {
	pending := pendingOf(4000, 4000, 3000, 1000)
	if got := ids(nextBatch(pending, 10000, false)); !reflect.DeepEqual(got, []string{"p0", "p1", "p2"}) {
		t.Fatal("first batch", got)
	}
	if got := nextBatch(pending[3:], 10000, false); got != nil {
		t.Fatal("a short rest was folded without flush", ids(got))
	}
	if got := ids(nextBatch(pending[3:], 10000, true)); !reflect.DeepEqual(got, []string{"p3"}) {
		t.Fatal("flush", got)
	}
	if got := nextBatch(nil, 10000, true); got != nil {
		t.Fatal("empty flush", got)
	}
	// The author's case: 60/60/30 in batches of 100.
	small := pendingOf(60, 60, 30)
	if got := ids(nextBatch(small, 100, false)); !reflect.DeepEqual(got, []string{"p0", "p1"}) {
		t.Fatal("author case", got)
	}
	// Sizes are JavaScript lengths: an emoji counts two.
	emoji := []pendingRecord{{ID: "e0", Text: strings.Repeat("😀", 50)}, {ID: "e1", Text: "x"}}
	if got := ids(nextBatch(emoji, 100, false)); !reflect.DeepEqual(got, []string{"e0"}) {
		t.Fatal("UTF-16 sizes", got)
	}
}

func TestTimelineSystemPromptVerbatim(t *testing.T) {
	sum := sha256.Sum256([]byte(timelineSystem))
	if hex.EncodeToString(sum[:]) != "f2f48a4df384be76cc8982bc70122e1299cbecc7da81fb635fcb07d93a66a333" || !strings.Contains(timelineSystem, `"thing · attribute"`) {
		t.Fatal("consolidation system prompt differs from the author's (consolidation.ts:75-82)")
	}
}

func TestTimelinePromptGolden(t *testing.T) {
	empty := timelinePrompt(timelineMemory{}, []pendingRecord{{ID: "x", Header: "header", Text: "text"}})
	want := "Threads so far:\n(none)\n\nState keys so far, with the latest value:\n(none)\n\nDirectives so far:\n(none)\n\nNew records:\n\n[r0] header\ntext"
	if empty != want {
		t.Fatalf("empty prompt:\n%s\nwant:\n%s", empty, want)
	}
	memory := timelineMemory{
		Threads: []thread{{ID: "T1", Title: "Demo"}, {ID: "T2", Title: "Trip"}, {ID: "T3", Title: "Budget"}},
		Events: []event{
			{Thread: "T1", Date: "2024/03/14", Text: "Demo set for Thursday.", Records: []string{"a"}},
			{Thread: "T2", Date: "2024/03/20", Text: "Trip booked.", Records: []string{"b"}},
			{Thread: "T1", Date: "2024/03/15", Text: "Demo moved to Friday.", Records: []string{"c"}},
			{Thread: "T2", Date: "2024/03/10", Text: "Trip planned earlier.", Records: []string{"d"}},
		},
		Facts: map[string][]factValue{
			"demo · day":      {{Value: "Thursday", Date: "2024/03/14"}, {Value: "Friday", Date: "2024/03/15", Conflict: true}},
			"budget · amount": {{Value: "$500", Date: "2024/03/16"}},
		},
		FactOrder:  []string{"demo · day", "budget · amount"},
		Directives: []directive{{Text: "Answer briefly.", Date: "2024/03/14"}, {Text: "Use 24-hour times.", Date: "2024/03/15"}},
	}
	batch := []pendingRecord{{ID: "x", Header: "2024/03/21 (session 3)", Text: "user: hi"}, {ID: "y", Header: "Note", Text: "Buy milk."}}
	want = "Threads so far:\nT1 Demo (2 events, last 2024/03/15)\nT2 Trip (2 events, last 2024/03/10)\nT3 Budget (0 events, last -)" +
		"\n\nState keys so far, with the latest value:\n- demo · day: Friday\n- budget · amount: $500" +
		"\n\nDirectives so far:\n- Answer briefly.\n- Use 24-hour times." +
		"\n\nNew records:\n\n[r0] 2024/03/21 (session 3)\nuser: hi\n\n[r1] Note\nBuy milk."
	if got := timelinePrompt(memory, batch); got != want {
		t.Fatalf("populated prompt:\n%s\nwant:\n%s", got, want)
	}
}

func TestApplyReplyMerge(t *testing.T) {
	memory := timelineMemory{}
	batch := []pendingRecord{{ID: "rec-a", Header: "h", Text: "x"}, {ID: "rec-b", Header: "h", Text: "y"}}
	first := "```json\n" + `{"events":[{"thread":"new: Demo","date":"2024/03/15","text":"Demo set for Thursday.","records":["r0"]}],` +
		`"facts":[{"key":"Demo · day","value":"Thursday","date":"2024/03/15","records":["r0","r9"],"conflict":false}],` +
		`"directives":[{"text":"Always give times in 24-hour format.","date":"2024/03/15","records":["r1"]}]}` + "\n```"
	if err := applyReply(&memory, batch, first); err != nil {
		t.Fatal(err)
	}
	if memorySize(memory) != 3 {
		t.Fatal("first reply added", memorySize(memory))
	}
	second := `{"events":[{"thread":"T1","date":"2024/03/16","text":"Demo moved to Friday.","records":["r1"]},{"thread":"new: demo","date":"2024/03/16","text":"Slides ready.","records":[]}],` +
		`"facts":[{"key":"demo · day","value":"Friday","date":"2024/03/16","records":["r1"],"conflict":true}],` +
		`"directives":[{"text":"Always give times in the 24-hour format","date":"2024/03/16","records":["r1"]}]}`
	if err := applyReply(&memory, batch, second); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(memory.Threads, []thread{{ID: "T1", Title: "Demo"}}) {
		t.Fatal("threads", memory.Threads)
	}
	wantEvents := []event{
		{Thread: "T1", Date: "2024/03/15", Text: "Demo set for Thursday.", Records: []string{"rec-a"}},
		{Thread: "T1", Date: "2024/03/16", Text: "Demo moved to Friday.", Records: []string{"rec-b"}},
		{Thread: "T1", Date: "2024/03/16", Text: "Slides ready.", Records: []string{}},
	}
	if !reflect.DeepEqual(memory.Events, wantEvents) {
		t.Fatalf("events %+v", memory.Events)
	}
	wantFacts := []factValue{{Value: "Thursday", Date: "2024/03/15", Records: []string{"rec-a"}}, {Value: "Friday", Date: "2024/03/16", Records: []string{"rec-b"}, Conflict: true}}
	if !reflect.DeepEqual(memory.Facts["demo · day"], wantFacts) || !reflect.DeepEqual(memory.FactOrder, []string{"demo · day"}) {
		t.Fatalf("facts %+v %v", memory.Facts, memory.FactOrder)
	}
	if len(memory.Directives) != 1 {
		t.Fatal("a restated directive was added", memory.Directives)
	}

	// New threads take the next id; an empty label is Other; refs are
	// trimmed, may be bare numbers, stay duplicated, and only those in the
	// batch are kept; values must be strings; keys are trimmed and lowercased.
	third := `{"events":[{"thread":"new: Gym","date":"2024/03/17","text":" Gym on Mondays. ","records":[" r1 ","0",1,"r0","r2","x1",-1,"1.5"]},` +
		`{"thread":"","date":"2024/03/17","text":"Misc.","records":"r0"},{"thread":"T9","date":5,"text":"Unknown id becomes a title.","records":[]},` +
		`{"thread":"T2","text":7},null,"stray"],` +
		`"facts":[{"key":"  Zeta · Count ","value":3,"records":["r0"]},{"key":"Zeta · count","value":"4","conflict":"true"},{"key":"","value":"x"},{"key":"alpha · day","value":"Monday","conflict":true}],` +
		`"directives":[{"text":"Reply in French."},{"text":"Please reply in French"},{"text":"Never use emoji."},{"text":5}]}`
	before := memorySize(memory)
	if err := applyReply(&memory, batch, third); err != nil {
		t.Fatal(err)
	}
	if added := memorySize(memory) - before; added != 7 {
		t.Fatal("third reply added", added)
	}
	wantThreads := []thread{{ID: "T1", Title: "Demo"}, {ID: "T2", Title: "Gym"}, {ID: "T3", Title: "Other"}, {ID: "T4", Title: "T9"}}
	if !reflect.DeepEqual(memory.Threads, wantThreads) {
		t.Fatal("threads", memory.Threads)
	}
	gym := memory.Events[3]
	if gym.Thread != "T2" || gym.Text != "Gym on Mondays." || !reflect.DeepEqual(gym.Records, []string{"rec-b", "rec-a", "rec-b", "rec-a"}) {
		t.Fatalf("gym event %+v", gym)
	}
	if other := memory.Events[4]; other.Thread != "T3" || len(other.Records) != 0 {
		t.Fatalf("other event %+v", other)
	}
	if unknown := memory.Events[5]; unknown.Thread != "T4" || unknown.Date != "" {
		t.Fatalf("unknown-id event %+v", unknown)
	}
	if !reflect.DeepEqual(memory.FactOrder, []string{"demo · day", "zeta · count", "alpha · day"}) {
		t.Fatal("fact order", memory.FactOrder)
	}
	if got := memory.Facts["zeta · count"]; len(got) != 1 || got[0].Value != "4" || got[0].Conflict {
		t.Fatalf("zeta facts %+v", got)
	}
	if got := memory.Facts["alpha · day"]; len(got) != 1 || !got[0].Conflict {
		t.Fatalf("alpha facts %+v", got)
	}
	var texts []string
	for _, d := range memory.Directives {
		texts = append(texts, d.Text)
	}
	if !reflect.DeepEqual(texts, []string{"Always give times in 24-hour format.", "Reply in French.", "Never use emoji."}) {
		t.Fatal("directives", texts)
	}

	// A reply without a JSON object changes nothing.
	snapshot, _ := json.Marshal(memory)
	for _, reply := range []string{"no json here", "} {", `{"events":[`, "[1, 2]"} {
		if err := applyReply(&memory, batch, reply); err == nil {
			t.Fatalf("reply %q parsed", reply)
		}
	}
	if after, _ := json.Marshal(memory); string(after) != string(snapshot) {
		t.Fatal("a failed reply changed the memory")
	}
	// Lists that are not arrays add nothing.
	if err := applyReply(&memory, batch, `{"events":{"thread":"T1"},"facts":"x","directives":3}`); err != nil {
		t.Fatal(err)
	}
	if after, _ := json.Marshal(memory); string(after) != string(snapshot) {
		t.Fatal("non-array lists changed the memory")
	}
}

// eventsReply answers a fold with one event per record it was shown, its text
// the record's first line.
func eventsReply(prompt string) string {
	type item struct {
		Thread  string   `json:"thread"`
		Date    string   `json:"date"`
		Text    string   `json:"text"`
		Records []string `json:"records"`
	}
	events := []item{}
	for _, match := range regexp.MustCompile(`\[r(\d+)\] [^\n]*\n([^\n]*)`).FindAllStringSubmatch(prompt, -1) {
		events = append(events, item{Thread: "new: Notes", Date: "2024/03/15", Text: match[2], Records: []string{"r" + match[1]}})
	}
	raw, _ := json.Marshal(map[string]any{"events": events, "facts": []any{}, "directives": []any{}})
	return string(raw)
}

// timelinesAgent is an agent in timelines mode over s whose consolidator is model.
func timelinesAgent(t *testing.T, s *records.Store, dir string, settings ConsolidationConfig, models Models) (*Agent, *Overlay) {
	t.Helper()
	overlay, err := OpenOverlay(filepath.Join(dir, "memory-index.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { overlay.Close() })
	settings.Mode = "timelines"
	a, err := New(s, Config{Version: 1, Policy: &ReadPolicy{Consolidation: true}, Consolidation: &settings}, models, overlay)
	if err != nil {
		t.Fatal(err)
	}
	return a, overlay
}

func loadState(t *testing.T, overlay *Overlay) timelineState {
	t.Helper()
	state, _, err := overlay.loadTimelines(context.Background(), records.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func eventTexts(state timelineState) []string {
	out := []string{}
	for _, e := range state.Memory.Events {
		out = append(out, e.Text+"@"+strings.Join(e.Records, ","))
	}
	return out
}

func TestConsolidateTimelinesRetrySplit(t *testing.T) {
	notes := []string{"alpha note number one here...", "bravo note number two here....", "charlie note number three.....", "delta note number four here..."}
	seed := func(t *testing.T) (*records.Store, string) {
		s, dir := agentStore(t)
		for i, text := range notes {
			seedNote(t, s, fmt.Sprintf("n%d", i), text)
		}
		return s, dir
	}
	t.Run("halves fold after two failed attempts", func(t *testing.T) {
		s, dir := seed(t)
		model := &limitPlanner{reply: func(_, prompt string) (string, error) {
			if strings.Contains(prompt, "[r3]") {
				return `{"events":[{"thread":"new: cut off`, nil
			}
			return eventsReply(prompt), nil
		}}
		a, overlay := timelinesAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100}, Models{Consolidator: model})
		result, err := a.Consolidate(context.Background(), ConsolidateRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if result.Visited != 4 || result.Added != 4 || result.Remaining != 0 || result.Truncated || result.EmbeddingReady || result.Usage.InputTokens != 40 {
			t.Fatalf("result %+v", result)
		}
		state := loadState(t, overlay)
		if state.Batches != 2 || state.Failed != 0 || state.Calls != 4 || len(state.Pending) != 0 || state.Usage.OutputTokens != 8 {
			t.Fatalf("state batches %d failed %d calls %d pending %d", state.Batches, state.Failed, state.Calls, len(state.Pending))
		}
		want := []string{notes[0] + "@n0", notes[1] + "@n1", notes[2] + "@n2", notes[3] + "@n3"}
		if got := eventTexts(state); !reflect.DeepEqual(got, want) {
			t.Fatal("events", got)
		}
		for _, call := range model.calls {
			if call.system != timelineSystem || call.maxTokens != 8192 {
				t.Fatal("consolidator call", call.maxTokens)
			}
		}
	})
	t.Run("a half that fails twice is dropped", func(t *testing.T) {
		s, dir := seed(t)
		model := &limitPlanner{reply: func(_, prompt string) (string, error) {
			if strings.Contains(prompt, "delta") {
				return "no json", nil
			}
			return eventsReply(prompt), nil
		}}
		a, overlay := timelinesAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100}, Models{Consolidator: model})
		result, err := a.Consolidate(context.Background(), ConsolidateRequest{})
		if err != nil || result.Added != 2 || result.Remaining != 0 || result.Dropped != 2 || len(result.Drops) != 1 || !strings.Contains(result.Drops[0].Reason, "did not parse") {
			t.Fatalf("result %+v %v", result, err)
		}
		state := loadState(t, overlay)
		if state.Batches != 1 || state.Failed != 1 || state.Calls != 5 || len(state.Pending) != 0 {
			t.Fatalf("state batches %d failed %d calls %d pending %d", state.Batches, state.Failed, state.Calls, len(state.Pending))
		}
		if got := eventTexts(state); !reflect.DeepEqual(got, []string{notes[0] + "@n0", notes[1] + "@n1"}) {
			t.Fatal("events", got)
		}
	})
	t.Run("a model error keeps the batch for the next job", func(t *testing.T) {
		s, dir := seed(t)
		down := errors.New("model unavailable")
		// The whole batch does not parse, its first half folds, and the
		// second half's model call fails: the batch's events are withdrawn
		// and its records stay pending.
		model := &limitPlanner{reply: func(_, prompt string) (string, error) {
			if strings.Contains(prompt, "[r3]") {
				return "cut", nil
			}
			if strings.Contains(prompt, "charlie") {
				return "", down
			}
			return eventsReply(prompt), nil
		}}
		a, overlay := timelinesAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100}, Models{Consolidator: model})
		result, err := a.Consolidate(context.Background(), ConsolidateRequest{})
		if !errors.Is(err, down) || result.Visited != 4 || result.Added != 0 || result.Remaining != 4 || result.Dropped != 0 || result.Folded != 0 {
			t.Fatalf("result %+v %v", result, err)
		}
		state := loadState(t, overlay)
		// Like the author, a call counts once it returns.
		if len(state.Memory.Events) != 0 || len(state.Memory.Threads) != 0 || state.Batches != 0 || state.Failed != 0 || state.Calls != 3 || len(state.Pending) != 4 || state.Watermark == 0 {
			t.Fatalf("state after error %+v", state)
		}
		model.reply = func(_, prompt string) (string, error) { return eventsReply(prompt), nil }
		result, err = a.Consolidate(context.Background(), ConsolidateRequest{})
		if err != nil || result.Visited != 0 || result.Added != 4 || result.Remaining != 0 {
			t.Fatalf("resumed %+v %v", result, err)
		}
	})
}

func TestConsolidateWatermarkResume(t *testing.T) {
	s, dir := agentStore(t)
	for i := 0; i < 5; i++ {
		seedNote(t, s, fmt.Sprintf("n%d", i), fmt.Sprintf("note %d has thirty characters.", i))
	}
	model := &limitPlanner{reply: func(_, prompt string) (string, error) { return eventsReply(prompt), nil }}
	a, overlay := timelinesAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100}, Models{Planner: model})
	shown := func() []string {
		out := []string{}
		for _, call := range model.calls {
			for _, match := range regexp.MustCompile(`note (\d+) has`).FindAllStringSubmatch(call.prompt, -1) {
				out = append(out, match[1])
			}
		}
		return out
	}
	result, err := a.Consolidate(context.Background(), ConsolidateRequest{})
	if err != nil || result.Visited != 5 || result.Added != 4 || result.Remaining != 1 || result.Revision == "" {
		t.Fatalf("first %+v %v", result, err)
	}
	state := loadState(t, overlay)
	if state.Format != "mnemon-consolidation/v1" || !reflect.DeepEqual(ids(state.Pending), []string{"n4"}) || state.Pending[0].Header != "Note" {
		t.Fatalf("first state %+v", state)
	}
	// A new record short of a batch waits; nothing read before is read again.
	seedNote(t, s, "n5", "note 5 has thirty characters.")
	result, err = a.Consolidate(context.Background(), ConsolidateRequest{})
	if err != nil || result.Visited != 1 || result.Added != 0 || result.Remaining != 2 || len(model.calls) != 1 {
		t.Fatalf("second %+v %v calls %d", result, err, len(model.calls))
	}
	// A job that finds nothing new folds the rest.
	result, err = a.Consolidate(context.Background(), ConsolidateRequest{})
	if err != nil || result.Visited != 0 || result.Added != 2 || result.Remaining != 0 {
		t.Fatalf("third %+v %v", result, err)
	}
	if got := shown(); !reflect.DeepEqual(got, []string{"0", "1", "2", "3", "4", "5"}) {
		t.Fatal("records shown to the model", got)
	}
	result, err = a.Consolidate(context.Background(), ConsolidateRequest{})
	if err != nil || result.Visited != 0 || result.Added != 0 || len(model.calls) != 2 {
		t.Fatalf("idle %+v %v", result, err)
	}
	state = loadState(t, overlay)
	want := []string{}
	for i := 0; i < 6; i++ {
		want = append(want, fmt.Sprintf("note %d has thirty characters.@n%d", i, i))
	}
	if got := eventTexts(state); !reflect.DeepEqual(got, want) || state.Batches != 2 {
		t.Fatal("events", got, state.Batches)
	}
	// Reset starts over from the first record.
	result, err = a.Consolidate(context.Background(), ConsolidateRequest{Reset: true})
	if err != nil || result.Visited != 6 || result.Remaining != 2 {
		t.Fatalf("reset %+v %v", result, err)
	}
}

func TestConsolidateTimelinesBatchLimitAndConversations(t *testing.T) {
	s, dir := agentStore(t)
	at := time.Date(2024, 3, 15, 9, 0, 0, 0, time.UTC)
	messages := []ConversationMessage{}
	for i := 0; i < 12; i++ {
		messages = append(messages, ConversationMessage{Role: "user", Content: fmt.Sprintf("Message %d about the demo schedule, %s", i, strings.Repeat("words ", 60)), At: &at},
			ConversationMessage{Role: "assistant", Content: "Answer " + strings.Repeat("long ", 100), At: &at})
	}
	written, err := WriteConversation(context.Background(), s, ConversationRequest{ID: "talk", SessionID: "s1", Messages: messages})
	if err != nil {
		t.Fatal(err)
	}
	model := &limitPlanner{reply: func(_, prompt string) (string, error) { return eventsReply(prompt), nil }}
	// Pending holds at most 4 × 1000 units: three full batches, and the
	// job's limit of two binds.
	a, overlay := timelinesAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 1000, MaxBatches: 4}, Models{Planner: model})
	if _, err := a.Consolidate(context.Background(), ConsolidateRequest{MaxBatches: 65}); err == nil {
		t.Fatal("maxBatches 65 accepted")
	}
	result, err := a.Consolidate(context.Background(), ConsolidateRequest{MaxBatches: 2})
	if err != nil || result.Visited == 0 || result.Visited >= len(written.Records) || len(model.calls) != 2 || result.Folded != 2 || result.Remaining == 0 {
		t.Fatalf("result %+v %v calls %d", result, err, len(model.calls))
	}
	state := loadState(t, overlay)
	if state.Batches != 2 || !strings.Contains(model.calls[0].prompt, "[r0] 2024/03/15 (session s1)\n") {
		t.Fatalf("batches %d prompt %s", state.Batches, model.calls[0].prompt)
	}
	for _, call := range model.calls {
		if strings.Contains(call.prompt, "Conversation s1") || strings.Contains(call.prompt, strings.Repeat("long ", 60)) {
			t.Fatal("a fold saw a heading or a whole assistant message")
		}
	}
	result, err = a.Consolidate(context.Background(), ConsolidateRequest{MaxBatches: 1})
	if err != nil || len(model.calls) != 3 {
		t.Fatalf("one batch %+v %v calls %d", result, err, len(model.calls))
	}
}

func TestConsolidateTimelinesSerializesPerScope(t *testing.T) {
	s, dir := agentStore(t)
	// Every record fills a batch, so each job folds one.
	for i := 0; i < 4; i++ {
		seedNote(t, s, fmt.Sprintf("n%d", i), fmt.Sprintf("note %d %s", i, strings.Repeat("x", 100)))
	}
	model := &limitPlanner{reply: func(_, prompt string) (string, error) {
		time.Sleep(20 * time.Millisecond)
		return eventsReply(prompt), nil
	}}
	a, overlay := timelinesAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100}, Models{Planner: model})
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = a.Consolidate(context.Background(), ConsolidateRequest{MaxBatches: 1})
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal("concurrent consolidation lost a write", err)
		}
	}
	if state := loadState(t, overlay); len(state.Memory.Events) != 4 || len(model.calls) != 4 {
		t.Fatal("events", eventTexts(state), len(model.calls))
	}
}

func TestTimelinesStoreCompareAndSwap(t *testing.T) {
	_, dir := agentStore(t)
	overlay, err := OpenOverlay(filepath.Join(dir, "memory-index.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer overlay.Close()
	ctx := context.Background()
	state, token, err := overlay.loadTimelines(ctx, records.Scope{})
	if err != nil || token != "" || state.Format != "mnemon-consolidation/v1" || state.Memory.Facts == nil {
		t.Fatalf("empty state %+v %q %v", state, token, err)
	}
	state.Watermark = 7
	first, err := overlay.saveTimelines(ctx, records.Scope{}, "", state)
	if err != nil || first == "" {
		t.Fatal(err)
	}
	if _, err := overlay.saveTimelines(ctx, records.Scope{}, "", state); err == nil {
		t.Fatal("a second first write was accepted")
	}
	state.Watermark = 8
	second, err := overlay.saveTimelines(ctx, records.Scope{}, first, state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := overlay.saveTimelines(ctx, records.Scope{}, first, state); err == nil {
		t.Fatal("a stale token was accepted")
	}
	loaded, token, err := overlay.loadTimelines(ctx, records.Scope{})
	if err != nil || token != second || loaded.Watermark != 8 {
		t.Fatal(loaded.Watermark, token, err)
	}
	other := "/elsewhere"
	if loaded, token, err := overlay.loadTimelines(ctx, records.Scope{WorkspaceID: &other}); err != nil || token != "" || loaded.Watermark != 0 {
		t.Fatal("scopes share a state", loaded.Watermark, err)
	}
	if _, err := overlay.db.Exec(`UPDATE timelines SET payload=replace(payload,'"watermark":8','"watermark":9')`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := overlay.loadTimelines(ctx, records.Scope{}); err == nil {
		t.Fatal("a changed payload was accepted")
	}
}

func TestConsolidationConfig(t *testing.T) {
	s, _ := agentStore(t)
	planner := &limitPlanner{reply: func(string, string) (string, error) { return "{}", nil }}
	a, err := New(s, Config{Version: 1, Policy: &ReadPolicy{}, Consolidation: &ConsolidationConfig{Mode: "timelines"}}, Models{Planner: planner}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := *a.config.Consolidation; !reflect.DeepEqual(got, ConsolidationConfig{Mode: "timelines", BatchCharacters: 10000, AssistantCharacters: 240, MaxBatches: 16}) {
		t.Fatalf("defaults %+v", got)
	}
	for _, bad := range []Config{
		{Version: 1, Policy: &ReadPolicy{}, Consolidation: &ConsolidationConfig{Mode: "graph"}},
		{Version: 1, Policy: &ReadPolicy{}, Consolidation: &ConsolidationConfig{Mode: "timelines", BatchCharacters: 99}},
		{Version: 1, Policy: &ReadPolicy{}, Consolidation: &ConsolidationConfig{Mode: "timelines", BatchCharacters: 50001}},
		{Version: 1, Policy: &ReadPolicy{}, Consolidation: &ConsolidationConfig{Mode: "timelines", AssistantCharacters: 4001}},
		{Version: 1, Policy: &ReadPolicy{}, Consolidation: &ConsolidationConfig{Mode: "timelines", AssistantCharacters: -1}},
		{Version: 1, Policy: &ReadPolicy{}, Consolidation: &ConsolidationConfig{Mode: "timelines", MaxBatches: 65}},
		{Version: 1, Policy: &ReadPolicy{}, Consolidation: &ConsolidationConfig{BatchCharacters: 1000}},
		{Version: 1, Policy: &ReadPolicy{}, Consolidator: &ModelConfig{Kind: "local", Model: "m", BaseURL: "http://127.0.0.1:1/v1"}},
	} {
		if _, err := New(s, bad, Models{Planner: planner}, nil); err == nil {
			t.Fatalf("accepted %+v %+v", bad.Consolidation, bad.Consolidator)
		}
	}
	// A configuration without consolidation settings keeps its bytes.
	raw, _ := json.Marshal(Config{Version: 1, Strategy: "simple"})
	if strings.Contains(string(raw), "consolidat") {
		t.Fatal("v1 config gained consolidation fields", string(raw))
	}
	// Timelines consolidation needs the overlay store.
	if _, err := a.Consolidate(context.Background(), ConsolidateRequest{}); err == nil || !strings.Contains(err.Error(), "timelines consolidation requires an overlay store") {
		t.Fatal("timelines consolidation ran without an overlay store", err)
	}
}

func TestConsolidationConfigEmptyModeStaysV1(t *testing.T) {
	s, dir := agentStore(t)
	seedNote(t, s, "answer", "codename ORBIT")
	overlay, err := OpenOverlay(filepath.Join(dir, "memory-index.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer overlay.Close()
	var config Config
	if err := json.Unmarshal([]byte(`{"version":1,"policy":{"consolidation":true},"consolidation":{}}`), &config); err != nil {
		t.Fatal(err)
	}
	planner := completeFunc(func(context.Context, string, string) (string, Usage, error) {
		return `{"items":[{"kind":"value","title":"codename","text":"ORBIT","records":["r0"]}]}`, Usage{}, nil
	})
	a, err := New(s, config, Models{Planner: planner}, overlay)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*a.config.Consolidation, ConsolidationConfig{}) {
		t.Fatal("mode \"\" took timelines defaults", *a.config.Consolidation)
	}
	result, err := a.Consolidate(context.Background(), ConsolidateRequest{})
	if err != nil || result.Visited != 1 || result.Added != 1 {
		t.Fatal(result, err)
	}
	if v1, _, err := overlay.load(context.Background(), records.Scope{}); err != nil || len(v1.Items) != 1 {
		t.Fatal("mode \"\" did not run the v1 fold", v1.Items, err)
	}
	if state := loadState(t, overlay); state.Watermark != 0 || len(state.Pending) != 0 {
		t.Fatal("mode \"\" wrote timelines state", state)
	}
}

func TestConsolidateTimelinesResetEmptyScope(t *testing.T) {
	s, dir := agentStore(t)
	for i := 0; i < 4; i++ {
		seedNote(t, s, fmt.Sprintf("n%d", i), fmt.Sprintf("note %d has thirty characters.", i))
	}
	model := &limitPlanner{reply: func(_, prompt string) (string, error) { return eventsReply(prompt), nil }}
	a, overlay := timelinesAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100}, Models{Planner: model})
	if result, err := a.Consolidate(context.Background(), ConsolidateRequest{}); err != nil || result.Added != 4 {
		t.Fatal(result, err)
	}
	if _, err := s.Put(context.Background(), records.PutRequest{Delete: []string{"n0", "n1", "n2", "n3"}}); err != nil {
		t.Fatal(err)
	}
	// With no record left, a reset still replaces the stored state.
	result, err := a.Consolidate(context.Background(), ConsolidateRequest{Reset: true})
	if err != nil || result.Visited != 0 || result.Added != 0 || result.Remaining != 0 {
		t.Fatal(result, err)
	}
	state := loadState(t, overlay)
	if raw, _ := json.Marshal(state); string(raw) != func() string { raw, _ := json.Marshal(emptyTimelineState()); return string(raw) }() {
		t.Fatal("reset kept the old state", string(raw))
	}
}

func TestConsolidateTimelinesSavesAfterCanceledModel(t *testing.T) {
	s, dir := agentStore(t)
	for i := 0; i < 4; i++ {
		seedNote(t, s, fmt.Sprintf("n%d", i), fmt.Sprintf("note %d has thirty characters.", i))
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := &limitPlanner{reply: func(string, string) (string, error) {
		cancel()
		return "", context.Canceled
	}}
	a, overlay := timelinesAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100}, Models{Planner: model})
	result, err := a.Consolidate(ctx, ConsolidateRequest{})
	if !errors.Is(err, context.Canceled) || result.Visited != 4 || result.Remaining != 4 {
		t.Fatal(result, err)
	}
	// The records read stay read: the next job does not read them again.
	if state := loadState(t, overlay); state.Watermark == 0 || len(state.Pending) != 4 || state.Usage.InputTokens != 10 {
		t.Fatalf("state after a canceled fold %+v", state)
	}
}

// kayakMemory is the author's index-limit fixture (consolidation.spec.ts):
// twelve topics and values, one of each about a kayak, and a directive.
func kayakMemory() (timelineMemory, []pendingRecord) {
	memory := emptyTimelineState().Memory
	for i := 1; i <= 12; i++ {
		title, value := fmt.Sprintf("Topic number %d", i), fmt.Sprintf("value %d", i)
		if i == 3 {
			title = "Kayak trip to the lake"
		}
		if i == 5 {
			value = "kayak rented"
		}
		date := fmt.Sprintf("2024/01/%02d", i)
		memory.Threads = append(memory.Threads, thread{ID: fmt.Sprintf("T%d", i), Title: title})
		memory.Events = append(memory.Events, event{Thread: fmt.Sprintf("T%d", i), Date: date, Text: fmt.Sprintf("event %d", i), Records: []string{}})
		key := fmt.Sprintf("thing %d · status", i)
		memory.Facts[key] = []factValue{{Value: value, Date: date, Records: []string{}}}
		memory.FactOrder = append(memory.FactOrder, key)
	}
	memory.Directives = append(memory.Directives, directive{Text: "Answer briefly.", Date: "2024/01/01", Records: []string{}})
	return memory, []pendingRecord{{ID: "r", Header: "2024/02/01 (session 9)", Text: "user: We booked the kayak for the lake next week."}}
}

func TestTimelinePromptShownUnderLimit(t *testing.T) {
	memory, batch := kayakMemory()
	whole := renderTimelinePrompt(memory, batch, nil)
	if got := renderTimelinePrompt(memory, batch, &indexLimit{Threads: 12, Values: 12, Directives: 5}); got != whole {
		t.Fatalf("a memory within its limit changed the prompt:\n%s\nwant:\n%s", got, whole)
	}
	if got := timelinePrompt(memory, batch); got != whole || strings.Contains(got, "most related") {
		t.Fatal("the default limits changed a small memory's prompt")
	}
}

func TestTimelinePromptShownCut(t *testing.T) {
	memory, batch := kayakMemory()
	small := renderTimelinePrompt(memory, batch, &indexLimit{Threads: 4, Values: 4, Directives: 5})
	of := " (the 4 of 12 most related to these records or most recent; the rest keep their ids and keys)"
	want := "Threads so far" + of + ":\nT3 Kayak trip to the lake (1 events, last 2024/01/03)\nT10 Topic number 10 (1 events, last 2024/01/10)\nT11 Topic number 11 (1 events, last 2024/01/11)\nT12 Topic number 12 (1 events, last 2024/01/12)" +
		"\n\nState keys so far, with the latest value" + of + ":\n- thing 5 · status: kayak rented\n- thing 10 · status: value 10\n- thing 11 · status: value 11\n- thing 12 · status: value 12" +
		"\n\nDirectives so far:\n- Answer briefly." +
		"\n\nNew records:\n\n[r0] 2024/02/01 (session 9)\nuser: We booked the kayak for the lake next week."
	if small != want {
		t.Fatalf("cut prompt:\n%s\nwant:\n%s", small, want)
	}
	// The default limits are the author's long-memory limits 120/240/60.
	for i := 13; i <= 121; i++ {
		memory.Threads = append(memory.Threads, thread{ID: fmt.Sprintf("T%d", i), Title: fmt.Sprintf("Topic number %d", i)})
	}
	if got := timelinePrompt(memory, batch); !strings.HasPrefix(got, "Threads so far (the 120 of 121 most related") || strings.Count(strings.Split(got, "\n\n")[0], "\n") != 120 {
		t.Fatal("default thread limit", strings.Split(got, "\n\n")[0][:80])
	}
	if !strings.Contains(timelinePrompt(memory, batch), "State keys so far, with the latest value:\n") {
		t.Fatal("values under their limit were cut")
	}
}

func TestShownLexicalAndRecency(t *testing.T) {
	tokens := lexicalTokens("Hello WORLD-wide a_b:c x- 2024/03 İstanbul 记忆力 记 𠀀")
	want := []string{"hello", "world-wide", "a_b:c", "x", "2024", "03", "i", "stanbul", "记忆", "忆力", "记", "𠀀"}
	if !reflect.DeepEqual(tokens, want) {
		t.Fatalf("tokens %q", tokens)
	}
	// A run of two characters outside the BMP is four UTF-16 units: the
	// author's bigrams are both characters and the pair across them.
	if got := lexicalTokens("𠀀𠀁"); len(got) != 3 || got[0] != "𠀀" || got[2] != "𠀁" || got[1] == "��" {
		t.Fatalf("supplementary Han tokens %q", got)
	}
	// Equal scores rank by id as a string: "0", "1", "10" before "2".
	texts := make([]string, 12)
	for i := range texts {
		texts[i] = "kayak"
	}
	if got := bm25Top(texts, "the kayak", 3); !reflect.DeepEqual(got, []int{0, 1, 10}) {
		t.Fatal("tie order", got)
	}
	// Only positive scores count; BM25 favors the shorter of two matches.
	if got := bm25Top([]string{"a kayak on a long lake trip", "kayak", "lake"}, "kayak", 5); !reflect.DeepEqual(got, []int{1, 0}) {
		t.Fatal("bm25 order", got)
	}
	// Half the room by words, then the newest; equal recency keeps the
	// memory's order (a stable descending sort).
	dates := []string{"2024/01/02", "2024/01/05", "2024/01/05", "2024/01/01", "2024/01/05"}
	keep := shownItems(len(dates), 3, func(i int) string { return []string{"kayak", "x", "y", "z", "w"}[i] },
		func(a, b int) int { return jsCompare(dates[a], dates[b]) }, "kayak")
	if !reflect.DeepEqual(keep, []bool{true, true, true, false, false}) {
		t.Fatal("shown", keep)
	}
	if !reflect.DeepEqual(shownItems(2, 2, nil, nil, ""), []bool{true, true}) {
		t.Fatal("a list within its limit is shown whole")
	}
	// JavaScript compares strings by UTF-16 units: U+FF5E sorts after an
	// emoji's surrogate pair start, unlike code point order.
	if jsCompare("～", "😀") <= 0 || jsCompare("a", "ab") >= 0 {
		t.Fatal("jsCompare is not UTF-16 order")
	}
}

func TestConsolidateTimelinesTooLargeSplits(t *testing.T) {
	s, dir := agentStore(t)
	notes := []string{"alpha note number one here...", "bravo note number two here....", "charlie note number three.....", "delta note number four here..."}
	for i, text := range notes {
		seedNote(t, s, fmt.Sprintf("n%d", i), text)
	}
	// The provider refuses every request of more than one record, and any
	// holding delta, before the network: a refused request splits at once,
	// down to single records, and a single record still refused is dropped.
	model := &limitPlanner{reply: func(_, prompt string) (string, error) {
		if strings.Contains(prompt, "[r1]") || strings.Contains(prompt, "delta") {
			return "", fmt.Errorf("%w: model request exceeds 160000 bytes", ErrRequestTooLarge)
		}
		return eventsReply(prompt), nil
	}}
	a, overlay := timelinesAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100}, Models{Consolidator: model})
	result, err := a.Consolidate(context.Background(), ConsolidateRequest{})
	if err != nil || result.Visited != 4 || result.Added != 3 || result.Remaining != 0 || result.Folded != 1 {
		t.Fatalf("result %+v %v", result, err)
	}
	if result.Dropped != 1 || len(result.Drops) != 1 || result.Drops[0].Records != 1 || !strings.Contains(result.Drops[0].Reason, "too large") {
		t.Fatalf("drops %+v", result)
	}
	state := loadState(t, overlay)
	// [0-3] [0,1] [0] [1] [2,3] [2] [3]: refused requests are not calls.
	if state.Batches != 3 || state.Failed != 1 || state.Calls != 3 || len(state.Pending) != 0 || len(model.calls) != 7 {
		t.Fatalf("state batches %d failed %d calls %d pending %d attempts %d", state.Batches, state.Failed, state.Calls, len(state.Pending), len(model.calls))
	}
	if got := eventTexts(state); !reflect.DeepEqual(got, []string{notes[0] + "@n0", notes[1] + "@n1", notes[2] + "@n2"}) {
		t.Fatal("events", got)
	}
}

// timelineClock is a settable clock for an agent.
type timelineClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *timelineClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *timelineClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestConsolidateTimelinesDropsHeadBatchAfterRepeatedErrors(t *testing.T) {
	notes := []string{"alpha note number one here...", "bravo note number two here....", "charlie note number three.....", "delta note number four here..."}
	seed := func(t *testing.T, reply func(string, string) (string, error)) (*Agent, *Overlay, *timelineClock, *limitPlanner) {
		s, dir := agentStore(t)
		for i, text := range notes {
			seedNote(t, s, fmt.Sprintf("n%d", i), text)
		}
		model := &limitPlanner{reply: reply}
		a, overlay := timelinesAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100}, Models{Consolidator: model})
		clock := &timelineClock{now: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
		a.now = clock.Now
		return a, overlay, clock, model
	}
	broken := errors.New("model HTTP 400; reservation released")
	failDelta := func(_, prompt string) (string, error) {
		if strings.Contains(prompt, "delta") {
			return "", broken
		}
		return eventsReply(prompt), nil
	}
	t.Run("three errors over ten minutes drop the failing half", func(t *testing.T) {
		a, overlay, clock, _ := seed(t, failDelta)
		ctx := context.Background()
		for i, wait := range []time.Duration{0, time.Minute, time.Minute, 7 * time.Minute} {
			clock.advance(wait)
			result, err := a.Consolidate(ctx, ConsolidateRequest{})
			if !errors.Is(err, broken) || result.Remaining != 4 || result.Dropped != 0 {
				t.Fatal(i, result, err)
			}
			state := loadState(t, overlay)
			if state.ModelErrors != i+1 || state.FirstModelError != time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC).UnixMilli() || len(state.Pending) != 4 || state.Failed != 0 {
				t.Fatalf("job %d state %+v", i, state)
			}
		}
		// Ten minutes after the first error: the batch splits once; the
		// half that folds is kept, the half that fails again is dropped.
		clock.advance(time.Minute)
		result, err := a.Consolidate(ctx, ConsolidateRequest{})
		if err != nil || result.Added != 2 || result.Remaining != 0 || result.Folded != 1 || result.Dropped != 2 || len(result.Drops) != 1 || result.Drops[0].Records != 2 ||
			!strings.Contains(result.Drops[0].Reason, "model HTTP 400") || !strings.Contains(result.Drops[0].Reason, "4 failed jobs") {
			t.Fatalf("result %+v %v", result, err)
		}
		state := loadState(t, overlay)
		if state.ModelErrors != 0 || state.FirstModelError != 0 || state.Failed != 1 || len(state.Pending) != 0 {
			t.Fatalf("state %+v", state)
		}
		if got := eventTexts(state); !reflect.DeepEqual(got, []string{notes[0] + "@n0", notes[1] + "@n1"}) {
			t.Fatal("events", got)
		}
	})
	t.Run("errors do not count in ten minutes alone", func(t *testing.T) {
		a, overlay, clock, _ := seed(t, failDelta)
		for range 2 {
			a.Consolidate(context.Background(), ConsolidateRequest{})
			clock.advance(time.Hour)
		}
		if result, err := a.Consolidate(context.Background(), ConsolidateRequest{}); !errors.Is(err, broken) || result.Dropped != 0 {
			t.Fatal(result, err)
		}
		if state := loadState(t, overlay); state.ModelErrors != 3 || len(state.Pending) != 4 {
			t.Fatalf("state %+v", state)
		}
	})
	t.Run("a fold resets the count", func(t *testing.T) {
		healthy := false
		a, overlay, _, _ := seed(t, func(system, prompt string) (string, error) {
			if healthy {
				return eventsReply(prompt), nil
			}
			return failDelta(system, prompt)
		})
		a.Consolidate(context.Background(), ConsolidateRequest{})
		if state := loadState(t, overlay); state.ModelErrors != 1 {
			t.Fatalf("state %+v", state)
		}
		healthy = true
		if result, err := a.Consolidate(context.Background(), ConsolidateRequest{}); err != nil || result.Added != 4 || result.Dropped != 0 {
			t.Fatal(result, err)
		}
		if state := loadState(t, overlay); state.ModelErrors != 0 || state.FirstModelError != 0 {
			t.Fatalf("state %+v", state)
		}
	})
	t.Run("a spent budget and cancellation never count or drop", func(t *testing.T) {
		capped := fmt.Errorf("deepseek: %w", ErrBudgetCap)
		var cancel context.CancelFunc
		a, overlay, clock, _ := seed(t, func(_, prompt string) (string, error) {
			if cancel != nil {
				cancel()
				return "", context.Canceled
			}
			return "", capped
		})
		for range 5 {
			if _, err := a.Consolidate(context.Background(), ConsolidateRequest{}); !errors.Is(err, ErrBudgetCap) {
				t.Fatal(err)
			}
			clock.advance(time.Hour)
		}
		var ctx context.Context
		ctx, cancel = context.WithCancel(context.Background())
		if _, err := a.Consolidate(ctx, ConsolidateRequest{}); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if state := loadState(t, overlay); state.ModelErrors != 0 || len(state.Pending) != 4 || state.Failed != 0 {
			t.Fatalf("state %+v", state)
		}
		// In drop mode a spent budget still ends the job without dropping.
		cancel = nil
		raw := loadState(t, overlay)
		raw.ModelErrors, raw.FirstModelError = 3, clock.Now().Add(-time.Hour).UnixMilli()
		_, token, _ := overlay.loadTimelines(context.Background(), records.Scope{})
		if _, err := overlay.saveTimelines(context.Background(), records.Scope{}, token, raw); err != nil {
			t.Fatal(err)
		}
		if result, err := a.Consolidate(context.Background(), ConsolidateRequest{}); !errors.Is(err, ErrBudgetCap) || result.Dropped != 0 {
			t.Fatal(result, err)
		}
		if state := loadState(t, overlay); state.ModelErrors != 3 || len(state.Pending) != 4 || state.Failed != 0 {
			t.Fatalf("state %+v", state)
		}
	})
}

func TestConsolidateTimelinesHoldFoldsOnlyFullBatches(t *testing.T) {
	s, dir := agentStore(t)
	for i := 0; i < 5; i++ {
		seedNote(t, s, fmt.Sprintf("n%d", i), fmt.Sprintf("note %d has thirty characters.", i))
	}
	model := &limitPlanner{reply: func(_, prompt string) (string, error) { return eventsReply(prompt), nil }}
	a, overlay := timelinesAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100}, Models{Planner: model})
	ctx := context.Background()
	result, err := a.Consolidate(ctx, ConsolidateRequest{Hold: true})
	if err != nil || result.Visited != 5 || result.Folded != 1 || result.Remaining != 1 {
		t.Fatalf("first %+v %v", result, err)
	}
	// Nothing new, but a held job leaves the short rest alone, and saves nothing.
	_, token, _ := overlay.loadTimelines(ctx, records.Scope{})
	result, err = a.Consolidate(ctx, ConsolidateRequest{Hold: true})
	if err != nil || result.Visited != 0 || result.Folded != 0 || result.Remaining != 1 || len(model.calls) != 1 {
		t.Fatalf("held %+v %v", result, err)
	}
	if _, again, _ := overlay.loadTimelines(ctx, records.Scope{}); again != token {
		t.Fatal("an idle held job saved the state")
	}
	result, err = a.Consolidate(ctx, ConsolidateRequest{})
	if err != nil || result.Folded != 1 || result.Remaining != 0 || len(model.calls) != 2 {
		t.Fatalf("flush %+v %v", result, err)
	}
	if state := loadState(t, overlay); len(state.Memory.Events) != 5 {
		t.Fatal(eventTexts(state))
	}
}

func TestConsolidateTimelinesPendingIsBounded(t *testing.T) {
	// Each note is read as 34 units: the header "Note" and 30 of text.
	seed := func(t *testing.T, settings ConsolidationConfig, notes int) (*Agent, *Overlay, *limitPlanner, []string) {
		s, dir := agentStore(t)
		want := []string{}
		for i := 0; i < notes; i++ {
			seedNote(t, s, fmt.Sprintf("n%02d", i), fmt.Sprintf("note %02d has thirty characters.", i))
			want = append(want, fmt.Sprintf("note %02d has thirty characters.@n%02d", i, i))
		}
		model := &limitPlanner{reply: func(_, prompt string) (string, error) { return eventsReply(prompt), nil }}
		a, overlay := timelinesAgent(t, s, dir, settings, Models{Planner: model})
		return a, overlay, model, want
	}
	units := func(pending []pendingRecord) int {
		n := 0
		for _, record := range pending {
			n += jsLen(record.Header) + jsLen(record.Text)
		}
		return n
	}
	for _, hold := range []bool{false, true} {
		t.Run(fmt.Sprint("hold ", hold), func(t *testing.T) {
			// Two batches of 100 units: pending holds at most 200 units.
			const notes = 21
			a, overlay, _, want := seed(t, ConsolidationConfig{BatchCharacters: 100, MaxBatches: 2}, notes)
			result, err := a.Consolidate(context.Background(), ConsolidateRequest{Hold: hold})
			// Five notes (170 units) are read, a sixth would pass 200; four fold.
			if err != nil || result.Visited != 5 || result.Folded != 1 || result.Remaining != notes-4 {
				t.Fatalf("first %+v %v", result, err)
			}
			for jobs := 0; result.Remaining > 0 && jobs < 20; jobs++ {
				if result, err = a.Consolidate(context.Background(), ConsolidateRequest{Hold: hold}); err != nil {
					t.Fatal(err)
				}
				if n := units(loadState(t, overlay).Pending); n > 200 {
					t.Fatal("pending grew past its bound", n)
				}
				if hold && result.Folded == 0 {
					break
				}
			}
			if hold {
				// A held job leaves the tail; the next job flushes it.
				if result.Remaining != 1 {
					t.Fatal("a held job flushed the tail", result)
				}
				if result, err = a.Consolidate(context.Background(), ConsolidateRequest{}); err != nil || result.Remaining != 0 {
					t.Fatal(result, err)
				}
			}
			if got := eventTexts(loadState(t, overlay)); !reflect.DeepEqual(got, want) {
				t.Fatal("records lost or repeated", got)
			}
		})
	}
	t.Run("a bound without a full batch folds what is pending", func(t *testing.T) {
		// One batch of 100 units: two notes are pending (68 units) and hold
		// no full batch, yet more follow: they fold as one, held or not.
		const notes = 20
		a, overlay, model, want := seed(t, ConsolidationConfig{BatchCharacters: 100, MaxBatches: 1}, notes)
		for job := 0; job < notes/2-1; job++ {
			result, err := a.Consolidate(context.Background(), ConsolidateRequest{Hold: job%2 == 0})
			if err != nil || result.Folded != 1 || result.Remaining != notes-2*(job+1) {
				t.Fatalf("job %d %+v %v", job, result, err)
			}
		}
		// The last two are the tail: read, then flushed by a job that finds
		// nothing new.
		for _, folded := range []int{0, 1} {
			if result, err := a.Consolidate(context.Background(), ConsolidateRequest{}); err != nil || result.Folded != folded {
				t.Fatal(result, err)
			}
		}
		if got := eventTexts(loadState(t, overlay)); !reflect.DeepEqual(got, want) || len(model.calls) != notes/2 {
			t.Fatal("records lost or repeated", got, len(model.calls))
		}
	})
}

func TestCountedErrorIgnoresProviderWideFailures(t *testing.T) {
	ctx := context.Background()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	for _, c := range []struct {
		ctx  context.Context
		err  error
		want bool
	}{
		{ctx, errors.New("model HTTP 400; reservation released"), true},
		{ctx, errors.New("model request failed; any reservation remains held"), true},
		{ctx, fmt.Errorf("%w: model HTTP 402; reservation released", ErrModelUnavailable), false},
		{ctx, fmt.Errorf("%w: endpoint unreachable", ErrModelUnavailable), false},
		{ctx, ErrBudgetCap, false},
		{ctx, context.DeadlineExceeded, false},
		{canceled, errors.New("model HTTP 400"), false},
	} {
		if got := countedError(c.ctx, c.err); got != c.want {
			t.Fatal(c.err, got)
		}
	}
}
