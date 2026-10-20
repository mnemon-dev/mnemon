package aml

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
)

// timedSessions adds three sessions about the support group, written late
// day first: 2023-05-09, then 05-07, then 05-08.
func timedSessions(t *testing.T, h *harness, user string) {
	t.Helper()
	for _, s := range []struct {
		id  string
		day int
	}{{"s-late", 9}, {"s-early", 7}, {"s-mid", 8}} {
		ms := time.Date(2023, 5, s.day, 13, 0, 0, 0, time.UTC).UnixMilli()
		h.mustAdd(t, AddRequest{RequestID: "r-" + s.id, UserID: user, SessionID: s.id, Messages: []Message{{Role: "user", Content: "Caroline went to the LGBTQ support group, " + s.id, Timestamp: &ms}}})
	}
}

func timeOrdered(config *ServerConfig) { config.MemoryConfig.View.Order = "time" }

func ids(items []SearchItem) []string {
	out := []string{}
	for _, item := range items {
		out = append(out, item.ID)
	}
	return out
}

func checkTimeOrder(t *testing.T, items []SearchItem) {
	t.Helper()
	dates := []string{}
	for _, item := range items {
		if !strings.HasPrefix(item.ID, "index:") {
			dates = append(dates, item.CreatedAt)
		}
	}
	if !slices.IsSorted(dates) {
		t.Fatal(dates)
	}
}

func TestSearchDeliversInTimeOrder(t *testing.T) {
	ranked, timed := newHarness(t, nil), newHarness(t, timeOrdered)
	for _, h := range []*harness{ranked, timed} {
		timedSessions(t, h, "u1")
	}
	query := SearchRequest{Query: "When did Caroline go to the LGBTQ support group?", UserID: "u1", TopK: 10}
	byRank, byTime := ranked.search(t, query), timed.search(t, query)
	if len(byTime) != 3 || len(byRank) != 3 || !slices.Equal(slices.Sorted(slices.Values(ids(byTime))), slices.Sorted(slices.Values(ids(byRank)))) {
		t.Fatal(byRank, byTime)
	}
	checkTimeOrder(t, byTime)
	checkRankScores(t, byTime)
	if timed.server.DegradedSearches() != 0 {
		t.Fatal(timed.log.String())
	}
}

// A degraded Search cuts the raw ranking at top_k by rank, then delivers in
// the same order as a read, scored by position.
func TestDegradedSearchDeliversInTimeOrder(t *testing.T) {
	ranked, timed := newHarness(t, nil), newHarness(t, timeOrdered)
	for _, h := range []*harness{ranked, timed} {
		timedSessions(t, h, "u1")
		h.judge.fail.Store(true)
	}
	query := SearchRequest{Query: "Caroline support group s-late", UserID: "u1", TopK: 2}
	byRank, byTime := ranked.search(t, query), timed.search(t, query)
	if len(byTime) != 2 || !slices.Equal(slices.Sorted(slices.Values(ids(byTime))), slices.Sorted(slices.Values(ids(byRank)))) || timed.server.DegradedSearches() != 1 {
		t.Fatal(byRank, byTime)
	}
	checkTimeOrder(t, byTime)
	checkRankScores(t, byTime)
}

// When the blocks and the records pass top_k, the records cut are the worst
// ranked, whatever order delivers them.
func TestSearchTopKCutsByRankBeforeTimeOrder(t *testing.T) {
	setup := func(config *ServerConfig) {
		config.MemoryConfig.Consolidation = &memoryagent.ConsolidationConfig{Mode: "timelines"}
		config.ConsolidateOnAdd = true
	}
	ranked := newHarness(t, setup)
	timed := newHarness(t, func(config *ServerConfig) { setup(config); timeOrdered(config) })
	for _, h := range []*harness{ranked, timed} {
		timedSessions(t, h, "u1")
	}
	// The latest session ranks first: its id is in the query.
	query := SearchRequest{Query: "When did Caroline go to the LGBTQ support group, s-late?", UserID: "u1", TopK: 3}
	byRank, byTime := ranked.search(t, query), timed.search(t, query)
	if len(byTime) != 3 || !strings.HasPrefix(byTime[0].ID, "index:") || !strings.HasPrefix(byTime[1].ID, "index:") || byTime[2].ID != byRank[2].ID || !strings.Contains(byTime[2].Content, "s-late") {
		t.Fatal(ids(byRank), ids(byTime))
	}
	// With room for them all, they come in time order.
	query.TopK = 10
	all := timed.search(t, query)
	checkTimeOrder(t, all)
	if len(all) != 5 || strings.Contains(all[2].Content, "s-late") {
		t.Fatal(ids(all))
	}
	checkRankScores(t, byTime)
}
