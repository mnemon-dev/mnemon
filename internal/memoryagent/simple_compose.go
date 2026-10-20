package memoryagent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
)

// The end of the simple read (author N:978-1044 in simple mode): the
// shortlist of what could enter the View, JEV's full judgement of it (needed
// and superseded, batchRequest J:45-62 inline), and the View composed from it
// (compose N:187-218). With View.Fusion "rrf" the View is instead filled from
// the reciprocal-rank fusion of the judged order and the message search, so a
// View larger than the author's 16 gets the search's ranking past what JEV
// judged. Lengths are JavaScript string lengths (UTF-16 units).

const (
	judgeBatch    = 40 // candidates a judgement call
	shortlistSize = 64
	rrfK          = 60
)

// batchState is batchRequest's inline state: no rubric, each question carries
// its own criteria.
type batchState struct {
	Dialogue   []judgeMessage   `json:"dialogue"`
	Candidates []judgeCandidate `json:"candidates"`
}

// batchRequest is the author's batchRequest(dialogue, candidates, 'inline',
// 600, 'id', 'needed') (J:45-62): the last six messages excerpted to 600
// units, the candidates labelled c0, c1, … with their source and their whole
// text excerpted to 600, and for each candidate the needed question n<i> and
// the superseded question s<i> (peers included), each with its criteria.
// Candidate ids are set here; the given ones are ignored.
func batchRequest(dialogue []Message, candidates []judgeCandidate) (batchState, map[string]Question) {
	state, questions := batchState{Dialogue: judgeDialogue(dialogue, 6, simpleListing), Candidates: []judgeCandidate{}}, map[string]Question{}
	for i, candidate := range candidates {
		id := fmt.Sprintf("c%d", i)
		state.Candidates = append(state.Candidates, judgeCandidate{ID: id, Source: candidate.Source, Text: authorExcerpt(candidate.Text, simpleListing)})
		questions[fmt.Sprintf("n%d", i)] = Question{Type: "noul", Instructions: neededQuestion(byID(id)), Criteria: map[string]string{"true": neededYes, "false": neededNo}}
		questions[fmt.Sprintf("s%d", i)] = Question{Type: "noul", Instructions: supersededQuestion(byID(id), true), Criteria: map[string]string{"true": supersededYes, "false": supersededNo}}
	}
	return state, questions
}

// tableItems is the need table's items in table order.
func (r *simpleRun) tableItems() []*simpleItem {
	out := []*simpleItem{}
	for _, id := range r.rows {
		out = append(out, r.items[id])
	}
	return out
}

// shortlist is what JEV judges (N:1002-1009 in simple mode): for each need,
// the table's items JEV says give it, best answer first (ties in table
// order), then the screened list (the author's `screened`: the screen's yes
// and its best 16, r.viewable(r.screened())); each item once, the first
// kept, at most 64. table is the need table in table order.
func shortlist(table, screened []*simpleItem, needs []Need) []*simpleItem {
	list := []*simpleItem{}
	for k := range needs {
		yes := []*simpleItem{}
		for _, item := range table {
			if k < len(item.Row) && item.Row[k] >= 0.5 {
				yes = append(yes, item)
			}
		}
		slices.SortStableFunc(yes, func(a, b *simpleItem) int { return probabilityOrder(a.Row[k], b.Row[k]) })
		list = append(list, yes...)
	}
	out, seen := []*simpleItem{}, map[string]bool{}
	for _, item := range append(list, screened...) {
		if !seen[item.ID] && len(out) < shortlistSize {
			seen[item.ID] = true
			out = append(out, item)
		}
	}
	return out
}

// judgeAll asks JEV of every listed item whether the reply should use it and
// whether it is no longer current (judgeAll N:978-986): batches of 40 in
// parallel, each candidate its whole text excerpted to 600 units, the
// questions n<i> and s<i> with their criteria (J:45-62, inline, by id). Every
// item first takes Needed = Screen and Superseded = 0, which stand where its
// batch is not judged: past a spent decision budget (only the first batches
// it can pay for run, and errCallBudget is returned) or after a failed call
// (its error is returned). The read goes on with what was judged.
func (r *simpleRun) judgeAll(ctx context.Context, list []*simpleItem) error {
	for _, item := range list {
		item.Needed, item.Superseded, item.Judged = item.Screen, 0, false
	}
	batches := [][]*simpleItem{}
	for start := 0; start < len(list); start += judgeBatch {
		batches = append(batches, list[start:min(start+judgeBatch, len(list))])
	}
	afford := min(len(batches), r.decisionsLeft())
	errs := make([]error, afford)
	var work sync.WaitGroup
	for b, batch := range batches[:afford] {
		work.Add(1)
		go func() {
			defer work.Done()
			errs[b] = recovered(func() error {
				texts := []judgeCandidate{}
				for _, item := range batch {
					texts = append(texts, judgeCandidate{Source: judgeSource, Text: item.Evidence.Text})
				}
				state, questions := batchRequest(r.dialogue, texts)
				answers, err := r.decide(ctx, state, questions)
				if err != nil {
					return err
				}
				for i, item := range batch {
					item.Needed, item.Superseded, item.Judged = answers[fmt.Sprintf("n%d", i)], answers[fmt.Sprintf("s%d", i)], true
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

// current reports whether JEV does not call the item no longer current.
func current(item *simpleItem) bool { return item.Superseded < 0.5 }

// judgedOrder is the judged items by Needed, best first; ties keep their order.
func judgedOrder(scored []*simpleItem) []*simpleItem {
	order := slices.Clone(scored)
	slices.SortStableFunc(order, func(a, b *simpleItem) int { return probabilityOrder(a.Needed, b.Needed) })
	return order
}

// viewFill is a View being filled (compose's fits and take): an item fits
// when its whitespace-collapsed text is not shown yet, the View holds fewer
// than maxItems and its length keeps the View within maxChars.
type viewFill struct {
	maxItems, maxChars, used int
	selected                 []*simpleItem
	texts                    map[string]bool
}

func newViewFill(maxItems, maxChars int) *viewFill {
	return &viewFill{maxItems: maxItems, maxChars: maxChars, texts: map[string]bool{}}
}

func (v *viewFill) fits(item *simpleItem) bool {
	return !v.texts[collapseSpace(item.Evidence.Text)] && len(v.selected) < v.maxItems && v.used+jsLen(item.Evidence.Text) <= v.maxChars
}

func (v *viewFill) take(item *simpleItem) {
	v.selected = append(v.selected, item)
	v.used += jsLen(item.Evidence.Text)
	v.texts[collapseSpace(item.Evidence.Text)] = true
}

// cut reports whether the View left out a candidate for want of items or
// characters: one not shown whose text is not shown either.
func (v *viewFill) cut(candidates []*simpleItem) bool {
	return slices.ContainsFunc(candidates, func(item *simpleItem) bool {
		return !slices.Contains(v.selected, item) && !v.texts[collapseSpace(item.Evidence.Text)]
	})
}

// composeSimple is the author's compose (N:187-218) as simple mode calls it
// (N:1039-1044): enter, stay and withdraw at JEV's own line 0.5, a fill to
// maxItems unless the plan named no need, no fill floor, and no groups (a
// per-group cap of maxItems binds nothing). Items go by Needed, best first
// (ties in shortlist order); an item is current while Superseded is under
// 0.5. Every current item JEV calls needed enters if it fits; then the fill
// takes the other current items in order while they fit. An item that does
// not fit is skipped. Withdrawn are the items JEV calls needed but no longer
// current (the author's reason "superseded").
func composeSimple(scored []*simpleItem, needless bool, maxItems, maxChars int) (selected, withdrawn []*simpleItem) {
	v := composed(scored, needless, maxItems, maxChars)
	for _, item := range judgedOrder(scored) {
		if !current(item) && item.Needed >= 0.5 {
			withdrawn = append(withdrawn, item)
		}
	}
	return v.selected, withdrawn
}

func composed(scored []*simpleItem, needless bool, maxItems, maxChars int) *viewFill {
	v, _ := composedFrom(judgedOrder(scored), 0, needless, maxItems, maxChars)
	return v
}

// composedFrom is composed over items in compose's order, with up to
// reserve slots held for expansions (view.expand.reserve). With a reserve,
// first the current needed (≥ 0.5) records that are not expansions take
// their slots and characters, then up to reserve expansions are held
// (heldExpansions), then the needed records and the fill run as without it
// over what is left; the View lists what it took in compose's order.
// Without a reserve it is composed. It returns the held items.
func composedFrom(order []*simpleItem, reserve int, needless bool, maxItems, maxChars int) (*viewFill, []*simpleItem) {
	fill := maxItems
	if needless {
		fill = 0
	}
	v := newViewFill(maxItems, maxChars)
	var held []*simpleItem
	if reserve > 0 {
		takeEvidence(v, order)
		held = heldExpansions(v, order, reserve, needless)
	}
	for _, item := range order {
		if current(item) && item.Needed >= 0.5 && v.fits(item) {
			v.take(item)
		}
	}
	for _, item := range order {
		if len(v.selected) >= fill {
			break
		}
		if current(item) && !slices.Contains(v.selected, item) && v.fits(item) {
			v.take(item)
		}
	}
	if reserve > 0 {
		v.selected = slices.DeleteFunc(slices.Clone(order), func(item *simpleItem) bool { return !slices.Contains(v.selected, item) })
	}
	return v, held
}

// takeEvidence takes into v the current needed (≥ 0.5) items of order that
// are not expansions, while they fit: a held slot never displaces them.
func takeEvidence(v *viewFill, order []*simpleItem) {
	for _, item := range order {
		if !item.Expanded && current(item) && item.Needed >= 0.5 && v.fits(item) {
			v.take(item)
		}
	}
}

// heldExpansions takes into v the first reserve current expansions of order
// that fit, whatever JEV's needed answer (needed ones only when the plan
// named no need, which shows nothing else), and returns them.
func heldExpansions(v *viewFill, order []*simpleItem, reserve int, needless bool) []*simpleItem {
	held := []*simpleItem{}
	for _, item := range order {
		if len(held) < reserve && item.Expanded && current(item) && (!needless || item.Needed >= 0.5) && v.fits(item) {
			v.take(item)
			held = append(held, item)
		}
	}
	return held
}

// ordered is the judged items in compose's order: judgedOrder (by Needed,
// best first), and when target asked the expansions the need table's
// question, each expansion that answers no need moves after the last
// needed (≥ 0.5) current item that answers one (a need-table answer ≥ 0.5;
// for the items JEV tabled in the loop, of any need), keeping its order
// among those moved.
func (r *simpleRun) ordered(scored []*simpleItem) []*simpleItem {
	order := judgedOrder(scored)
	if !r.targeted {
		return order
	}
	answers := func(item *simpleItem) bool {
		return slices.ContainsFunc(item.Row, func(answer float64) bool { return answer >= 0.5 })
	}
	last := -1
	for i, item := range order {
		if answers(item) && item.Needed >= 0.5 && current(item) {
			last = i
		}
	}
	out, moved := []*simpleItem{}, []*simpleItem{}
	for i, item := range order {
		if i < last && item.Expanded && !answers(item) {
			moved = append(moved, item)
			continue
		}
		out = append(out, item)
		if i == last {
			out = append(out, moved...)
		}
	}
	return out
}

// wouldShow is the records compose would show without the expansion, as
// far as the read can tell before the timelines layer: compose's selection
// of the judged list (selection, the very selection compose makes), and
// with the layer only the leading records the layer's character trim can
// never drop, those within the View's characters less the most the
// layer's blocks can take (layerBlocksMax). It makes no model call; under
// fusion it reads the evidence of raw hits not pooled, as compose does,
// and that time counts in queryMs.
func (r *simpleRun) wouldShow(ctx context.Context, scored []*simpleItem) ([]*simpleItem, error) {
	start := time.Now()
	v, _, _, _, err := r.selection(ctx, scored)
	r.queryTime(start)
	if err != nil {
		return nil, err
	}
	if !r.agent.layerOn() {
		return v.selected, nil
	}
	room, kept := r.agent.config.View.MaxCharacters-layerBlocksMax, []*simpleItem{}
	for _, item := range v.selected {
		if room -= jsLen(item.Evidence.Text); room < 0 {
			break
		}
		kept = append(kept, item)
	}
	return kept, nil
}

// reserve is the View slots held for expansions: view.expand.reserve, at
// most half the View's items (so a small View keeps its evidence); 0
// without expansion.
func (r *simpleRun) reserve() int {
	if expand := r.agent.config.View.Expand; expand != nil {
		return min(expand.Reserve, r.req.MaxItems/2)
	}
	return 0
}

// eligible is what composeSimple may show: the current items JEV calls
// needed, and with a fill every current item.
func eligible(scored []*simpleItem, needless bool) []*simpleItem {
	return slices.DeleteFunc(slices.Clone(scored), func(item *simpleItem) bool { return !current(item) || needless && item.Needed < 0.5 })
}

// rrfScores sums 1/(k+rank) over the lists for every id, ranks from 0.
func rrfScores(lists [][]string, k int) map[string]float64 {
	score := map[string]float64{}
	for _, list := range lists {
		for rank, id := range list {
			score[id] += 1 / float64(k+rank)
		}
	}
	return score
}

// fuseRRF orders the ids of the lists by reciprocal-rank fusion, best first;
// ties keep the order ids were first seen in.
func fuseRRF(lists [][]string, k int) []string {
	score, ids, seen := rrfScores(lists, k), []string{}, map[string]bool{}
	for _, list := range lists {
		for _, id := range list {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	slices.SortStableFunc(ids, func(a, b string) int { return probabilityOrder(score[a], score[b]) })
	return ids
}

// fused is the fusion View's candidates in order, with their fused scores:
// the reciprocal-rank fusion (k 60) of the judged order (the current judged
// items by Needed) and the message search's whole ranking (up to 200 ids,
// not only the pages read), then the rest of the pool in pool order; what
// JEV calls no longer current is left out. Hits the pool does not hold are
// read through evidence(); those without evidence are skipped.
func (r *simpleRun) fused(ctx context.Context, scored []*simpleItem) ([]*simpleItem, map[string]float64, error) {
	judged := []string{}
	for _, item := range r.ordered(scored) {
		if current(item) {
			judged = append(judged, item.ID)
		}
	}
	raw := []string{}
	for _, s := range r.searches {
		if s.Cue.Kind == "message" {
			raw = s.IDs
			break
		}
	}
	lists := [][]string{judged, raw}
	order, seen := fuseRRF(lists, rrfK), map[string]bool{}
	for _, id := range order {
		seen[id] = true
	}
	for _, id := range r.order {
		if !seen[id] {
			order = append(order, id)
		}
	}
	unpooled := slices.DeleteFunc(slices.Clone(order), func(id string) bool { return r.items[id] != nil })
	hits := map[string]*simpleItem{}
	for start := 0; start < len(unpooled); start += 200 {
		values, err := evidence(ctx, r.agent.store, r.revision, r.req.Scope, unpooled[start:min(start+200, len(unpooled))])
		if err != nil {
			return nil, nil, err
		}
		for _, value := range values {
			hits[value.ID] = &simpleItem{ID: value.ID, Evidence: value}
		}
	}
	candidates := []*simpleItem{}
	for _, id := range order {
		item := r.items[id]
		if item == nil {
			item = hits[id]
		}
		if item != nil && current(item) {
			candidates = append(candidates, item)
		}
	}
	return candidates, rrfScores(lists, rrfK), nil
}

// compose makes the View from the judged shortlist: composeSimple's
// selection scored by Needed, or with fusion the fused order filled by the
// same fit, scored by the fused score. Truncated reports a current candidate
// cut for want of items or characters. Withdrawn items have no place in the
// View and are dropped.
func (r *simpleRun) compose(ctx context.Context, stop string, scored []*simpleItem) (View, error) {
	v, candidates, score, held, err := r.selection(ctx, scored)
	if err != nil {
		return View{}, err
	}
	r.held = map[string]bool{}
	for _, item := range held {
		r.held[item.ID] = true
	}
	records, err := r.records(ctx, v.selected, score)
	if err != nil {
		return View{}, err
	}
	return View{Revision: r.revision, Policy: r.policy(), Records: records, Needs: r.viewNeeds(records), StopReason: stop, Truncated: v.cut(candidates), Stats: r.statsNow()}, nil
}

// selection is what compose shows of the judged list: the filled View (its
// items in View order), the candidates whose cut marks the View truncated,
// the shown items' scores and the expansions held reserved slots.
// composeSimple's selection scored by Needed, or with fusion the fused order
// filled by the same fit, scored by the fused score. With a reserve the
// current needed records that are not expansions take their slots first,
// then the held expansions (composedFrom; under fusion the View lists the
// records that are not held in fused order, then the held ones). Under
// fusion it reads the evidence of raw hits not pooled.
func (r *simpleRun) selection(ctx context.Context, scored []*simpleItem) (v *viewFill, candidates []*simpleItem, score map[string]float64, held []*simpleItem, err error) {
	maxItems, maxChars := r.req.MaxItems, r.agent.config.View.MaxCharacters
	score = map[string]float64{}
	if r.agent.config.View.Fusion != "rrf" {
		v, held = composedFrom(r.ordered(scored), r.reserve(), r.needless, maxItems, maxChars)
		for _, item := range v.selected {
			score[item.ID] = item.Needed
		}
		return v, eligible(scored, r.needless), score, held, nil
	}
	if candidates, score, err = r.fused(ctx, scored); err != nil {
		return nil, nil, nil, nil, err
	}
	v = newViewFill(maxItems, maxChars)
	reserve := r.reserve()
	if reserve > 0 {
		takeEvidence(v, candidates)
		held = heldExpansions(v, r.ordered(scored), reserve, r.needless)
	}
	for _, item := range candidates {
		if v.fits(item) {
			v.take(item)
		}
	}
	if reserve > 0 {
		listed := slices.DeleteFunc(slices.Clone(candidates), func(item *simpleItem) bool {
			return !slices.Contains(v.selected, item) || slices.Contains(held, item)
		})
		v.selected = append(listed, held...)
	}
	return v, candidates, score, held, nil
}

// viewNeeds is the plan's needs as a View of records reports them: covered
// when the loop met or finished the need, with evidence when a shown record
// gives it.
func (r *simpleRun) viewNeeds(records []Evidence) []Need {
	needs := []Need{}
	for k, need := range r.needs {
		need.Covered = k < len(r.needStates) && (r.needStates[k] == needMet || r.needStates[k] == needDone)
		need.HasEvidence = slices.ContainsFunc(records, func(record Evidence) bool { return r.answer(record.ID, k) >= 0.5 })
		need.Evaluated = r.tabled()
		needs = append(needs, need)
	}
	return needs
}

// records re-reads the shown items through evidence(), which proves each
// chunk against its original, checks they are the evidence judged, and
// returns them in View order with their scores.
func (r *simpleRun) records(ctx context.Context, shown []*simpleItem, score map[string]float64) ([]Evidence, error) {
	ids := []string{}
	for _, item := range shown {
		ids = append(ids, item.ID)
	}
	values := map[string]Evidence{}
	for start := 0; start < len(ids); start += 200 {
		batch, err := evidence(ctx, r.agent.store, r.revision, r.req.Scope, ids[start:min(start+200, len(ids))])
		if err != nil {
			return nil, err
		}
		for _, value := range batch {
			values[value.ID] = value
		}
	}
	out := []Evidence{}
	for _, item := range shown {
		value, ok := values[item.ID]
		if !ok || value.Hash != item.Evidence.Hash {
			return nil, errors.New("evidence changed inside pinned revision")
		}
		value.Score = score[item.ID]
		out = append(out, value)
	}
	return out, nil
}
