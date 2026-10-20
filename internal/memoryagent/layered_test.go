package memoryagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mnemon-dev/mnemon/internal/records"
)

func TestLayeredSystemPromptEdits(t *testing.T) {
	edits := []struct{ old, new string }{
		{`Reuse an existing thread id when the event continues that sub-topic; otherwise start a new thread ("new: <short title>").`,
			`Name the thread with a short title ("new: <short title>") and use the same title for every event of that sub-topic in this batch.`},
		{`When an existing key changes, repeat that exact key with the new value.`,
			`When a key's value changes within the batch, repeat that exact key with the new value.`},
		{`contradicts an earlier one and`, `contradicts an earlier one in this batch and`},
		{`Use only what the records say.`,
			`For each event and fact, list in "entities" the names of the people, organizations, places, products or documents it is about. Use only what the records say.`},
		{`{"events":[{"thread":"T3","date":"YYYY/MM/DD","text":"...","records":["r12"]}],"facts":[{"key":"...","value":"...","date":"YYYY/MM/DD","records":["r15"],"conflict":false}],"directives":[{"text":"...","date":"YYYY/MM/DD","records":["r20"]}]}`,
			`{"events":[{"thread":"new: <short title>","date":"YYYY/MM/DD","text":"...","entities":["..."],"records":["r12"]}],"facts":[{"key":"...","value":"...","date":"YYYY/MM/DD","entities":["..."],"records":["r15"],"conflict":false}],"directives":[{"text":"...","date":"YYYY/MM/DD","records":["r20"]}]}`},
	}
	want := timelineSystem
	for _, edit := range edits {
		if strings.Count(want, edit.old) != 1 {
			t.Fatalf("the author's prompt does not hold %q once", edit.old)
		}
		want = strings.Replace(want, edit.old, edit.new, 1)
	}
	if layeredSystem != want {
		t.Fatalf("layered system prompt:\n%s\nwant:\n%s", layeredSystem, want)
	}
	for _, gone := range []string{"Reuse an existing thread id", "When an existing key changes", `"thread":"T3"`} {
		if strings.Contains(layeredSystem, gone) {
			t.Fatal("the layered prompt still refers to an index:", gone)
		}
	}
	if !strings.Contains(layeredSystem, `it is about. Use only what the records say. Dates are the session dates`) {
		t.Fatal("the entities sentence is not where it belongs")
	}
}

func TestLayeredPromptHasNoIndex(t *testing.T) {
	memory, batch := kayakMemory()
	batch = append(batch, pendingRecord{ID: "y", Header: "Note", Text: "Buy milk."})
	want := "New records:\n\n[r0] 2024/02/01 (session 9)\nuser: We booked the kayak for the lake next week.\n\n[r1] Note\nBuy milk."
	for _, m := range []timelineMemory{{}, memory} {
		if got := layeredFold.prompt(m, batch); got != want {
			t.Fatalf("layered prompt:\n%s\nwant:\n%s", got, want)
		}
	}
	if !strings.HasSuffix(timelinePrompt(memory, batch), "\n\n"+want) {
		t.Fatal("the layered prompt is not the batch part of the author's")
	}
	if layeredFold.system != layeredSystem || !layeredFold.entities || timelinesFold.entities {
		t.Fatal("fold specs")
	}
}

func TestApplyReplyEntities(t *testing.T) {
	batch := []pendingRecord{{ID: "rec-a", Header: "h", Text: "x"}}
	reply := `{"events":[{"thread":"new: Demo","date":"2024/03/15","text":"Demo with Acme set.","entities":[" Acme ","Bob","Acme","",7,null],"records":["r0"]},` +
		`{"thread":"new: Demo","date":"2024/03/15","text":"No names.","records":["r0"]},{"thread":"new: Demo","text":"Bad names.","entities":"Acme"}],` +
		`"facts":[{"key":"demo · day","value":"Friday","date":"2024/03/15","entities":["Acme"],"records":["r0"]}]}`
	var layered timelineMemory
	if err := applyReplyWith(&layered, batch, reply, true); err != nil {
		t.Fatal(err)
	}
	if got := layered.Events[0].Entities; !reflect.DeepEqual(got, []string{"Acme", "Bob"}) {
		t.Fatal("event entities", got)
	}
	if layered.Events[1].Entities != nil || layered.Events[2].Entities != nil {
		t.Fatal("entities from nothing", layered.Events[1:])
	}
	if got := layered.Facts["demo · day"][0].Entities; !reflect.DeepEqual(got, []string{"Acme"}) {
		t.Fatal("fact entities", got)
	}
	// Timelines consolidation never keeps them, so its state bytes stay as
	// they were.
	var timelines timelineMemory
	if err := applyReply(&timelines, batch, reply); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(timelines)
	if strings.Contains(string(raw), "entities") {
		t.Fatal("timelines kept entities", string(raw))
	}
	layered.Events[0].Entities = nil
	layered.Facts["demo · day"][0].Entities = nil
	if again, _ := json.Marshal(layered); string(again) != string(raw) {
		t.Fatalf("entities changed more than themselves:\n%s\n%s", again, raw)
	}
}

func TestLayeredConsolidationConfig(t *testing.T) {
	s, _ := agentStore(t)
	planner := &limitPlanner{reply: func(string, string) (string, error) { return "{}", nil }}
	judge := decisionFunc(fixtureJudge)
	models := Models{Planner: planner, Judge: judge}
	a, err := New(s, Config{Version: 1, Policy: &ReadPolicy{Consolidation: true}, Consolidation: &ConsolidationConfig{Mode: "layered"}}, models, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := ConsolidationConfig{Mode: "layered", BatchCharacters: 10000, AssistantCharacters: 240, MaxBatches: 16, LinkCandidates: 8, Parallel: 4}
	if got := *a.config.Consolidation; !reflect.DeepEqual(got, want) {
		t.Fatalf("defaults %+v", got)
	}
	if !a.layerOn() || !a.config.Consolidation.Timelines() {
		t.Fatal("layered mode does not keep the timelines layer")
	}
	for _, good := range []ConsolidationConfig{
		{Mode: "layered", LinkCandidates: 1, Parallel: 1},
		{Mode: "layered", LinkCandidates: 32, Parallel: 16, BatchCharacters: 100, MaxBatches: 64},
	} {
		if _, err := New(s, Config{Version: 1, Policy: &ReadPolicy{}, Consolidation: &good}, models, nil); err != nil {
			t.Fatal(good, err)
		}
	}
	for _, bad := range []ConsolidationConfig{
		{Mode: "layered", LinkCandidates: 33},
		{Mode: "layered", LinkCandidates: -1},
		{Mode: "layered", Parallel: 17},
		{Mode: "layered", Parallel: -1},
		{Mode: "layered", BatchCharacters: 99},
		{Mode: "layered", MaxBatches: 65},
		{Mode: "timelines", LinkCandidates: 8},
		{Mode: "timelines", Parallel: 4},
		{LinkCandidates: 8},
		{Parallel: 4},
	} {
		if _, err := New(s, Config{Version: 1, Policy: &ReadPolicy{}, Consolidation: &bad}, models, nil); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	// Layered consolidation needs a decision model and a completion model.
	layered := Config{Version: 1, Policy: &ReadPolicy{}, Consolidation: &ConsolidationConfig{Mode: "layered"}}
	for _, missing := range []Models{{Planner: planner}, {Judge: judge}} {
		if _, err := New(s, layered, missing, nil); err == nil || !strings.Contains(err.Error(), "layered consolidation requires") {
			t.Fatal("accepted models", missing, err)
		}
	}
	if _, err := New(s, layered, Models{Consolidator: planner, Judge: judge}, nil); err != nil {
		t.Fatal(err)
	}
	// A consolidator model may fold either timelines mode.
	layered.Consolidator = &ModelConfig{Kind: "local", Model: "m", BaseURL: "http://127.0.0.1:1/v1"}
	if _, err := New(s, layered, models, nil); err != nil {
		t.Fatal(err)
	}
	// Timelines settings keep their bytes.
	raw, _ := json.Marshal(ConsolidationConfig{Mode: "timelines", BatchCharacters: 10000, AssistantCharacters: 240, MaxBatches: 16})
	if string(raw) != `{"mode":"timelines","batchCharacters":10000,"assistantCharacters":240,"maxBatches":16}` {
		t.Fatal(string(raw))
	}
	for config, want := range map[*ConsolidationConfig]bool{nil: false, {}: false, {Mode: "timelines"}: true, {Mode: "layered"}: true} {
		if config.Timelines() != want {
			t.Fatal(config, want)
		}
	}
	// Without an overlay store there is nothing to consolidate into.
	if _, err := a.Consolidate(context.Background(), ConsolidateRequest{}); err == nil || !strings.Contains(err.Error(), "requires an overlay store") {
		t.Fatal(err)
	}
}

var layeredNote = regexp.MustCompile(`\[r(\d+)\] [^\n]*\n(\S+) note (\d+)[^\n]*`)

// layeredReply is L0 over notes "<topic> note <n> …": per note, an event on
// the topic's thread naming the topic, and the value of "<topic> · status".
func layeredReply(prompt string) string {
	events, facts := []map[string]any{}, []map[string]any{}
	for _, m := range layeredNote.FindAllStringSubmatch(prompt, -1) {
		events = append(events, map[string]any{"thread": "new: " + m[2], "date": "2024/03/15", "text": m[0][strings.Index(m[0], "\n")+1:], "entities": []string{m[2]}, "records": []string{"r" + m[1]}})
		facts = append(facts, map[string]any{"key": m[2] + " · status", "value": "update " + m[3], "date": "2024/03/15", "records": []string{"r" + m[1]}, "conflict": false})
	}
	raw, _ := json.Marshal(map[string]any{"events": events, "facts": facts, "directives": []any{}})
	return string(raw)
}

// seedTopics stores 3n notes of 34 units, cycling kayak, taxes, garden:
// with batches of 100, every three notes are a batch.
func seedTopics(t *testing.T, s *records.Store, n int) {
	t.Helper()
	for i := 0; i < 3*n; i++ {
		topic := []string{"kayak", "taxes", "garden"}[i%3]
		seedNote(t, s, fmt.Sprintf("n%02d", i), fmt.Sprintf("%s note %02d %s", topic, i, strings.Repeat(".", 34-len(topic)-9)))
	}
}

// topicJudge links threads of the same topic and finds every new garden
// value contradicts the stored one.
func topicJudge() *linkJudge {
	return &linkJudge{answer: func(kind, newText, candidate string) float64 {
		topic := func(text string) string { return strings.Fields(text)[0] }
		switch kind {
		case "thread":
			if topic(newText) == topic(candidate) {
				return 0.9
			}
		case "contradiction":
			if strings.HasPrefix(newText, "garden") {
				return 0.8
			}
		}
		return 0.1
	}}
}

func layeredAgent(t *testing.T, s *records.Store, dir string, settings ConsolidationConfig, consolidator Completer, judge Decider) (*Agent, *Overlay) {
	t.Helper()
	overlay, err := OpenOverlay(filepath.Join(dir, "memory-index.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { overlay.Close() })
	settings.Mode = "layered"
	a, err := New(s, Config{Version: 1, Policy: &ReadPolicy{Consolidation: true}, Consolidation: &settings}, Models{Consolidator: consolidator, Judge: judge}, overlay)
	if err != nil {
		t.Fatal(err)
	}
	return a, overlay
}

func threadEvents(state timelineState) map[string][]string {
	titles := map[string]string{}
	for _, th := range state.Memory.Threads {
		titles[th.ID] = th.Title
	}
	out := map[string][]string{}
	for _, e := range state.Memory.Events {
		out[titles[e.Thread]] = append(out[titles[e.Thread]], e.Records...)
	}
	return out
}

func TestConsolidateLayeredLinksInBatchOrder(t *testing.T) {
	s, dir := agentStore(t)
	seedTopics(t, s, 3)
	model := &limitPlanner{reply: func(_, prompt string) (string, error) { return layeredReply(prompt), nil }}
	judge := topicJudge()
	a, overlay := layeredAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100}, model, judge)
	result, err := a.Consolidate(context.Background(), ConsolidateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Visited != 9 || result.Folded != 3 || result.Added != 18 || result.Remaining != 0 || result.Dropped != 0 {
		t.Fatalf("result %+v", result)
	}
	// The first batch has nothing to link to; the next two link every
	// topic, and their garden values contradict the stored ones.
	if result.LinkCalls != 2 || result.ThreadsLinked != 6 || result.ThreadsNew != 3 || result.KeysLinked != 6 || result.KeysNew != 3 || result.Conflicts != 2 || result.LinkFallbacks != 0 {
		t.Fatalf("links %+v", result)
	}
	if result.Usage.InputTokens != 3*10+2*100 || result.LinkUsage == nil || result.LinkUsage.InputTokens != 200 {
		t.Fatalf("usage %+v %+v", result.Usage, result.LinkUsage)
	}
	state := loadState(t, overlay)
	if state.Mode != "layered" || state.Batches != 3 || state.Calls != 3 || state.Usage.InputTokens != 230 || len(state.Pending) != 0 || state.Watermark == 0 {
		t.Fatalf("state %+v", state)
	}
	want := map[string][]string{"kayak": {"n00", "n03", "n06"}, "taxes": {"n01", "n04", "n07"}, "garden": {"n02", "n05", "n08"}}
	if got := threadEvents(state); !reflect.DeepEqual(got, want) || len(state.Memory.Threads) != 3 {
		t.Fatal("threads", got, state.Memory.Threads)
	}
	if !slices.Equal(state.Memory.FactOrder, []string{"kayak · status", "taxes · status", "garden · status"}) {
		t.Fatal(state.Memory.FactOrder)
	}
	garden := state.Memory.Facts["garden · status"]
	if len(garden) != 3 || garden[0].Conflict || !garden[1].Conflict || !garden[2].Conflict || state.Memory.Facts["kayak · status"][2].Conflict {
		t.Fatalf("garden %+v", garden)
	}
	if !reflect.DeepEqual(state.Memory.Events[0].Entities, []string{"kayak"}) {
		t.Fatal(state.Memory.Events[0])
	}
	// L0 calls: the layered system prompt, the batch alone, 8192 tokens.
	for _, call := range model.calls {
		if call.system != layeredSystem || call.maxTokens != 8192 || !strings.HasPrefix(call.prompt, "New records:\n\n[r0] Note\n") || strings.Contains(call.prompt, "so far") {
			t.Fatalf("L0 call %+v", call)
		}
	}
	// A later job links into what is stored.
	seedNote(t, s, "n09", "kayak note 09 "+strings.Repeat(".", 90))
	if result, err = a.Consolidate(context.Background(), ConsolidateRequest{}); err != nil || result.Folded != 1 || result.ThreadsLinked != 1 {
		t.Fatal(result, err)
	}
	if got := threadEvents(loadState(t, overlay))["kayak"]; !slices.Equal(got, []string{"n00", "n03", "n06", "n09"}) {
		t.Fatal(got)
	}
}

func TestConsolidateLayeredParallelMatchesSerial(t *testing.T) {
	run := func(parallel int, reorder bool) string {
		s, dir := agentStore(t)
		seedTopics(t, s, 3)
		done := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})}
		model := &limitPlanner{reply: func(_, prompt string) (string, error) {
			first := layeredNote.FindStringSubmatch(prompt)
			number, _ := strconv.Atoi(first[3])
			b := number / 3
			// In reverse: a batch replies only after the next one has.
			if reorder && b < 2 {
				<-done[b+1]
			}
			defer close(done[b])
			return layeredReply(prompt), nil
		}}
		a, overlay := layeredAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100, Parallel: parallel}, model, topicJudge())
		if result, err := a.Consolidate(context.Background(), ConsolidateRequest{}); err != nil || result.Folded != 3 {
			t.Fatal(result, err)
		}
		raw, _ := json.Marshal(loadState(t, overlay))
		return string(raw)
	}
	serial := run(1, false)
	if parallel := run(3, true); parallel != serial {
		t.Fatalf("parallel extraction changed the state:\n%s\n%s", parallel, serial)
	}
}

func TestConsolidateLayeredModelErrorKeepsLaterBatches(t *testing.T) {
	s, dir := agentStore(t)
	seedTopics(t, s, 3)
	broken := errors.New("model HTTP 400; reservation released")
	failing, third := true, make(chan struct{})
	model := &limitPlanner{reply: func(_, prompt string) (string, error) {
		// The second batch fails once the third is extracted.
		if failing && strings.Contains(prompt, "note 03") {
			<-third
			return "", broken
		}
		if failing && strings.Contains(prompt, "note 06") {
			defer close(third)
		}
		return layeredReply(prompt), nil
	}}
	a, overlay := layeredAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100}, model, topicJudge())
	result, err := a.Consolidate(context.Background(), ConsolidateRequest{})
	if !errors.Is(err, broken) || result.Folded != 1 || result.Remaining != 6 || result.Added != 6 || result.Dropped != 0 || result.Usage.InputTokens != 30 {
		t.Fatalf("result %+v %v", result, err)
	}
	// The third batch's extraction is discarded; its call and usage count.
	state := loadState(t, overlay)
	if state.Batches != 1 || state.ModelErrors != 1 || state.FirstModelError == 0 || !slices.Equal(ids(state.Pending), []string{"n03", "n04", "n05", "n06", "n07", "n08"}) || state.Calls != 2 || state.Usage.InputTokens != 30 {
		t.Fatalf("state %+v", state)
	}
	if got := threadEvents(state); !reflect.DeepEqual(got, map[string][]string{"kayak": {"n00"}, "taxes": {"n01"}, "garden": {"n02"}}) {
		t.Fatal("the third batch's extraction was kept", got)
	}
	failing = false
	if result, err = a.Consolidate(context.Background(), ConsolidateRequest{}); err != nil || result.Folded != 2 || result.Remaining != 0 {
		t.Fatal(result, err)
	}
	state = loadState(t, overlay)
	want := map[string][]string{"kayak": {"n00", "n03", "n06"}, "taxes": {"n01", "n04", "n07"}, "garden": {"n02", "n05", "n08"}}
	if got := threadEvents(state); !reflect.DeepEqual(got, want) || state.ModelErrors != 0 || state.Batches != 3 {
		t.Fatal(got, state.ModelErrors)
	}
}

func TestConsolidateLayeredLinkingDegradesOrCancels(t *testing.T) {
	t.Run("a failing judge links nothing and the job goes on", func(t *testing.T) {
		s, dir := agentStore(t)
		seedTopics(t, s, 3)
		model := &limitPlanner{reply: func(_, prompt string) (string, error) { return layeredReply(prompt), nil }}
		judge := topicJudge()
		judge.err = func(context.Context) error { return fmt.Errorf("jev: %w", ErrBudgetCap) }
		a, overlay := layeredAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100}, model, judge)
		result, err := a.Consolidate(context.Background(), ConsolidateRequest{})
		if err != nil || result.Folded != 3 || result.LinkFallbacks != 2 || result.LinkCalls != 2 || result.ThreadsNew != 9 || result.KeysLinked != 6 || result.KeysNew != 3 || result.Conflicts != 0 {
			t.Fatalf("result %+v %v", result, err)
		}
		if state := loadState(t, overlay); len(state.Memory.Threads) != 9 || len(state.Memory.Facts["garden · status"]) != 3 || state.ModelErrors != 0 {
			t.Fatalf("state %+v", state.Memory.Threads)
		}
	})
	t.Run("a canceled job keeps what it linked", func(t *testing.T) {
		s, dir := agentStore(t)
		seedTopics(t, s, 3)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		model := &limitPlanner{reply: func(_, prompt string) (string, error) { return layeredReply(prompt), nil }}
		judge := topicJudge()
		calls := 0
		judge.err = func(ctx context.Context) error {
			if calls++; calls == 2 {
				cancel()
				return ctx.Err()
			}
			return nil
		}
		a, overlay := layeredAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100}, model, judge)
		result, err := a.Consolidate(ctx, ConsolidateRequest{})
		if !errors.Is(err, context.Canceled) || result.Folded != 2 || result.Remaining != 3 || result.LinkFallbacks != 0 {
			t.Fatalf("result %+v %v", result, err)
		}
		state := loadState(t, overlay)
		if state.Batches != 2 || state.ModelErrors != 0 || len(state.Pending) != 3 || state.Calls != 3 || state.Usage.InputTokens != 3*10+100+3 {
			t.Fatalf("state %+v", state)
		}
	})
}

func TestConsolidateLayeredCanceledSaveKeepsSpending(t *testing.T) {
	// The job is canceled while the second batch is linked: linking ends
	// well, but saving it fails. The final save, without cancellation,
	// keeps that batch and every call the job made.
	s, dir := agentStore(t)
	seedTopics(t, s, 3)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := &limitPlanner{reply: func(_, prompt string) (string, error) { return layeredReply(prompt), nil }}
	judge := topicJudge()
	judge.err = func(context.Context) error {
		cancel()
		return nil
	}
	a, overlay := layeredAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100}, model, judge)
	result, err := a.Consolidate(ctx, ConsolidateRequest{})
	if !errors.Is(err, context.Canceled) || result.Folded != 2 || result.Remaining != 3 {
		t.Fatalf("result %+v %v", result, err)
	}
	state := loadState(t, overlay)
	if state.Batches != 2 || len(state.Pending) != 3 || state.Calls != 3 || state.Usage.InputTokens != 3*10+100 || state.ModelErrors != 0 {
		t.Fatalf("state %+v", state)
	}
	if got := threadEvents(state); !reflect.DeepEqual(got, map[string][]string{"kayak": {"n00", "n03"}, "taxes": {"n01", "n04"}, "garden": {"n02", "n05"}}) {
		t.Fatal(got)
	}
}

func TestConsolidateLayeredDropsHeadBatchAfterRepeatedErrors(t *testing.T) {
	s, dir := agentStore(t)
	notes := []string{"alpha note number one here...", "bravo note number two here....", "charlie note number three.....", "delta note number four here..."}
	for i, text := range notes {
		seedNote(t, s, fmt.Sprintf("n%d", i), text)
	}
	broken := errors.New("model HTTP 400; reservation released")
	model := &limitPlanner{reply: func(_, prompt string) (string, error) {
		if strings.Contains(prompt, "delta") {
			return "", broken
		}
		return eventsReply(prompt), nil
	}}
	a, overlay := layeredAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100}, model, topicJudge())
	clock := &timelineClock{now: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
	a.now = clock.Now
	if _, err := a.Consolidate(context.Background(), ConsolidateRequest{}); !errors.Is(err, broken) {
		t.Fatal(err)
	}
	raw := loadState(t, overlay)
	raw.ModelErrors, raw.FirstModelError = 3, clock.Now().Add(-time.Hour).UnixMilli()
	_, token, _ := overlay.loadTimelines(context.Background(), records.Scope{})
	if _, err := overlay.saveTimelines(context.Background(), records.Scope{}, token, raw); err != nil {
		t.Fatal(err)
	}
	// The head batch splits once; the half that fails again is dropped.
	result, err := a.Consolidate(context.Background(), ConsolidateRequest{})
	if err != nil || result.Folded != 1 || result.Added != 2 || result.Dropped != 2 || len(result.Drops) != 1 || !strings.Contains(result.Drops[0].Reason, "model error after 3 failed jobs over 1h0m0s: model HTTP 400") {
		t.Fatalf("result %+v %v", result, err)
	}
	state := loadState(t, overlay)
	if got := eventTexts(state); !slices.Equal(got, []string{notes[0] + "@n0", notes[1] + "@n1"}) || state.Failed != 1 || state.ModelErrors != 0 || len(state.Pending) != 0 {
		t.Fatal(got, state.Failed)
	}
	for _, call := range model.calls {
		if call.system != layeredSystem || strings.Contains(call.prompt, "so far") {
			t.Fatal("a drop-mode fold was not layered", call.system[:40])
		}
	}
}

func TestReadSimpleWithLayeredState(t *testing.T) {
	s, overlay := layerStore(t)
	ctx := context.Background()
	state, token, err := overlay.loadTimelines(ctx, records.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	state.Mode = "layered"
	for i := range state.Memory.Events {
		state.Memory.Events[i].Entities = []string{"ORBIT"}
	}
	if _, err := overlay.saveTimelines(ctx, records.Scope{}, token, state); err != nil {
		t.Fatal(err)
	}
	a := layerAgent(t, s, overlay, Config{View: &ViewConfig{MaxItems: 6}, Consolidation: &ConsolidationConfig{Mode: "layered"}}, codenamePlanner("NEED: the codename"), layerJudge())
	view, err := a.Read(ctx, ReadRequest{Question: "What is the codename?"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(blockIDs(view.Index), []string{"1-instructions", "2-topics", "3-timelines", "4-values"}) || view.Records[0].ID != "orbit" {
		t.Fatal(blockIDs(view.Index), recordIDs(view.Records))
	}
}
