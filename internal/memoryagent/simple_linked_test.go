package memoryagent

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/records"
)

// linkNotes gives each note of from a consolidation link to the note of to.
func linkNotes(t *testing.T, s *records.Store, pairs map[string]string) {
	t.Helper()
	items := map[string]records.DerivedMetadata{}
	for from, to := range pairs {
		items[from] = records.DerivedMetadata{Version: 1, Links: []records.Link{{ID: to, Version: 1, Type: "temporal", Weight: 0.8}}}
	}
	if err := s.ReplaceDerivedMetadata(context.Background(), "consolidation", items); err != nil {
		t.Fatal(err)
	}
}

// linkedRun is a pooled and screened run of the linked loop over cue, which
// follows the consolidation links (linkNotes).
func linkedRun(t *testing.T, s *records.Store, planner Completer, judge Decider, needs []Need, c cue) *simpleRun {
	t.Helper()
	a := judgedAgent(t, s, planner, judge)
	a.config.View = &ViewConfig{MaxItems: 100, LoopLinks: true}
	a.config.Consolidation = &ConsolidationConfig{NativeEdges: true}
	r, pool := poolRun(t, a, needs, c)
	revision, _, err := s.Heads(context.Background(), "", records.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	r.revision = revision
	if err := r.scan(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	return r
}

// twoNeeds are tabled one by one (one single need would stand on the screen).
var twoNeeds = []Need{{Text: "the codename"}, {Text: "the clue"}}

// clueJudge screens the clue and the answer useful; the answer gives need 0,
// the clue need 1.
func clueJudge() *scriptedJudge {
	return &scriptedJudge{answer: func(text string, need int) float64 {
		orbit, clue := strings.Contains(text, "ORBIT"), strings.Contains(text, "clue")
		switch {
		case need < 0 && orbit, need == 0 && orbit, need == 1 && clue:
			return 0.9
		case need < 0 && clue:
			return 0.7
		}
		return 0.1
	}}
}

func TestLinkedFreshFollowsConsolidationLinksOnce(t *testing.T) {
	s, _ := agentStore(t)
	seedNote(t, s, "clue", "codename clue")
	seedNote(t, s, "answer", "it is ORBIT")
	seedNote(t, s, "other", "unrelated")
	linkNotes(t, s, map[string]string{"clue": "answer"})
	r := linkedRun(t, s, replies("", "", ""), clueJudge(), twoNeeds, cue{"codename", "search"})
	expanded := map[string]bool{}
	got, err := r.linkedFresh(context.Background(), []string{"clue"}, expanded)
	if err != nil || !slices.Equal(got, []string{"answer"}) || !expanded["clue"] {
		t.Fatal(got, err, expanded)
	}
	if again, err := r.linkedFresh(context.Background(), []string{"clue"}, expanded); err != nil || len(again) != 0 {
		t.Fatal("followed twice", again, err)
	}
}

func TestLinkedFreshFollowsEachSourceOnlyWithItsSwitch(t *testing.T) {
	s, dir := agentStore(t)
	seedNote(t, s, "clue", "codename clue")
	seedNote(t, s, "answer", "it is ORBIT")
	seedNote(t, s, "near", "a note")
	linkNotes(t, s, map[string]string{"clue": "near"})
	overlay, err := OpenOverlay(filepath.Join(dir, "memory-index.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { overlay.Close() })
	// The answer chose the clue: a one-shot link, followed either way.
	if err := overlay.saveLinks(context.Background(), records.Scope{}, 3, []recordLink{{ID: "answer", Position: 3, Target: "clue", TargetPosition: 1, P: 0.9}}); err != nil {
		t.Fatal(err)
	}
	r := linkedRun(t, s, replies("", "", ""), clueJudge(), twoNeeds, cue{"codename", "search"})
	r.agent.overlay = overlay
	for _, c := range []struct {
		native, shot bool
		want         []string
	}{{true, false, []string{"near"}}, {false, true, []string{"answer"}}, {true, true, []string{"near", "answer"}}, {false, false, nil}} {
		r.agent.config.Consolidation = &ConsolidationConfig{NativeEdges: c.native, OneShotLinks: c.shot}
		got, err := r.linkedFresh(context.Background(), []string{"clue"}, map[string]bool{})
		if err != nil || !slices.Equal(got, c.want) {
			t.Fatal(c.native, c.shot, got, err)
		}
	}
}

func TestLinkedLoopFollowsALinkToTheAnswer(t *testing.T) {
	s, _ := agentStore(t)
	seedNote(t, s, "clue", "codename clue")
	seedNote(t, s, "answer", "it is ORBIT")
	linkNotes(t, s, map[string]string{"clue": "answer"})
	planner := replies("", "", "nothing-matches-this")
	r := linkedRun(t, s, planner, clueJudge(), twoNeeds, cue{"codename", "search"})
	stop, err := r.loop(context.Background())
	// Round 1 rewrites for the open need (its search finds nothing) and
	// follows the clue's link; the answer is screened and tabled, and the
	// need is met before round 2.
	if err != nil || stop != stopCovered || r.rounds != 1 || r.stats.LoopLinked != 1 || len(planner.calls) != 1 {
		t.Fatal(stop, err, r.rounds, r.stats.LoopLinked, len(planner.calls))
	}
	if answer := r.items["answer"]; answer == nil || !answer.HasScreen || !slices.Contains(r.yes(0), "answer") {
		t.Fatal("the linked answer was not judged")
	}
	if r.pageReads != 2 {
		t.Fatal("the linked loop paged:", r.pageReads)
	}
}

func TestLinkedLoopRewriteFailureEndsTheLoopNotTheRead(t *testing.T) {
	s, _ := agentStore(t)
	seedNote(t, s, "clue", "codename clue")
	planner := &limitPlanner{reply: func(system, prompt string) (string, error) {
		if strings.Contains(prompt, "NOT FOUND YET") {
			return "", errors.New("planner down")
		}
		return "", nil
	}}
	r := linkedRun(t, s, planner, clueJudge(), twoNeeds, cue{"codename", "search"})
	stop, err := r.loop(context.Background())
	if err != nil || stop != stopRewrite || !slices.Contains(r.stats.Degraded, "loop-rewrite") {
		t.Fatal(stop, err, r.stats.Degraded)
	}
}

func TestLinkedLoopKeepsTheJudgeReserve(t *testing.T) {
	s, _ := agentStore(t)
	seedNote(t, s, "clue", "codename clue")
	judge := clueJudge()
	planner := replies("", "", "codename")
	r := linkedRun(t, s, planner, judge, twoNeeds, cue{"codename", "search"})
	r.stats.DecisionCalls = simpleDecisionBudget - loopJudgeReserve
	stop, err := r.loop(context.Background())
	// The pool's table takes a call of the reserve; nothing is rewritten or
	// followed.
	if err != nil || stop != stopReserve || len(planner.calls) != 0 || r.rounds != 0 {
		t.Fatal(stop, err, len(planner.calls), r.rounds)
	}
}

func TestLinkedLoopStopsWhenARoundBringsNothingUseful(t *testing.T) {
	s, _ := agentStore(t)
	seedNote(t, s, "clue", "codename clue")
	seedNotes(t, s, "zebra", 3)
	// The rewrite finds zebras, which JEV finds useless: round 2 stops.
	r := linkedRun(t, s, replies("", "", "zebra"), clueJudge(), twoNeeds, cue{"codename", "search"})
	stop, err := r.loop(context.Background())
	if err != nil || stop != stopNothingNew || r.rounds != 1 {
		t.Fatal(stop, err, r.rounds)
	}
}
