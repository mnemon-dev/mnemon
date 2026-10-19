package memoryagent

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

// The stages of a simple read that fell back are named in its stats, so a
// partially degraded read is visible.

// failingJudge answers like layerJudge but fails the calls fail picks.
func failingJudge(fail func(raw string, questions map[string]Question) bool) Decider {
	judge := layerJudge()
	return decisionFunc(func(ctx context.Context, state any, questions map[string]Question) (map[string]float64, Usage, error) {
		raw, _ := json.Marshal(state)
		if fail(string(raw), questions) {
			return nil, Usage{}, errors.New("judge down")
		}
		return judge.Decide(ctx, state, questions)
	})
}

func TestReadSimpleNamesDegradedStages(t *testing.T) {
	s, overlay := layerStore(t)
	ctx := context.Background()
	question := ReadRequest{Question: "What is the codename?"}
	read := func(planner Completer, judge Decider) View {
		t.Helper()
		view, err := layerAgent(t, s, overlay, Config{View: &ViewConfig{MaxItems: 6}}, planner, judge).Read(ctx, question)
		if err != nil {
			t.Fatal(err)
		}
		return view
	}
	if view := read(codenamePlanner("NEED: the codename"), layerJudge()); view.Stats.Degraded != nil {
		t.Fatal(view.Stats.Degraded)
	}
	if raw, _ := json.Marshal(ReadStats{}); strings.Contains(string(raw), "degraded") {
		t.Fatal(string(raw))
	}
	// The judgement (its calls ask s<i>) and the layer (topic candidates).
	judgeAndLayer := failingJudge(func(raw string, questions map[string]Question) bool {
		_, judgement := questions["s0"]
		return judgement || strings.Contains(raw, `"source":"topic"`)
	})
	if view := read(codenamePlanner("NEED: the codename"), judgeAndLayer); !slices.Equal(view.Stats.Degraded, []string{"judge", "layer"}) || len(view.Index) == 0 {
		t.Fatal(view.Stats.Degraded, view.Index)
	}
	// The expansion: needed questions about records, without a rubric. A
	// plan with no need expands to the linked launch record.
	expansion := failingJudge(func(raw string, questions map[string]Question) bool {
		_, judgement := questions["s0"]
		return !judgement && !strings.Contains(raw, `"rubric"`) && strings.Contains(raw, `"source":"journal"`)
	})
	if view := read(codenamePlanner("nothing stored is needed"), expansion); !slices.Equal(view.Stats.Degraded, []string{"expand"}) || slices.Contains(recordIDs(view.Records), "launch") {
		t.Fatal(view.Stats.Degraded, recordIDs(view.Records))
	}
	// A state that does not load.
	if _, err := overlay.db.Exec(`UPDATE timelines SET payload=payload||' '`); err != nil {
		t.Fatal(err)
	}
	if view := read(codenamePlanner("NEED: the codename"), layerJudge()); !slices.Equal(view.Stats.Degraded, []string{"layer-state"}) || view.Index != nil || len(view.Records) != 6 {
		t.Fatal(view.Stats.Degraded, view.Index)
	}
}

func TestSimpleBudgetStopsAreDegraded(t *testing.T) {
	s, _ := agentStore(t)
	seedNote(t, s, "answer", "codename ORBIT")
	seedNotes(t, s, "codename", 5)
	judge := &scriptedJudge{answer: func(text string, _ int) float64 { return 0.2 }}
	// The pool's screen with no decision call left.
	r, pool := poolRun(t, judgedAgent(t, s, replies("", "", "never"), judge), []Need{{Text: "the codename"}}, cue{"codename", "search"})
	r.stats.DecisionCalls = simpleDecisionBudget
	if err := r.screen(context.Background(), pool); err != nil || !slices.Equal(r.stats.Degraded, []string{"scan-budget"}) || judge.count() != 0 {
		t.Fatal(err, r.stats.Degraded, judge.count())
	}
	// The loop's table with none left.
	r, pool = poolRun(t, judgedAgent(t, s, replies("", "", "never"), judge), []Need{{Text: "the codename"}, {Text: "the launch"}}, cue{"codename", "search"})
	if err := r.screen(context.Background(), pool); err != nil || r.stats.Degraded != nil {
		t.Fatal(err, r.stats.Degraded)
	}
	r.stats.DecisionCalls = simpleDecisionBudget
	if stop, err := r.loop(context.Background()); err != nil || stop != stopRounds || !slices.Equal(r.stats.Degraded, []string{"loop-budget"}) {
		t.Fatal(stop, err, r.stats.Degraded)
	}
	// A stage is named once.
	r.degrade("loop-budget")
	if !slices.Equal(r.stats.Degraded, []string{"loop-budget"}) {
		t.Fatal(r.stats.Degraded)
	}
}
