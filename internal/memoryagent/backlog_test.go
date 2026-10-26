package memoryagent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/records"
)

// backlogNotes are six notes titled "Note", read under that 4-unit header:
// 44, 44 and 34 units, one only a data line (a record nothing reads), 44 (20
// emoji count 40) and 54.
var backlogNotes = []string{
	strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 30), `  {"a":1}  `, strings.Repeat("😀", 20), strings.Repeat("d", 50),
}

func TestBacklogCountsPendingAndUnreadWithoutAJob(t *testing.T) {
	ctx := context.Background()
	s, dir := agentStore(t)
	for i, text := range backlogNotes {
		seedNote(t, s, fmt.Sprintf("n%d", i), text)
	}
	model := &limitPlanner{reply: func(_, prompt string) (string, error) { return eventsReply(prompt), nil }}
	// A job reads 200 units and folds batches of 100.
	a, overlay := timelinesAgent(t, s, dir, ConsolidationConfig{BatchCharacters: 100, MaxBatches: 2}, Models{Consolidator: model})
	measure := func(want Backlog) {
		t.Helper()
		_, before, err := overlay.loadTimelines(ctx, records.Scope{})
		if err != nil {
			t.Fatal(err)
		}
		calls := plannerCalls(model)
		got, err := a.Backlog(ctx, records.Scope{})
		if err != nil || got != want {
			t.Fatalf("backlog %+v %v, want %+v", got, err, want)
		}
		// Measuring runs no job: no model call, no state written.
		if _, after, _ := overlay.loadTimelines(ctx, records.Scope{}); after != before || plannerCalls(model) != calls {
			t.Fatal("Backlog ran a job", before, after, plannerCalls(model), calls)
		}
	}
	// Nothing read yet: every record is after the watermark, the data line
	// among them, with the characters a job reads.
	measure(Backlog{Records: 6, Characters: 44 + 44 + 34 + 44 + 54})
	// A held job reads up to 200 units (the last note stays unread), folds
	// the first full batch and keeps the emoji note pending.
	if result, err := a.Consolidate(ctx, ConsolidateRequest{Hold: true}); err != nil || result.Folded != 1 {
		t.Fatal(result, err)
	}
	if state := loadState(t, overlay); len(state.Pending) != 1 || state.Pending[0].ID != "n4" {
		t.Fatal(ids(state.Pending))
	}
	measure(Backlog{Records: 2, Characters: 44 + 54})
	// The next held job reads the last note and folds no short rest: the
	// same backlog, all of it pending now.
	if result, err := a.Consolidate(ctx, ConsolidateRequest{Hold: true}); err != nil || result.Folded != 0 {
		t.Fatal(result, err)
	}
	if state := loadState(t, overlay); len(state.Pending) != 2 {
		t.Fatal(ids(state.Pending))
	}
	measure(Backlog{Records: 2, Characters: 44 + 54})
	// A job that folds the rest leaves none.
	if result, err := a.Consolidate(ctx, ConsolidateRequest{}); err != nil || result.Remaining != 0 {
		t.Fatal(result, err)
	}
	measure(Backlog{})
}

func TestBacklogReadsAsTheJobReads(t *testing.T) {
	ctx := context.Background()
	s, dir := agentStore(t)
	// Conversation chunks written without data.lines are read whole under
	// their session header (23 units): an assistant message (311 units) and a
	// chunk that continues one (32), whose speaker its text does not say.
	seedNote(t, s, "a0", "Conversation s1 on 2024/03/15 (Fri) 00:00:\nassistant: "+strings.Repeat("x", 300))
	seedNote(t, s, "a1", "Conversation s1 on 2024/03/15 (Fri) 00:00:\nthe rest of an assistant message")
	a, _ := timelinesAgent(t, s, dir, ConsolidationConfig{}, Models{Consolidator: &limitPlanner{reply: func(_, prompt string) (string, error) { return eventsReply(prompt), nil }}})
	if got, err := a.Backlog(ctx, records.Scope{}); err != nil || got != (Backlog{Records: 2, Characters: 23 + 311 + 23 + 32}) {
		t.Fatal(got, err)
	}
	// Without a timelines state there is no backlog.
	plain, err := New(s, Config{Version: 1, Policy: &ReadPolicy{}}, Models{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := plain.Backlog(ctx, records.Scope{}); err != nil || got != (Backlog{}) {
		t.Fatal(got, err)
	}
}

func TestJobWindowIsMaxBatchesTimesBatchCharacters(t *testing.T) {
	s, dir := agentStore(t)
	model := Models{Consolidator: &limitPlanner{reply: func(_, prompt string) (string, error) { return eventsReply(prompt), nil }}}
	// The defaults: 16 batches of 10,000 characters.
	if a, _ := timelinesAgent(t, s, dir, ConsolidationConfig{}, model); a.JobWindow() != 160000 {
		t.Fatal(a.JobWindow())
	}
	if a, _ := timelinesAgent(t, s, t.TempDir(), ConsolidationConfig{BatchCharacters: 300, MaxBatches: 2}, model); a.JobWindow() != 600 {
		t.Fatal(a.JobWindow())
	}
	plain, err := New(s, Config{Version: 1, Policy: &ReadPolicy{}}, Models{}, nil)
	if err != nil || plain.JobWindow() != 0 {
		t.Fatal(plain.JobWindow(), err)
	}
}
