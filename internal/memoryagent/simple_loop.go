package memoryagent

import (
	"context"
	"errors"
	"slices"
)

// The simple loop (author simpleLoop, N:695-795): a judge-act loop whose only
// numbers are its budgets (3 rounds, the pages and JEV calls of the read, the
// View's items). JEV tables what could enter the View (the screen's yes and
// its best MaxItems) against each need, and each open need takes at most one
// step a round:
//   - a single need JEV answers yes for (0.5) is met;
//   - a need for every instance (the plan's ALL, or two records that give it)
//     reads the next page of each search that found new yes, and is done once
//     a round brings none;
//   - a need with no yes reads the next page of the search that ranked its
//     lead (the item JEV answers best) highest; after a page that did not help
//     (no new yes, no lead among what the round read) the planner words a new
//     search, and a need both failed for is given up.

const simpleRounds = 3 // the author's `loop`

// Loop stop reasons; a spent budget ends the loop with the reason it had.
const (
	stopCovered = "covered"
	stopGivenUp = "given up"
	stopRounds  = "rounds"
)

// Need states and round steps.
const (
	needOpen    = "open"
	needMet     = "met"
	needDone    = "done"
	needGivenUp = "given up"
	stepNone    = "none"
	stepPage    = "page"
	stepSearch  = "search"
)

// placement is the search that ranked an item highest, and that rank,
// counted from the search's first page.
type placement struct{ at, rank int }

type needFailure struct{ page, search bool }

// answer is the table's answer for item id and need k; 0 when untabled.
func (r *simpleRun) answer(id string, k int) float64 {
	if item := r.items[id]; item != nil && k < len(item.Row) {
		return item.Row[k]
	}
	return 0
}

// yes is the tabled items JEV answers need k yes for, in table order.
func (r *simpleRun) yes(k int) []string {
	out := []string{}
	for _, id := range r.rows {
		if r.answer(id, k) >= 0.5 {
			out = append(out, id)
		}
	}
	return out
}

// lead is the tabled item with the best answer for need k, then the best
// screen; the first in table order on ties (the author's stable sort). It
// is "" while the table is empty.
func (r *simpleRun) lead(k int) string {
	best := ""
	for _, id := range r.rows {
		if best == "" || r.answer(id, k) > r.answer(best, k) || r.answer(id, k) == r.answer(best, k) && r.items[id].Screen > r.items[best].Screen {
			best = id
		}
	}
	return best
}

// viewable is what of ids could enter the View: screened items by screen,
// best first (stable), those ranked under MaxItems or screened yes. MaxItems
// is the read's View size (the author's config.maxItems, 16 by default).
func (r *simpleRun) viewable(ids []string) []*simpleItem {
	items := []*simpleItem{}
	for _, id := range ids {
		if item := r.items[id]; item != nil && item.HasScreen {
			items = append(items, item)
		}
	}
	slices.SortStableFunc(items, func(a, b *simpleItem) int { return probabilityOrder(a.Screen, b.Screen) })
	out := []*simpleItem{}
	for rank, item := range items {
		if rank < r.req.MaxItems || item.Screen >= 0.5 {
			out = append(out, item)
		}
	}
	return out
}

// screened is the ids JEV has screened, in pool order.
func (r *simpleRun) screened() []string {
	return slices.DeleteFunc(slices.Clone(r.order), func(id string) bool { return !r.items[id].HasScreen })
}

// loop runs the simple loop over the screened pool and the searches run so
// far, each read only to its first page, and returns why it stopped:
// covered, given up or rounds. A spent JEV budget ends it with the reason it
// had (rounds); spent page reads read nothing, as in the author. New finds
// are pooled, screened (their Screen set) and tabled like the first round's.
func (r *simpleRun) loop(ctx context.Context) (string, error) {
	stop := stopRounds
	ended := func(err error) (string, error) {
		if errors.Is(err, errCallBudget) {
			return stop, nil
		}
		return "", err
	}
	// Each search's latest page, and for every item the search that ranked it
	// highest; a tie keeps the search that ranked it first.
	latest, offset, place := [][]string{}, []int{}, map[string]placement{}
	locate := func(at int, page []string) {
		for i, id := range page {
			rank := offset[at] + i
			if was, ok := place[id]; !ok || rank < was.rank {
				place[id] = placement{at, rank}
			}
		}
	}
	for at, s := range r.searches {
		latest, offset = append(latest, s.IDs[:s.Offset]), append(offset, 0)
		locate(at, latest[at])
	}
	pageable := func(at int) bool { return !r.searches[at].Exhausted }
	if err := r.table(ctx, r.viewable(r.screened())); err != nil {
		return ended(err)
	}
	needs := r.needs
	every, state, failed := make([]bool, len(needs)), make([]string, len(needs)), make([]needFailure, len(needs))
	for k, need := range needs {
		every[k], state[k] = need.All, needOpen
	}
	// The planner is required by the simple strategy, so a need can always be
	// searched for (the author's `searchable`).
	last := map[string]bool{}
	for _, id := range r.screened() {
		last[id] = true
	}
	for round := 1; round <= simpleRounds; round++ {
		steps, pages, lost := make([]string, len(needs)), []int{}, []Need{}
		addPage := func(at int) {
			if !slices.Contains(pages, at) {
				pages = append(pages, at)
			}
		}
		for k, need := range needs {
			steps[k] = stepNone
			if state[k] != needOpen {
				continue
			}
			found := r.yes(k)
			if len(found) >= 2 {
				every[k] = true
			}
			if len(found) > 0 && !every[k] {
				state[k], steps[k] = needMet, needMet
				continue
			}
			if len(found) > 0 {
				// Along the searches that found this round's new yes.
				along := []int{}
				for _, id := range found {
					if p, ok := place[id]; last[id] && ok && !slices.Contains(along, p.at) {
						along = append(along, p.at)
					}
				}
				along = slices.DeleteFunc(along, func(at int) bool { return !pageable(at) })
				if len(along) == 0 {
					state[k], steps[k] = needDone, needDone
					continue
				}
				for _, at := range along {
					addPage(at)
				}
				steps[k] = stepPage
				continue
			}
			p, placed := place[r.lead(k)]
			switch {
			case failed[k].page && !failed[k].search:
				lost, steps[k] = append(lost, need), stepSearch
			case placed && pageable(p.at) && !failed[k].page:
				addPage(p.at)
				steps[k] = stepPage
			case !failed[k].search:
				lost, steps[k] = append(lost, need), stepSearch
			default:
				state[k], steps[k] = needGivenUp, needGivenUp
			}
		}
		if len(pages) == 0 && len(lost) == 0 {
			stop = stopCovered
			if slices.Contains(state, needGivenUp) {
				stop = stopGivenUp
			}
			break
		}
		r.rounds = round
		type mark struct {
			yes  map[string]bool
			lead string
		}
		before := []mark{}
		for k := range needs {
			yes := map[string]bool{}
			for _, id := range r.yes(k) {
				yes[id] = true
			}
			before = append(before, mark{yes, r.lead(k)})
		}
		fresh, seen := []string{}, map[string]bool{}
		addFresh := func(ids []string) {
			for _, id := range ids {
				if !seen[id] {
					seen[id] = true
					fresh = append(fresh, id)
				}
			}
		}
		for _, at := range pages {
			paged := r.next(r.searches[at])
			offset[at] += len(latest[at])
			latest[at] = paged
			locate(at, paged)
			addFresh(paged)
		}
		if len(lost) > 0 {
			cues, err := r.rewrite(ctx, lost)
			if err != nil {
				return "", err
			}
			first := len(r.searches)
			found, err := r.runCues(ctx, cues)
			if err != nil {
				return "", err
			}
			for j, page := range found {
				latest, offset = append(latest, page), append(offset, 0)
				locate(first+j, page)
				addFresh(page)
			}
		}
		if _, err := r.admit(ctx, fresh); err != nil {
			return "", err
		}
		unseen, unseenItems := []string{}, []*simpleItem{}
		for _, id := range fresh {
			if item := r.items[id]; item != nil && !item.HasScreen {
				unseen, unseenItems = append(unseen, id), append(unseenItems, item)
			}
		}
		if len(unseen) > 0 {
			if err := r.scan(ctx, unseenItems); err != nil {
				return ended(err)
			}
			if err := r.table(ctx, r.viewable(unseen)); err != nil {
				return ended(err)
			}
		}
		last = map[string]bool{}
		for _, id := range unseen {
			last[id] = true
		}
		// A step helped when it brought a new yes, or a lead among what it read.
		for k := range needs {
			if steps[k] != stepPage && steps[k] != stepSearch {
				continue
			}
			lead := r.lead(k)
			improved := slices.ContainsFunc(r.yes(k), func(id string) bool { return !before[k].yes[id] }) || lead != before[k].lead && last[lead]
			switch {
			case improved:
				failed[k] = needFailure{}
			case steps[k] == stepPage:
				failed[k].page = true
			default:
				failed[k].search = true
			}
		}
	}
	return stop, nil
}
