package aml

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
	"github.com/mnemon-dev/mnemon/internal/records"
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

// rankedAdds are Adds of one message each on which the two lexical rankings
// of rankedQuery differ: bm25 and the frozen rules (words).
var rankedQuery, rankedAdds = "rome camping", func() []AddRequest {
	adds := []AddRequest{}
	for i, text := range []string{
		"camping camping camping lake caroline sunrise",
		"paris camping rome caroline camping trip melanie group lake",
		"paris lake camping painted sunrise sunday camping paris",
		"painted lake rome sunrise sunrise",
		"caroline paris",
		"group painted paris",
		"group painted caroline sunrise group sunrise painted rome lake",
		"group trip camping sunday melanie",
	} {
		adds = append(adds, AddRequest{RequestID: fmt.Sprintf("r%d", i), UserID: "u1", SessionID: fmt.Sprintf("s%d", i), Messages: []Message{{Role: "user", Content: text, Timestamp: at(i)}}})
	}
	return adds
}()

// ranking is store's own ranking of query by match, its lexical index caught
// up first.
func ranking(t *testing.T, store *records.Store, query, match string) []string {
	t.Helper()
	ctx := context.Background()
	if err := store.CatchUpLexical(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Get(ctx, records.GetRequest{IDs: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	text, _ := json.Marshal(query)
	input := map[string]json.RawMessage{"query": text, "match": json.RawMessage(`"` + match + `"`)}
	result, err := store.Query(ctx, records.QueryRequest{Revision: snapshot.Revision, Input: input, CandidateLimit: 200})
	if err != nil {
		t.Fatal(err)
	}
	return result.IDs
}

// storeRanking is ranking over userID's store.
func storeRanking(t *testing.T, h *harness, userID, query, match string) []string {
	t.Helper()
	user, err := h.server.users.acquire(context.Background(), userID, false)
	if err != nil || user == nil {
		t.Fatal(err)
	}
	defer h.server.users.release(user)
	return ranking(t, user.store, query, match)
}

// A degraded Search ranks as the read's searches do: by bm25 over the
// lexical index, caught up first.
func TestDegradedSearchRanksByBM25(t *testing.T) {
	h := newHarness(t, nil)
	for _, add := range rankedAdds {
		h.mustAdd(t, add)
	}
	bm25, words := storeRanking(t, h, "u1", rankedQuery, "bm25"), storeRanking(t, h, "u1", rankedQuery, "words")
	if slices.Equal(bm25, words) {
		t.Fatal("the records no longer tell the two rankings apart", bm25)
	}
	h.judge.fail.Store(true)
	items := h.search(t, SearchRequest{Query: rankedQuery, UserID: "u1", TopK: 100})
	if h.server.DegradedSearches() != 1 || !slices.Equal(ids(items), bm25) {
		t.Fatal(ids(items), "bm25:", bm25, "words:", words)
	}
}

// The raw ranking catches the lexical index up itself: records that the
// background build has not indexed still rank by bm25, not by the frozen
// rules over an in-memory index of the whole store.
func TestRawRankingCatchesTheIndexUp(t *testing.T) {
	dir := t.TempDir()
	store, err := records.Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	run, cancel := context.WithCancel(context.Background())
	stop, err := store.StartRuntime(run)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	// The runtime's background build ends with its context: from here on only
	// a catch-up indexes what is written.
	cancel()
	time.Sleep(50 * time.Millisecond)
	agent, err := memoryagent.OpenWithModels(store, dir, simpleConfig(), false, memoryagent.Models{Planner: &fakePlanner{}, Judge: &fakeJudge{}})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	ctx := context.Background()
	for _, add := range rankedAdds {
		parts, _, err := add.parts()
		if err != nil {
			t.Fatal(err)
		}
		for _, part := range parts {
			if _, err := memoryagent.WriteConversation(ctx, store, part.conversation); err != nil {
				t.Fatal(err)
			}
		}
	}
	raw, err := rawRanking(ctx, store, agent, simpleConfig(), rankedQuery, 100, func(event string, err error) { t.Error(event, err) })
	if err != nil {
		t.Fatal(err)
	}
	bm25, words := ranking(t, store, rankedQuery, "bm25"), ranking(t, store, rankedQuery, "words")
	if slices.Equal(bm25, words) {
		t.Fatal("the records no longer tell the two rankings apart", bm25)
	}
	if got := ids(raw.items); !slices.Equal(got, bm25) {
		t.Fatal(got, "bm25:", bm25, "words:", words)
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

// A degraded Search whose records' write positions cannot be read keeps the
// raw ranking's order and logs it; the Search still answers.
func TestDegradedSearchKeepsRankOrderWhenPositionsFail(t *testing.T) {
	h := newHarness(t, timeOrdered)
	timedSessions(t, h, "u1")
	ctx := context.Background()
	user, err := h.server.users.acquire(ctx, "u1", false)
	if err != nil || user == nil {
		t.Fatal(user, err)
	}
	defer h.server.users.release(user)
	snapshot, err := user.store.Get(ctx, records.GetRequest{IDs: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	found, err := rawQuery(ctx, user.store, snapshot.Revision, "Caroline went to the LGBTQ support group", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := user.agent.Hydrate(ctx, snapshot.Revision, records.Scope{}, found)
	if err != nil || len(evidence) != 3 {
		t.Fatal(len(evidence), err)
	}
	// A ranking that is not in time order: the latest session first.
	ranked := slices.Clone(evidence)
	slices.SortFunc(ranked, func(a, b memoryagent.Evidence) int { return strings.Compare(b.Date, a.Date) })
	evidenceIDs := func(list []memoryagent.Evidence) []string {
		out := []string{}
		for _, record := range list {
			out = append(out, record.ID)
		}
		return out
	}
	// The positions of the snapshot's revision give the time order.
	timed := h.server.ordered(ctx, user, snapshot.Revision, ranked)
	dates := []string{}
	for _, record := range timed {
		dates = append(dates, record.Date)
	}
	if slices.Equal(evidenceIDs(timed), evidenceIDs(ranked)) || !slices.IsSorted(dates) || strings.Contains(h.log.String(), "kept rank order") {
		t.Fatal(evidenceIDs(timed), dates, evidenceIDs(ranked), h.log.String())
	}
	// At a revision the store does not hold they cannot be read: the rank
	// order stays, and the log says so.
	kept := h.server.ordered(ctx, user, "no such revision", ranked)
	if !slices.Equal(evidenceIDs(kept), evidenceIDs(ranked)) {
		t.Fatal(evidenceIDs(kept), evidenceIDs(ranked))
	}
	if want := "degraded search kept rank order (user " + user.key[:8] + "): "; !strings.Contains(h.log.String(), want) {
		t.Fatal(h.log.String())
	}
}
