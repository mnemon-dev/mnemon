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

// needJudge answers the need table by need, the codename (q0) for ORBIT and
// the launch day (q1) for a launch, and the screen and the judgement by
// needed; it calls what was redone no longer current.
func needJudge(needed func(text string) float64) *scriptedJudge {
	return &scriptedJudge{
		answer: func(text string, need int) float64 {
			switch {
			case need < 0:
				return needed(text)
			case need == 0 && strings.Contains(text, "ORBIT"), need == 1 && strings.Contains(text, "launch"):
				return 0.9
			}
			return 0.1
		},
		superseded: func(text string) float64 {
			if strings.Contains(text, "redone") {
				return 0.7
			}
			return 0
		},
	}
}

// byWords is needed answers by the first word of the text that a table
// gives, else 0.2.
func byWords(answers map[string]float64) func(string) float64 {
	return func(text string) float64 {
		best, at := 0.2, len(text)
		for word, answer := range answers {
			if i := strings.Index(text, word); i >= 0 && i < at {
				best, at = answer, i
			}
		}
		return best
	}
}

// needsRead reads "What is the codename?" from expandFixture with the
// planner's needs, whose cues pool a1, a2 and doc-00000 (and with an
// unmet need the rewrite "codename rocket" pools b0 and b2).
func needsRead(t *testing.T, s *records.Store, needs string, view *ViewConfig, judge Decider) View {
	t.Helper()
	planner := replies("codename project\nNOTE: the codename is ORBIT", needs, "codename rocket")
	a, err := New(s, Config{Version: 1, Strategy: "simple", View: view}, Models{Planner: planner, Judge: judge}, nil)
	if err != nil {
		t.Fatal(err)
	}
	view2, err := a.Read(context.Background(), ReadRequest{Question: "What is the codename?"})
	if err != nil {
		t.Fatal(err)
	}
	return view2
}

func TestExpandStage4bConfig(t *testing.T) {
	s, _ := agentStore(t)
	both := Models{Planner: expandPlanner(), Judge: expandJudge()}
	good := ExpandConfig{Kinds: []string{"adjacent"}, Gate: "needs", Target: "needs"}
	a, err := New(s, Config{Version: 1, Strategy: "simple", View: &ViewConfig{Expand: &good}}, both, nil)
	if err != nil || a.config.View.Expand.Gate != "needs" || a.config.View.Expand.Target != "needs" {
		t.Fatal(a, err)
	}
	for _, bad := range []ExpandConfig{
		{Kinds: []string{"adjacent"}, Gate: "always"}, {Kinds: []string{"adjacent"}, Gate: "Needs"}, {Kinds: []string{"adjacent"}, Target: "need"},
	} {
		if _, err = New(s, Config{Version: 1, Strategy: "simple", View: &ViewConfig{Expand: &bad}}, both, nil); err == nil {
			t.Error("invalid expansion accepted", bad)
		}
	}
	// Absent, they stay out of the config's JSON (and the policy).
	raw, _ := json.Marshal(ExpandConfig{Kinds: []string{"adjacent"}, Limit: 24, Seeds: 16, AdjacentWindow: 1})
	if strings.Contains(string(raw), "gate") || strings.Contains(string(raw), "target") {
		t.Fatal(string(raw))
	}
	decoder := json.NewDecoder(strings.NewReader(`{"version":1,"strategy":"simple","view":{"expand":{"kinds":["adjacent"],"gate":"needs","target":"needs"}}}`))
	decoder.DisallowUnknownFields()
	var decoded Config
	if err = decoder.Decode(&decoded); err != nil || !reflect.DeepEqual(*decoded.View.Expand, ExpandConfig{Kinds: []string{"adjacent"}, Gate: "needs", Target: "needs"}) {
		t.Fatal(decoded, err)
	}
	if raw, _ := json.Marshal(ReadStats{}); strings.Contains(string(raw), "expandGated") || strings.Contains(string(raw), "expandAnsweredNeeds") {
		t.Fatal(string(raw))
	}
}

func TestExpansionGate(t *testing.T) {
	s := expandFixture(t)
	judge := func() *scriptedJudge { return needJudge(byWords(map[string]float64{"ORBIT": 0.9, "launch": 0.9})) }
	gated := &ViewConfig{Expand: &ExpandConfig{Kinds: []string{"adjacent", "temporal", "causal"}, Gate: "needs"}}
	open := &ViewConfig{Expand: &ExpandConfig{Kinds: []string{"adjacent", "temporal", "causal"}}}
	// The loop stopped covered, the one need has a record JEV judged needed
	// that gives it, and no need is ALL: no expansion, no call for it.
	plain := needsRead(t, s, "NEED: the codename", nil, judge())
	view := needsRead(t, s, "NEED: the codename", gated, judge())
	if view.StopReason != "covered" || !view.Stats.ExpandGated || view.Stats.Expanded != 0 || view.Stats.DecisionCalls != plain.Stats.DecisionCalls || !slices.Equal(recordIDs(view.Records), recordIDs(plain.Records)) {
		t.Fatal(view.StopReason, view.Stats, recordIDs(view.Records))
	}
	if ungated := needsRead(t, s, "NEED: the codename", open, judge()); ungated.Stats.Expanded == 0 || ungated.Stats.ExpandGated {
		t.Fatal(ungated.Stats)
	}
	// An ALL need always expands.
	if view = needsRead(t, s, "ALL: every codename", gated, judge()); view.Stats.ExpandGated || view.Stats.Expanded == 0 {
		t.Fatal(view.StopReason, view.Stats)
	}
	// A need the loop gave up on expands.
	if view = needsRead(t, s, "NEED: the codename\nNEED: the launch day", gated, judge()); view.StopReason == "covered" || view.Stats.ExpandGated || view.Stats.Expanded == 0 {
		t.Fatal(view.StopReason, view.Stats)
	}
	// Covered, but the record that gives the need is no longer current:
	// the need has no evidence, so the read expands.
	stale := judge()
	stale.superseded = func(text string) float64 {
		if strings.Contains(text, "ORBIT") {
			return 0.7
		}
		return 0
	}
	if view = needsRead(t, s, "NEED: the codename", gated, stale); view.StopReason != "covered" || view.Stats.ExpandGated || view.Stats.Expanded == 0 {
		t.Fatal(view.StopReason, view.Stats)
	}
}

func TestExpansionTargetsUncoveredNeeds(t *testing.T) {
	s := expandFixture(t)
	// a2 gives the codename (q0); nothing pooled gives the launch day (q1),
	// so the loop gives up on it. a2's neighbors: a3 (one step), a0 and a4
	// (two), b1 (temporal), doc-00001 (causal); the weekend greeting a0 is
	// needed but gives no launch day.
	judge := func() *scriptedJudge {
		return needJudge(byWords(map[string]float64{"ORBIT": 0.9, "launch": 0.9, "weekend": 0.95}))
	}
	needs := "NEED: the codename\nNEED: the launch day"
	expand := ExpandConfig{Kinds: []string{"adjacent", "temporal", "causal"}, AdjacentWindow: 2}
	plain := needsRead(t, s, needs, &ViewConfig{Expand: &expand}, judge())
	targeted := expand
	targeted.Target = "needs"
	j := judge()
	view := needsRead(t, s, needs, &ViewConfig{Expand: &targeted}, j)
	// The expansions are asked the need table's question for q1 alone, in
	// their rank order, before the judgement.
	var asked *judgeCall
	for i := range j.calls {
		if call := &j.calls[i]; call.State.Rubric.Question == needQuestion && len(call.State.Needs) == 1 {
			asked = call
		}
	}
	if asked == nil {
		t.Fatal("the expansions were not asked the need table's question")
	}
	texts := []string{}
	for _, candidate := range asked.State.Candidates {
		texts = append(texts, candidate.Text)
	}
	if asked.State.Needs[0].ID != "q1" || asked.State.Needs[0].Need != "the launch day" || len(asked.Questions) != 5 || asked.Questions["c4q1"].Instructions != needInstruction(4, 1) ||
		!slices.Equal(texts, []string{"Melanie: launch window opens Friday at dawn", "Caroline: good morning, how was your weekend", "Caroline: sounds great, see you there", "Caroline: launch moved to Monday", "Notes: launch checklist signed"}) {
		t.Fatal(asked)
	}
	// Without target the weekend greeting leads the expansions (0.95);
	// with it, it follows every record that gives a need.
	if got := recordIDs(plain.Records)[:5]; !slices.Equal(got, []string{"a0", "a2", "a3", "b1", "doc-00001"}) {
		t.Fatal(got)
	}
	if got := recordIDs(view.Records)[:5]; !slices.Equal(got, []string{"a2", "a3", "b1", "doc-00001", "a0"}) {
		t.Fatal(got)
	}
	// The launch day has evidence in the View now; one need was answered.
	if plain.Needs[1].HasEvidence || !view.Needs[1].HasEvidence || view.Stats.ExpandAnsweredNeeds != 1 || plain.Stats.ExpandAnsweredNeeds != 0 || view.Stats.DecisionCalls != plain.Stats.DecisionCalls+1 {
		t.Fatal(plain.Needs, view.Needs, view.Stats, plain.Stats)
	}
	// With every need covered, target asks nothing and reads as without it.
	one := needsRead(t, s, "NEED: the codename", &ViewConfig{Expand: &expand}, judge())
	same := needsRead(t, s, "NEED: the codename", &ViewConfig{Expand: &targeted}, judge())
	if !slices.Equal(recordIDs(same.Records), recordIDs(one.Records)) || same.Stats.DecisionCalls != one.Stats.DecisionCalls || same.Stats.ExpandAnsweredNeeds != 0 {
		t.Fatal(recordIDs(same.Records), recordIDs(one.Records), same.Stats)
	}
	// An ALL need is never covered: the expansions (a3 is one) are asked it.
	j = judge()
	needsRead(t, s, "ALL: every launch", &ViewConfig{Expand: &targeted}, j)
	if !slices.ContainsFunc(j.calls, func(call judgeCall) bool {
		return call.State.Rubric.Question == needQuestion && len(call.State.Needs) == 1 && call.State.Needs[0].ID == "q0" && call.State.Needs[0].Need == "every launch" &&
			slices.ContainsFunc(call.State.Candidates, func(c struct{ ID, Source, Text string }) bool {
				return c.Text == "Melanie: launch window opens Friday at dawn"
			})
	}) {
		t.Fatal("ALL need not asked")
	}
}

func TestExpansionTargetWithinTheBudget(t *testing.T) {
	ctx := context.Background()
	needAsked := func(judge *scriptedJudge) bool {
		return slices.ContainsFunc(judge.calls, func(call judgeCall) bool { return call.State.Rubric.Question == needQuestion })
	}
	setup := func(judge *scriptedJudge) (*simpleRun, []*simpleItem) {
		r, scored := expansionRunWith(t, judge, &ExpandConfig{Kinds: []string{"temporal"}, Limit: 64, Target: "needs"})
		// The seed gives the codename; nothing gives the launch day.
		r.needs = []Need{{Text: "the codename"}, {Text: "the launch day"}}
		scored[0].Row = []float64{0.9, 0.1}
		return r, scored
	}
	// Fifty candidates: three need-table calls and two judgement calls.
	judge := needJudge(byWords(map[string]float64{"ORBIT": 0.9, "launch": 0.9}))
	r, scored := setup(judge)
	r.stats.DecisionCalls = simpleDecisionBudget - 5
	got, err := r.expandEvidence(ctx, scored, stopGivenUp)
	if err != nil || len(got) != 51 || !needAsked(judge) || judge.count() != 5 || r.stats.ExpandAnsweredNeeds != 1 || r.stats.Degraded != nil || got[1].Row[1] != 0.9 {
		t.Fatal(len(got), err, judge.count(), r.stats)
	}
	// One call short: the judgement goes first, the need table is skipped
	// and the read degraded.
	judge = needJudge(byWords(map[string]float64{"ORBIT": 0.9, "launch": 0.9}))
	r, scored = setup(judge)
	r.stats.DecisionCalls = simpleDecisionBudget - 4
	got, err = r.expandEvidence(ctx, scored, stopGivenUp)
	if err != nil || len(got) != 51 || needAsked(judge) || judge.count() != 2 || r.stats.ExpandAnsweredNeeds != 0 || !slices.Equal(r.stats.Degraded, []string{"expansion"}) || got[1].Row != nil {
		t.Fatal(len(got), err, judge.count(), r.stats)
	}
	// A need table failing at its second batch degrades the same way: no
	// candidate keeps a row, and the judgement still runs.
	failing := needJudge(byWords(map[string]float64{"ORBIT": 0.9, "launch": 0.9}))
	r, scored = setup(failing)
	r.agent.models.Judge = decisionFunc(func(ctx context.Context, state any, questions map[string]Question) (map[string]float64, Usage, error) {
		if raw, _ := json.Marshal(state); strings.Contains(string(raw), needQuestion) && strings.Contains(string(raw), "launch note 24") {
			return nil, Usage{}, errors.New("judge down")
		}
		return failing.Decide(ctx, state, questions)
	})
	got, err = r.expandEvidence(ctx, scored, stopGivenUp)
	if err != nil || len(got) != 51 || r.stats.ExpandAnsweredNeeds != 0 || !slices.Equal(r.stats.Degraded, []string{"expansion"}) || got[1].Row != nil {
		t.Fatal(len(got), err, r.stats)
	}
}
