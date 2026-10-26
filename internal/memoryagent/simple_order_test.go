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

// orderStore writes, in this order: early-2 and late, a two-chunk document,
// early-1, mid and a record whose date does not parse. early-1 and early-2
// share a time; ids sort apart from the write order.
func orderStore(t *testing.T) *records.Store {
	t.Helper()
	s, _ := agentStore(t)
	put := func(upserts ...records.Upsert) {
		if _, err := s.Put(context.Background(), records.PutRequest{Upsert: upserts}); err != nil {
			t.Fatal(err)
		}
	}
	put(expandNote("early-2", "codename early two", "2023-05-08T09:00:00Z", nil), expandNote("late", "codename late", "2023-05-09T10:00:00Z", nil))
	put(expandDocument("doc", "2023-05-08T11:00:00Z", []string{"codename chunk zero", "codename chunk one"})...)
	put(expandNote("early-1", "codename early one", "2023-05-08T09:00:00Z", nil), expandNote("mid", "codename mid", "2023-05-08T12:00:00Z", nil))
	undated := expandNote("undated", "codename undated", "2023-05-08T09:00:00Z", nil)
	undated.RecordJSON = strings.Replace(undated.RecordJSON, `"eventAt":"2023-05-08T09:00:00Z"`, `"eventAt":"sometime"`, 1)
	put(undated)
	return s
}

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

// orderJudge ranks the records apart: late first, then mid, undated, the
// chunks, early-1 and early-2.
func orderJudge() *scriptedJudge {
	return needJudge(byWords(map[string]float64{"late": 0.99, "mid": 0.95, "undated": 0.9, "chunk zero": 0.88, "chunk one": 0.87, "early one": 0.85, "early two": 0.8}))
}

func orderRead(t *testing.T, s *records.Store, view *ViewConfig) View {
	t.Helper()
	planner := replies("codename\nNOTE: the codename", "NEED: the codename", "codename")
	a, err := New(s, Config{Version: 1, Strategy: "simple", View: view}, Models{Planner: planner, Judge: orderJudge()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.Read(context.Background(), ReadRequest{Question: "What is the codename?"})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestViewOrderConfig(t *testing.T) {
	s, _ := agentStore(t)
	both := Models{Planner: expandPlanner(), Judge: expandJudge()}
	for _, good := range []string{"", "time"} {
		if _, err := New(s, Config{Version: 1, Strategy: "simple", View: &ViewConfig{Order: good}}, both, nil); err != nil {
			t.Error(good, err)
		}
	}
	for _, bad := range []string{"rank", "Time", "date"} {
		if _, err := New(s, Config{Version: 1, Strategy: "simple", View: &ViewConfig{Order: bad}}, both, nil); err == nil {
			t.Error("order accepted", bad)
		}
	}
	if raw, _ := json.Marshal(ViewConfig{MaxItems: 16, MaxCharacters: 12000}); strings.Contains(string(raw), "order") {
		t.Fatal(string(raw))
	}
	if raw, _ := json.Marshal(View{RankOrder: []string{"ranked-id"}}); strings.Contains(string(raw), "ranked-id") || strings.Contains(strings.ToLower(string(raw)), "rankorder") {
		t.Fatal(string(raw))
	}
}

func TestReadSimpleTimeOrder(t *testing.T) {
	s := orderStore(t)
	ranked := orderRead(t, s, nil)
	if got := recordIDs(ranked.Records); !slices.Equal(got, []string{"late", "mid", "undated", "doc-00000", "doc-00001", "early-1", "early-2"}) || ranked.RankOrder != nil {
		t.Fatal(got)
	}
	// By date; at one date by write position (the document's, then the
	// chunk's place); records whose date does not parse last.
	timed := orderRead(t, s, &ViewConfig{Order: "time"})
	if got := recordIDs(timed.Records); !slices.Equal(got, []string{"early-2", "early-1", "doc-00000", "doc-00001", "mid", "late", "undated"}) {
		t.Fatal(got)
	}
	if !slices.Equal(timed.RankOrder, recordIDs(ranked.Records)) {
		t.Fatal(timed.RankOrder)
	}
	// Each record keeps its score; the needs and the rest are as ranked.
	for _, record := range timed.Records {
		i := slices.IndexFunc(ranked.Records, func(r Evidence) bool { return r.ID == record.ID })
		if !reflect.DeepEqual(record, ranked.Records[i]) {
			t.Fatal(record, ranked.Records[i])
		}
	}
	if !reflect.DeepEqual(timed.Needs, ranked.Needs) || timed.Truncated != ranked.Truncated || timed.StopReason != ranked.StopReason {
		t.Fatal(timed.Needs, ranked.Needs)
	}
	// "" is absent: the same View, policy included.
	same := orderRead(t, s, &ViewConfig{Order: ""})
	if pinnedView(t, same) != pinnedView(t, ranked) {
		t.Fatal("order \"\" changed the read")
	}
	// A View cut short is cut by rank, then ordered.
	short := orderRead(t, s, &ViewConfig{MaxItems: 3, Order: "time"})
	if got := recordIDs(short.Records); !slices.Equal(got, []string{"mid", "late", "undated"}) {
		t.Fatal(got)
	}
}

// The timelines layer still trims by rank: the time order lists the records
// the trim kept, the blocks keep their place.
func TestTimeOrderAfterTheLayer(t *testing.T) {
	s, overlay := eventsStore(t)
	read := func(order string, chars int) View {
		view := &ViewConfig{MaxCharacters: chars, Order: order}
		got, err := layerAgent(t, s, overlay, Config{View: view}, replies("codename project\nNOTE: the codename is ORBIT", "NEED: the codename", "codename rocket"), expandJudge()).Read(context.Background(), ReadRequest{Question: "What is the codename?"})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	for _, chars := range []int{560, 12000} {
		ranked, timed := read("", chars), read("time", chars)
		rankedIDs, timedIDs := recordIDs(ranked.Records), recordIDs(timed.Records)
		if len(ranked.Index) == 0 || !reflect.DeepEqual(timed.Index, ranked.Index) || !slices.Equal(timed.RankOrder, rankedIDs) || len(timedIDs) != len(rankedIDs) {
			t.Fatal(chars, rankedIDs, timedIDs)
		}
		for _, id := range rankedIDs {
			if !slices.Contains(timedIDs, id) {
				t.Fatal(chars, rankedIDs, timedIDs)
			}
		}
		// The fixture's session A (2023-05-08 13:56) before the document
		// written that day at 18:00.
		if a, d := slices.Index(timedIDs, "a2"), slices.Index(timedIDs, "doc-00000"); a < 0 || d >= 0 && d < a {
			t.Fatal(chars, timedIDs)
		}
	}
}

// A read whose records' write positions cannot be read keeps its rank
// order, has no RankOrder and names order in stats.degraded, whatever was
// degraded before; the rest of the View is as composed. A canceled read
// fails instead of degrading.
func TestTimeOrderFailureKeepsRankOrder(t *testing.T) {
	s := orderStore(t)
	ctx := context.Background()
	ranked := orderRead(t, s, nil)
	composed := ranked
	composed.Stats.LayerInsights = 2
	agent := func(view *ViewConfig) *Agent {
		a, err := New(s, Config{Version: 1, Strategy: "simple", View: view}, Models{Planner: expandPlanner(), Judge: orderJudge()}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	timed := agent(&ViewConfig{Order: "time"})
	// The read's own revision gives the positions.
	view, err := (&simpleRun{agent: timed, revision: ranked.Revision}).orderView(ctx, composed)
	if err != nil || view.Stats.Degraded != nil || !slices.Equal(view.RankOrder, recordIDs(ranked.Records)) || slices.Equal(recordIDs(view.Records), recordIDs(ranked.Records)) {
		t.Fatal(recordIDs(view.Records), view.RankOrder, view.Stats.Degraded, err)
	}
	// A revision the store does not hold: the positions cannot be read.
	lost := &simpleRun{agent: timed, revision: "no such revision"}
	lost.degrade("layer")
	kept, err := lost.orderView(ctx, composed)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(kept.Stats.Degraded, []string{"layer", "order"}) {
		t.Fatal(kept.Stats.Degraded)
	}
	if kept.RankOrder != nil || !reflect.DeepEqual(kept.Records, ranked.Records) {
		t.Fatal(recordIDs(kept.Records), kept.RankOrder)
	}
	kept.Stats.Degraded = nil
	if !reflect.DeepEqual(kept, composed) {
		t.Fatal(kept, composed)
	}
	// A canceled read fails; it is not degraded.
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = (&simpleRun{agent: timed, revision: ranked.Revision}).orderView(canceled, composed); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// Without view.order the View is as composed and no position is read.
	plain, err := (&simpleRun{agent: agent(nil), revision: "no such revision"}).orderView(ctx, composed)
	if err != nil || !reflect.DeepEqual(plain, composed) {
		t.Fatal(plain, err)
	}
}
