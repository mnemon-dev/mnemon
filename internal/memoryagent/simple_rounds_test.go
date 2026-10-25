package memoryagent

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/records"
)

// roundPlanner plans the Rome searches, then the museum and the hotel; it
// writes the same again when asked for more.
func roundPlanner() *limitPlanner {
	return &limitPlanner{reply: func(_, prompt string) (string, error) {
		if strings.Contains(prompt, "RECORDS FOUND SO FAR") {
			return "SEARCH: museum tickets\nSEARCH: hotel station", nil
		}
		return "SEARCH: flights Rome\nNEED: the trip plan", nil
	}}
}

func seedTrip(t *testing.T, s *records.Store) {
	t.Helper()
	seedNote(t, s, "flights", "we booked the flights to Rome")
	seedNote(t, s, "museum", "museum tickets for Rome")
	seedNote(t, s, "hotel", "the hotel near the old station")
	seedNote(t, s, "taxes", "tax forms are due")
}

func TestRoundRecallSelectsUntilNoNewSearch(t *testing.T) {
	s, _ := agentStore(t)
	seedTrip(t, s)
	planner, judge := roundPlanner(), tripJudge()
	a, err := New(s, Config{Version: 1, Strategy: "simple", View: &ViewConfig{RoundRecall: true, MaxItems: 100, MaxCharacters: 240000}}, Models{Planner: planner, Judge: judge}, nil)
	if err != nil {
		t.Fatal(err)
	}
	view, err := a.Read(context.Background(), ReadRequest{Question: "What did we plan for Rome?"})
	if err != nil {
		t.Fatal(err)
	}
	// Round one: the message and "flights Rome" find the flights and the
	// museum, both needed. Round two: the hotel is new and needed. Round three
	// plans the searches already run, so the rounds end.
	got := recordIDs(view.Records)
	slices.Sort(got)
	if !slices.Equal(got, []string{"flights", "hotel", "museum"}) || view.Stats.RecallRounds != 2 || view.StopReason != "planned out" {
		t.Fatal(got, view.Stats.RecallRounds, view.StopReason)
	}
	if len(planner.calls) != 3 || !strings.Contains(planner.calls[1].prompt, "SEARCHES ALREADY RUN:\n- What did we plan for Rome?\n- flights Rome") || !strings.Contains(planner.calls[1].prompt, "museum tickets for Rome") {
		t.Fatal(len(planner.calls), planner.calls[1].prompt)
	}
	if len(view.Needs) != 1 || view.Needs[0].Text != "the trip plan" || len(view.Index) != 0 {
		t.Fatal(view.Needs, view.Index)
	}
}

func TestRoundRecallEndsWhenARoundSelectsNone(t *testing.T) {
	s, _ := agentStore(t)
	seedTrip(t, s)
	// Nothing is needed: the first round selects none and ends the read.
	judge := &scriptedJudge{answer: func(string, int) float64 { return 0.1 }}
	a, err := New(s, Config{Version: 1, Strategy: "simple", View: &ViewConfig{RoundRecall: true, MaxItems: 100, MaxCharacters: 240000}}, Models{Planner: roundPlanner(), Judge: judge}, nil)
	if err != nil {
		t.Fatal(err)
	}
	view, err := a.Read(context.Background(), ReadRequest{Question: "What did we plan for Rome?"})
	if err != nil || len(view.Records) != 0 || view.Stats.RecallRounds != 1 || view.StopReason != "dry" {
		t.Fatal(recordIDs(view.Records), view.Stats.RecallRounds, view.StopReason, err)
	}
}

func TestRoundRecallPlansWithTheRecalledLayer(t *testing.T) {
	ctx := context.Background()
	planner := roundPlanner()
	a, overlay, s := recallAgent(t, ViewConfig{LayerRecall: true, RoundRecall: true}, planner, tripJudge())
	seedTrip(t, s)
	if _, err := overlay.saveTimelines(ctx, records.Scope{}, "", recallState()); err != nil {
		t.Fatal(err)
	}
	revision, _, err := s.Heads(ctx, "", records.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	r := testRun(a, user("What did we plan for the Rome trip?"))
	r.revision, r.req.MaxItems = revision, 100
	view, err := r.readRounds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The planner reads the recalled layer; the View shows it before the
	// selected records.
	if !strings.Contains(planner.calls[0].prompt, "MEMORY NOTES") || !strings.Contains(planner.calls[0].prompt, "Booked flights to Rome.") {
		t.Fatal(planner.calls[0].prompt)
	}
	if !strings.Contains(blockText(view.Index, "3-timelines"), "Booked flights to Rome.") || !slices.Contains(recordIDs(view.Records), "hotel") {
		t.Fatal(view.Index, recordIDs(view.Records))
	}
}
