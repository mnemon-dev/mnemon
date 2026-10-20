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
	good := ExpandConfig{Kinds: []string{"adjacent"}, Gate: "needs", Target: "needs", Reserve: 8}
	a, err := New(s, Config{Version: 1, Strategy: "simple", View: &ViewConfig{Expand: &good}}, both, nil)
	if err != nil || a.config.View.Expand.Gate != "needs" || a.config.View.Expand.Target != "needs" || a.config.View.Expand.Reserve != 8 {
		t.Fatal(a, err)
	}
	for _, bad := range []ExpandConfig{
		{Kinds: []string{"adjacent"}, Gate: "always"}, {Kinds: []string{"adjacent"}, Gate: "Needs"},
		{Kinds: []string{"adjacent"}, Target: "need"}, {Kinds: []string{"adjacent"}, Reserve: 9}, {Kinds: []string{"adjacent"}, Reserve: -1},
	} {
		if _, err = New(s, Config{Version: 1, Strategy: "simple", View: &ViewConfig{Expand: &bad}}, both, nil); err == nil {
			t.Error("invalid expansion accepted", bad)
		}
	}
	// Absent, they stay out of the config's JSON (and the policy).
	raw, _ := json.Marshal(ExpandConfig{Kinds: []string{"adjacent"}, Limit: 24, Seeds: 16, AdjacentWindow: 1})
	if strings.Contains(string(raw), "gate") || strings.Contains(string(raw), "target") || strings.Contains(string(raw), "reserve") {
		t.Fatal(string(raw))
	}
	decoder := json.NewDecoder(strings.NewReader(`{"version":1,"strategy":"simple","view":{"expand":{"kinds":["adjacent"],"gate":"needs","target":"needs","reserve":4}}}`))
	decoder.DisallowUnknownFields()
	var decoded Config
	if err = decoder.Decode(&decoded); err != nil || !reflect.DeepEqual(*decoded.View.Expand, ExpandConfig{Kinds: []string{"adjacent"}, Gate: "needs", Target: "needs", Reserve: 4}) {
		t.Fatal(decoded, err)
	}
	if raw, _ := json.Marshal(ReadStats{}); strings.Contains(string(raw), "expandGated") || strings.Contains(string(raw), "expandAnsweredNeeds") || strings.Contains(string(raw), "expandReserved") {
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
	// Reserved slots follow the target order: with two slots and one held,
	// the held one is a3, not the greeting.
	reserve := targeted
	reserve.Reserve = 1
	if got := recordIDs(needsRead(t, s, needs, &ViewConfig{MaxItems: 2, Expand: &reserve}, judge()).Records); !slices.Equal(got, []string{"a2", "a3"}) {
		t.Fatal(got)
	}
	untargeted := expand
	untargeted.Reserve = 1
	if got := recordIDs(needsRead(t, s, needs, &ViewConfig{MaxItems: 2, Expand: &untargeted}, judge()).Records); !slices.Equal(got, []string{"a0", "a2"}) {
		t.Fatal(got)
	}
}

func TestExpansionReserve(t *testing.T) {
	s := expandFixture(t)
	// JEV needs a2 (0.99); the pooled a1 (0.45) and doc-00000 (0.44) beat
	// the expansions a3 (0.3), doc-00001 (0.29) and b1 (0.28) on merit.
	judge := func(a1 float64) *scriptedJudge {
		return needJudge(byWords(map[string]float64{"ORBIT": 0.99, "picked a codename": a1, "codename review": 0.44, "launch window": 0.3, "launch checklist": 0.29, "launch moved": 0.28}))
	}
	read := func(view *ViewConfig, a1 float64) View { return needsRead(t, s, "NEED: the codename", view, judge(a1)) }
	expand := func(reserve int) *ExpandConfig {
		return &ExpandConfig{Kinds: []string{"adjacent", "temporal", "causal"}, Reserve: reserve}
	}
	// The needed record first, then the held expansions (at most half the
	// View), then the fill; listed in compose's order.
	for _, tc := range []struct {
		maxItems, reserve, held int
		want                    []string
	}{
		{3, 0, 0, []string{"a2", "a1", "doc-00000"}},
		{3, 1, 1, []string{"a2", "a1", "a3"}},
		{3, 8, 1, []string{"a2", "a1", "a3"}},
		{4, 0, 0, []string{"a2", "a1", "doc-00000", "a3"}},
		{4, 2, 2, []string{"a2", "a1", "a3", "doc-00001"}},
	} {
		view := read(&ViewConfig{MaxItems: tc.maxItems, Expand: expand(tc.reserve)}, 0.45)
		if got := recordIDs(view.Records); !slices.Equal(got, tc.want) || view.Stats.ExpandReserved != tc.held {
			t.Fatal(tc.maxItems, tc.reserve, got, view.Stats)
		}
		if view.Records[len(view.Records)-1].Score != judge(0.45).answer(view.Records[len(view.Records)-1].Text, -1) {
			t.Fatal(view.Records)
		}
	}
	// A held slot never displaces a needed record that is not an
	// expansion: with a1 needed too, two slots stay the evidence's.
	if view := read(&ViewConfig{MaxItems: 2, Expand: expand(8)}, 0.98); !slices.Equal(recordIDs(view.Records), []string{"a2", "a1"}) || view.Stats.ExpandReserved != 0 || !view.Needs[0].HasEvidence {
		t.Fatal(recordIDs(view.Records), view.Stats)
	}
	// No expansion that could be shown: the slots go back to the
	// composition.
	stale := func() *scriptedJudge {
		j := judge(0.45)
		j.superseded = func(text string) float64 {
			if strings.Contains(text, "launch") {
				return 0.9
			}
			return 0
		}
		return j
	}
	plain := needsRead(t, s, "NEED: the codename", &ViewConfig{MaxItems: 3, Expand: expand(0)}, stale())
	held := needsRead(t, s, "NEED: the codename", &ViewConfig{MaxItems: 3, Expand: expand(1)}, stale())
	if !reflect.DeepEqual(held.Records, plain.Records) || held.Stats.ExpandReserved != 0 {
		t.Fatal(recordIDs(held.Records), recordIDs(plain.Records))
	}
	// With fusion: the needed record and the held expansion take their
	// slots first; the View lists the others in fused order, then the held.
	fused := read(&ViewConfig{MaxItems: 3, Fusion: "rrf", Expand: expand(1)}, 0.45)
	if got := recordIDs(fused.Records); len(got) != 3 || got[0] != "a2" || got[2] != "a3" || fused.Stats.ExpandReserved != 1 {
		t.Fatal(got, fused.Stats)
	}
}

// The timelines layer's character trim keeps the needed records, then the
// held ones, then the others; expandReserved counts the held records the
// final View shows.
func TestExpansionReserveSurvivesTheLayer(t *testing.T) {
	s, overlay := eventsStore(t)
	judge := func() *scriptedJudge {
		return needJudge(byWords(map[string]float64{"ORBIT": 0.99, "picked a codename": 0.45, "codename review": 0.44, "launch window": 0.3, "launch checklist": 0.29, "launch moved": 0.28}))
	}
	read := func(reserve, chars int) View {
		view := &ViewConfig{MaxItems: 5, MaxCharacters: chars, Expand: &ExpandConfig{Kinds: []string{"adjacent", "temporal", "causal"}, Reserve: reserve}}
		got, err := layerAgent(t, s, overlay, Config{View: view}, replies("codename project\nNOTE: the codename is ORBIT", "NEED: the codename", "codename rocket"), judge()).Read(context.Background(), ReadRequest{Question: "What is the codename?"})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	// 584 characters: the blocks leave room for three records.
	if got := recordIDs(read(0, 584).Records); !slices.Equal(got, []string{"a2", "a1", "doc-00000"}) {
		t.Fatal(got)
	}
	if view := read(2, 584); !slices.Equal(recordIDs(view.Records), []string{"a2", "a3", "doc-00001"}) || view.Stats.ExpandReserved != 2 || view.Stats.ExpandShown != 2 || len(view.Index) == 0 {
		t.Fatal(recordIDs(view.Records), view.Stats)
	}
	// Room for two: a held record gives way, never the needed one, and the
	// count follows.
	if view := read(2, 546); !slices.Equal(recordIDs(view.Records), []string{"a2", "a3"}) || view.Stats.ExpandReserved != 1 {
		t.Fatal(recordIDs(view.Records), view.Stats)
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
	// and the read degraded (expansion-target).
	judge = needJudge(byWords(map[string]float64{"ORBIT": 0.9, "launch": 0.9}))
	r, scored = setup(judge)
	r.stats.DecisionCalls = simpleDecisionBudget - 4
	got, err = r.expandEvidence(ctx, scored, stopGivenUp)
	if err != nil || len(got) != 51 || needAsked(judge) || judge.count() != 2 || r.stats.ExpandAnsweredNeeds != 0 || !slices.Equal(r.stats.Degraded, []string{"expansion-target"}) || got[1].Row != nil {
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
	if err != nil || len(got) != 51 || r.stats.ExpandAnsweredNeeds != 0 || !slices.Equal(r.stats.Degraded, []string{"expansion-target"}) || got[1].Row != nil {
		t.Fatal(len(got), err, r.stats)
	}
}

// The gate reads the coverage of the View compose would show without the
// expansion: under fusion a need whose record is not shown is not covered
// (the stage 4b review's probe), and with the timelines layer only the
// records its character trim can never drop count.
func TestExpansionGateReadsTheView(t *testing.T) {
	s := expandFixture(t)
	judge := func() *scriptedJudge {
		return &scriptedJudge{answer: func(text string, _ int) float64 {
			switch {
			case strings.Contains(text, "ORBIT"):
				return 0.9
			case strings.Contains(text, "launch"):
				return 0.8
			}
			return 0.3
		}}
	}
	gated := func(maxItems int) View {
		planner := replies("codename project\nNOTE: the codename is ORBIT", "NEED: the codename", "codename rocket")
		a, err := New(s, Config{Version: 1, Strategy: "simple", View: &ViewConfig{MaxItems: maxItems, Fusion: "rrf", Expand: &ExpandConfig{Kinds: []string{"adjacent", "temporal", "causal"}, Gate: "needs"}}}, Models{Planner: planner, Judge: judge()}, nil)
		if err != nil {
			t.Fatal(err)
		}
		view, err := a.Read(context.Background(), ReadRequest{Question: "What was the rocket project called?"})
		if err != nil {
			t.Fatal(err)
		}
		return view
	}
	// One slot: the fused order shows a1, which does not give the codename.
	if view := gated(1); view.StopReason != "covered" || view.Stats.ExpandGated || view.Stats.Expanded == 0 {
		t.Fatal(recordIDs(view.Records), view.Stats)
	}
	// Two slots show a2 as well: covered, so gated.
	if view := gated(2); !view.Stats.ExpandGated || !view.Needs[0].HasEvidence {
		t.Fatal(recordIDs(view.Records), view.Stats)
	}
	// With the layer, a2 (31 characters) counts while the View's characters
	// less the most the blocks can take leave room for it.
	events, overlay := eventsStore(t)
	layered := func(chars int) View {
		planner := replies("codename project\nNOTE: the codename is ORBIT", "NEED: the codename", "codename rocket")
		view := &ViewConfig{MaxCharacters: chars, Expand: &ExpandConfig{Kinds: []string{"adjacent"}, Gate: "needs"}}
		got, err := layerAgent(t, events, overlay, Config{View: view}, planner, judge()).Read(context.Background(), ReadRequest{Question: "What is the codename?"})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if view := layered(layerBlocksMax + 31); !view.Stats.ExpandGated {
		t.Fatal(view.Stats)
	}
	if view := layered(layerBlocksMax + 30); view.Stats.ExpandGated {
		t.Fatal(view.Stats)
	}
}

// The pivot of target's order is the last needed, current item that
// answers a need: a fill record that answers one does not pull the
// expansions behind it (the stage 4b review's probe).
func TestOrderedPivotIsANeededItem(t *testing.T) {
	r := &simpleRun{targeted: true}
	item := func(id string, needed float64, row []float64, expanded bool) *simpleItem {
		return &simpleItem{ID: id, Needed: needed, Row: row, Expanded: expanded, Judged: true}
	}
	scored := []*simpleItem{
		item("p-needed", 0.9, []float64{0.9, 0}, false),
		item("p-fill1", 0.3, []float64{0.1, 0}, false),
		item("p-fill2", 0.2, []float64{0.8, 0}, false),
		item("x-greeting", 0.95, []float64{0, 0.1}, true),
		item("x-answers", 0.6, []float64{0, 0.9}, true),
	}
	if got := itemIDs(r.ordered(scored)); !slices.Equal(got, []string{"p-needed", "x-answers", "x-greeting", "p-fill1", "p-fill2"}) {
		t.Fatal(got)
	}
	// An answering item no longer current is no pivot either: p-needed is.
	scored[4].Superseded = 0.9
	if got := itemIDs(r.ordered(scored)); !slices.Equal(got, []string{"p-needed", "x-greeting", "x-answers", "p-fill1", "p-fill2"}) {
		t.Fatal(got)
	}
}

// expandAnsweredNeeds counts the answers of current expansions only (the
// stage 4b review's probe): b1 answers the launch day but was moved.
func TestExpansionAnsweredNeedsAreCurrent(t *testing.T) {
	s := expandFixture(t)
	judge := func(moved float64) *scriptedJudge {
		return &scriptedJudge{
			answer: func(text string, need int) float64 {
				switch {
				case need < 0 && (strings.Contains(text, "ORBIT") || strings.Contains(text, "launch moved")):
					return 0.9
				case need < 0:
					return 0.2
				case need == 0 && strings.Contains(text, "ORBIT"), need == 1 && strings.Contains(text, "moved"):
					return 0.9
				}
				return 0.1
			},
			superseded: func(text string) float64 {
				if strings.Contains(text, "moved") {
					return moved
				}
				return 0
			},
		}
	}
	targeted := &ViewConfig{Expand: &ExpandConfig{Kinds: []string{"adjacent", "temporal", "causal"}, Target: "needs"}}
	needs := "NEED: the codename\nNEED: the launch day"
	if view := needsRead(t, s, needs, targeted, judge(0)); view.Stats.ExpandAnsweredNeeds != 1 || !view.Needs[1].HasEvidence {
		t.Fatal(view.Stats, view.Needs)
	}
	if view := needsRead(t, s, needs, targeted, judge(0.7)); view.Stats.ExpandAnsweredNeeds != 0 || view.Needs[1].HasEvidence {
		t.Fatal(view.Stats, view.Needs)
	}
}
