package memoryagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mnemon-dev/mnemon/internal/records"
)

var insightLinePattern = regexp.MustCompile(`(?m)^(E\d+|V\d+\.\d+|D\d+) \(([^)]*)\) (.*)$`)

// insightReply is L2 over the lines it is shown: for each topic (kayak,
// taxes, garden) with two items or more, "<topic> keeps changing." citing
// them all.
func insightReply(prompt string) string {
	byTopic, order := map[string][]string{}, []string{}
	for _, m := range insightLinePattern.FindAllStringSubmatch(prompt, -1) {
		topic := "other"
		for _, name := range []string{"kayak", "taxes", "garden"} {
			if strings.Contains(m[3], name) {
				topic = name
				break
			}
		}
		if byTopic[topic] == nil {
			order = append(order, topic)
		}
		byTopic[topic] = append(byTopic[topic], m[1])
	}
	insights := []map[string]any{}
	for _, topic := range order {
		if ids := byTopic[topic]; len(ids) >= 2 {
			insights = append(insights, map[string]any{"text": topic + " keeps changing.", "date": "2024/03/16", "items": ids})
		}
	}
	raw, _ := json.Marshal(map[string]any{"insights": insights})
	return string(raw)
}

// insightModel is L0 over topic notes (layeredReply) and L2 over their
// items (insightReply); l2, when set, answers the L2 calls instead.
func insightModel(l2 func(prompt string) (string, error)) *limitPlanner {
	return &limitPlanner{reply: func(system, prompt string) (string, error) {
		if system == insightSystem {
			if l2 != nil {
				return l2(prompt)
			}
			return insightReply(prompt), nil
		}
		return layeredReply(prompt), nil
	}}
}

// insightJudge links threads of the same topic, as topicJudge, and finds
// that insights about the same topic state the same conclusion.
func insightJudge() *linkJudge {
	judge := topicJudge()
	threads := judge.answer
	judge.answer = func(kind, newText, candidate string) float64 {
		if kind == "insight" {
			if strings.Fields(newText)[0] == strings.Fields(candidate)[0] {
				return 0.9
			}
			return 0.1
		}
		return threads(kind, newText, candidate)
	}
	return judge
}

// l2Calls is the prompts of the L2 calls a model received, and the L0
// calls in order.
func l2Calls(model *limitPlanner) (l2, l0 []string) {
	model.mu.Lock()
	defer model.mu.Unlock()
	for _, call := range model.calls {
		if call.system == insightSystem {
			l2 = append(l2, call.prompt)
		} else {
			l0 = append(l0, call.prompt)
		}
	}
	return l2, l0
}

// readOnce checks that the L2 calls read the items once each: their lines
// are those of the items, none twice (calls run in parallel, so in any
// order; each call's lines are consecutive items).
func readOnce(t *testing.T, l2 []string, items []insightItem) {
	t.Helper()
	read := []string{}
	for _, prompt := range l2 {
		read = append(read, strings.Split(prompt, "\n")...)
	}
	want := itemLines(items)
	slices.Sort(read)
	slices.Sort(want)
	if !slices.Equal(read, want) {
		t.Fatalf("L2 read:\n%s\nthe items:\n%s", strings.Join(read, "\n"), strings.Join(want, "\n"))
	}
}

func insightAgent(t *testing.T, s *records.Store, dir string, settings ConsolidationConfig, model Completer, judge Decider) (*Agent, *Overlay) {
	t.Helper()
	settings.Insights = true
	return layeredAgent(t, s, dir, settings, model, judge)
}

func TestInsightsConfig(t *testing.T) {
	s, _ := agentStore(t)
	models := Models{Planner: &limitPlanner{reply: func(string, string) (string, error) { return "{}", nil }}, Judge: decisionFunc(fixtureJudge)}
	a, err := New(s, Config{Version: 1, Policy: &ReadPolicy{}, Consolidation: &ConsolidationConfig{Mode: "layered", Insights: true}}, models, nil)
	if err != nil || !a.config.Consolidation.Insights || !a.insightsOn() {
		t.Fatal(err)
	}
	for _, bad := range []ConsolidationConfig{{Mode: "timelines", Insights: true}, {Insights: true}} {
		if _, err := New(s, Config{Version: 1, Policy: &ReadPolicy{}, Consolidation: &bad}, models, nil); err == nil || !strings.Contains(err.Error(), "insights requires consolidation mode \"layered\"") {
			t.Fatal(bad, err)
		}
	}
	// Off, the setting stays out of the config's JSON, so the policy hash
	// is the same.
	raw, _ := json.Marshal(ConsolidationConfig{Mode: "layered", LinkCandidates: 8})
	if string(raw) != `{"mode":"layered","linkCandidates":8}` {
		t.Fatal(string(raw))
	}
	if raw, _ = json.Marshal(ConsolidationConfig{Mode: "layered", Insights: true}); string(raw) != `{"mode":"layered","insights":true}` {
		t.Fatal(string(raw))
	}
	if off, _ := New(s, Config{Version: 1, Policy: &ReadPolicy{}, Consolidation: &ConsolidationConfig{Mode: "layered"}}, models, nil); off.insightsOn() {
		t.Fatal("insights on by default")
	}
}

func TestConsolidateInsightsReadsEachItemOnce(t *testing.T) {
	ctx := context.Background()
	s, dir := agentStore(t)
	seedTopics(t, s, 6)
	model, judge := insightModel(nil), insightJudge()
	a, overlay := insightAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 300}, model, judge)
	// A held job folds the full record batches, then the full L2 batches of
	// what they consolidated; the short rest of the stream waits.
	result, err := a.Consolidate(ctx, ConsolidateRequest{Hold: true})
	if err != nil || result.Folded != 2 || result.InsightBatches == 0 {
		t.Fatalf("%+v %v", result, err)
	}
	state := loadState(t, overlay)
	tail := insightStream(state.Memory, *state.InsightRead)
	l2, l0 := l2Calls(model)
	if len(tail) == 0 || jsLen(insightPrompt(tail)) >= 300 || len(state.Pending) != 0 || result.Remaining != len(tail) || len(l2) != result.InsightBatches || len(l0) != 2 {
		t.Fatalf("tail %d (%d units), remaining %d, %d L2 calls", len(tail), jsLen(insightPrompt(tail)), result.Remaining, len(l2))
	}
	for _, prompt := range l2 {
		if jsLen(prompt) < 300 {
			t.Fatal("a held job folded a short L2 batch", prompt)
		}
	}
	// Each batch was linked and saved: the insights are the state's, every
	// one counted new or merged.
	if len(state.Memory.Insights) == 0 || result.InsightsNew != len(state.Memory.Insights) || result.InsightsMerged == 0 || result.InsightLinkCalls == 0 {
		t.Fatalf("%+v %+v", result, state.Memory.Insights)
	}
	// A job without hold, with no record left, folds the tail.
	if result, err = a.Consolidate(ctx, ConsolidateRequest{}); err != nil || result.Folded != 0 || result.InsightBatches != 1 || result.Remaining != 0 {
		t.Fatalf("%+v %v", result, err)
	}
	state = loadState(t, overlay)
	if len(insightStream(state.Memory, *state.InsightRead)) != 0 {
		t.Fatal("the tail was not read")
	}
	// Every item was read, once.
	l2, _ = l2Calls(model)
	readOnce(t, l2, insightStream(state.Memory, insightRead{}))
	// A job finds nothing new to read.
	if result, err = a.Consolidate(ctx, ConsolidateRequest{}); err != nil || result.InsightBatches != 0 {
		t.Fatal(result, err)
	}
	if again, _ := l2Calls(model); len(again) != len(l2) {
		t.Fatal("read again")
	}
	// New records: L2 reads only the items they added, after L0 and L1.
	before := state.Memory
	seedNote(t, s, "n18", "kayak note 18 "+strings.Repeat(".", 90))
	seedNote(t, s, "n19", "taxes note 19 "+strings.Repeat(".", 90))
	seedNote(t, s, "n20", "garden note 20 "+strings.Repeat(".", 90))
	if result, err = a.Consolidate(ctx, ConsolidateRequest{}); err != nil || result.Folded != 1 || result.InsightBatches == 0 || result.Remaining != 0 {
		t.Fatal(result, err)
	}
	state = loadState(t, overlay)
	all, _ := l2Calls(model)
	fresh := insightStream(state.Memory, insightRead{Events: len(before.Events), Values: map[string]int{"kayak · status": 6, "taxes · status": 6, "garden · status": 6}})
	readOnce(t, all[len(l2):], fresh)
	if len(fresh) != 6 || fresh[0].id != "E19" || fresh[3].id != "V1.7" {
		t.Fatal(itemLines(fresh))
	}
	// A job that leaves a record pending (it read new ones, so it does not
	// fold their short rest) leaves L2's short rest too: the three events
	// fill an L2 batch, the three values wait.
	for i, topic := range []string{"kayak", "taxes", "garden", "kayak"} {
		seedNote(t, s, fmt.Sprintf("n%d", 21+i), fmt.Sprintf("%s note %d %s", topic, 21+i, strings.Repeat(".", 90)))
	}
	if result, err = a.Consolidate(ctx, ConsolidateRequest{}); err != nil || result.Folded != 1 || result.InsightBatches != 1 || result.Remaining != 1+3 {
		t.Fatalf("%+v %v", result, err)
	}
	state = loadState(t, overlay)
	if rest := insightStream(state.Memory, *state.InsightRead); len(state.Pending) != 1 || len(rest) != 3 || rest[0].id != "V1.8" {
		t.Fatal(len(state.Pending), itemLines(rest))
	}
	// The next job folds the pending record, then the rest of L2's stream.
	if result, err = a.Consolidate(ctx, ConsolidateRequest{}); err != nil || result.Folded != 1 || result.InsightBatches != 1 || result.Remaining != 0 {
		t.Fatalf("%+v %v", result, err)
	}
	all, _ = l2Calls(model)
	readOnce(t, all, insightStream(loadState(t, overlay).Memory, insightRead{}))
}

func TestConsolidateInsightsOverAConsolidatedStore(t *testing.T) {
	ctx := context.Background()
	s, dir := agentStore(t)
	seedTopics(t, s, 6)
	model, judge := insightModel(nil), insightJudge()
	// Consolidated without insights: the state keeps no read position and
	// no insights, and L2 is never asked.
	off, overlay := layeredAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 300}, model, judge)
	if result, err := off.Consolidate(ctx, ConsolidateRequest{}); err != nil || result.Folded != 2 || result.InsightBatches != 0 {
		t.Fatal(result, err)
	}
	if l2, _ := l2Calls(model); len(l2) != 0 {
		t.Fatal(l2)
	}
	raw := iddStatePayload(t, overlay)
	if strings.Contains(raw, "insight") {
		t.Fatal(raw)
	}
	// With insights, a job that reads no record runs L2 over every item.
	on, err := New(s, Config{Version: 1, Policy: &ReadPolicy{Consolidation: true}, Consolidation: &ConsolidationConfig{Mode: "layered", BatchCharacters: 300, Insights: true}}, Models{Consolidator: model, Judge: judge}, overlay)
	if err != nil {
		t.Fatal(err)
	}
	result, err := on.Consolidate(ctx, ConsolidateRequest{})
	if err != nil || result.Visited != 0 || result.Folded != 0 || result.InsightBatches == 0 || result.Remaining != 0 || result.Added != 0 {
		t.Fatalf("%+v %v", result, err)
	}
	state := loadState(t, overlay)
	l2, l0 := l2Calls(model)
	readOnce(t, l2, insightStream(state.Memory, insightRead{}))
	if len(l0) != 2 || len(state.Memory.Insights) == 0 {
		t.Fatal(len(l0), state.Memory.Insights)
	}
	// The read position is kept, and nothing else of the state changed.
	if state.InsightRead == nil || state.InsightRead.Events != len(state.Memory.Events) {
		t.Fatalf("%+v", state.InsightRead)
	}
	var plain timelineState
	json.Unmarshal([]byte(raw), &plain)
	state.Memory.Insights, state.InsightRead = nil, nil
	plain.Usage, state.Usage, plain.Calls, state.Calls = Usage{}, Usage{}, 0, 0
	if a, b := mustJSON(t, plain), mustJSON(t, state); a != b {
		t.Fatalf("L2 changed more than its own state:\n%s\n%s", a, b)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// iddStatePayload is the stored state's JSON, as saved.
func iddStatePayload(t *testing.T, overlay *Overlay) string {
	t.Helper()
	var raw string
	if err := overlay.db.QueryRow(`SELECT payload FROM timelines WHERE scope=?`, scopeKey(records.Scope{})).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestConsolidateInsightsMaxBatches(t *testing.T) {
	ctx := context.Background()
	s, dir := agentStore(t)
	seedTopics(t, s, 6)
	model, judge := insightModel(nil), insightJudge()
	a, overlay := insightAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 150, MaxBatches: 2}, model, judge)
	// Each job folds at most two record batches, then at most two L2
	// batches; remaining counts the records not folded (pending or not yet
	// read) and the items L2 has not read.
	for jobs := 1; ; jobs++ {
		before, _ := l2Calls(model)
		result, err := a.Consolidate(ctx, ConsolidateRequest{})
		if err != nil || result.Folded > 2 || result.InsightBatches > 2 {
			t.Fatal(result, err)
		}
		after, _ := l2Calls(model)
		state := loadState(t, overlay)
		_, heads, err := s.Heads(ctx, "", records.Scope{})
		if err != nil {
			t.Fatal(err)
		}
		unread := 0
		for _, head := range heads {
			if head.Version > state.Watermark {
				unread++
			}
		}
		if want := len(state.Pending) + unread + len(insightStream(state.Memory, *state.InsightRead)); result.Remaining != want {
			t.Fatal(result.Remaining, want)
		}
		// While records are left to fold, L2 reads full batches only.
		for _, prompt := range after[len(before):] {
			if len(state.Pending)+unread > 0 && jsLen(prompt) < 150 {
				t.Fatal("L2 read its short rest before the records were folded:", prompt)
			}
		}
		if result.Remaining == 0 {
			break
		}
		if jobs == 30 {
			t.Fatal("remaining never reached 0")
		}
	}
	state := loadState(t, overlay)
	l2, _ := l2Calls(model)
	readOnce(t, l2, insightStream(state.Memory, insightRead{}))
}

func TestConsolidateInsightsParallelMatchesSerial(t *testing.T) {
	run := func(parallel int) string {
		s, dir := agentStore(t)
		seedTopics(t, s, 6)
		model := insightModel(func(prompt string) (string, error) {
			// Later L2 batches reply first when they can.
			if parallel > 1 && strings.HasPrefix(prompt, "E1 ") {
				time.Sleep(20 * time.Millisecond)
			}
			return insightReply(prompt), nil
		})
		a, overlay := insightAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 150, Parallel: parallel}, model, insightJudge())
		if result, err := a.Consolidate(context.Background(), ConsolidateRequest{}); err != nil || result.InsightBatches < 3 {
			t.Fatal(result, err)
		}
		return iddStatePayload(t, overlay)
	}
	if serial, parallel := run(1), run(4); serial != parallel {
		t.Fatalf("parallel L2 changed the state:\n%s\n%s", serial, parallel)
	}
}

func TestConsolidateInsightsRetriesSplitsAndDrops(t *testing.T) {
	ctx := context.Background()
	t.Run("a reply that does not parse is asked twice, then in halves", func(t *testing.T) {
		s, dir := agentStore(t)
		seedTopics(t, s, 2)
		// Lines with V1.1 never parse; everything else does.
		model := insightModel(func(prompt string) (string, error) {
			if strings.Contains(prompt, "V1.1 ") {
				return "not json", nil
			}
			return insightReply(prompt), nil
		})
		a, overlay := insightAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 2000}, model, insightJudge())
		// The first job reads the records; the next folds them as one batch,
		// then their items as one L2 batch.
		result, err := a.Consolidate(ctx, ConsolidateRequest{})
		if err != nil || result.Folded != 0 || result.InsightBatches != 0 || result.Remaining != 6 {
			t.Fatal(result, err)
		}
		result, err = a.Consolidate(ctx, ConsolidateRequest{})
		if err != nil || result.Folded != 1 || result.InsightBatches != 1 || result.Remaining != 0 {
			t.Fatal(result, err)
		}
		l2, _ := l2Calls(model)
		state := loadState(t, overlay)
		stream := insightStream(state.Memory, insightRead{})
		half := (len(stream) + 1) / 2
		at := slices.IndexFunc(stream, func(item insightItem) bool { return item.id == "V1.1" })
		// The batch twice, then each half twice at most: the half without
		// V1.1 parses at once, the other is dropped.
		want := []string{insightPrompt(stream), insightPrompt(stream), insightPrompt(stream[:half]), insightPrompt(stream[:half]), insightPrompt(stream[half:])}
		dropped := len(stream) - half
		if at >= half {
			want = []string{insightPrompt(stream), insightPrompt(stream), insightPrompt(stream[:half]), insightPrompt(stream[half:]), insightPrompt(stream[half:])}
			dropped = half
		}
		if !slices.Equal(l2, want) || result.InsightsDropped != dropped || len(insightStream(state.Memory, *state.InsightRead)) != 0 {
			t.Fatalf("%d calls, %d dropped (want %d)", len(l2), result.InsightsDropped, dropped)
		}
	})
	t.Run("a request too large splits down to single items", func(t *testing.T) {
		s, dir := agentStore(t)
		seedTopics(t, s, 2)
		long := ""
		model := insightModel(func(prompt string) (string, error) {
			if strings.Count(prompt, "\n") >= 2 || strings.Contains(prompt, long) && long != "" {
				return "", fmt.Errorf("%w: test", ErrRequestTooLarge)
			}
			return insightReply(prompt), nil
		})
		a, overlay := insightAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 2000}, model, insightJudge())
		long = "V1.2 "
		if _, err := a.Consolidate(ctx, ConsolidateRequest{}); err != nil {
			t.Fatal(err)
		}
		result, err := a.Consolidate(ctx, ConsolidateRequest{})
		if err != nil || result.InsightBatches != 1 || result.InsightsDropped != 1 {
			t.Fatal(result, err)
		}
		// Requests of three lines or more were split down to pairs; the
		// single item V1.2 was still too large and was dropped; the others
		// were read.
		state := loadState(t, overlay)
		l2, _ := l2Calls(model)
		read := []string{}
		for _, prompt := range l2 {
			if strings.Count(prompt, "\n") < 2 && !strings.Contains(prompt, "V1.2 ") {
				read = append(read, prompt)
			}
		}
		stream := insightStream(state.Memory, insightRead{})
		without := slices.DeleteFunc(slices.Clone(stream), func(item insightItem) bool { return item.id == "V1.2" })
		if len(insightStream(state.Memory, *state.InsightRead)) != 0 || strings.Join(read, "\n") != insightPrompt(without) {
			t.Fatalf("%q", read)
		}
	})
}

func TestConsolidateInsightsModelErrors(t *testing.T) {
	ctx := context.Background()
	s, dir := agentStore(t)
	seedTopics(t, s, 6)
	broken := errors.New("model HTTP 400; reservation released")
	failing := true
	model := insightModel(func(prompt string) (string, error) {
		if failing && strings.Contains(prompt, "E7 ") {
			return "", broken
		}
		return insightReply(prompt), nil
	})
	a, overlay := insightAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 200, Parallel: 1}, model, insightJudge())
	clock := &timelineClock{now: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
	a.now = clock.Now
	// The L2 batch with E7 fails: the batches before it stay read, it and
	// the later ones stay unread, L0's work is kept, and the job ends with
	// the error, counted for L2 alone.
	result, err := a.Consolidate(ctx, ConsolidateRequest{})
	if !errors.Is(err, broken) || result.Folded == 0 || result.InsightBatches == 0 {
		t.Fatalf("%+v %v", result, err)
	}
	state := loadState(t, overlay)
	stream := insightStream(state.Memory, *state.InsightRead)
	if len(state.Pending) != 0 || state.ModelErrors != 0 || state.InsightRead.ModelErrors != 1 || state.InsightRead.FirstModelError == 0 || len(stream) == 0 || !strings.Contains(insightPrompt(stream), "E7 ") || result.Remaining != len(stream) {
		t.Fatalf("%+v %+v", state.InsightRead, result)
	}
	// After three such jobs, the first ten minutes before, the head L2 batch
	// splits once and the half that fails again is dropped.
	for range 2 {
		clock.advance(6 * time.Minute)
		if _, err := a.Consolidate(ctx, ConsolidateRequest{}); !errors.Is(err, broken) {
			t.Fatal(err)
		}
	}
	if state = loadState(t, overlay); state.InsightRead.ModelErrors != 3 {
		t.Fatal(state.InsightRead)
	}
	head := insightBatches(insightStream(state.Memory, *state.InsightRead), 200, 1, true)[0]
	half := (len(head) + 1) / 2
	failed := head[:half]
	if !strings.Contains(insightPrompt(failed), "E7 ") {
		failed = head[half:]
	}
	clock.advance(time.Minute)
	result, err = a.Consolidate(ctx, ConsolidateRequest{})
	if err != nil || result.InsightsDropped != len(failed) || result.Remaining != 0 {
		t.Fatalf("%+v %v", result, err)
	}
	if state = loadState(t, overlay); state.InsightRead.ModelErrors != 0 || state.InsightRead.FirstModelError != 0 {
		t.Fatal(state.InsightRead)
	}
	// A spent budget or an unavailable provider is not counted.
	failing = false
	seedNote(t, s, "n18", "kayak note 18 "+strings.Repeat(".", 190))
	unavailable := insightModel(func(string) (string, error) { return "", fmt.Errorf("%w: model HTTP 429", ErrModelUnavailable) })
	b, err := New(s, Config{Version: 1, Policy: &ReadPolicy{Consolidation: true}, Consolidation: &ConsolidationConfig{Mode: "layered", BatchCharacters: 200, Insights: true}}, Models{Consolidator: unavailable, Judge: insightJudge()}, overlay)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Consolidate(ctx, ConsolidateRequest{}); !errors.Is(err, ErrModelUnavailable) {
		t.Fatal(err)
	}
	if state = loadState(t, overlay); state.InsightRead.ModelErrors != 0 || len(state.Pending) != 0 {
		t.Fatal(state.InsightRead, len(state.Pending))
	}
}

func TestConsolidateInsightsLeaveTheEdgesAlone(t *testing.T) {
	ctx := context.Background()
	s, dir := agentStore(t)
	seedTopics(t, s, 3)
	model, judge := insightModel(nil), insightJudge()
	off, overlay := layeredAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100, NativeEdges: true}, model, judge)
	if result, err := off.Consolidate(ctx, ConsolidateRequest{}); err != nil || result.EdgesPending || result.EdgeRecords == 0 {
		t.Fatal(result, err)
	}
	built := loadState(t, overlay).EdgesMemory
	on, err := New(s, Config{Version: 1, Policy: &ReadPolicy{Consolidation: true}, Consolidation: &ConsolidationConfig{Mode: "layered", BatchCharacters: 100, NativeEdges: true, Insights: true}}, Models{Consolidator: model, Judge: judge}, overlay)
	if err != nil {
		t.Fatal(err)
	}
	// L2 changes the insights only: the records' edges stay built.
	result, err := on.Consolidate(ctx, ConsolidateRequest{})
	if err != nil || result.InsightBatches == 0 || result.EdgesPending || result.EdgeRecords != 0 {
		t.Fatalf("%+v %v", result, err)
	}
	if state := loadState(t, overlay); state.EdgesMemory != built || len(state.Memory.Insights) == 0 {
		t.Fatal("the edges were rebuilt for the insights")
	}
	if !reflect.DeepEqual(edgesDigest(loadState(t, overlay).Memory), built) {
		t.Fatal("the insights changed the edges' digest")
	}
}

func TestConsolidateInsightsWaitForL0(t *testing.T) {
	ctx := context.Background()
	s, dir := agentStore(t)
	seedTopics(t, s, 6)
	broken := errors.New("model HTTP 400; reservation released")
	model := &limitPlanner{reply: func(system, prompt string) (string, error) {
		if system == insightSystem {
			return insightReply(prompt), nil
		}
		if strings.Contains(prompt, "note 12") {
			return "", broken
		}
		return layeredReply(prompt), nil
	}}
	a, overlay := insightAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 300, Parallel: 1}, model, insightJudge())
	// A job whose L0 ends in a model error does not run L2.
	result, err := a.Consolidate(ctx, ConsolidateRequest{})
	if !errors.Is(err, broken) || result.Folded != 1 || result.InsightBatches != 0 {
		t.Fatalf("%+v %v", result, err)
	}
	if l2, _ := l2Calls(model); len(l2) != 0 {
		t.Fatal(l2)
	}
	state := loadState(t, overlay)
	if state.InsightRead != nil || state.ModelErrors != 1 || result.Remaining != len(state.Pending)+insightsUnread(state) || insightsUnread(state) == 0 {
		t.Fatalf("%+v %+v", state.InsightRead, result)
	}
}

func TestConsolidateInsightsLinkWithoutJudgement(t *testing.T) {
	ctx := context.Background()
	s, dir := agentStore(t)
	seedTopics(t, s, 6)
	judge := insightJudge()
	down := decisionFunc(func(ctx context.Context, state any, questions map[string]Question) (map[string]float64, Usage, error) {
		for _, q := range questions {
			if strings.HasPrefix(q.Instructions, "Does the new insight") {
				return nil, Usage{InputTokens: 5}, fmt.Errorf("jev: %w", ErrBudgetCap)
			}
		}
		return judge.Decide(ctx, state, questions)
	})
	model := insightModel(nil)
	a, overlay := insightAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 300, Parallel: 1}, model, down)
	// L2 links without JEV: every insight of a batch whose linking JEV
	// failed is added; the batch counts as a fallback, and the job goes on.
	result, err := a.Consolidate(ctx, ConsolidateRequest{})
	if err != nil || result.InsightBatches < 2 || result.InsightFallbacks == 0 || result.InsightFallbacks >= result.InsightBatches || result.InsightsMerged != 0 || result.Remaining != 0 {
		t.Fatalf("%+v %v", result, err)
	}
	if state := loadState(t, overlay); len(state.Memory.Insights) != result.InsightsNew || result.InsightLinkCalls != result.InsightFallbacks {
		t.Fatalf("%d insights, %+v", len(state.Memory.Insights), result)
	}
}

func TestConsolidateInsightsDropModeDropsAWholeHalf(t *testing.T) {
	ctx := context.Background()
	s, dir := agentStore(t)
	seedTopics(t, s, 6)
	broken := errors.New("model HTTP 400; reservation released")
	// A request of more than two lines is too large; one with E11 fails;
	// a pair concludes something.
	model := insightModel(func(prompt string) (string, error) {
		if strings.Count(prompt, "\n") > 1 {
			return "", fmt.Errorf("%w: test", ErrRequestTooLarge)
		}
		if strings.Contains(prompt, "E11 ") {
			return "", broken
		}
		ids := []string{}
		for _, m := range insightLinePattern.FindAllStringSubmatch(prompt, -1) {
			ids = append(ids, m[1])
		}
		if len(ids) < 2 {
			return `{"insights":[]}`, nil
		}
		return `{"insights":[{"text":"` + ids[0] + ` and ` + ids[1] + ` go together.","items":["` + ids[0] + `","` + ids[1] + `"]}]}`, nil
	})
	a, overlay := insightAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 700, Parallel: 1, MaxBatches: 1}, model, insightJudge())
	clock := &timelineClock{now: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
	a.now = clock.Now
	if _, err := a.Consolidate(ctx, ConsolidateRequest{}); err != nil {
		t.Fatal(err)
	}
	// The head L2 batch holds E1–E12: its pairs before E11 conclude, then
	// E11 fails; three such jobs over ten minutes put it in drop mode.
	for range 3 {
		if _, err := a.Consolidate(ctx, ConsolidateRequest{}); !errors.Is(err, broken) {
			t.Fatal(err)
		}
		clock.advance(6 * time.Minute)
	}
	state := loadState(t, overlay)
	head := insightBatches(insightStream(state.Memory, *state.InsightRead), 700, 1, true)[0]
	if state.InsightRead.ModelErrors != 3 || len(state.Memory.Insights) != 0 || head[0].id != "E1" || len(head) < 12 {
		t.Fatalf("%+v %d %v", state.InsightRead, len(state.Memory.Insights), itemLines(head))
	}
	half := (len(head) + 1) / 2
	failed := head[half:]
	if !strings.Contains(insightPrompt(failed), "E11 ") || failed[0].id != "E7" {
		t.Fatal(itemLines(failed))
	}
	// In drop mode the half with E11 is dropped whole: the insights its
	// pairs before E11 gave are withdrawn with it; the other half's stay.
	result, err := a.Consolidate(ctx, ConsolidateRequest{})
	if err != nil || result.InsightBatches != 1 || result.InsightsDropped != len(failed) {
		t.Fatalf("%+v %v", result, err)
	}
	state = loadState(t, overlay)
	for _, in := range state.Memory.Insights {
		for _, record := range in.Records {
			if slices.ContainsFunc(failed, func(item insightItem) bool { return slices.Contains(item.records, record) }) && !slices.ContainsFunc(head[:half], func(item insightItem) bool { return slices.Contains(item.records, record) }) {
				t.Fatal("an insight of the dropped half was kept", in)
			}
		}
	}
	if len(state.Memory.Insights) == 0 || state.InsightRead.ModelErrors != 0 {
		t.Fatal(state.Memory.Insights, state.InsightRead)
	}
}

var datedRecord = regexp.MustCompile(`(?m)^\[r(\d+)\] (\d{4}/\d{2}/\d{2}) \(session [^)]*\)\n`)

// datedReply is L0 over dated conversation notes: per record, an event on
// its topic's thread and a value of "<topic> · status", both dated by the
// record's session.
func datedReply(prompt string) string {
	matches := datedRecord.FindAllStringSubmatchIndex(prompt, -1)
	events, facts := []map[string]any{}, []map[string]any{}
	for n, m := range matches {
		end := len(prompt)
		if n+1 < len(matches) {
			end = matches[n+1][0]
		}
		id, date, text := prompt[m[2]:m[3]], prompt[m[4]:m[5]], strings.TrimSpace(prompt[m[1]:end])
		topic := "misc"
		for _, name := range []string{"kayak", "taxes", "garden"} {
			if strings.Contains(text, name) {
				topic = name
				break
			}
		}
		events = append(events, map[string]any{"thread": "new: " + topic, "date": date, "text": text, "records": []string{"r" + id}})
		facts = append(facts, map[string]any{"key": topic + " · status", "value": text, "date": date, "records": []string{"r" + id}})
	}
	raw, _ := json.Marshal(map[string]any{"events": events, "facts": facts, "directives": []any{}})
	return string(raw)
}

// seedDated stores, for each day, one conversation note per topic, dated
// that day of March 2024.
func seedDated(t *testing.T, s *records.Store, days ...int) {
	t.Helper()
	for _, day := range days {
		for _, topic := range []string{"kayak", "taxes", "garden"} {
			at := time.Date(2024, 3, day, 9, 0, 0, 0, time.UTC)
			id := fmt.Sprintf("%s-%02d", topic, day)
			text := fmt.Sprintf("the %s plan changed on day %d", topic, day)
			if _, err := WriteConversation(context.Background(), s, ConversationRequest{ID: id, SessionID: id, Messages: []ConversationMessage{{Role: "user", Content: text, At: &at}}}); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// promptDates is the dates of a prompt's lines, in order.
func promptDates(prompt string) []string {
	out := []string{}
	for _, m := range insightLinePattern.FindAllStringSubmatch(prompt, -1) {
		out = append(out, m[2])
	}
	return out
}

func TestConsolidateInsightsBackfillInDateOrder(t *testing.T) {
	ctx := context.Background()
	s, dir := agentStore(t)
	seedDated(t, s, 1, 2, 3, 4, 5, 6)
	model := &limitPlanner{reply: func(system, prompt string) (string, error) {
		if system == insightSystem {
			return insightReply(prompt), nil
		}
		return datedReply(prompt), nil
	}}
	judge := insightJudge()
	off, overlay := layeredAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 400, Parallel: 1}, model, judge)
	for range 10 {
		if result, err := off.Consolidate(ctx, ConsolidateRequest{}); err != nil || result.Remaining == 0 {
			break
		}
	}
	state := loadState(t, overlay)
	if len(state.Memory.Events) != 18 || len(state.Pending) != 0 {
		t.Fatal(len(state.Memory.Events), len(state.Pending))
	}
	// The memory lists the events, then the values: L2 reads them by date,
	// each batch a period with every kind of item of it.
	on, err := New(s, Config{Version: 1, Policy: &ReadPolicy{Consolidation: true}, Consolidation: &ConsolidationConfig{Mode: "layered", BatchCharacters: 400, Parallel: 1, Insights: true}}, Models{Consolidator: model, Judge: judge}, overlay)
	if err != nil {
		t.Fatal(err)
	}
	result, err := on.Consolidate(ctx, ConsolidateRequest{})
	if err != nil || result.InsightBatches < 3 || result.Remaining != 0 {
		t.Fatalf("%+v %v", result, err)
	}
	l2, _ := l2Calls(model)
	readOnce(t, l2, insightStream(loadState(t, overlay).Memory, insightRead{}))
	all := []string{}
	for _, prompt := range l2 {
		// Each batch holds a day's events and values, events first.
		kinds := map[string]string{}
		for _, m := range insightLinePattern.FindAllStringSubmatch(prompt, -1) {
			kinds[m[2]] += m[1][:1]
		}
		mixed := false
		for _, k := range kinds {
			mixed = mixed || strings.Contains(k, "EV") && !strings.Contains(k, "VE")
		}
		if !mixed {
			t.Fatalf("no day with its events and values in a batch:\n%s", prompt)
		}
		all = append(all, promptDates(prompt)...)
	}
	if !slices.IsSorted(all) || len(all) != 36 {
		t.Fatal("not in date order:", all)
	}
	if state = loadState(t, overlay); state.InsightRead.EventsAfter != nil || state.InsightRead.ValuesAfter != nil || state.InsightRead.Events != 18 {
		t.Fatalf("%+v", state.InsightRead)
	}
}

func TestConsolidateInsightsReadOutOfListOrderOnce(t *testing.T) {
	ctx := context.Background()
	s, dir := agentStore(t)
	model := &limitPlanner{reply: func(system, prompt string) (string, error) {
		if system == insightSystem {
			return insightReply(prompt), nil
		}
		return datedReply(prompt), nil
	}}
	a, overlay := insightAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 300}, model, insightJudge())
	// Later days are written first; earlier days come later, so the later
	// items of each list are the earlier ones.
	adds := func(days ...int) {
		seedDated(t, s, days...)
		for range 10 {
			if result, err := a.Consolidate(ctx, ConsolidateRequest{Hold: true}); err != nil {
				t.Fatal(err)
			} else if result.Folded == 0 {
				return
			}
		}
	}
	adds(20, 21, 22)
	adds(10, 11, 12)
	state := loadState(t, overlay)
	if state.InsightRead.EventsAfter == nil && state.InsightRead.ValuesAfter == nil {
		t.Fatalf("no item was read out of list order: %+v", state.InsightRead)
	}
	adds(5)
	// The flush folds every rest; nothing was read twice, and the
	// positions took in every item read out of order.
	for range 10 {
		result, err := a.Consolidate(ctx, ConsolidateRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if result.Remaining == 0 {
			break
		}
	}
	state = loadState(t, overlay)
	l2, _ := l2Calls(model)
	readOnce(t, l2, insightStream(state.Memory, insightRead{}))
	read := state.InsightRead
	if read.EventsAfter != nil || read.ValuesAfter != nil || read.DirectivesAfter != nil || read.Events != len(state.Memory.Events) || len(insightStream(state.Memory, *read)) != 0 {
		t.Fatalf("%+v", read)
	}
	if raw := iddStatePayload(t, overlay); strings.Contains(raw, "After") {
		t.Fatal(raw)
	}
}

// insightQuestions reports whether a linking call asks about insights.
func insightQuestions(questions map[string]Question) bool {
	for _, q := range questions {
		if strings.HasPrefix(q.Instructions, "Does the new insight") {
			return true
		}
	}
	return false
}

func TestConsolidateInsightsDeferLinks(t *testing.T) {
	settings := ConsolidationConfig{BatchCharacters: 300, Parallel: 1}
	t.Run("an outage leaves the L2 batch unread", func(t *testing.T) {
		ctx := context.Background()
		// The insights and read position of a run whose JEV never fails.
		s0, dir0 := agentStore(t)
		seedTopics(t, s0, 6)
		healthy, overlay0 := insightAgent(t, s0, dir0, settings, insightModel(nil), insightJudge())
		if result, err := healthy.Consolidate(ctx, ConsolidateRequest{DeferLinks: true}); err != nil || result.Remaining != 0 || result.Deferred != 0 {
			t.Fatal(result, err)
		}
		want := loadState(t, overlay0)

		s, dir := agentStore(t)
		seedTopics(t, s, 6)
		var down atomic.Bool
		down.Store(true)
		judge := insightJudge()
		jev := decisionFunc(func(ctx context.Context, state any, questions map[string]Question) (map[string]float64, Usage, error) {
			if down.Load() && insightQuestions(questions) {
				return nil, Usage{InputTokens: 5}, fmt.Errorf("jev: %w: model HTTP 503; reservation released", ErrModelUnavailable)
			}
			return judge.Decide(ctx, state, questions)
		})
		a, overlay := insightAgent(t, s, dir, settings, insightModel(nil), jev)
		// L0 and L1 fold every record; L2 reads its first batch, whose
		// insights have nothing stored to link to, and leaves the second,
		// whose linking JEV cannot do, unread with the rest.
		result, err := a.Consolidate(ctx, ConsolidateRequest{DeferLinks: true})
		if !errors.Is(err, ErrLinkDeferred) || result.Folded != 2 || result.InsightBatches != 1 || result.Deferred != 1 || result.InsightFallbacks != 0 || result.LinkFallbacks != 0 {
			t.Fatalf("%+v %v", result, err)
		}
		state := loadState(t, overlay)
		if len(state.Pending) != 0 || state.InsightRead.ModelErrors != 0 || state.InsightRead.LinkErrors != 0 || insightsUnread(state) == 0 || result.Remaining != insightsUnread(state) {
			t.Fatalf("%+v %+v", state.InsightRead, result)
		}
		down.Store(false)
		result, err = a.Consolidate(ctx, ConsolidateRequest{DeferLinks: true})
		if err != nil || result.InsightBatches == 0 || result.Deferred != 0 || result.InsightFallbacks != 0 || result.Remaining != 0 {
			t.Fatalf("%+v %v", result, err)
		}
		state = loadState(t, overlay)
		if got, wanted := mustJSON(t, state.Memory.Insights), mustJSON(t, want.Memory.Insights); got != wanted {
			t.Fatalf("insights after the outage:\n%s\nwant:\n%s", got, wanted)
		}
		if got, wanted := mustJSON(t, state.InsightRead), mustJSON(t, want.InsightRead); got != wanted {
			t.Fatal(got, wanted)
		}
	})
	t.Run("the request's own failure counts, then links without JEV", func(t *testing.T) {
		ctx := context.Background()
		s, dir := agentStore(t)
		seedTopics(t, s, 6)
		judge := insightJudge()
		// The first four insight linking calls get probabilities outside
		// [0, 1]: the request's own failure.
		var mu sync.Mutex
		insightCalls := 0
		jev := decisionFunc(func(ctx context.Context, state any, questions map[string]Question) (map[string]float64, Usage, error) {
			if insightQuestions(questions) {
				mu.Lock()
				insightCalls++
				bad := insightCalls <= 4
				mu.Unlock()
				if bad {
					answers := map[string]float64{}
					for key := range questions {
						answers[key] = 2
					}
					return answers, Usage{InputTokens: 5}, nil
				}
			}
			return judge.Decide(ctx, state, questions)
		})
		// L2 concludes from the first two items of any batch it reads, so
		// that every batch's linking asks JEV.
		pairs := insightModel(func(prompt string) (string, error) {
			ids := []string{}
			for _, m := range insightLinePattern.FindAllStringSubmatch(prompt, -1) {
				ids = append(ids, m[1])
			}
			if len(ids) < 2 {
				return `{"insights":[]}`, nil
			}
			return `{"insights":[{"text":"` + ids[0] + ` and ` + ids[1] + ` go together.","items":["` + ids[0] + `","` + ids[1] + `"]}]}`, nil
		})
		a, overlay := insightAgent(t, s, dir, settings, pairs, jev)
		clock := &timelineClock{now: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)}
		a.now = clock.Now
		first := clock.Now().UnixMilli()
		for i, wait := range []time.Duration{0, 5 * time.Minute, 5 * time.Minute} {
			clock.advance(wait)
			result, err := a.Consolidate(ctx, ConsolidateRequest{DeferLinks: true})
			if err == nil || errors.Is(err, ErrLinkDeferred) || !strings.Contains(err.Error(), "invalid or missing decision probability") || result.Deferred != 1 || result.InsightFallbacks != 0 {
				t.Fatalf("job %d: %+v %v", i, result, err)
			}
			// Counted toward L2's linking rule, not its extraction's drop
			// rule.
			if state := loadState(t, overlay); state.InsightRead.LinkErrors != i+1 || state.InsightRead.FirstLinkError != first || state.InsightRead.ModelErrors != 0 || state.ModelErrors != 0 || state.LinkErrors != 0 {
				t.Fatalf("job %d: %+v", i, state.InsightRead)
			}
		}
		// Due: the head L2 batch links without JEV from its whole
		// extraction, never split, after its call fails once more, its
		// insights all added, dropping nothing; the later batches link with
		// JEV, one call each.
		before, _ := l2Calls(pairs)
		result, err := a.Consolidate(ctx, ConsolidateRequest{DeferLinks: true})
		if err != nil || result.InsightFallbacks != 1 || result.InsightBatches < 3 || result.InsightLinkCalls != result.InsightBatches || result.Deferred != 0 || result.InsightsDropped != 0 || result.Remaining != 0 {
			t.Fatalf("%+v %v", result, err)
		}
		if after, _ := l2Calls(pairs); len(after)-len(before) != result.InsightBatches {
			t.Fatal("an L2 batch was split", len(after)-len(before), result.InsightBatches)
		}
		if state := loadState(t, overlay); state.InsightRead.LinkErrors != 0 || state.InsightRead.FirstLinkError != 0 || state.InsightRead.ModelErrors != 0 {
			t.Fatal(state.InsightRead)
		}
	})
}

func TestConsolidateInsightsTimeoutCountsOnlyWhenAnswered(t *testing.T) {
	ctx := context.Background()
	s, dir := agentStore(t)
	seedTopics(t, s, 6)
	timeout := fmt.Errorf("%w; any reservation remains held (after 3 attempts)", ErrAttemptTimeout)
	model := insightModel(func(string) (string, error) { return "", timeout })
	a, overlay := insightAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 300, Parallel: 1}, model, insightJudge())
	// L0's and L1's calls were answered in this job: L2's timeout is the
	// request's own, and counts.
	if _, err := a.Consolidate(ctx, ConsolidateRequest{}); !errors.Is(err, ErrAttemptTimeout) {
		t.Fatal(err)
	}
	if state := loadState(t, overlay); state.InsightRead.ModelErrors != 1 || len(state.Pending) != 0 {
		t.Fatal(state.InsightRead, len(state.Pending))
	}
	// A job with no record to fold makes no other call: the timeout is an
	// outage, counted nowhere.
	if _, err := a.Consolidate(ctx, ConsolidateRequest{DeferLinks: true}); !errors.Is(err, ErrAttemptTimeout) {
		t.Fatal(err)
	}
	if state := loadState(t, overlay); state.InsightRead.ModelErrors != 1 {
		t.Fatal(state.InsightRead)
	}
}

func TestInsightsDropModeCountsAnsweredTimeouts(t *testing.T) {
	timeout := fmt.Errorf("%w; any reservation remains held (after 3 attempts)", ErrAttemptTimeout)
	model := insightModel(func(prompt string) (string, error) {
		if strings.Contains(prompt, "E1 ") {
			return "", timeout
		}
		return `{"insights":[]}`, nil
	})
	a := linkAgent(t, &linkJudge{}, 8, nil)
	batch := []insightItem{{id: "E1", line: "E1 (2024/01/01) a", date: "2024/01/01", dated: true}, {id: "E2", line: "E2 (2024/01/02) b", date: "2024/01/02", dated: true, index: 1}}
	for _, answered := range []bool{false, true} {
		job := &foldJob{}
		if answered {
			job.answer()
		}
		var state timelineState
		var result ConsolidateResult
		err := a.extractInsightsDropping(withFoldJob(context.Background(), job), model, &state, batch, &result)
		// The half with E1 times out first: dropped when another call of
		// the job was answered, else the outage ends the job.
		if answered && (err != nil || result.InsightsDropped != 1) || !answered && (!errors.Is(err, ErrAttemptTimeout) || result.InsightsDropped != 0) {
			t.Fatal(answered, err, result.InsightsDropped)
		}
	}
}
