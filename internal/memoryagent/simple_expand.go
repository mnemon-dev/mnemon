package memoryagent

import (
	"context"
	"slices"
	"strings"
	"time"
)

// Evidence-anchored expansion (view.expand), after Jev-Mem's split: code
// proposes a bounded set of candidates from evidence JEV has already judged,
// and JEV decides. Once the shortlist is judged, the records JEV calls needed
// are the seeds; their neighbors are the records written just before and
// after them in their session (records.Store.Adjacent) and the records the
// derived index links them to (records.Store.Links: the writer's and the
// overlay's links, corrections both ways). The best of those not already
// pooled are read, judged with judgeAll's questions, and join the judged
// list only when JEV calls them needed. They add to what the cue searches
// found and displace nothing before it is judged; composition then treats
// them like any judged item (with fusion: judged items the message search
// may not rank).

// neighbor is one way to reach a record from a seed: the kind that proposes
// it, its weight (a link's, or 1 for an adjacent record) and its distance
// (an adjacent record's records away; 1 for a link).
type neighbor struct {
	id, kind string
	weight   float64
	distance int
}

// expansionCandidate is a proposed neighbor: its best score over the seeds
// (seed needed × weight), the rank of the first seed that gave that score,
// the shortest distance it gave it at, and every kind that proposed it, in
// the order they did.
type expansionCandidate struct {
	id       string
	score    float64
	seed     int
	distance int
	kinds    []string
}

// expansionSeeds is the judged items JEV calls needed (≥ 0.5, whether or not
// still current), best first, ties in shortlist order, at most n.
func expansionSeeds(scored []*simpleItem, n int) []*simpleItem {
	seeds := []*simpleItem{}
	for _, item := range judgedOrder(scored) {
		if item.Needed >= 0.5 && len(seeds) < n {
			seeds = append(seeds, item)
		}
	}
	return seeds
}

// rankExpansion orders the neighbors of the seeds that known does not hold
// by their best score, then the rank of the seed that gave it, then the
// distance it gave it at, then id, and keeps at most limit.
func rankExpansion(seeds []*simpleItem, neighbors map[string][]neighbor, known func(string) bool, limit int) []expansionCandidate {
	byID, list := map[string]*expansionCandidate{}, []*expansionCandidate{}
	for rank, seed := range seeds {
		for _, n := range neighbors[seed.ID] {
			if known(n.id) {
				continue
			}
			score := seed.Needed * n.weight
			c := byID[n.id]
			switch {
			case c == nil:
				c = &expansionCandidate{id: n.id, score: score, seed: rank, distance: n.distance}
				byID[n.id] = c
				list = append(list, c)
			case score > c.score:
				c.score, c.seed, c.distance = score, rank, n.distance
			case score == c.score && rank == c.seed && n.distance < c.distance:
				c.distance = n.distance
			}
			if !slices.Contains(c.kinds, n.kind) {
				c.kinds = append(c.kinds, n.kind)
			}
		}
	}
	slices.SortFunc(list, func(a, b *expansionCandidate) int {
		if order := probabilityOrder(a.score, b.score); order != 0 {
			return order
		}
		if a.seed != b.seed {
			return a.seed - b.seed
		}
		if a.distance != b.distance {
			return a.distance - b.distance
		}
		return strings.Compare(a.id, b.id)
	})
	out := []expansionCandidate{}
	for _, c := range list[:min(limit, len(list))] {
		out = append(out, *c)
	}
	return out
}

// neighbors reads the seeds' neighbors of the configured kinds at the read's
// revision and scope: the records within the adjacent window with weight 1
// at their distance, links with their weight at distance 1.
func (r *simpleRun) neighbors(ctx context.Context, seeds []*simpleItem, config *ExpandConfig) (map[string][]neighbor, error) {
	start := time.Now()
	defer func() {
		r.mu.Lock()
		r.stats.QueryMS += float64(time.Since(start).Microseconds()) / 1000
		r.mu.Unlock()
	}()
	ids := []string{}
	for _, seed := range seeds {
		ids = append(ids, seed.ID)
	}
	kinds, out := config.Kinds, map[string][]neighbor{}
	if slices.Contains(kinds, expandAdjacent) {
		adjacent, err := r.agent.store.Adjacent(ctx, r.revision, r.req.Scope, ids, config.AdjacentWindow)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			for _, near := range adjacent[id] {
				out[id] = append(out[id], neighbor{near.ID, expandAdjacent, 1, max(near.Offset, -near.Offset)})
			}
		}
	}
	if slices.ContainsFunc(kinds, func(kind string) bool { return kind != expandAdjacent }) {
		links, err := r.agent.store.Links(ctx, r.revision, r.req.Scope, ids)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			for _, link := range links[id] {
				if slices.Contains(kinds, link.Type) {
					out[id] = append(out[id], neighbor{link.ID, link.Type, link.Weight, 1})
				}
			}
		}
	}
	return out, nil
}

// expandEvidence returns the judged list with the needed neighbors of its
// needed items added after it. The best view.expand.limit neighbors of the
// best view.expand.seeds needed items, among records not pooled, are read
// (evidence(), proved against their originals; one without evidence is
// skipped) and judged by judgeAll within the read's decision budget; those
// judged join the run's items (so fusion keeps what JEV calls no longer
// current out), and those JEV calls needed join the list. A neighbor left
// unjudged (the budget ran short, or its call failed) is dropped. Reading
// or judging that falls short degrades the read (expansion) and keeps what
// was judged; only a canceled read fails.
func (r *simpleRun) expandEvidence(ctx context.Context, scored []*simpleItem) ([]*simpleItem, error) {
	config := r.agent.config.View.Expand
	seeds := expansionSeeds(scored, config.Seeds)
	if len(seeds) == 0 {
		return scored, nil
	}
	short := func() ([]*simpleItem, error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		r.degrade("expansion")
		return scored, nil
	}
	if r.decisionsLeft() == 0 {
		return short()
	}
	neighbors, err := r.neighbors(ctx, seeds, config)
	if err != nil {
		return short()
	}
	candidates := rankExpansion(seeds, neighbors, func(id string) bool { return r.items[id] != nil }, config.Limit)
	if len(candidates) == 0 {
		return scored, nil
	}
	ids := []string{}
	for _, c := range candidates {
		ids = append(ids, c.id)
	}
	values, err := evidence(ctx, r.agent.store, r.revision, r.req.Scope, ids)
	if err != nil {
		return short()
	}
	found := map[string]Evidence{}
	for _, value := range values {
		found[value.ID] = value
	}
	items, kinds := []*simpleItem{}, [][]string{}
	for _, c := range candidates {
		if value, ok := found[c.id]; ok {
			items, kinds = append(items, &simpleItem{ID: c.id, Evidence: value}), append(kinds, c.kinds)
		}
	}
	if err = r.judgeAll(ctx, items); err != nil {
		if _, err = short(); err != nil {
			return nil, err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, item := range items {
		if !item.Judged {
			continue
		}
		r.items[item.ID] = item
		r.stats.Expanded++
		if r.stats.ExpandKinds == nil {
			r.stats.ExpandKinds = map[string]int{}
		}
		for _, kind := range kinds[i] {
			r.stats.ExpandKinds[kind]++
		}
		if item.Needed >= 0.5 {
			r.stats.ExpandedNeeded++
			scored = append(scored, item)
		}
	}
	return scored, nil
}
