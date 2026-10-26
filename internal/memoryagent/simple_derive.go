package memoryagent

import (
	"context"
	"errors"
	"slices"
	"strings"
)

// The derive loop (view.deriveLoop,
// docs/superpowers/specs/2026-10-26-neutral-memory-and-single-pass-search-design.md):
// the planner writes every search and need in one call (planRound, round 0),
// the message being a search too. The screened pool is tabled against the
// needs; while a need is open, a round asks the planner for the searches the
// open needs still lack (and any need it now sees), searches them (first
// pages, mixed as the first searches are), and screens and tables what is
// new. A need without an ALL mark is covered once an item gives it; an ALL
// need stays open while a round finds new items giving it. Rounds end when
// no need is open, after simpleRounds, once roundRecallMax items give a
// need, or when the planner writes no new search. The shortlist, judgement
// and fill follow as in the original loop. No call or page budget applies.

const (
	deriveMoreTask = "\n\nWrite every new search, worded differently from the searches already run, that would find what the needs not covered yet still lack." +
		" One search per line, starting with \"SEARCH:\". If the reply needs a fact not listed yet, add it on a line starting with \"NEED:\", or with \"ALL:\" when it needs every instance of something (a count, total or list)."
)

// derive is the first round's cues: the message, then every search the
// planner writes; it records the needs and the searches tried.
func (r *simpleRun) derive(ctx context.Context) ([]cue, error) {
	r.unlimited = true
	cues, err := r.planRound(ctx, "", nil, 0)
	if err != nil {
		return nil, err
	}
	if last := lastMessage(r.dialogue); last != "" {
		cues = append([]cue{{Text: jsSlice(last, 0, 1000), Kind: "message"}}, cues...)
	}
	r.tried = []string{}
	out := []cue{}
	for _, c := range cues {
		if !slices.Contains(r.tried, c.Text) {
			r.tried = append(r.tried, c.Text)
			out = append(out, c)
		}
	}
	return out, nil
}

// deriveLoop runs the derive loop over the screened pool and returns why it
// stopped.
func (r *simpleRun) deriveLoop(ctx context.Context) (string, error) {
	ended := func(err error) (string, error) {
		if errors.Is(err, errCallBudget) {
			r.degrade("loop-budget")
			return stopRounds, nil
		}
		return "", err
	}
	if err := r.table(ctx, r.viewable(r.screened())); err != nil {
		return ended(err)
	}
	closed := make([]bool, len(r.needs)) // ALL needs a round found nothing new for
	counts := func() []int {
		out := []int{}
		for k := range r.needs {
			out = append(out, len(r.yes(k)))
		}
		return out
	}
	open := func() []Need {
		out := []Need{}
		for k, need := range r.needs {
			if k < len(closed) && closed[k] {
				continue
			}
			if !need.All && len(r.yes(k)) > 0 {
				continue
			}
			out = append(out, need)
		}
		return out
	}
	stop := stopRounds
	for round := 1; round <= simpleRounds; round++ {
		lacking := open()
		if len(lacking) == 0 {
			stop = stopCovered
			break
		}
		if r.givingCount() >= roundRecallMax {
			stop = "full"
			break
		}
		before := counts()
		cues, err := r.rederive(ctx, lacking)
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if err != nil {
			r.degrade("plan")
			break
		}
		if len(cues) == 0 {
			stop = stopGivenUp
			break
		}
		r.rounds = round
		pages, err := r.runCues(ctx, cues)
		if err != nil {
			return "", err
		}
		items, err := r.admit(ctx, mixCues(pages, simplePoolSize))
		if err != nil {
			return "", err
		}
		if len(items) > 0 {
			if err := r.scan(ctx, items); err != nil {
				return ended(err)
			}
			fresh := []string{}
			for _, item := range items {
				fresh = append(fresh, item.ID)
			}
			if err := r.table(ctx, r.viewable(fresh)); err != nil {
				return ended(err)
			}
		}
		// An ALL need closes after a round that found nothing new for it.
		after := counts()
		for len(closed) < len(r.needs) {
			closed = append(closed, false)
		}
		for k, need := range r.needs {
			if need.All && k < len(before) && after[k] == before[k] {
				closed[k] = true
			}
		}
		if len(items) == 0 {
			stop = stopGivenUp
			break
		}
	}
	r.needStates = make([]string, len(r.needs))
	for k, need := range r.needs {
		switch {
		case len(r.yes(k)) > 0 && !need.All:
			r.needStates[k] = needMet
		case len(r.yes(k)) > 0:
			r.needStates[k] = needDone
		default:
			r.needStates[k] = needGivenUp
		}
	}
	return stop, nil
}

// givingCount counts the tabled items that give a need.
func (r *simpleRun) givingCount() int {
	n := 0
	for _, id := range r.rows {
		for k := range r.needs {
			if r.answer(id, k) >= 0.5 {
				n++
				break
			}
		}
	}
	return n
}

// rederive asks the planner for the searches the open needs still lack,
// worded differently from those run; needs it adds join the read's needs. It
// returns the new searches, recorded as tried.
func (r *simpleRun) rederive(ctx context.Context, lacking []Need) ([]cue, error) {
	tried, missing := []string{}, []string{}
	for _, text := range r.tried {
		tried = append(tried, "- "+jsSlice(text, 0, 200))
	}
	for _, need := range lacking {
		mark := "NEED: "
		if need.All {
			mark = "ALL: "
		}
		missing = append(missing, "- "+mark+need.Text)
	}
	prompt := "CONVERSATION:\n" + conversationText(r.dialogue) + "\n\nSEARCHES ALREADY RUN:\n" + strings.Join(tried, "\n") + "\n\nNEEDS NOT COVERED YET:\n" + strings.Join(missing, "\n") + deriveMoreTask
	text, err := r.complete(ctx, roundSystem, prompt, roundPlanTokens)
	if err != nil {
		return nil, err
	}
	cues := []cue{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimFunc(line, isJSSpace)
		if m := needLine.FindStringSubmatch(line); m != nil {
			need := Need{Text: jsSlice(strings.TrimFunc(m[2], isJSSpace), 0, 200), All: strings.EqualFold(m[1], "ALL")}
			if !slices.ContainsFunc(r.needs, func(old Need) bool { return strings.EqualFold(old.Text, need.Text) }) {
				r.needs = append(r.needs, need)
			}
			continue
		}
		prefix := foldPrefix(line, "SEARCH:")
		if prefix == "" {
			continue
		}
		word := searchText(strings.TrimFunc(line[len(prefix):], isJSSpace))
		if n := jsLen(word); n >= 2 && n <= 120 && !slices.Contains(r.tried, word) {
			r.tried = append(r.tried, word)
			cues = append(cues, cue{Text: word, Kind: "search"})
		}
	}
	return cues, nil
}
