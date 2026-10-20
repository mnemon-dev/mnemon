package memoryagent

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
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
// that score, the shortest distance it gave it at, and the seeds a
// correction (supersedes) links it to, its partners. From either source:
// every kind that proposed it, in the order they did.
type expansionCandidate struct {
	id       string
	score    float64
	seed     int
	distance int
	kinds    []string
	partners []string
}

// expansionSeeds is the judged items whose needed answer is at least min
// (whether or not still current), best first, ties in shortlist order, at
// most n.
func expansionSeeds(scored []*simpleItem, n int, min float64) []*simpleItem {
	seeds := []*simpleItem{}
	for _, item := range judgedOrder(scored) {
		if item.Needed >= min && len(seeds) < n {
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
			if n.kind == "supersedes" && !slices.Contains(c.partners, seed.ID) {
				c.partners = append(c.partners, seed.ID)
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
// both sources propose is picked once, with the kinds (the seed's first)
// and partners of both. One source alone is that source cut to limit.
func alternate(seed, events []expansionCandidate, limit int) []expansionCandidate {
	kinds, partners := map[string][]string{}, map[string][]string{}
	for _, c := range slices.Concat(seed, events) {
		for _, kind := range c.kinds {
			if !slices.Contains(kinds[c.id], kind) {
				kinds[c.id] = append(kinds[c.id], kind)
			}
		}
		for _, partner := range c.partners {
			if !slices.Contains(partners[c.id], partner) {
				partners[c.id] = append(partners[c.id], partner)
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
		c.kinds, c.partners = kinds[c.id], partners[c.id]
		out = append(out, c)
		turn = 1 - turn
	}
	return out
}

// neighbors reads the seeds' neighbors of the configured kinds at the read's
// revision and scope: the records within the adjacent window with weight 1
// at their distance, links with their weight at distance 1.
func (r *simpleRun) neighbors(ctx context.Context, seeds []*simpleItem, config *ExpandConfig) (map[string][]neighbor, error) {
	defer r.queryTime(time.Now())
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
	// The needs' coverage, of the gate and target alike, is that of the
	// View compose would show without the expansion.
	var covered []bool
	if config.Gate == expandNeeds || config.Target == expandNeeds {
		shown, err := r.wouldShow(ctx, scored)
		if err != nil {
			return nil, err
		}
		covered = r.covered(shown)
	}
	if config.Gate == expandNeeds && sufficient(stop, covered) {
		r.mu.Lock()
		r.stats.ExpandGated = true
		r.mu.Unlock()
		return scored, nil
	}
	seeds := expansionSeeds(scored, config.Seeds, config.seedMin())
	if !slices.ContainsFunc(config.Kinds, seedKind) {
		seeds = nil
	}
	r.mu.Lock()
	r.stats.ExpandSeeds = len(seeds)
	r.mu.Unlock()
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
	// Records already pooled are not proposed; with rejudge, those JEV
	// judged (the shortlist) are not.
	known := func(id string) bool { return r.items[id] != nil }
	if config.Rejudge {
		judged := map[string]bool{}
		for _, item := range scored {
			judged[item.ID] = true
		}
		known = func(id string) bool { return judged[id] }
	}
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
		if fromEvents, err = r.eventCandidates(ctx, config.EventsTop, known); err != nil {
			return nil, err
		}
	}
	candidates := alternate(fromSeeds, fromEvents, config.Limit)
	if len(candidates) == 0 {
		return scored, nil
	}
	// A pooled record (rejudge) keeps its item; the others are read.
	ids := []string{}
	for _, c := range candidates {
		if r.items[c.id] == nil {
			ids = append(ids, c.id)
		}
	}
	start := time.Now()
	values, err := evidence(ctx, r.agent.store, r.revision, r.req.Scope, ids)
	r.queryTime(start)
	if err != nil {
		return short()
	}
	found := map[string]Evidence{}
	for _, value := range values {
		found[value.ID] = value
	}
	items, kinds, partners := []*simpleItem{}, [][]string{}, map[*simpleItem][]*simpleItem{}
	for _, c := range candidates {
		item := r.items[c.id]
		if item == nil {
			value, ok := found[c.id]
			if !ok {
				continue
			}
			item = &simpleItem{ID: c.id, Evidence: value}
		}
		items, kinds = append(items, item), append(kinds, c.kinds)
		for _, id := range c.partners {
			if partner := r.items[id]; partner != nil {
				partners[item] = append(partners[item], partner)
			}
		}
	}
	// The candidates are judged with context peers: the seeds and the
	// current needed judged records (expansionPeers), and with each
	// candidate the seeds a correction links it to.
	batches := peerBatches(items, expansionPeers(scored, seeds), partners)
	if config.Target == expandNeeds {
		if err = r.target(ctx, items, uncovered(covered), len(batches)); err != nil {
			return nil, err
		}
	}
	if err = r.judgeExpansions(ctx, batches); err != nil {
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
		}
		// An answer counts from a current candidate, for a need target
		// asked.
		if r.targeted && current(item) {
			for _, k := range uncovered(covered) {
				answered[k] = answered[k] || item.Row[k] >= 0.5
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

// covered reports, for each need, whether the shown records give it: the
// need is not an ALL need (one wants every instance), and a shown record
// answers it in the need table (≥ 0.5; the screen for an untabled single
// need), as the View's needs report hasEvidence.
func (r *simpleRun) covered(shown []*simpleItem) []bool {
	out := make([]bool, len(r.needs))
	for k, need := range r.needs {
		out[k] = !need.All && slices.ContainsFunc(shown, func(item *simpleItem) bool { return r.answer(item.ID, k) >= 0.5 })
	}
	return out
}

// uncovered is the indexes of the needs not covered.
func uncovered(covered []bool) []int {
	out := []int{}
	for k, done := range covered {
		if !done {
			out = append(out, k)
		}
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
// (askNeeds) for each asked need (those not covered), and their rows are
// set; compose then ranks those that answer one first (ordered). With no
// need to ask it asks nothing. The judgement comes first: when the budget
// cannot pay for the need table and the judgement both, or the need table
// fails, the candidates keep the rows they had and the read is degraded
// (expansion-target). Only a canceled read is an error.
func (r *simpleRun) target(ctx context.Context, items []*simpleItem, asked []int, judgeCalls int) error {
	if len(asked) == 0 || len(items) == 0 {
		return nil
	}
	if calls := (len(items)+tableBatch-1)/tableBatch + judgeCalls; r.decisionsLeft() < calls {
		r.degrade("expansion-target")
		return nil
	}
	// A pooled record (rejudge) keeps the answers the loop tabled for the
	// other needs; a failure gives every candidate its rows back.
	before := map[*simpleItem][]float64{}
	for _, item := range items {
		before[item] = item.Row
	}
	set := func(item *simpleItem, row []float64) {
		if old := before[item]; old != nil {
			merged := slices.Clone(old)
			for _, k := range asked {
				merged[k] = row[k]
			}
			row = merged
		}
		item.Row = row
	}
	if err := r.askNeeds(ctx, items, asked, set); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		for _, item := range items {
			item.Row = before[item]
		}
		r.degrade("expansion-target")
		return nil
	}
	r.targeted = true
	return nil
}

// expandPeers bounds the context peers of an expansion judgement.
const expandPeers = 8

// expansionPeers is the context the expansion's candidates are judged with,
// as judgeAll's shared batches give each item the others: the seeds and the
// current judged items JEV calls needed (≥ 0.5), each once, best first
// (ties in the judged list's order), at most expandPeers.
func expansionPeers(scored, seeds []*simpleItem) []*simpleItem {
	peers := []*simpleItem{}
	for _, item := range judgedOrder(scored) {
		if len(peers) < expandPeers && (slices.Contains(seeds, item) || item.Needed >= 0.5 && current(item)) {
			peers = append(peers, item)
		}
	}
	return peers
}

// peerBatch is one judgement call of the expansion: its peers, context
// only, and its candidates.
type peerBatch struct{ peers, items []*simpleItem }

// peerBatches splits the candidates, in order, into judgement calls of at
// most judgeBatch items in all: each carries the peers, the partners of its
// candidates (the seeds a correction links them to) that are not peers
// already, and as many candidates as fit.
func peerBatches(items, peers []*simpleItem, partners map[*simpleItem][]*simpleItem) []peerBatch {
	batches, b := []peerBatch{}, peerBatch{peers: slices.Clone(peers)}
	extra := func(item *simpleItem) []*simpleItem {
		out := []*simpleItem{}
		for _, partner := range partners[item] {
			if !slices.Contains(b.peers, partner) && !slices.Contains(out, partner) {
				out = append(out, partner)
			}
		}
		return out
	}
	for _, item := range items {
		add := extra(item)
		if len(b.items) > 0 && len(b.peers)+len(add)+len(b.items)+1 > judgeBatch {
			batches, b = append(batches, b), peerBatch{peers: slices.Clone(peers)}
			add = extra(item)
		}
		b.peers, b.items = append(b.peers, add...), append(b.items, item)
	}
	if len(b.items) > 0 {
		batches = append(batches, b)
	}
	return batches
}

// judgeExpansions is judgeAll for the expansion's candidates with their
// peers: each call shows the peers first, then the candidates, and asks the
// needed and superseded questions (judgeAll's, J:45-62) of the candidates
// alone; nothing about a peer is asked or changed. The calls run in
// parallel; every candidate first takes Needed = Screen and Superseded = 0,
// which stand where its call is not made (past a spent decision budget: the
// first calls it can pay for run, and errCallBudget is returned) or fails
// (its error is returned).
func (r *simpleRun) judgeExpansions(ctx context.Context, batches []peerBatch) error {
	for _, b := range batches {
		for _, item := range b.items {
			item.Needed, item.Superseded, item.Judged = item.Screen, 0, false
		}
	}
	afford := min(len(batches), r.decisionsLeft())
	errs := make([]error, afford)
	var work sync.WaitGroup
	for i, b := range batches[:afford] {
		work.Add(1)
		go func() {
			defer work.Done()
			errs[i] = recovered(func() error {
				texts := []judgeCandidate{}
				for _, item := range slices.Concat(b.peers, b.items) {
					texts = append(texts, judgeCandidate{Source: judgeSource, Text: item.Evidence.Text})
				}
				state, questions := batchRequest(r.dialogue, texts)
				for p := range b.peers {
					delete(questions, fmt.Sprintf("n%d", p))
					delete(questions, fmt.Sprintf("s%d", p))
				}
				answers, err := r.decide(ctx, state, questions)
				if err != nil {
					return err
				}
				for k, item := range b.items {
					at := len(b.peers) + k
					item.Needed, item.Superseded, item.Judged = answers[fmt.Sprintf("n%d", at)], answers[fmt.Sprintf("s%d", at)], true
				}
				return nil
			})
		}()
	}
	work.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	if afford < len(batches) {
		return errCallBudget
	}
	return nil
}

// queryTime adds the time since start to the read's queryMs: the expansion's
// work that is not a model call (neighbors, events, evidence reads).
func (r *simpleRun) queryTime(start time.Time) {
	r.mu.Lock()
	r.stats.QueryMS += float64(time.Since(start).Microseconds()) / 1000
	r.mu.Unlock()
}
