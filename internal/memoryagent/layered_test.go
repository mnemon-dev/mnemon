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
	"sync"
	"sync/atomic"
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

// plannerCalls is the number of calls p received.
func plannerCalls(p *limitPlanner) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

// topicModel is L0 over topic notes (layeredReply).
func topicModel() *limitPlanner {
	return &limitPlanner{reply: func(_, prompt string) (string, error) { return layeredReply(prompt), nil }}
}

// jevDown is topicJudge failing every call with an unavailable provider
// while down is set.
func jevDown(down *atomic.Bool) *linkJudge {
	judge := topicJudge()
	judge.err = func(context.Context) error {
		if down.Load() {
			return fmt.Errorf("jev: %w: model HTTP 503; reservation released", ErrModelUnavailable)
		}
		return nil
	}
	return judge
}

// healthyMemory is, as JSON, the memory a layered job folds from
// seedTopics(n) with a judge that never fails.
func healthyMemory(t *testing.T, n int) string {
	t.Helper()
	s, dir := agentStore(t)
	seedTopics(t, s, n)
	a, overlay := layeredAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100}, topicModel(), topicJudge())
	if result, err := a.Consolidate(context.Background(), ConsolidateRequest{}); err != nil || result.Remaining != 0 {
		t.Fatal(result, err)
	}
	raw, _ := json.Marshal(cloneMemory(loadState(t, overlay).Memory))
	return string(raw)
}

func TestDeferredLinkLeavesBatchPending(t *testing.T) {
	ctx := context.Background()
	s, dir := agentStore(t)
	seedTopics(t, s, 3)
	var down atomic.Bool
	down.Store(true)
	a, overlay := layeredAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100}, topicModel(), jevDown(&down))
	// The first batch links into an empty memory, asking JEV nothing; JEV
	// is unavailable for the second, which stays pending with the third.
	result, err := a.Consolidate(ctx, ConsolidateRequest{DeferLinks: true})
	if !errors.Is(err, ErrLinkDeferred) || !errors.Is(err, ErrModelUnavailable) || result.Folded != 1 || result.Deferred != 1 || result.LinkFallbacks != 0 || result.Remaining != 6 || result.Dropped != 0 {
		t.Fatalf("result %+v %v", result, err)
	}
	_, heads, err := s.Heads(ctx, "", records.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	newest := int64(0)
	for _, head := range heads {
		newest = max(newest, head.Version)
	}
	// The state is the one saved after the first batch: every record read,
	// the first batch linked, nothing counted against the second.
	state := loadState(t, overlay)
	if state.Watermark != newest || state.Batches != 1 || state.ModelErrors != 0 || state.FirstModelError != 0 || state.LinkErrors != 0 || !slices.Equal(ids(state.Pending), []string{"n03", "n04", "n05", "n06", "n07", "n08"}) {
		t.Fatalf("state %+v", state)
	}
	if got := threadEvents(state); !reflect.DeepEqual(got, map[string][]string{"kayak": {"n00"}, "taxes": {"n01"}, "garden": {"n02"}}) {
		t.Fatal("the deferred batch was linked", got)
	}
	// JEV answers again: the next job links the second and third batches,
	// into the memory a job whose JEV never failed folds.
	down.Store(false)
	result, err = a.Consolidate(ctx, ConsolidateRequest{DeferLinks: true})
	if err != nil || result.Folded != 2 || result.Deferred != 0 || result.LinkFallbacks != 0 || result.Remaining != 0 {
		t.Fatalf("result %+v %v", result, err)
	}
	state = loadState(t, overlay)
	want := healthyMemory(t, 3)
	if got, _ := json.Marshal(cloneMemory(state.Memory)); string(got) != want {
		t.Fatalf("memory after the outage:\n%s\nwant:\n%s", got, want)
	}
	if state.Batches != 3 || len(state.Pending) != 0 || state.ModelErrors != 0 {
		t.Fatalf("state %+v", state)
	}
	// Neither field is written when unset.
	if raw, _ := json.Marshal(ConsolidateRequest{}); strings.Contains(string(raw), "deferLinks") {
		t.Fatal(string(raw))
	}
	if raw, _ := json.Marshal(ConsolidateResult{}); strings.Contains(string(raw), "deferred") {
		t.Fatal(string(raw))
	}
}

func TestDeferredLinkOutagesCountNothing(t *testing.T) {
	for name, cause := range map[string]error{
		"unavailable":       fmt.Errorf("jev: %w: model HTTP 503; reservation released", ErrModelUnavailable),
		"timeout":           fmt.Errorf("jev: %w; any reservation remains held (after 3 attempts)", ErrAttemptTimeout),
		"breaker":           fmt.Errorf("jev: %w", ErrBreakerOpen),
		"budget":            fmt.Errorf("jev: %w", ErrBudgetCap),
		"background budget": fmt.Errorf("jev: %w", ErrBackgroundReserve),
	} {
		t.Run(name, func(t *testing.T) {
			s, dir := agentStore(t)
			seedTopics(t, s, 3)
			judge := topicJudge()
			judge.err = func(context.Context) error { return cause }
			a, overlay := layeredAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100}, topicModel(), judge)
			// The job's extractions were answered, yet an outage of JEV,
			// a timeout too, counts nowhere: the batch is deferred.
			for range 2 {
				result, err := a.Consolidate(context.Background(), ConsolidateRequest{DeferLinks: true})
				if !errors.Is(err, ErrLinkDeferred) || !errors.Is(err, cause) || result.Deferred != 1 || result.LinkFallbacks != 0 {
					t.Fatalf("result %+v %v", result, err)
				}
				if state := loadState(t, overlay); state.ModelErrors != 0 || state.FirstModelError != 0 || state.LinkErrors != 0 || state.FirstLinkError != 0 || len(state.Pending) != 6 {
					t.Fatalf("state %+v", state)
				}
			}
		})
	}
}

func TestWithoutDeferLinksStillFallsBack(t *testing.T) {
	for name, failure := range map[string]error{
		"unavailable":   fmt.Errorf("jev: %w: model HTTP 503; reservation released", ErrModelUnavailable),
		"budget":        fmt.Errorf("jev: %w", ErrBudgetCap),
		"request's own": errors.New("jev: model HTTP 400; reservation released"),
	} {
		t.Run(name, func(t *testing.T) {
			s, dir := agentStore(t)
			seedTopics(t, s, 3)
			judge := topicJudge()
			calls := 0
			// JEV fails its first call, the second batch's only one.
			judge.err = func(context.Context) error {
				if calls++; calls == 1 {
					return failure
				}
				return nil
			}
			a, overlay := layeredAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100}, topicModel(), judge)
			// The second batch links without JEV: its topics start threads
			// of their own; the third batch's continue stored ones.
			result, err := a.Consolidate(context.Background(), ConsolidateRequest{})
			if err != nil || result.Folded != 3 || result.LinkFallbacks != 1 || result.Deferred != 0 || result.Remaining != 0 || result.ThreadsNew != 6 || result.ThreadsLinked != 3 {
				t.Fatalf("result %+v %v", result, err)
			}
			if state := loadState(t, overlay); len(state.Memory.Threads) != 6 || state.ModelErrors != 0 || state.Batches != 3 {
				t.Fatalf("state %+v", state)
			}
		})
	}
}

func TestRequestSpecificLinkFailureFallsBackAfterDropRule(t *testing.T) {
	// restart: each job runs in a new agent over a new overlay handle, as
	// after a restart: nothing kept, the state reloaded from its database.
	for _, restart := range []bool{false, true} {
		t.Run(fmt.Sprint("restart ", restart), func(t *testing.T) {
			ctx := context.Background()
			s, dir := agentStore(t)
			seedTopics(t, s, 3)
			// The consolidator would fail, with a counted error, the half
			// of the second batch that holds note 05 alone: splitting the
			// batch would drop its record.
			var mu sync.Mutex
			split := 0
			model := &limitPlanner{reply: func(_, prompt string) (string, error) {
				if strings.Contains(prompt, "note 05") && !strings.Contains(prompt, "note 04") {
					mu.Lock()
					split++
					mu.Unlock()
					return "", errors.New("model HTTP 400; reservation released")
				}
				return layeredReply(prompt), nil
			}}
			judge := topicJudge()
			answer := judge.answer
			// JEV answers the second batch's questions with probabilities
			// outside [0, 1], which fail validation: the request's own
			// failure, each time.
			second := regexp.MustCompile(`(note|update) 0[345]\b`)
			judge.answer = func(kind, newText, candidate string) float64 {
				if second.MatchString(newText) {
					return 2
				}
				return answer(kind, newText, candidate)
			}
			clock := &timelineClock{now: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)}
			kept, keptOverlay := layeredAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100}, model, judge)
			kept.now = clock.Now
			job := func() (ConsolidateResult, error, timelineState) {
				a, overlay := kept, keptOverlay
				if restart {
					var err error
					if overlay, err = OpenOverlay(filepath.Join(dir, "memory-index.db"), false); err != nil {
						t.Fatal(err)
					}
					defer overlay.Close()
					if a, err = New(s, Config{Version: 1, Policy: &ReadPolicy{Consolidation: true}, Consolidation: &ConsolidationConfig{Mode: "layered", BatchCharacters: 100}}, Models{Consolidator: model, Judge: judge}, overlay); err != nil {
						t.Fatal(err)
					}
					a.now = clock.Now
				}
				result, err := a.Consolidate(ctx, ConsolidateRequest{DeferLinks: true})
				return result, err, loadState(t, overlay)
			}
			first := clock.Now().UnixMilli()
			// Each job counts the failure toward the batch's linking rule,
			// not its extraction's drop rule; the batch stays pending with
			// the third.
			for i, wait := range []time.Duration{0, 5 * time.Minute, 5 * time.Minute} {
				clock.advance(wait)
				result, err, state := job()
				if err == nil || errors.Is(err, ErrLinkDeferred) || errors.Is(err, ErrModelUnavailable) || !strings.Contains(err.Error(), "invalid or missing decision probability") ||
					result.Folded != []int{1, 0, 0}[i] || result.Deferred != 1 || result.LinkFallbacks != 0 || result.Remaining != 6 {
					t.Fatalf("job %d: %+v %v", i, result, err)
				}
				if state.LinkErrors != i+1 || state.FirstLinkError != first || state.ModelErrors != 0 || state.FirstModelError != 0 || state.Batches != 1 || len(state.Pending) != 6 {
					t.Fatalf("job %d: state %+v", i, state)
				}
			}
			// Three counted failures, the first ten minutes ago: the linking
			// rule is due, so the second batch links without JEV from its
			// whole extraction, never split, nothing dropped; the third
			// links with JEV.
			result, err, state := job()
			if err != nil || result.Folded != 2 || result.LinkFallbacks != 1 || result.Deferred != 0 || result.Dropped != 0 || result.Remaining != 0 || result.ThreadsNew != 3 || result.ThreadsLinked != 3 {
				t.Fatalf("result %+v %v", result, err)
			}
			if state.LinkErrors != 0 || state.FirstLinkError != 0 || state.ModelErrors != 0 || state.Failed != 0 || state.Batches != 3 || len(state.Memory.Threads) != 6 || len(state.Pending) != 0 || split != 0 {
				t.Fatalf("state %+v, %d split calls", state, split)
			}
			want := map[string][]string{"kayak": {"n00", "n03", "n06"}, "taxes": {"n01", "n04", "n07"}, "garden": {"n02", "n05", "n08"}}
			if got := threadEvents(state); !reflect.DeepEqual(got, want) {
				t.Fatal("records lost", got)
			}
		})
	}
}

func TestDeferredBatchReusesExtraction(t *testing.T) {
	ctx := context.Background()
	s, dir := agentStore(t)
	seedTopics(t, s, 3)
	var calls atomic.Int32
	sent := make(chan struct{})
	model := completeFunc(func(ctx context.Context, _, prompt string) (string, Usage, error) {
		if calls.Add(1) == 3 {
			close(sent)
		}
		if strings.Contains(prompt, "note 06") {
			// The third batch's reply takes a moment: a job that cut its
			// request instead of letting it end would lose it.
			select {
			case <-ctx.Done():
				return "", Usage{InputTokens: 10, OutputTokens: 2}, ctx.Err()
			case <-time.After(20 * time.Millisecond):
			}
		}
		return layeredReply(prompt), Usage{InputTokens: 10, OutputTokens: 2}, nil
	})
	var down atomic.Bool
	down.Store(true)
	judge := topicJudge()
	judge.err = func(context.Context) error {
		if !down.Load() {
			return nil
		}
		// JEV fails once every batch was sent for extraction, so the third
		// batch's extraction is kept with the second's.
		<-sent
		return fmt.Errorf("jev: %w", ErrModelUnavailable)
	}
	a, overlay := layeredAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100}, model, judge)
	result, err := a.Consolidate(ctx, ConsolidateRequest{DeferLinks: true})
	if !errors.Is(err, ErrLinkDeferred) || result.Folded != 1 || result.Deferred != 1 || calls.Load() != 3 || result.Usage.InputTokens != 3*10+3 {
		t.Fatalf("result %+v %v", result, err)
	}
	spent := loadState(t, overlay)
	if spent.Calls != 3 || spent.Usage.InputTokens != 3*10+3 || len(a.extractions.entries) != 2 {
		t.Fatalf("state %+v, %d kept", spent, len(a.extractions.entries))
	}
	// The retry links both batches with the extractions it kept: no
	// extraction request, no extraction spending counted twice.
	down.Store(false)
	result, err = a.Consolidate(ctx, ConsolidateRequest{DeferLinks: true})
	if err != nil || result.Folded != 2 || result.Remaining != 0 || calls.Load() != 3 || result.Usage.InputTokens != 200 || result.LinkUsage == nil || result.LinkUsage.InputTokens != 200 {
		t.Fatalf("result %+v %v", result, err)
	}
	state := loadState(t, overlay)
	if state.Calls != 3 || state.Usage.InputTokens != spent.Usage.InputTokens+200 || state.Batches != 3 {
		t.Fatalf("state %+v", state)
	}
	if got, want := mustJSON(t, cloneMemory(state.Memory)), healthyMemory(t, 3); got != want {
		t.Fatalf("memory:\n%s\nwant:\n%s", got, want)
	}
	// What was linked is no longer kept.
	if len(a.extractions.entries) != 0 {
		t.Fatal("linked batches stayed kept", len(a.extractions.entries))
	}
}

func TestExtractionTimeoutCountsAfterAnsweredLink(t *testing.T) {
	ctx := context.Background()
	s, dir := agentStore(t)
	seedTopics(t, s, 2)
	var down, slow atomic.Bool
	down.Store(true)
	model := &limitPlanner{reply: func(_, prompt string) (string, error) {
		if slow.Load() {
			return "", fmt.Errorf("%w; any reservation remains held (after 3 attempts)", ErrAttemptTimeout)
		}
		return layeredReply(prompt), nil
	}}
	a, overlay := layeredAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100}, model, jevDown(&down))
	clock := &timelineClock{now: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)}
	a.now = clock.Now
	// JEV is down: the second batch's linking defers, its extraction kept.
	if _, err := a.Consolidate(ctx, ConsolidateRequest{DeferLinks: true}); !errors.Is(err, ErrLinkDeferred) {
		t.Fatal(err)
	}
	for i := 6; i < 9; i++ {
		topic := []string{"kayak", "taxes", "garden"}[i%3]
		seedNote(t, s, fmt.Sprintf("n%02d", i), fmt.Sprintf("%s note %02d %s", topic, i, strings.Repeat(".", 34-len(topic)-9)))
	}
	// JEV answers again, but every extraction times out. The next job
	// links the kept batch, JEV answering, and the third batch's
	// extraction times out: that counts, since the job had a call
	// answered, if only by JEV.
	down.Store(false)
	slow.Store(true)
	calls := plannerCalls(model)
	result, err := a.Consolidate(ctx, ConsolidateRequest{DeferLinks: true})
	if !errors.Is(err, ErrAttemptTimeout) || result.Folded != 1 || result.LinkCalls != 1 || plannerCalls(model) != calls+1 {
		t.Fatalf("result %+v %v", result, err)
	}
	if state := loadState(t, overlay); state.ModelErrors != 1 || !slices.Equal(ids(state.Pending), []string{"n06", "n07", "n08"}) {
		t.Fatalf("state %+v", state)
	}
	// A job whose only call is that extraction has nothing answered: its
	// timeout is an outage.
	if _, err := a.Consolidate(ctx, ConsolidateRequest{DeferLinks: true}); !errors.Is(err, ErrAttemptTimeout) {
		t.Fatal(err)
	}
	if state := loadState(t, overlay); state.ModelErrors != 1 {
		t.Fatalf("state %+v", state)
	}
}

func TestKeptExtractionsServeOnlyTheirAgentsDeferringJobs(t *testing.T) {
	ctx := context.Background()
	// deferred is an agent whose job deferred the second batch, and JEV
	// answering again.
	deferred := func(t *testing.T) (*Agent, *records.Store, *Overlay, *limitPlanner, *linkJudge) {
		s, dir := agentStore(t)
		seedTopics(t, s, 3)
		var down atomic.Bool
		down.Store(true)
		model, judge := topicModel(), jevDown(&down)
		a, overlay := layeredAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100}, model, judge)
		if _, err := a.Consolidate(ctx, ConsolidateRequest{DeferLinks: true}); !errors.Is(err, ErrLinkDeferred) || len(a.extractions.entries) == 0 {
			t.Fatal(err, len(a.extractions.entries))
		}
		down.Store(false)
		return a, s, overlay, model, judge
	}
	t.Run("another agent extracts again", func(t *testing.T) {
		_, s, overlay, model, judge := deferred(t)
		calls := plannerCalls(model)
		b, err := New(s, Config{Version: 1, Policy: &ReadPolicy{Consolidation: true}, Consolidation: &ConsolidationConfig{Mode: "layered", BatchCharacters: 100}}, Models{Consolidator: model, Judge: judge}, overlay)
		if err != nil {
			t.Fatal(err)
		}
		if result, err := b.Consolidate(ctx, ConsolidateRequest{DeferLinks: true}); err != nil || result.Folded != 2 || plannerCalls(model) != calls+2 {
			t.Fatal(result, err, plannerCalls(model)-calls)
		}
	})
	t.Run("a job without deferLinks extracts again", func(t *testing.T) {
		a, _, _, model, _ := deferred(t)
		calls := plannerCalls(model)
		if result, err := a.Consolidate(ctx, ConsolidateRequest{}); err != nil || result.Folded != 2 || plannerCalls(model) != calls+2 {
			t.Fatal(result, err, plannerCalls(model)-calls)
		}
	})
}

func TestExtractionCacheKeepsTheLatest(t *testing.T) {
	var c extractionCache
	if _, ok := c.take("0"); ok {
		t.Fatal("an empty cache kept something")
	}
	for i := range extractionCacheSize + 1 {
		c.put(strconv.Itoa(i), extraction{state: timelineState{Batches: i}})
	}
	if len(c.entries) != extractionCacheSize || c.order.Len() != extractionCacheSize {
		t.Fatal("bound", len(c.entries), c.order.Len())
	}
	if _, ok := c.take("0"); ok {
		t.Fatal("the oldest was kept past the bound")
	}
	if x, ok := c.take("1"); !ok || x.state.Batches != 1 {
		t.Fatal(x, ok)
	}
	if _, ok := c.take("1"); ok {
		t.Fatal("taken twice")
	}
	// Storing a kept key again replaces it and makes it the latest: the
	// oldest left, 3, goes first.
	c.put("2", extraction{state: timelineState{Batches: 20}})
	c.put("a", extraction{})
	c.put("b", extraction{})
	if _, ok := c.take("3"); ok {
		t.Fatal("3 was kept")
	}
	if x, ok := c.take("2"); !ok || x.state.Batches != 20 {
		t.Fatal(x, ok)
	}
}

func TestExtractionRunHaltKeepsWhatRuns(t *testing.T) {
	started, release := make(chan int, 5), make(chan struct{})
	run := startExtractions(context.Background(), 5, 1, func(ctx context.Context, i int, x *extraction) error {
		started <- i
		<-release
		return nil
	})
	if i := <-started; i != 0 {
		t.Fatal(i)
	}
	// Halted while the first batch is extracted: that extraction ends and
	// is kept, and no other batch is taken.
	run.halt()
	close(release)
	if value := run.stop(); value != nil {
		t.Fatal(value)
	}
	if len(started) != 0 || !run.extracted(0) || run.extracted(1) {
		t.Fatal("batches taken after the halt", len(started), run.extracted(0), run.extracted(1))
	}
}

func TestExtractionKey(t *testing.T) {
	batch := []pendingRecord{{ID: "a", Header: "h", Text: "x"}, {ID: "b", Header: "h", Text: "y"}}
	key := extractionKey(false, batch)
	if extractionKey(false, slices.Clone(batch)) != key {
		t.Fatal("the same batch has two keys")
	}
	changed := func(edit func(*pendingRecord)) []pendingRecord {
		out := slices.Clone(batch)
		edit(&out[0])
		return out
	}
	for name, other := range map[string]string{
		"drop mode": extractionKey(true, batch),
		"an id":     extractionKey(false, changed(func(r *pendingRecord) { r.ID = "c" })),
		"a header":  extractionKey(false, changed(func(r *pendingRecord) { r.Header = "g" })),
		"a text":    extractionKey(false, changed(func(r *pendingRecord) { r.Text = "z" })),
		"the order": extractionKey(false, []pendingRecord{batch[1], batch[0]}),
		"a record":  extractionKey(false, batch[:1]),
	} {
		if other == key {
			t.Fatal(name, "does not change the key")
		}
	}
}

func TestProviderWideTimeoutDefersNotDrops(t *testing.T) {
	for _, deferLinks := range []bool{true, false} {
		t.Run(fmt.Sprint("deferLinks ", deferLinks), func(t *testing.T) {
			ctx := context.Background()
			s, dir := agentStore(t)
			seedTopics(t, s, 3)
			var slow atomic.Bool
			slow.Store(true)
			model := &limitPlanner{reply: func(_, prompt string) (string, error) {
				if slow.Load() {
					return "", fmt.Errorf("%w; any reservation remains held (after 3 attempts)", ErrAttemptTimeout)
				}
				return layeredReply(prompt), nil
			}}
			a, overlay := layeredAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100}, model, topicJudge())
			clock := &timelineClock{now: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)}
			a.now = clock.Now
			// For fifteen minutes every extraction times out: no call of a
			// job is answered, so nothing counts against the head batch and
			// nothing is dropped; the batches stay pending.
			for minute := 0; minute <= 15; minute += 5 {
				result, err := a.Consolidate(ctx, ConsolidateRequest{DeferLinks: deferLinks})
				if !errors.Is(err, ErrAttemptTimeout) || result.Folded != 0 || result.Dropped != 0 || result.Remaining != 9 {
					t.Fatalf("minute %d: %+v %v", minute, result, err)
				}
				if state := loadState(t, overlay); state.ModelErrors != 0 || state.Failed != 0 || len(state.Pending) != 9 {
					t.Fatalf("minute %d: state %+v", minute, state)
				}
				clock.advance(5 * time.Minute)
			}
			slow.Store(false)
			result, err := a.Consolidate(ctx, ConsolidateRequest{DeferLinks: deferLinks})
			if err != nil || result.Folded != 3 || result.Dropped != 0 || result.Remaining != 0 {
				t.Fatalf("result %+v %v", result, err)
			}
			if got, want := mustJSON(t, cloneMemory(loadState(t, overlay).Memory)), healthyMemory(t, 3); got != want {
				t.Fatalf("memory:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

func TestLoneExtractionTimeoutCounts(t *testing.T) {
	for _, deferLinks := range []bool{true, false} {
		t.Run(fmt.Sprint("deferLinks ", deferLinks), func(t *testing.T) {
			ctx := context.Background()
			s, dir := agentStore(t)
			seedTopics(t, s, 3)
			timeout := fmt.Errorf("%w; any reservation remains held (after 3 attempts)", ErrAttemptTimeout)
			third := make(chan struct{}, 8)
			// Every extraction that reads note 05 times out, once the third
			// batch's was sent: in every job a call of another batch is
			// answered.
			model := &limitPlanner{reply: func(_, prompt string) (string, error) {
				switch {
				case strings.Contains(prompt, "note 06"):
					third <- struct{}{}
				case strings.Contains(prompt, "note 05"):
					<-third
					return "", timeout
				}
				return layeredReply(prompt), nil
			}}
			a, overlay := layeredAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100}, model, topicJudge())
			clock := &timelineClock{now: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)}
			a.now = clock.Now
			first := clock.Now().UnixMilli()
			for i, wait := range []time.Duration{0, 5 * time.Minute, 5 * time.Minute} {
				clock.advance(wait)
				result, err := a.Consolidate(ctx, ConsolidateRequest{DeferLinks: deferLinks})
				if !errors.Is(err, ErrAttemptTimeout) || result.Dropped != 0 || result.Deferred != 0 || result.Remaining != 6 {
					t.Fatalf("job %d: %+v %v", i, result, err)
				}
				if state := loadState(t, overlay); state.ModelErrors != i+1 || state.FirstModelError != first || len(state.Pending) != 6 {
					t.Fatalf("job %d: state %+v", i, state)
				}
			}
			// The batch alone keeps timing out: in drop mode it splits
			// once, and the half that times out again is dropped.
			result, err := a.Consolidate(ctx, ConsolidateRequest{DeferLinks: deferLinks})
			if err != nil || result.Folded != 2 || result.Dropped != 1 || len(result.Drops) != 1 || !strings.Contains(result.Drops[0].Reason, "model error after 3 failed jobs over 10m0s") || !strings.Contains(result.Drops[0].Reason, "attempt timed out") {
				t.Fatalf("result %+v %v", result, err)
			}
			state := loadState(t, overlay)
			want := map[string][]string{"kayak": {"n00", "n03", "n06"}, "taxes": {"n01", "n04", "n07"}, "garden": {"n02", "n08"}}
			if got := threadEvents(state); !reflect.DeepEqual(got, want) || state.Failed != 1 || state.ModelErrors != 0 || len(state.Pending) != 0 {
				t.Fatalf("state %v %+v", got, state)
			}
		})
	}
}
