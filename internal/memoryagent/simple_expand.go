package memoryagent

import (
	"context"
	"slices"
	"strings"
	"time"
)

// Evidence-anchored expansion (view.expand), after Jev-Mem's split: code
// proposes a bounded set of candidates, and JEV decides. Two sources
// propose them once the shortlist is judged. From the evidence JEV has
// judged ("seed"): the records JEV calls needed are the seeds, and their
// neighbors are the records written near them in their session
// (records.Store.Adjacent) and the records the derived index links them to
// (records.Store.Links: the writer's and the overlay's links, corrections
// both ways). From the question ("events"): the records cited by the
// consolidated events nearest it (simple_events.go). The sources take turns
// filling the limit; the candidates not already pooled are read, judged
// with judgeAll's questions, and join the judged list only when JEV calls
// them needed. They add to what the cue searches found and displace nothing
// before it is judged; composition then treats them like any judged item
// (with fusion: judged items the message search may not rank).

// neighbor is one way to reach a record from a seed: the kind that proposes
// it, its weight (a link's, or 1 for an adjacent record) and its distance
// (an adjacent record's records away; 1 for a link).
type neighbor struct {
	id, kind string
	weight   float64
	distance int
}

// expansionCandidate is a proposed record. From the seeds: its best score
// over them (seed needed × weight), the rank of the first seed that gave
// that score and the shortest distance it gave it at. From either source:
// every kind that proposed it, in the order they did.
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

// alternate fills limit from the seed source and the events source by
// turns, seed first: each turn takes the source's next record not picked
// yet, and once a source has none left the other fills the rest. A record
// both sources propose is picked once, with the kinds of both (the seed's
// first). One source alone is that source cut to limit.
func alternate(seed, events []expansionCandidate, limit int) []expansionCandidate {
	kinds := map[string][]string{}
	for _, c := range slices.Concat(seed, events) {
		for _, kind := range c.kinds {
			if !slices.Contains(kinds[c.id], kind) {
				kinds[c.id] = append(kinds[c.id], kind)
			}
		}
	}
	sources, next, picked := [2][]expansionCandidate{seed, events}, [2]int{}, map[string]bool{}
	// take is the source's next record not picked yet.
	take := func(source int) (expansionCandidate, bool) {
		for next[source] < len(sources[source]) {
			c := sources[source][next[source]]
			next[source]++
			if !picked[c.id] {
				return c, true
			}
		}
		return expansionCandidate{}, false
	}
	out, turn := []expansionCandidate{}, 0
	for len(out) < limit {
		c, ok := take(turn)
		if !ok {
			if c, ok = take(1 - turn); !ok {
				break
			}
		}
		picked[c.id] = true
		c.kinds = kinds[c.id]
		out = append(out, c)
		turn = 1 - turn
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
	if slices.ContainsFunc(kinds, linkKind) {
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

// linkKind reports whether an expansion kind is a link type.
func linkKind(kind string) bool { return kind != expandAdjacent && kind != expandEvents }

// seedKind reports whether an expansion kind expands from the seeds.
func seedKind(kind string) bool { return kind != expandEvents }

// expandEvidence returns the judged list with the needed candidates of the
// expansion added after it. With gate "needs" it first asks whether the
// evidence is already sufficient (sufficient) and, if so, returns the list
// as it is. The seed source proposes the neighbors of the best
// view.expand.seeds needed items, as rankExpansion orders them; the events
// source the records cited by the events nearest the question
// (simple_events.go); neither proposes a record already pooled. The sources
// fill view.expand.limit by turns (alternate). The candidates are read
// (evidence(), proved against their originals; one without evidence is
// skipped); with target "needs" they are asked the need table's question
// for the needs not covered yet (target); then they are judged by judgeAll
// within the read's decision budget; those judged join the run's items (so
// fusion keeps what JEV calls no longer current out), and those JEV calls
// needed join the list. A candidate left unjudged (the budget ran short, or
// its call failed) is dropped. Reading or judging that falls short degrades
// the read (expansion) and keeps what was judged; only a canceled read
// fails. stop is why the need loop stopped.
func (r *simpleRun) expandEvidence(ctx context.Context, scored []*simpleItem, stop string) ([]*simpleItem, error) {
	config := r.agent.config.View.Expand
	covered := r.covered(scored)
	if config.Gate == expandNeeds && sufficient(stop, covered) {
		r.mu.Lock()
		r.stats.ExpandGated = true
		r.mu.Unlock()
		return scored, nil
	}
	seeds := expansionSeeds(scored, config.Seeds)
	if !slices.ContainsFunc(config.Kinds, seedKind) {
		seeds = nil
	}
	events := slices.Contains(config.Kinds, expandEvents)
	if len(seeds) == 0 && !events {
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
	known := func(id string) bool { return r.items[id] != nil }
	var fromSeeds, fromEvents []expansionCandidate
	if len(seeds) > 0 {
		// Neighbors that cannot be read degrade the read; the events, if
		// any, still propose.
		neighbors, err := r.neighbors(ctx, seeds, config)
		if err != nil {
			if _, err = short(); err != nil {
				return nil, err
			}
		} else {
			fromSeeds = rankExpansion(seeds, neighbors, known, config.Limit)
		}
	}
	if events {
		var err error
		if fromEvents, err = r.eventCandidates(ctx, config.EventsTop); err != nil {
			return nil, err
		}
	}
	candidates := alternate(fromSeeds, fromEvents, config.Limit)
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
	if config.Target == expandNeeds {
		if err = r.target(ctx, items, covered); err != nil {
			return nil, err
		}
	}
	if err = r.judgeAll(ctx, items); err != nil {
		if _, err = short(); err != nil {
			return nil, err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	answered := make([]bool, len(r.needs))
	for i, item := range items {
		if !item.Judged {
			continue
		}
		item.Expanded = true
		r.items[item.ID] = item
		r.stats.Expanded++
		if r.stats.ExpandKinds == nil {
			r.stats.ExpandKinds = map[string]int{}
		}
		for _, kind := range kinds[i] {
			r.stats.ExpandKinds[kind]++
		}
		// Every judged candidate joins the judged list and composes on its
		// needed answer like a shortlisted record.
		scored = append(scored, item)
		if item.Needed >= 0.5 {
			r.stats.ExpandedNeeded++
			for k, answer := range item.Row {
				answered[k] = answered[k] || answer >= 0.5
			}
		}
	}
	for _, yes := range answered {
		if yes {
			r.stats.ExpandAnsweredNeeds++
		}
	}
	return scored, nil
}

// covered reports, for each need, whether the evidence judged so far gives
// it: the need is not an ALL need (one wants every instance), and an item
// JEV judged needed (≥ 0.5) and current answers it in the need table
// (≥ 0.5).
func (r *simpleRun) covered(scored []*simpleItem) []bool {
	out := make([]bool, len(r.needs))
	for k, need := range r.needs {
		out[k] = !need.All && slices.ContainsFunc(scored, func(item *simpleItem) bool {
			return item.Needed >= 0.5 && current(item) && r.answer(item.ID, k) >= 0.5
		})
	}
	return out
}

// sufficient is gate "needs", Jev-Mem's question whether the evidence
// suffices before it expands, answered from the need loop's state with no
// model call: the loop stopped as covered and every need is covered (so no
// need is an ALL need).
func sufficient(stop string, covered []bool) bool {
	return stop == stopCovered && !slices.Contains(covered, false)
}

// target is target "needs", Jev-Mem's new-information question: before
// their judgement, the candidates are asked the need table's question
// (askNeeds) for each need not covered, and their rows are set; compose then
// ranks those that answer one first (ordered). With every need covered it
// asks nothing. The judgement comes first: when the budget cannot pay for
// the need table and the judgement both, or the need table fails, the
// candidates go without rows and the read is degraded (expansion). Only a
// canceled read is an error.
func (r *simpleRun) target(ctx context.Context, items []*simpleItem, covered []bool) error {
	asked := []int{}
	for k, done := range covered {
		if !done {
			asked = append(asked, k)
		}
	}
	if len(asked) == 0 || len(items) == 0 {
		return nil
	}
	if calls := (len(items)+tableBatch-1)/tableBatch + (len(items)+judgeBatch-1)/judgeBatch; r.decisionsLeft() < calls {
		r.degrade("expansion")
		return nil
	}
	if err := r.askNeeds(ctx, items, asked, func(item *simpleItem, row []float64) { item.Row = row }); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		for _, item := range items {
			item.Row = nil
		}
		r.degrade("expansion")
		return nil
	}
	r.targeted = true
	return nil
}
