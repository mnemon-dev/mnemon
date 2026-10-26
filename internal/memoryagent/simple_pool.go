package memoryagent

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/mnemon-dev/mnemon/internal/records"
)

// Search and pooling (author N:572-591, N:411-417, N:922-924). The author's
// searches are route reads of 20 records a page, and a View may read 16 pages
// in all, one of them the head read the store needs none of here. A cue is
// searched once (at most 200 ids, hybrid with an embedding, else words) and
// read a page of 20 at a time; every page read, first pages included, counts
// toward the 15 left. When they are spent, further pages and searches silently
// read nothing, as the author's reads return nothing past their budget.

const (
	simplePageSize    = 20
	simplePageBudget  = 15
	simpleSearchLimit = 200
	simplePoolSize    = 48 // the author's `candidates`
)

// cueSearch is one cue's ranked search and how far it has been read.
type cueSearch struct {
	Cue       cue
	IDs       []string
	Offset    int
	Exhausted bool // nothing is left to read
}

// page reads the next page of up to 20 ids.
func (s *cueSearch) page() []string {
	end := min(s.Offset+simplePageSize, len(s.IDs))
	ids := s.IDs[s.Offset:end]
	s.Offset = end
	s.Exhausted = end == len(s.IDs)
	return ids
}

// search runs one cue on the store and adds it to the run's searches; none of
// it is read yet. With the page budget spent it does not query, and the
// search has nothing to read.
func (r *simpleRun) search(ctx context.Context, c cue) (*cueSearch, error) {
	s := &cueSearch{Cue: c}
	if r.pageReads >= simplePageBudget {
		s.Exhausted = true
		r.searches = append(r.searches, s)
		return s, nil
	}
	// Searches rank by SQLite FTS5's own bm25() (records match "bm25", by the
	// frozen rules until the index covers the selection); with an embedder,
	// by words and meaning.
	input := map[string]json.RawMessage{"match": json.RawMessage(`"bm25"`)}
	input["query"], _ = json.Marshal(c.Text)
	if r.agent.config.Embedding != nil {
		input["match"] = json.RawMessage(`"hybrid"`)
	}
	if r.intent != "" {
		input["intent"], _ = json.Marshal(r.intent)
	}
	start := time.Now()
	result, err := r.agent.store.Query(ctx, records.QueryRequest{Revision: r.revision, Scope: r.req.Scope, Input: input, Embedding: r.agent.config.Embedding, Index: r.agent.config.Index, CandidateLimit: simpleSearchLimit})
	r.stats.Searches++
	r.stats.QueryMS += float64(time.Since(start).Microseconds()) / 1000
	if err != nil {
		return nil, err
	}
	if result.Index != nil {
		r.mu.Lock()
		for route, n := range result.Index.Routes {
			if route != "base" && n > 0 {
				if r.stats.IndexRoutes == nil {
					r.stats.IndexRoutes = map[string]int{}
				}
				r.stats.IndexRoutes[route]++
			}
		}
		r.mu.Unlock()
	}
	s.IDs = result.IDs[:min(simpleSearchLimit, len(result.IDs))]
	r.searches = append(r.searches, s)
	return s, nil
}

// next reads a search's next page within the page budget. A spent budget
// reads nothing and ends the search, as a failed author read leaves it no
// continuation.
func (r *simpleRun) next(s *cueSearch) []string {
	if s.Exhausted {
		return nil
	}
	if r.pageReads >= simplePageBudget {
		s.Exhausted = true
		return nil
	}
	r.pageReads++
	return s.page()
}

// runCues searches each cue in order and reads its first page; it returns the
// first pages, empty ones included, in cue order.
func (r *simpleRun) runCues(ctx context.Context, cues []cue) ([][]string, error) {
	pages := [][]string{}
	for _, c := range cues {
		s, err := r.search(ctx, c)
		if err != nil {
			return nil, err
		}
		pages = append(pages, r.next(s))
	}
	return pages, nil
}

// mixCues mixes what each cue found into at most limit ids (author N:411-417,
// exactly): every list first gets an equal share of its own best finds, so a
// two-part question keeps both parts; the rest go by reciprocal-rank sum
// (1/(60+rank), ties in first-seen order).
func mixCues(lists [][]string, limit int) []string {
	score, keys := map[string]float64{}, []string{}
	for _, list := range lists {
		for rank, id := range list {
			if _, ok := score[id]; !ok {
				keys = append(keys, id)
			}
			score[id] += 1 / float64(60+rank)
		}
	}
	share := max(1, limit/max(1, len(lists)))
	chosen, out := map[string]bool{}, []string{}
	add := func(id string) {
		if !chosen[id] && len(out) < limit {
			chosen[id] = true
			out = append(out, id)
		}
	}
	for _, list := range lists {
		// The share is of the list's ids not chosen before this list.
		fresh := slices.DeleteFunc(slices.Clone(list), func(id string) bool { return chosen[id] })
		for _, id := range fresh[:min(share, len(fresh))] {
			add(id)
		}
	}
	slices.SortStableFunc(keys, func(a, b string) int { return probabilityOrder(score[a], score[b]) })
	for _, id := range keys {
		add(id)
	}
	return out
}

// admit pools the records of ids not pooled yet, in the given order, and
// returns the new items. Ids without readable evidence are skipped, as the
// author skips finds with no text.
func (r *simpleRun) admit(ctx context.Context, ids []string) ([]*simpleItem, error) {
	fresh := []string{}
	for _, id := range ids {
		if r.items[id] == nil && !slices.Contains(fresh, id) {
			fresh = append(fresh, id)
		}
	}
	values := map[string]Evidence{}
	for start := 0; start < len(fresh); start += 200 {
		batch, err := evidence(ctx, r.agent.store, r.revision, r.req.Scope, fresh[start:min(start+200, len(fresh))])
		if err != nil {
			return nil, err
		}
		for _, value := range batch {
			values[value.ID] = value
		}
	}
	added := []*simpleItem{}
	for _, id := range fresh {
		value, ok := values[id]
		if !ok {
			continue
		}
		item := &simpleItem{ID: id, Evidence: value, FirstSeen: len(r.order)}
		r.items[id] = item
		r.order = append(r.order, id)
		added = append(added, item)
	}
	r.stats.Candidates = len(r.items)
	return added, nil
}
