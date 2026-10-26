package memoryagent

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/records"
)

func TestDeriveLoopAsksAgainForOpenNeeds(t *testing.T) {
	s, _ := agentStore(t)
	seedTrip(t, s)
	planner := &limitPlanner{reply: func(_, prompt string) (string, error) {
		if strings.Contains(prompt, "NEEDS NOT COVERED YET") {
			return "SEARCH: hotel station", nil
		}
		return "SEARCH: flights Rome\nNEED: the flights\nNEED: the hotel", nil
	}}
	judge := &scriptedJudge{answer: func(text string, need int) float64 {
		switch {
		case need == 0 && strings.Contains(text, "flights"), need == 1 && strings.Contains(text, "hotel"):
			return 0.9
		case need < 0 && (strings.Contains(text, "Rome") || strings.Contains(text, "hotel")):
			return 0.9
		}
		return 0.1
	}}
	a, err := New(s, Config{Version: 1, Strategy: "simple", View: &ViewConfig{DeriveLoop: true, MaxItems: 100, MaxCharacters: 240000}}, Models{Planner: planner, Judge: judge}, nil)
	if err != nil {
		t.Fatal(err)
	}
	view, err := a.Read(context.Background(), ReadRequest{Question: "What did we plan for Rome?"})
	if err != nil {
		t.Fatal(err)
	}
	// The first plan finds the flights; the hotel need stays open, so one
	// more round asks for it and covers it.
	if len(planner.calls) != 2 || !strings.Contains(planner.calls[1].prompt, "- NEED: the hotel") || strings.Contains(planner.calls[1].prompt, "- NEED: the flights") {
		t.Fatal(len(planner.calls), planner.calls)
	}
	if got := recordIDs(view.Records); !slices.Contains(got, "hotel") || !slices.Contains(got, "flights") || view.StopReason != stopCovered {
		t.Fatal(got, view.StopReason)
	}
}

func TestLayerRankedFillsFromLitAndFoundEvents(t *testing.T) {
	ctx := context.Background()
	a, overlay, _ := recallAgent(t, ViewConfig{LayerRanked: true}, replies("", "", ""), tripJudge())
	if _, err := overlay.saveTimelines(ctx, records.Scope{}, "", recallState()); err != nil {
		t.Fatal(err)
	}
	r := testRun(a, user("What did we plan for the trip?"))
	r.tried = []string{"hotel station"}
	view, err := r.layerRanked(ctx, View{Records: []Evidence{{Reference: Reference{ID: "flights"}, Text: "we booked the flights to Rome"}}})
	if err != nil {
		t.Fatal(err)
	}
	// The flights event is lit, the hotel event found; both are ranked and
	// shown in time order under their topic, and the taxes are neither.
	timelines := blockText(view.Index, "3-timelines")
	if !strings.Contains(timelines, "Thread: Rome trip\n- 2024/03/01: Booked flights to Rome.\n- 2024/03/05: Picked the hotel") || strings.Contains(timelines, "Tax") {
		t.Fatalf("timelines %q", timelines)
	}
	if !strings.Contains(blockText(view.Index, "2-topics"), "Rome trip (2024/03/01 to 2024/03/05)") || !strings.Contains(blockText(view.Index, "1-instructions"), "bullet points") {
		t.Fatalf("blocks %+v", view.Index)
	}
	if len(view.Records) != 1 {
		t.Fatal(view.Records)
	}
}
