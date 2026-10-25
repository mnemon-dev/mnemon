package memoryagent

import (
	"cmp"
	"context"
	"errors"
	"slices"

	"github.com/mnemon-dev/mnemon/internal/records"
)

// The linked loop (view.loopLinks) is the simple loop as one cycle. Each round
// JEV screens and tables the records new to it (the first round's are the
// pool, screened before the loop), and then the loop stops when no need is
// open, when a round after the first brought nothing JEV found useful, after
// loopLinkedRounds rounds, or when the read's JEV calls are down to the
// reserve the judgement, the layer and the expansions after the loop need.
// Otherwise it pools, for the next round, the first page of new searches the
// planner words for the needs still open, and every record the links tie to
// what this round found useful: the consolidation links with
// consolidation.nativeEdges (records.Store.Links: the derived metadata it
// writes from the consolidated threads and value updates), and the one-shot
// links with consolidation.oneShotLinks (oneshot_links.go); each source only
// with the switch that keeps it current. A rewrite that fails ends the loop,
// not the read. There is no paging and no plain neighbour: new searches and
// the models' links stand in for them, and each record is judged and
// followed once.

const (
	loopLinkedRounds = 10
	// loopJudgeReserve is the JEV calls the loop leaves for the judgement,
	// the layer and the expansions after it.
	loopJudgeReserve = 8
	// loopItemsPerCall is about how many new records one JEV call pays for in
	// a round: a screen call takes about 37 full excerpts, a table call 24.
	loopItemsPerCall = 14
)

// The linked loop's own stop reasons, beside stopCovered and stopRounds.
const (
	stopNothingNew = "nothing new"
	stopReserve    = "reserve"
	stopRewrite    = "rewrite failed"
)

// linkedLoop runs the linked loop over the screened pool and returns why it
// stopped.
func (r *simpleRun) linkedLoop(ctx context.Context) (string, error) {
	ended := func(err error) (string, error) {
		if errors.Is(err, errCallBudget) {
			r.degrade("loop-budget")
			return stopReserve, nil
		}
		return "", err
	}
	if err := r.table(ctx, r.viewable(r.screened())); err != nil {
		return ended(err)
	}
	needs := r.needs
	state, every, before := make([]string, len(needs)), make([]bool, len(needs)), make([]map[string]bool, len(needs))
	for k, need := range needs {
		state[k], every[k], before[k] = needOpen, need.All, map[string]bool{}
	}
	r.needStates = state // the View's coverage reads the final states
	fresh, expanded := r.screened(), map[string]bool{}
	for round := 1; ; round++ {
		// A single need with a yes is met; a need for every instance (the
		// plan's ALL, or two records that give it) is done once a round brings
		// no new yes.
		open := []Need{}
		for k, need := range needs {
			if state[k] != needOpen {
				continue
			}
			found := r.yes(k)
			if len(found) >= 2 {
				every[k] = true
			}
			switch {
			case len(found) > 0 && !every[k]:
				state[k] = needMet
			case len(found) > 0 && round > 1 && !slices.ContainsFunc(found, func(id string) bool { return !before[k][id] }):
				state[k] = needDone
			default:
				open = append(open, need)
			}
			before[k] = map[string]bool{}
			for _, id := range found {
				before[k][id] = true
			}
		}
		useful := slices.DeleteFunc(slices.Clone(fresh), func(id string) bool { return !r.useful(id) })
		left := r.decisionsLeft() - loopJudgeReserve
		switch {
		case len(open) == 0:
			return stopCovered, nil
		case round > 1 && len(useful) == 0:
			return stopNothingNew, nil
		case round > loopLinkedRounds:
			return stopRounds, nil
		case left <= 0:
			return stopReserve, nil
		}
		r.rounds = round
		next, seen := []string{}, map[string]bool{}
		add := func(ids []string) {
			for _, id := range ids {
				if !seen[id] && r.items[id] == nil {
					seen[id] = true
					next = append(next, id)
				}
			}
		}
		rewritten := true
		if cues, err := r.rewrite(ctx, open); err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			r.degrade("loop-rewrite")
			rewritten = false
		} else {
			pages, err := r.runCues(ctx, cues)
			if err != nil {
				return "", err
			}
			for _, page := range pages {
				add(page)
			}
		}
		linked, err := r.linkedFresh(ctx, useful, expanded)
		if err != nil {
			return "", err
		}
		add(linked)
		r.mu.Lock()
		r.stats.LoopLinked += len(linked)
		r.mu.Unlock()
		// What the calls left past the reserve can judge; the new searches'
		// finds first.
		next = next[:min(len(next), left*loopItemsPerCall)]
		if _, err := r.admit(ctx, next); err != nil {
			return "", err
		}
		unseen, items := []string{}, []*simpleItem{}
		for _, id := range next {
			if item := r.items[id]; item != nil && !item.HasScreen {
				unseen, items = append(unseen, id), append(items, item)
			}
		}
		if len(items) > 0 {
			if err := r.scan(ctx, items); err != nil {
				return ended(err)
			}
			if err := r.table(ctx, r.viewable(unseen)); err != nil {
				return ended(err)
			}
		}
		fresh = unseen
		switch {
		case !rewritten:
			return stopRewrite, nil
		case len(unseen) == 0:
			return stopNothingNew, nil
		}
	}
}

// useful reports whether JEV found item id useful: screened yes, or a yes for
// a need.
func (r *simpleRun) useful(id string) bool {
	item := r.items[id]
	if item == nil {
		return false
	}
	if item.HasScreen && item.Screen >= 0.5 {
		return true
	}
	return slices.ContainsFunc(item.Row, func(p float64) bool { return p >= 0.5 })
}

// linkedFresh is the records the links tie to useful's items the loop has
// not followed yet, not pooled yet: for each item in useful's order, its
// consolidation links strongest first, then its one-shot links nearest in
// write order first, each source only with its switch. An item is marked
// followed once its links were read; links it cannot read are no
// candidates. It calls no model.
func (r *simpleRun) linkedFresh(ctx context.Context, useful []string, expanded map[string]bool) ([]string, error) {
	seeds := slices.DeleteFunc(slices.Clone(useful), func(id string) bool { return expanded[id] })
	settings := r.agent.config.Consolidation
	if len(seeds) == 0 || settings == nil {
		return nil, nil
	}
	links := map[string][]records.Link{}
	if settings.NativeEdges {
		var err error
		if links, err = r.agent.store.Links(ctx, r.revision, r.req.Scope, seeds); err != nil {
			return nil, ctx.Err()
		}
	}
	shots := map[string][]string{}
	if settings.OneShotLinks && r.agent.overlay != nil {
		var err error
		if shots, err = r.oneShotNeighbours(ctx, seeds); err != nil {
			return nil, ctx.Err()
		}
	}
	out, taken := []string{}, map[string]bool{}
	add := func(id string) {
		if !taken[id] && r.items[id] == nil {
			taken[id] = true
			out = append(out, id)
		}
	}
	for _, id := range seeds {
		expanded[id] = true
		list := slices.Clone(links[id])
		slices.SortStableFunc(list, func(a, b records.Link) int { return cmp.Compare(b.Weight, a.Weight) })
		for _, l := range list {
			add(l.ID)
		}
		for _, other := range shots[id] {
			add(other)
		}
	}
	return out, nil
}

// oneShotNeighbours is, for each of ids, the records a one-shot link joins
// it with, either way, nearest in write order first.
func (r *simpleRun) oneShotNeighbours(ctx context.Context, ids []string) (map[string][]string, error) {
	links, err := r.agent.overlay.linksOf(ctx, r.req.Scope, ids)
	if err != nil {
		return nil, err
	}
	type near struct {
		id       string
		distance int64
	}
	ends := map[string][]near{}
	for _, l := range links {
		d := max(l.Position-l.TargetPosition, l.TargetPosition-l.Position)
		ends[l.ID] = append(ends[l.ID], near{l.Target, d})
		ends[l.Target] = append(ends[l.Target], near{l.ID, d})
	}
	out := map[string][]string{}
	for _, id := range ids {
		list := ends[id]
		slices.SortFunc(list, func(a, b near) int { return cmp.Or(cmp.Compare(a.distance, b.distance), cmp.Compare(a.id, b.id)) })
		for _, n := range list {
			out[id] = append(out[id], n.id)
		}
	}
	return out, nil
}
