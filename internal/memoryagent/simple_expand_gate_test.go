package memoryagent

import (
	"context"
	"encoding/json"
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
	good := ExpandConfig{Kinds: []string{"adjacent"}, Gate: "needs"}
	a, err := New(s, Config{Version: 1, Strategy: "simple", View: &ViewConfig{Expand: &good}}, both, nil)
	if err != nil || a.config.View.Expand.Gate != "needs" {
		t.Fatal(a, err)
	}
	for _, bad := range []ExpandConfig{{Kinds: []string{"adjacent"}, Gate: "always"}, {Kinds: []string{"adjacent"}, Gate: "Needs"}} {
		if _, err = New(s, Config{Version: 1, Strategy: "simple", View: &ViewConfig{Expand: &bad}}, both, nil); err == nil {
			t.Error("invalid expansion accepted", bad)
		}
	}
	// Absent, it stays out of the config's JSON (and the policy).
	raw, _ := json.Marshal(ExpandConfig{Kinds: []string{"adjacent"}, Limit: 24, Seeds: 16, AdjacentWindow: 1})
	if strings.Contains(string(raw), "gate") {
		t.Fatal(string(raw))
	}
	decoder := json.NewDecoder(strings.NewReader(`{"version":1,"strategy":"simple","view":{"expand":{"kinds":["adjacent"],"gate":"needs"}}}`))
	decoder.DisallowUnknownFields()
	var decoded Config
	if err = decoder.Decode(&decoded); err != nil || !reflect.DeepEqual(*decoded.View.Expand, ExpandConfig{Kinds: []string{"adjacent"}, Gate: "needs"}) {
		t.Fatal(decoded, err)
	}
	if raw, _ := json.Marshal(ReadStats{}); strings.Contains(string(raw), "expandGated") {
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
