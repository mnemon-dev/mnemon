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
	"testing"

	"github.com/mnemon-dev/mnemon/internal/records"
)

// linkCall is one linking judgement a linkJudge received.
type linkCall struct {
	State     linkState
	Raw       string // the state's JSON
	Questions map[string]Question
	Size      int // the request as JEV's client measures it
}

// linkJudge answers the linking questions by the texts they name: kind is
// "thread", "identity", "contradiction", "alias" (which gets the two
// names), an event edge kind ("semantic", "temporal", "causal",
// "entity"), or "insight". It records every call; err, when set, may fail
// a call.
type linkJudge struct {
	mu     sync.Mutex
	calls  []linkCall
	answer func(kind, newText, candidateText string) float64
	err    func(ctx context.Context) error
}

var linkRef = regexp.MustCompile("whose `id` is \"([^\"]+)\"")

func (j *linkJudge) Decide(ctx context.Context, state any, questions map[string]Question) (map[string]float64, Usage, error) {
	raw, err := json.Marshal(state)
	if err != nil {
		return nil, Usage{}, err
	}
	call := linkCall{Questions: questions, Raw: string(raw)}
	if err := json.Unmarshal(raw, &call.State); err != nil {
		return nil, Usage{}, err
	}
	request, _ := jevRequest(state, questions)
	call.Size = jsLen(request)
	j.mu.Lock()
	j.calls = append(j.calls, call)
	j.mu.Unlock()
	if j.err != nil {
		if err := j.err(ctx); err != nil {
			return nil, Usage{InputTokens: 3}, err
		}
	}
	item := func(items []linkItem, id string) linkItem {
		for _, item := range items {
			if item.ID == id {
				return item
			}
		}
		panic("no item " + id)
	}
	out := map[string]float64{}
	for key, q := range questions {
		refs := linkRef.FindAllStringSubmatch(q.Instructions, -1)
		if len(refs) != 2 || q.Type != "noul" {
			return nil, Usage{}, errors.New("unexpected question " + key)
		}
		kind := "contradiction"
		switch {
		case strings.HasPrefix(q.Instructions, "Does the new thread"):
			kind = "thread"
		case strings.HasPrefix(q.Instructions, "Do the new fact"):
			kind = "identity"
		case strings.HasPrefix(q.Instructions, "Do the name"):
			kind = "alias"
		case strings.HasPrefix(q.Instructions, "Are the new event") && strings.Contains(q.Instructions, "same matter"):
			kind = "semantic"
		case strings.HasPrefix(q.Instructions, "Is the new event"):
			kind = "temporal"
		case strings.HasPrefix(q.Instructions, "Did the candidate event"):
			// The candidate is named first.
			kind = "causal"
			refs[0], refs[1] = refs[1], refs[0]
		case strings.HasPrefix(q.Instructions, "Are the new event"):
			kind = "entity"
		case strings.HasPrefix(q.Instructions, "Does the new insight"):
			kind = "insight"
		case strings.HasPrefix(q.Instructions, "Is the candidate"):
			// A weight question names the candidate first.
			kind = "weight"
			refs[0], refs[1] = refs[1], refs[0]
		}
		n, c := item(call.State.New, refs[0][1]), item(call.State.Candidates, refs[1][1])
		newText, candidateText := n.Text, c.Text
		if kind == "alias" {
			newText, candidateText = n.Name, c.Name
		}
		out[key] = 0.1
		if j.answer != nil {
			out[key] = j.answer(kind, newText, candidateText)
		}
	}
	return out, Usage{InputTokens: 100, OutputTokens: 1, USD: 0.001}, nil
}

func (j *linkJudge) count() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.calls)
}

// linkAgent is an agent in layered mode with judge and no embedder unless
// embedding is set.
func linkAgent(t *testing.T, judge Decider, candidates int, embedding *records.EmbeddingConfig) *Agent {
	t.Helper()
	s, _ := agentStore(t)
	planner := &limitPlanner{reply: func(string, string) (string, error) { return "{}", nil }}
	a, err := New(s, Config{Version: 1, Policy: &ReadPolicy{}, Embedding: embedding, Consolidation: &ConsolidationConfig{Mode: "layered", LinkCandidates: candidates}}, Models{Planner: planner, Judge: judge}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// storedMemory is what earlier batches consolidated: three topics, two keys
// and a directive.
func storedMemory() timelineMemory {
	return timelineMemory{
		Threads: []thread{{ID: "T1", Title: "Kayak trip"}, {ID: "T2", Title: "Tax return"}, {ID: "T3", Title: "Garden"}},
		Events: []event{
			{Thread: "T1", Date: "2024/01/03", Text: "Booked a kayak for the lake trip.", Records: []string{"a"}},
			{Thread: "T2", Date: "2024/01/04", Text: "Filed the tax return.", Records: []string{"b"}},
			{Thread: "T3", Date: "2024/01/05", Text: "Planted tomatoes.", Records: []string{"c"}},
		},
		Facts: map[string][]factValue{
			"kayak trip · date": {{Value: "May 3", Date: "2024/01/03", Records: []string{"a"}}},
			"tax · refund":      {{Value: "$300", Date: "2024/01/04", Records: []string{"b"}}},
		},
		FactOrder:  []string{"kayak trip · date", "tax · refund"},
		Directives: []directive{{Text: "Answer briefly.", Date: "2024/01/02", Records: []string{"a"}}},
	}
}

// extractedBatch is what L0 read from one batch: three topics, three keys
// (one already stored) and two directives (one a restatement).
func extractedBatch() timelineMemory {
	return timelineMemory{
		Threads: []thread{{ID: "T1", Title: "Kayak trip planning"}, {ID: "T2", Title: "Tax documents"}, {ID: "T3", Title: "Kayak gear"}},
		Events: []event{
			{Thread: "T1", Date: "2024/02/01", Text: "Moved the kayak trip to May 10.", Records: []string{"d"}, Entities: []string{"Lake Tahoe"}},
			{Thread: "T2", Date: "2024/02/01", Text: "Gathered the tax documents.", Records: []string{"e"}},
			{Thread: "T3", Date: "2024/02/02", Text: "Bought kayak paddles.", Records: []string{"f"}},
			{Thread: "T1", Date: "2024/02/02", Text: "Invited Sam to the kayak trip.", Records: []string{"f"}},
		},
		Facts: map[string][]factValue{
			"kayak trip · date":  {{Value: "May 10", Date: "2024/02/01", Records: []string{"d"}}},
			"kayak outing · day": {{Value: "May 12", Date: "2024/02/02", Records: []string{"f"}, Entities: []string{"Sam"}}},
			"tax · deadline":     {{Value: "April 15", Date: "2024/02/01", Records: []string{"e"}}},
		},
		FactOrder:  []string{"kayak trip · date", "kayak outing · day", "tax · deadline"},
		Directives: []directive{{Text: "Please answer briefly", Date: "2024/02/01", Records: []string{"d"}}, {Text: "Use metric units.", Date: "2024/02/01", Records: []string{"e"}}},
	}
}

// kayakJudge links the kayak topics and the kayak keys, and finds the
// outing's day contradicts the stored date.
func kayakJudge() *linkJudge {
	return &linkJudge{answer: func(kind, newText, candidate string) float64 {
		switch kind {
		case "thread":
			if strings.HasPrefix(newText, "Kayak") && strings.HasPrefix(candidate, "Kayak trip:") {
				return 0.9
			}
		case "identity":
			if strings.HasPrefix(newText, "kayak outing · day:") && strings.HasPrefix(candidate, "kayak trip · date:") {
				return 0.8
			}
		case "contradiction":
			if strings.HasPrefix(newText, "kayak outing · day:") {
				return 0.7
			}
			return 0.2
		}
		return 0.1
	}}
}

func linkState0(m timelineMemory) *timelineState {
	state := emptyTimelineState()
	state.Memory = m
	return &state
}

func TestLinkBatchDecisions(t *testing.T) {
	judge := kayakJudge()
	a := linkAgent(t, judge, 8, nil)
	state, result := linkState0(storedMemory()), &ConsolidateResult{}
	linked, counts, err := a.newLinker().link(context.Background(), state, extractedBatch(), result)
	if err != nil {
		t.Fatal(err)
	}
	if counts != (linkCounts{ThreadsLinked: 2, ThreadsNew: 1, KeysLinked: 2, KeysNew: 1, Conflicts: 1}) {
		t.Fatalf("counts %+v", counts)
	}
	// Both kayak topics continue the stored trip; the tax documents start a
	// thread of their own (the judge said no to the tax return).
	if want := append(storedMemory().Threads, thread{ID: "T4", Title: "Tax documents"}); !reflect.DeepEqual(linked.Threads, want) {
		t.Fatal("threads", linked.Threads)
	}
	threads := []string{}
	for _, e := range linked.Events[3:] {
		threads = append(threads, e.Thread+" "+e.Text)
	}
	if want := []string{"T1 Moved the kayak trip to May 10.", "T4 Gathered the tax documents.", "T1 Bought kayak paddles.", "T1 Invited Sam to the kayak trip."}; !slices.Equal(threads, want) {
		t.Fatal("events", threads)
	}
	if !reflect.DeepEqual(linked.Events[3].Entities, []string{"Lake Tahoe"}) || !reflect.DeepEqual(linked.Events[3].Records, []string{"d"}) {
		t.Fatal("event fields", linked.Events[3])
	}
	// The exact key and the outing's day append to the stored key's history
	// under its name; only the outing's day contradicts it. The deadline is
	// new.
	wantDate := []factValue{
		{Value: "May 3", Date: "2024/01/03", Records: []string{"a"}},
		{Value: "May 10", Date: "2024/02/01", Records: []string{"d"}},
		{Value: "May 12", Date: "2024/02/02", Records: []string{"f"}, Conflict: true, Entities: []string{"Sam"}},
	}
	if !reflect.DeepEqual(linked.Facts["kayak trip · date"], wantDate) {
		t.Fatalf("history %+v", linked.Facts["kayak trip · date"])
	}
	if !slices.Equal(linked.FactOrder, []string{"kayak trip · date", "tax · refund", "tax · deadline"}) || len(linked.Facts) != 3 || linked.Facts["kayak outing · day"] != nil {
		t.Fatal("keys", linked.FactOrder)
	}
	if got := []string{linked.Directives[0].Text, linked.Directives[1].Text}; len(linked.Directives) != 2 || got[1] != "Use metric units." {
		t.Fatal("directives", linked.Directives)
	}
	// The stored memory is not changed in place.
	if !reflect.DeepEqual(state.Memory, storedMemory()) {
		t.Fatal("linking changed the stored memory")
	}
	// One call; the exact key is asked only whether it contradicts.
	if judge.count() != 1 {
		t.Fatal("calls", judge.count())
	}
	call := judge.calls[0]
	exact := ""
	for _, item := range call.State.New {
		if strings.HasPrefix(item.Text, "kayak trip · date: ") {
			exact = item.ID
		}
	}
	asked := map[string]int{}
	for _, q := range call.Questions {
		refs := linkRef.FindAllStringSubmatch(q.Instructions, -1)
		if refs[0][1] == exact {
			asked[q.Instructions[:20]]++
			if refs[1][1] != "K1" {
				t.Fatal("the exact key was compared with another key", q.Instructions)
			}
		}
	}
	if len(asked) != 1 || asked["Does the value of th"] != 1 {
		t.Fatal("questions about the exact key", asked)
	}
	// Usage is the job's and the state's.
	if result.LinkCalls != 1 || result.Usage.InputTokens != 100 || state.Usage.InputTokens != 100 || result.LinkUsage == nil || result.LinkUsage.USD != 0.001 {
		t.Fatalf("usage %+v %+v", result, state.Usage)
	}
}

func TestLinkQuestionsVerbatim(t *testing.T) {
	judge := kayakJudge()
	a := linkAgent(t, judge, 8, nil)
	if _, _, err := a.newLinker().link(context.Background(), linkState0(storedMemory()), extractedBatch(), &ConsolidateResult{}); err != nil {
		t.Fatal(err)
	}
	call := judge.calls[0]
	news, candidates := map[string]string{}, map[string]string{}
	for _, item := range call.State.New {
		news[item.ID] = item.Text
	}
	for _, item := range call.State.Candidates {
		candidates[item.ID] = item.Text
	}
	for id := range news {
		if !regexp.MustCompile(`^n[1-9][0-9]*$`).MatchString(id) {
			t.Fatal("new item id", id)
		}
	}
	if candidates["K1"] != "kayak trip · date: May 3" {
		t.Fatal("ids", news, candidates)
	}
	// The texts: a topic as the layer shows it, a key with its latest value.
	if candidates["T1"] != "Kayak trip: 1 events, 2024/01/03 to 2024/01/03. Latest: 2024/01/03 Booked a kayak for the lake trip." ||
		!slices.Contains(mapValues(news), "Kayak trip planning: 2 events, 2024/02/01 to 2024/02/02. Latest: 2024/02/01 Moved the kayak trip to May 10. / 2024/02/02 Invited Sam to the kayak trip.") ||
		!slices.Contains(mapValues(news), "kayak outing · day: May 12") {
		t.Fatal("texts", news, candidates)
	}
	kinds := map[string]int{}
	for key, q := range call.Questions {
		refs := linkRef.FindAllStringSubmatch(q.Instructions, -1)
		n, c := refs[0][1], refs[1][1]
		var want Question
		switch {
		case strings.HasPrefix(q.Instructions, "Does the new thread"):
			want = Question{Type: "noul", Instructions: "Does the new thread whose `id` is \"" + n + "\" follow the same specific sub-topic as the candidate whose `id` is \"" + c + "\", so that its events continue that thread?",
				Criteria: map[string]string{"true": "Both are about the same specific feature, component, problem, decision, document, exam, event, relationship or goal, and the new events continue or update it.",
					"false": "They only share a project, person, place or general topic, or are different parts of a larger project."}}
		case strings.HasPrefix(q.Instructions, "Do the new fact"):
			want = Question{Type: "noul", Instructions: "Do the new fact whose `id` is \"" + n + "\" and the candidate whose `id` is \"" + c + "\" give the value of the same attribute of the same thing?",
				Criteria: map[string]string{"true": "The same thing and the same attribute (the same deadline, count, place, status, choice or name), even if worded differently.",
					"false": "Different things, or different attributes of the same thing."}}
		default:
			want = Question{Type: "noul", Instructions: "Does the value of the new fact whose `id` is \"" + n + "\" contradict the latest value of the candidate whose `id` is \"" + c + "\" without the records saying that it changed?",
				Criteria: map[string]string{"true": "Both values cannot be true at once and nothing says the value was changed, moved, rescheduled or corrected.",
					"false": "The values agree, or the new value is stated as a change, update or correction."}}
		}
		if !reflect.DeepEqual(q, want) {
			t.Fatalf("%s:\n%+v\nwant\n%+v", key, q, want)
		}
		kinds[q.Instructions[:15]+":"+n+":"+c]++
	}
	for pair, n := range kinds {
		if n != 1 {
			t.Fatal("a pair was asked twice", pair)
		}
	}
	// Every non-exact key pair is asked both fact questions in the same call.
	for pair := range kinds {
		if strings.HasPrefix(pair, "Do the new fact") {
			_, rest, _ := strings.Cut(pair, ":")
			if kinds["Does the value :"+rest] != 1 {
				t.Fatal("identity without contradiction", pair)
			}
		}
	}
}

func mapValues(m map[string]string) []string {
	out := []string{}
	for _, value := range m {
		out = append(out, value)
	}
	return out
}

func TestLinkBatchNoAndTies(t *testing.T) {
	// The judge says no to everything: every topic and key is new, except
	// the exact key, which links without being asked whether it is the same.
	no := &linkJudge{}
	a := linkAgent(t, no, 8, nil)
	linked, counts, err := a.newLinker().link(context.Background(), linkState0(storedMemory()), extractedBatch(), &ConsolidateResult{})
	if err != nil || counts != (linkCounts{ThreadsNew: 3, KeysLinked: 1, KeysNew: 2}) {
		t.Fatal(counts, err)
	}
	if len(linked.Threads) != 6 || linked.Threads[5] != (thread{ID: "T6", Title: "Kayak gear"}) || len(linked.Facts["kayak trip · date"]) != 2 || len(linked.FactOrder) != 4 {
		t.Fatal(linked.Threads, linked.FactOrder)
	}
	// The best yes wins; ties go to the better-ranked candidate.
	for _, c := range []struct {
		answers []float64
		want    int
	}{
		{[]float64{0.4, 0.6, 0.9, 0.9}, 2},
		{[]float64{0.5, 0.5}, 0},
		{[]float64{0.49, 0.2}, -1},
		{nil, -1},
	} {
		if got := bestLink(c.answers); got != c.want {
			t.Fatal(c.answers, got)
		}
	}
	// An empty memory asks nothing: all is new.
	judge := kayakJudge()
	linked, counts, err = linkAgent(t, judge, 8, nil).newLinker().link(context.Background(), linkState0(emptyTimelineState().Memory), extractedBatch(), &ConsolidateResult{})
	if err != nil || judge.count() != 0 || counts != (linkCounts{ThreadsNew: 3, KeysNew: 3}) || len(linked.Threads) != 3 || len(linked.Directives) != 2 {
		t.Fatal(counts, err, judge.count())
	}
}

func TestLinkBatchFallsBackOnJudgeError(t *testing.T) {
	judge := kayakJudge()
	judge.err = func(context.Context) error { return fmt.Errorf("%w: model HTTP 429", ErrModelUnavailable) }
	a := linkAgent(t, judge, 8, nil)
	state, result := linkState0(storedMemory()), &ConsolidateResult{}
	linked, counts, err := a.newLinker().link(context.Background(), state, extractedBatch(), result)
	if err != nil || counts != (linkCounts{ThreadsNew: 3, KeysLinked: 1, KeysNew: 2, Fallback: true}) {
		t.Fatal(counts, err)
	}
	// Nothing is linked but the exact key, which keeps L0's flag.
	if len(linked.Threads) != 6 || len(linked.Facts["kayak trip · date"]) != 2 || linked.Facts["kayak trip · date"][1].Conflict || linked.Facts["kayak outing · day"] == nil {
		t.Fatal(linked.Threads, linked.Facts)
	}
	if result.LinkCalls != 1 || state.Usage.InputTokens != 3 {
		t.Fatal(result.LinkCalls, state.Usage)
	}
	// A canceled job returns its cancellation instead.
	ctx, cancel := context.WithCancel(context.Background())
	judge.err = func(ctx context.Context) error { cancel(); return ctx.Err() }
	if _, _, err := a.newLinker().link(ctx, linkState0(storedMemory()), extractedBatch(), &ConsolidateResult{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestLinkPackingBounds(t *testing.T) {
	// 320 stored topics and keys, eight of them sharing a rare word with
	// each of 40 new topics and keys, every text long: each new item has
	// eight candidates of its own, and the questions need more calls than
	// the question limit alone would, each within 128 questions and 96,000
	// units.
	stored := emptyTimelineState().Memory
	long := strings.Repeat("渡し舟の予定 ", 120)
	for i := 1; i <= 40; i++ {
		for g := 0; g < 8; g++ {
			id := fmt.Sprintf("T%d", len(stored.Threads)+1)
			stored.Threads = append(stored.Threads, thread{ID: id, Title: fmt.Sprintf("Topic uniq%d part %d", i, g)})
			for e := 0; e < 3; e++ {
				stored.Events = append(stored.Events, event{Thread: id, Date: "2024/01/01", Text: fmt.Sprintf("event %d %s", e, long)})
			}
			key := fmt.Sprintf("key uniq%d part %d", i, g)
			stored.Facts[key] = []factValue{{Value: "value " + long, Date: "2024/01/01"}}
			stored.FactOrder = append(stored.FactOrder, key)
		}
	}
	batch := emptyTimelineState().Memory
	for i := 1; i <= 40; i++ {
		id := fmt.Sprintf("T%d", i)
		batch.Threads = append(batch.Threads, thread{ID: id, Title: fmt.Sprintf("New topic uniq%d", i)})
		batch.Events = append(batch.Events, event{Thread: id, Date: "2024/02/01", Text: "news " + long})
		key := fmt.Sprintf("new key uniq%d", i)
		batch.Facts[key] = []factValue{{Value: "value", Date: "2024/02/01"}}
		batch.FactOrder = append(batch.FactOrder, key)
	}
	judge := &linkJudge{}
	_, counts, err := linkAgent(t, judge, 8, nil).newLinker().link(context.Background(), linkState0(stored), batch, &ConsolidateResult{})
	if err != nil || counts.Fallback {
		t.Fatal(err, counts)
	}
	questions := 0
	for _, call := range judge.calls {
		if len(call.Questions) > 128 || call.Size > maxJEVRequest {
			t.Fatal("call over the limits", len(call.Questions), call.Size)
		}
		for _, item := range append(slices.Clone(call.State.New), call.State.Candidates...) {
			if jsLen(item.Text) > 600 {
				t.Fatal("a text was not cut", jsLen(item.Text))
			}
		}
		for _, q := range call.Questions {
			refs := linkRef.FindAllStringSubmatch(q.Instructions, -1)
			n, c := refs[0][1], refs[1][1]
			if !slices.ContainsFunc(call.State.New, func(item linkItem) bool { return item.ID == n }) || !slices.ContainsFunc(call.State.Candidates, func(item linkItem) bool { return item.ID == c }) {
				t.Fatal("a question names an item its call does not hold", q.Instructions)
			}
		}
		questions += len(call.Questions)
	}
	// 40 topics and 40 keys, eight candidates each: 320 + 640 questions,
	// which 128 a call would ask in 8 calls.
	if questions != 960 || judge.count() <= 8 {
		t.Fatal(questions, judge.count())
	}
	// Short texts: the question limit alone binds, and calls are full.
	short := &linkJudge{}
	small := emptyTimelineState().Memory
	for i := 1; i <= 20; i++ {
		id := fmt.Sprintf("T%d", i)
		small.Threads = append(small.Threads, thread{ID: id, Title: fmt.Sprintf("topic %d", i)})
		small.Events = append(small.Events, event{Thread: id, Date: "2024/01/01", Text: "topic"})
	}
	news := emptyTimelineState().Memory
	for i := 1; i <= 40; i++ {
		id := fmt.Sprintf("T%d", i)
		news.Threads = append(news.Threads, thread{ID: id, Title: fmt.Sprintf("topic %d", 100+i)})
		news.Events = append(news.Events, event{Thread: id, Date: "2024/02/01", Text: "topic"})
	}
	if _, _, err := linkAgent(t, short, 8, nil).newLinker().link(context.Background(), linkState0(small), news, &ConsolidateResult{}); err != nil {
		t.Fatal(err)
	}
	if short.count() != 3 || len(short.calls[0].Questions) != 128 || len(short.calls[2].Questions) != 320-256 {
		t.Fatal(short.count())
	}
}

func TestLinkCandidatesByEmbedding(t *testing.T) {
	server := &embeddingServer{}
	url := server.start(t, func(text string) []float64 {
		text = strings.TrimPrefix(text, "search_document: ")
		switch {
		case strings.HasPrefix(text, "Boat outing"), strings.HasPrefix(text, "Garden:"):
			return []float64{1, 0, 0}
		case strings.HasPrefix(text, "Kayak trip:"):
			return []float64{0.6, 0.8, 0}
		}
		return []float64{0, 0, 1}
	})
	embedding := &records.EmbeddingConfig{URL: url, Model: "test", QueryPrefix: "search_query: ", DocumentPrefix: "search_document: "}
	judge := &linkJudge{}
	a := linkAgent(t, judge, 1, embedding)
	batch := timelineMemory{Threads: []thread{{ID: "T1", Title: "Boat outing"}}, Events: []event{{Thread: "T1", Date: "2024/02/01", Text: "Rented a kayak."}}, Facts: map[string][]factValue{}}
	if _, counts, err := a.newLinker().link(context.Background(), linkState0(storedMemory()), batch, &ConsolidateResult{}); err != nil || counts.Lexical {
		t.Fatal(counts, err)
	}
	// By meaning the garden is nearest (shared words would pick the trip);
	// one candidate, as configured.
	if judge.count() != 1 || len(judge.calls[0].State.Candidates) != 1 || judge.calls[0].State.Candidates[0].ID != "T3" {
		t.Fatal(judge.calls)
	}
	server.mu.Lock()
	inputs := slices.Clone(server.inputs)
	server.mu.Unlock()
	for _, input := range inputs {
		if !strings.HasPrefix(input, "search_document: ") {
			t.Fatal("not a document text", input)
		}
	}
	newText := "Boat outing: 1 events, 2024/02/01 to 2024/02/01. Latest: 2024/02/01 Rented a kayak."
	storedText := "Garden: 1 events, 2024/01/05 to 2024/01/05. Latest: 2024/01/05 Planted tomatoes."
	if !slices.Contains(inputs, "search_document: "+newText) || !slices.Contains(inputs, "search_document: "+storedText) {
		t.Fatal(inputs)
	}
	// The stored threads' vectors are kept for the layer; the batch's own
	// texts, read once, are not.
	kept, err := a.store.DerivedVectors(context.Background(), []string{newText, storedText}, *embedding, false)
	if err != nil || kept[0] != nil || kept[1] == nil {
		t.Fatal("stored vectors", kept[0] != nil, kept[1] != nil, err)
	}
	// A failing embedder leaves shared words.
	server.mu.Lock()
	server.fail = true
	server.mu.Unlock()
	judge = &linkJudge{}
	a = linkAgent(t, judge, 1, embedding)
	_, counts, err := a.newLinker().link(context.Background(), linkState0(storedMemory()), batch, &ConsolidateResult{})
	if err != nil || !counts.Lexical {
		t.Fatal(counts, err)
	}
	if judge.count() != 1 || judge.calls[0].State.Candidates[0].ID != "T1" {
		t.Fatal(judge.calls)
	}
	// The job counts such batches; shared words without an embedder are no
	// fallback (TestLinkBatchDecisions: Lexical stays false).
	var result ConsolidateResult
	result.addLinks(counts)
	result.addLinks(linkCounts{ThreadsNew: 1})
	if result.LinkLexical != 1 {
		t.Fatal(result.LinkLexical)
	}
	if raw, _ := json.Marshal(ConsolidateResult{}); strings.Contains(string(raw), "linkLexical") {
		t.Fatal(string(raw))
	}
}
