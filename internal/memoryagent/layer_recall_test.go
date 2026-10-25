package memoryagent

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/records"
)

// recallState is a consolidation of a Rome trip, taxes, a budget and a
// directive.
func recallState() timelineState {
	state := emptyTimelineState()
	state.Mode = "layered"
	state.Memory.Threads = []thread{{ID: "T1", Title: "Rome trip"}, {ID: "T2", Title: "Taxes"}}
	state.Memory.Events = []event{
		{Thread: "T1", Date: "2024/03/01", Text: "Booked flights to Rome.", Records: []string{"flights"}},
		{Thread: "T2", Date: "2024/03/02", Text: "Tax forms are due in April.", Records: []string{"taxes"}},
		{Thread: "T1", Date: "2024/03/05", Text: "Picked the hotel near the old station.", Records: []string{"hotel"}},
	}
	state.Memory.FactOrder = []string{"trip · budget"}
	state.Memory.Facts = map[string][]factValue{"trip · budget": {{Value: "2000 euros", Date: "2024/03/01", Records: []string{"budget"}}}}
	state.Memory.Directives = []directive{{Text: "Always answer in bullet points", Date: "2024/02/01"}}
	return state
}

// tripJudge calls needed what mentions Rome or the hotel.
func tripJudge() *scriptedJudge {
	return &scriptedJudge{answer: func(text string, _ int) float64 {
		if strings.Contains(text, "Rome") || strings.Contains(text, "hotel") {
			return 0.9
		}
		return 0.1
	}}
}

func recallAgent(t *testing.T, view ViewConfig, planner Completer, judge Decider) (*Agent, *Overlay, *records.Store) {
	t.Helper()
	s, dir := agentStore(t)
	overlay, err := OpenOverlay(filepath.Join(dir, "memory-index.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { overlay.Close() })
	view.MaxItems, view.MaxCharacters = 100, 240000
	a, err := New(s, Config{Version: 1, Strategy: "simple", Policy: &ReadPolicy{Consolidation: true}, Consolidation: &ConsolidationConfig{Mode: "layered"}, View: &view}, Models{Planner: planner, Judge: judge}, overlay)
	if err != nil {
		t.Fatal(err)
	}
	return a, overlay, s
}

func blockText(blocks []IndexItem, id string) string {
	for _, block := range blocks {
		if block.ID == id {
			return block.Text
		}
	}
	return ""
}

func TestLayerRecallShowsTheNeededEventsTheQuestionFinds(t *testing.T) {
	ctx := context.Background()
	a, overlay, _ := recallAgent(t, ViewConfig{LayerRecall: true}, replies("", "", ""), tripJudge())
	token, err := overlay.saveTimelines(ctx, records.Scope{}, "", recallState())
	if err != nil {
		t.Fatal(err)
	}
	r := testRun(a, user("What did we book for the Rome trip and which hotel?"))
	blocks, err := r.recallLayer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The trip's two events are found and needed, in time order under their
	// topic; the budget is found but not needed; the taxes share no word.
	timelines := blockText(blocks, "3-timelines")
	if !strings.Contains(timelines, "Thread: Rome trip\n- 2024/03/01: Booked flights to Rome.\n- 2024/03/05: Picked the hotel") || strings.Contains(timelines, "Tax") {
		t.Fatalf("timelines %q", timelines)
	}
	if blockText(blocks, "4-values") != "" || !strings.Contains(blockText(blocks, "1-instructions"), "bullet points") || r.stats.LayerRecalled != 2 {
		t.Fatalf("blocks %+v, recalled %d", blocks, r.stats.LayerRecalled)
	}
	// A new state is indexed by the next read.
	state := recallState()
	state.Memory.Events = append(state.Memory.Events, event{Thread: "T1", Date: "2024/03/09", Text: "Rome museum tickets bought.", Records: []string{"museum"}})
	if _, err := overlay.saveTimelines(ctx, records.Scope{}, token, state); err != nil {
		t.Fatal(err)
	}
	if blocks, err = testRun(a, user("Which Rome tickets?")).recallLayer(ctx); err != nil || !strings.Contains(blockText(blocks, "3-timelines"), "museum tickets") {
		t.Fatal(blocks, err)
	}
}

func TestLayerIndexSearchesEventsAndValuesNotDirectives(t *testing.T) {
	ctx := context.Background()
	_, overlay, _ := recallAgent(t, ViewConfig{LayerRecall: true}, replies("", "", ""), tripJudge())
	if _, err := overlay.saveTimelines(ctx, records.Scope{}, "", recallState()); err != nil {
		t.Fatal(err)
	}
	if err := overlay.syncLayerIndex(ctx, records.Scope{}); err != nil {
		t.Fatal(err)
	}
	found, err := overlay.searchLayer(ctx, records.Scope{}, "trip budget bullet points", 10)
	if err != nil {
		t.Fatal(err)
	}
	items := []string{}
	for _, entry := range found {
		items = append(items, entry.Item)
	}
	// The budget's key and the topic title share the words; the directive's
	// words are never searched.
	if !slices.Contains(items, "v:trip · budget") || slices.ContainsFunc(items, func(item string) bool { return strings.HasPrefix(item, "d") }) {
		t.Fatal(items)
	}
	if directives, err := overlay.layerDirectives(ctx, records.Scope{}); err != nil || len(directives) != 1 {
		t.Fatal(directives, err)
	}
}

func TestLayerRecallNeedsATimelinesConsolidation(t *testing.T) {
	s, _ := agentStore(t)
	_, err := New(s, Config{Version: 1, Strategy: "simple", View: &ViewConfig{LayerRecall: true}}, Models{Planner: replies("", "", ""), Judge: &scriptedJudge{}}, nil)
	if err == nil || !strings.Contains(err.Error(), "view.layerRecall requires") {
		t.Fatal(err)
	}
}
