package memoryagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/records"
)

// oneshotJudge answers one-shot link questions with pick (the question's key
// and sorted options → the chosen one) and records each request's questions.
type oneshotJudge struct {
	mu    sync.Mutex
	calls []map[string]Question
	pick  func(key string, options []string) string
	err   error
}

func (j *oneshotJudge) Decide(context.Context, any, map[string]Question) (map[string]float64, Usage, error) {
	return nil, Usage{}, errors.New("one-shot links ask choice questions only")
}

func (j *oneshotJudge) Choose(_ context.Context, _ any, questions map[string]Question) (map[string]Choice, Usage, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.calls = append(j.calls, questions)
	if j.err != nil {
		return nil, Usage{}, j.err
	}
	out := map[string]Choice{}
	for key, q := range questions {
		options := []string{}
		for option := range q.Criteria {
			options = append(options, option)
		}
		slices.Sort(options)
		out[key] = Choice{Option: j.pick(key, options), P: 0.9}
	}
	return out, Usage{}, nil
}

// previousUnit chooses the unit right before the asked one.
func previousUnit(key string, options []string) string {
	var i int
	fmt.Sscanf(key, "link_%d", &i)
	if want := fmt.Sprintf("u%d", i-1); slices.Contains(options, want) {
		return want
	}
	return "none"
}

func oneshotAgent(t *testing.T, judge Decider) (*Agent, *Overlay, *records.Store) {
	t.Helper()
	s, dir := agentStore(t)
	model := &limitPlanner{reply: func(_, prompt string) (string, error) { return layeredReply(prompt), nil }}
	a, overlay := layeredAgent(t, s, dir, ConsolidationConfig{OneShotLinks: true}, model, judge)
	return a, overlay, s
}

func linkPairs(t *testing.T, overlay *Overlay, ids ...string) [][2]string {
	t.Helper()
	links, err := overlay.linksOf(context.Background(), records.Scope{}, ids)
	if err != nil {
		t.Fatal(err)
	}
	out := [][2]string{}
	for _, l := range links {
		out = append(out, [2]string{l.ID, l.Target})
	}
	return out
}

func TestOneShotLinksAskEachRecordOnce(t *testing.T) {
	ctx := context.Background()
	judge := &oneshotJudge{pick: previousUnit}
	a, overlay, s := oneshotAgent(t, judge)
	texts := []string{"kayak trip to the lake", "the kayak needs a new paddle", "taxes are due in april", "garden tomatoes are ripe", "paddle bought for the kayak trip"}
	for i, text := range texts {
		seedNote(t, s, fmt.Sprintf("n%d", i), text)
	}
	asked, err := a.linkRecords(ctx, records.Scope{})
	if err != nil || asked != 5 || len(judge.calls) != 1 {
		t.Fatalf("asked %d, calls %d, err %v", asked, len(judge.calls), err)
	}
	// One request for the window; the first record has nothing earlier to
	// choose, every other one a choice among earlier units and none.
	questions := judge.calls[0]
	if _, ok := questions["link_0"]; ok || len(questions) != 4 {
		t.Fatalf("questions %v", questions)
	}
	for key, q := range questions {
		var i int
		fmt.Sscanf(key, "link_%d", &i)
		if q.Type != "choice" || q.Criteria["none"] == "" || q.Criteria[fmt.Sprintf("u%d", i-1)] == "" {
			t.Fatalf("question %s %+v", key, q)
		}
	}
	// The kayak paddle record (u4) may also choose the kayak records that
	// BM25 finds, beyond the two before it.
	if q := questions["link_4"]; q.Criteria["u1"] == "" || q.Criteria["u0"] == "" {
		t.Fatalf("far options of link_4: %v", q.Criteria)
	}
	if got := linkPairs(t, overlay, "n0"); !reflect.DeepEqual(got, [][2]string{{"n1", "n0"}}) {
		t.Fatalf("links of n0 %v", got)
	}
	// Asked records are not asked again; a new one is asked with the two
	// records before it shown as its options.
	if asked, err := a.linkRecords(ctx, records.Scope{}); err != nil || asked != 0 || len(judge.calls) != 1 {
		t.Fatalf("second run asked %d, calls %d, err %v", asked, len(judge.calls), err)
	}
	seedNote(t, s, "n5", "the lake was calm for the kayak")
	if asked, err := a.linkRecords(ctx, records.Scope{}); err != nil || asked != 1 || len(judge.calls) != 2 {
		t.Fatalf("third run asked %d, calls %d, err %v", asked, len(judge.calls), err)
	}
	if q, ok := judge.calls[1]["link_2"]; !ok || len(judge.calls[1]) != 1 || len(q.Criteria) != 3 {
		t.Fatalf("the new record's question %v", judge.calls[1])
	}
	if got := linkPairs(t, overlay, "n5"); !reflect.DeepEqual(got, [][2]string{{"n5", "n4"}}) {
		t.Fatalf("links of n5 %v", got)
	}
}

func TestOneShotLinksWaitWhileJEVIsUnavailable(t *testing.T) {
	ctx := context.Background()
	judge := &oneshotJudge{pick: previousUnit, err: fmt.Errorf("%w: model HTTP 402", ErrModelUnavailable)}
	a, overlay, s := oneshotAgent(t, judge)
	seedNote(t, s, "n0", "first note")
	seedNote(t, s, "n1", "second note")
	if asked, err := a.linkRecords(ctx, records.Scope{}); !errors.Is(err, ErrModelUnavailable) || asked != 0 {
		t.Fatalf("asked %d, err %v", asked, err)
	}
	if watermark, _ := overlay.linkWatermark(ctx, records.Scope{}); watermark != 0 {
		t.Fatal("an unavailable JEV moved the watermark", watermark)
	}
	// A refusal of the request itself stores the window as asked, unlinked.
	judge.err = errors.New("model HTTP 400")
	if asked, err := a.linkRecords(ctx, records.Scope{}); err != nil || asked != 2 {
		t.Fatalf("asked %d, err %v", asked, err)
	}
	judge.err = nil
	if asked, err := a.linkRecords(ctx, records.Scope{}); err != nil || asked != 0 || len(linkPairs(t, overlay, "n0", "n1")) != 0 {
		t.Fatalf("a refused window was asked again: %d %v", asked, err)
	}
}

func TestViewLinksAppendLinkedRecordsOnce(t *testing.T) {
	ctx := context.Background()
	a, _, s := oneshotAgent(t, &oneshotJudge{pick: previousUnit})
	for i := range 4 {
		seedNote(t, s, fmt.Sprintf("n%d", i), fmt.Sprintf("note %d", i))
	}
	if _, err := a.linkRecords(ctx, records.Scope{}); err != nil {
		t.Fatal(err)
	}
	revision, _, err := s.Heads(ctx, "", records.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	a.config.View = &ViewConfig{MaxItems: 100, Links: true}
	r := &simpleRun{agent: a, revision: revision}
	shown, err := evidence(ctx, s, revision, records.Scope{}, []string{"n1", "n2"})
	if err != nil {
		t.Fatal(err)
	}
	view, err := r.appendLinked(ctx, View{Records: shown})
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, record := range view.Records {
		ids = append(ids, record.ID)
	}
	// n1 chose n0 and n2 chose n1; n3 chose n2. The View's own records are
	// not added again.
	if !reflect.DeepEqual(ids, []string{"n1", "n2", "n0", "n3"}) || view.Stats.LinksAppended != 2 {
		t.Fatalf("view %v, appended %d", ids, view.Stats.LinksAppended)
	}
}

func TestChooseReadsJEVChoices(t *testing.T) {
	budget, err := OpenBudget(filepath.Join(t.TempDir(), "budget.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer budget.Close()
	t.Setenv("UNIT_JEV_CHOICE", "test-only-placeholder")
	p, err := NewProvider(ModelConfig{Kind: "jev", Model: "jev-1.13.0", KeyEnv: "UNIT_JEV_CHOICE"}, budget)
	if err != nil {
		t.Fatal(err)
	}
	var sent atomic.Int32
	reply := `{"model":"jev-1.13.0","answers":{"q":{"type":"choice","choice":"u1","confidence":0.8,"probabilities":{"u1":0.8,"none":0.2}}},"usage":{"input_tokens":40,"output_tokens":3}}`
	p.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		sent.Add(1)
		raw, _ := io.ReadAll(req.Body)
		var body struct{ Questions map[string]Question }
		if json.Unmarshal(raw, &body) != nil || body.Questions["q"].Type != "choice" {
			t.Error("the request did not carry the choice question")
		}
		return mockResponse(reply), nil
	})
	questions := map[string]Question{"q": {Type: "choice", Instructions: "Which one?", Criteria: map[string]string{"u1": "it depends on u1", "none": "it stands alone"}}}
	choices, usage, err := p.Choose(context.Background(), map[string]any{"units": []string{"u1"}}, questions)
	if err != nil || choices["q"] != (Choice{Option: "u1", P: 0.8}) || usage.InputTokens != 40 || sent.Load() != 1 {
		t.Fatalf("choices %v, usage %+v, sent %d, err %v", choices, usage, sent.Load(), err)
	}
	// A choice outside the options is an invalid reply, retried once.
	reply = `{"model":"jev-1.13.0","answers":{"q":{"type":"choice","choice":"u9","probabilities":{"u9":1}}},"usage":{"input_tokens":40,"output_tokens":3}}`
	if _, _, err := p.Choose(context.Background(), map[string]any{}, questions); err == nil || sent.Load() != 3 {
		t.Fatalf("an unknown option was accepted: sent %d, err %v", sent.Load(), err)
	}
}

func TestLoopLinksPoolLinkedAndAdjacentRecordsOfUsefulFinds(t *testing.T) {
	ctx := context.Background()
	a, _, s := oneshotAgent(t, &oneshotJudge{pick: previousUnit})
	for i := range 5 {
		seedNote(t, s, fmt.Sprintf("n%d", i), fmt.Sprintf("note %d", i))
	}
	if _, err := a.linkRecords(ctx, records.Scope{}); err != nil {
		t.Fatal(err)
	}
	revision, _, err := s.Heads(ctx, "", records.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	// n2 was screened useful and n4 was not; n1 is pooled already.
	r := &simpleRun{agent: a, revision: revision, order: []string{"n1", "n2", "n4"}, items: map[string]*simpleItem{
		"n1": {ID: "n1", HasScreen: true, Screen: 0.1},
		"n2": {ID: "n2", HasScreen: true, Screen: 0.9},
		"n4": {ID: "n4", HasScreen: true, Screen: 0.2},
	}}
	last, expanded := map[string]bool{"n1": true, "n2": true, "n4": true}, map[string]bool{}
	got, err := r.linkedFresh(ctx, last, expanded)
	if err != nil {
		t.Fatal(err)
	}
	// From n2 only: n3 chose it (n1, its own choice, is pooled). Nothing from
	// n4, which JEV did not find useful.
	if !slices.Contains(got, "n3") || slices.Contains(got, "n1") || slices.Contains(got, "n5") || !expanded["n2"] || expanded["n4"] {
		t.Fatalf("fresh %v, expanded %v", got, expanded)
	}
	// A seed is expanded from once.
	if again, err := r.linkedFresh(ctx, last, expanded); err != nil || len(again) != 0 {
		t.Fatalf("expanded again: %v %v", again, err)
	}
}
