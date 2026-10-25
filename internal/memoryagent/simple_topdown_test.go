package memoryagent

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/records"
)

func TestTopDownRecallsWhatTheQuestionsTopicsCite(t *testing.T) {
	ctx := context.Background()
	s, dir := agentStore(t)
	seedNote(t, s, "flights", "we booked the flights for the trip")
	seedNote(t, s, "hotel", "hotel near old station")
	seedNote(t, s, "pooled", "trip budget is fixed")
	seedNote(t, s, "taxes", "tax forms are due")
	model := &limitPlanner{reply: func(_, prompt string) (string, error) { return layeredReply(prompt), nil }}
	a, overlay := layeredAgent(t, s, dir, ConsolidationConfig{}, model, topicJudge())
	a.config.View = &ViewConfig{MaxItems: 100, TopDown: true}
	stored := emptyTimelineState()
	stored.Mode = "layered"
	stored.Memory.Threads = []thread{{ID: "T1", Title: "Rome trip"}, {ID: "T2", Title: "Taxes"}}
	stored.Memory.Events = []event{
		{Thread: "T1", Date: "2024/03/01", Text: "Booked flights to Rome.", Records: []string{"pooled", "hotel", "flights"}},
		{Thread: "T2", Date: "2024/03/02", Text: "Tax forms due in April.", Records: []string{"taxes"}},
	}
	if _, err := overlay.saveTimelines(ctx, records.Scope{}, "", stored); err != nil {
		t.Fatal(err)
	}
	revision, _, err := s.Heads(ctx, "", records.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	r := testRun(a, user("Current date: 2024/05/01\nWhat did we plan for the Rome trip?"))
	r.revision = revision
	got, err := r.topDown(ctx, []string{"pooled"})
	// The Rome trip topic cites three records: the pooled one is left out,
	// the one the question's words rank comes first, and the one they do not
	// rank (no word in common) follows instead of being dropped. The taxes
	// topic shares no word with the question.
	if err != nil || !slices.Equal(got, []string{"flights", "hotel"}) {
		t.Fatal(got, err)
	}
}

func TestReadSimpleTopDownPoolsTheLaunchRecord(t *testing.T) {
	s, overlay := layerStore(t)
	ctx := context.Background()
	// The cue searches look for the codename; only the topics bring the
	// launch record, which shares no word with them.
	a := layerAgent(t, s, overlay, Config{View: &ViewConfig{MaxItems: 10, TopDown: true}}, codenamePlanner("NEED: the codename"), layerJudge())
	view, err := a.Read(ctx, ReadRequest{Question: "What is the codename?"})
	if err != nil {
		t.Fatal(err)
	}
	if view.Stats.TopDown < 1 || !slices.Contains(recordIDs(view.Records), "launch") || len(view.Stats.Degraded) != 0 {
		t.Fatal(view.Stats.TopDown, recordIDs(view.Records), view.Stats.Degraded)
	}
}

func TestTopDownNeedsATimelinesConsolidation(t *testing.T) {
	s, _ := agentStore(t)
	_, err := New(s, Config{Version: 1, Strategy: "simple", View: &ViewConfig{TopDown: true}}, Models{Planner: replies("", "", ""), Judge: &scriptedJudge{}}, nil)
	if err == nil || !strings.Contains(err.Error(), "view.topDown requires") {
		t.Fatal("topDown accepted without a consolidation:", err)
	}
	// The server checks a config with an agent that has no overlay: that
	// agent is valid, and its reads recall nothing top-down.
	model := &limitPlanner{reply: func(_, prompt string) (string, error) { return layeredReply(prompt), nil }}
	a, err := New(s, Config{Version: 1, Strategy: "simple", View: &ViewConfig{TopDown: true}, Consolidation: &ConsolidationConfig{Mode: "layered"}}, Models{Planner: replies("", "", ""), Consolidator: model, Judge: topicJudge()}, nil)
	if err != nil {
		t.Fatal("topDown refused an agent without an overlay:", err)
	}
	if got, err := testRun(a, user("anything")).topDown(context.Background(), nil); err != nil || got != nil {
		t.Fatal(got, err)
	}
}
