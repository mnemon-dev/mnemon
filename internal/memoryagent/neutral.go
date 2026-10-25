package memoryagent

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Neutral consolidation (consolidation.mode "neutral",
// docs/superpowers/specs/2026-10-26-neutral-memory-and-single-pass-search-design.md)
// is the layered job with nothing of what to keep defined by hand. L0: the
// model reads each batch alone and writes, as notes it groups under titles
// of its own, what a reader may need to remember; a note is an event of the
// thread its group titles, and there are no facts or directives. L1: JEV
// joins each new group to the stored group it chooses among its candidates,
// or to none (one choice question per new group), and then answers, of each
// new note and each of the latest notes of its group before it, whether the
// new note updates or replaces what that note says (one yes/no question per
// pair, true at 0.5); every note one updates is marked, never removed. The
// code compares no scores. Every record is read whole and alike
// (evenReadable): no rule by role keeps one, cuts one or passes one over.
// Batching, deferral, storage and the read layer are the layered mode's.

// neutralSystem is the neutral L0 prompt, frozen.
const neutralSystem = `You keep a memory of a long conversation between a user and an assistant. The conversation arrives in batches of records, in order; each record shows its id, the session date and the text. For each batch, write down what a reader may need to remember later from these records, as notes. Put notes that belong together in a group with a short title of your own. Give each note its date and the ids of the records it comes from. Use only what the records say. Dates are the session dates shown with the records. Reply with JSON only, in this form:
{"notes":[{"group":"<short title>","date":"YYYY/MM/DD","text":"...","records":["r12"]}]}`

// evenReadable is how neutral mode reads a record: whole, like every other,
// with no rule by role. A leading session heading becomes the header, as
// readable has it, so that the record shows its date; otherwise the title
// does. Only an empty record is not read.
func evenReadable(title, content string) (header, body string, ok bool) {
	session := authorSessionHeading.FindStringSubmatch(content)
	if session == nil {
		session = sessionHeading.FindStringSubmatch(content)
	}
	header, body = jsSlice(title, 0, 40), content
	if session != nil {
		header, body = session[2]+" (session "+session[1]+")", content[len(session[0]):]
	}
	if strings.TrimFunc(body, isJSSpace) == "" {
		return "", "", false
	}
	return header, body, true
}

// recordReader is how the agent's consolidation reads a record:
// evenReadable in neutral mode and with evenReading, else readable with its
// assistant cut.
func (a *Agent) recordReader(assistantChars int) func(title, content string) (string, string, bool) {
	if a.config.Consolidation.readsEvenly() {
		return evenReadable
	}
	return func(title, content string) (string, string, bool) { return readable(title, content, assistantChars) }
}

// neutralFold is the neutral L0: the batch alone, under neutralSystem, its
// notes read as events.
var neutralFold = foldSpec{system: neutralSystem, prompt: func(_ timelineMemory, batch []pendingRecord) string { return batchSection(batch) }, notes: true}

// The neutral linking questions, frozen: which candidate group a new group
// continues (a choice, one option per candidate and none), and whether a new
// note updates a candidate note (yes/no).
func groupQuestion(n string) string {
	return `Which candidate group is the new group whose id is "` + n + `" about, so that its notes continue that group?`
}

func groupOption(c string) string {
	return `The new group is about the same thing as "` + c + `", and its notes continue or update it.`
}

const groupNone = "The new group is about something none of the candidates is about."

func updateQuestion(n, c string) string {
	return `Does the new note whose id is "` + n + `" update or replace what the candidate whose id is "` + c + `" says?`
}

const (
	updateYes = "The new note changes, replaces or corrects what the candidate says."
	updateNo  = "Both still hold, or they are about different things."
)

// linkNeutral is link in neutral mode: state.Memory with the batch's groups
// joined by JEV's choices and its notes appended, the notes they update
// marked; state.Memory is unchanged. A JEV error or spent budget joins
// nothing and marks nothing (Fallback), unless the batch stays pending
// (keepsPending): then, and for a canceled job, the error is returned.
func (l *linker) linkNeutral(ctx context.Context, state *timelineState, batch timelineMemory, result *ConsolidateResult) (timelineMemory, linkCounts, error) {
	m := state.Memory
	newThreads, newEvents := eventThreads(batch)
	oldThreads, oldEvents := eventThreads(m)
	titles, oldTitles := map[string]string{}, map[string]string{}
	for _, t := range batch.Threads {
		titles[t.ID] = t.Title
	}
	for _, t := range m.Threads {
		oldTitles[t.ID] = t.Title
	}
	news, cands, queries, docs := []linkItem{}, []linkItem{}, []string{}, []string{}
	for _, id := range newThreads {
		text := threadText(titles[id], batch, newEvents[id])
		news = append(news, linkItem{ID: fmt.Sprintf("n%d", len(news)+1), Text: authorExcerpt(text, simpleListing)})
		queries = append(queries, text)
	}
	for _, id := range oldThreads {
		text := threadText(oldTitles[id], m, oldEvents[id])
		cands = append(cands, linkItem{ID: id, Text: authorExcerpt(text, simpleListing)})
		docs = append(docs, text)
	}
	ranks, lexical, err := l.rank(ctx, queries, docs)
	if err != nil {
		return timelineMemory{}, linkCounts{}, err
	}
	counts := linkCounts{Lexical: lexical}
	stopped := func(err error) bool {
		return ctx.Err() != nil || keepsPending(ctx, err, l.agent.linkDue(*state))
	}
	chosen, err := l.chooseGroups(ctx, news, cands, ranks, state, result)
	if err != nil {
		if stopped(err) {
			return timelineMemory{}, linkCounts{}, err
		}
		counts.Fallback, chosen = true, nil
	}
	out := cloneMemory(m)
	threadOf := map[string]string{}
	for i, id := range newThreads {
		if i < len(chosen) && chosen[i] >= 0 {
			threadOf[id] = oldThreads[chosen[i]]
			counts.ThreadsLinked++
			continue
		}
		fresh := freshThreadID(out)
		out.Threads = append(out.Threads, thread{ID: fresh, Title: titles[id]})
		threadOf[id] = fresh
		counts.ThreadsNew++
	}
	first := len(out.Events)
	for _, e := range batch.Events {
		e.Thread = threadOf[e.Thread]
		out.Events = append(out.Events, e)
	}
	if counts.Fallback {
		return out, counts, nil
	}
	if err := l.markUpdates(ctx, &out, first, state, result); err != nil {
		if stopped(err) {
			return timelineMemory{}, linkCounts{}, err
		}
		counts.Fallback = true
	}
	return out, counts, nil
}

// chooseGroups asks, of each new group i that has candidates (ranks[i],
// indexes into cands), which candidate group it continues, one choice
// question each, packed in order into as few calls as hold the judge's
// question limit and maxJEVRequest units. It returns each new group's
// chosen candidate, -1 for none or for no candidates.
func (l *linker) chooseGroups(ctx context.Context, news, cands []linkItem, ranks [][]int, state *timelineState, result *ConsolidateResult) ([]int, error) {
	chosen := make([]int, len(news))
	for i := range chosen {
		chosen[i] = -1
	}
	type call struct {
		state     linkState
		questions map[string]Question
		groups    []int
		cands     map[int]bool
		size      int
	}
	empty, _ := jevRequest(linkState{New: []linkItem{}, Candidates: []linkItem{}}, map[string]Question{})
	calls := []*call{}
	for i, ranked := range ranks {
		if len(ranked) == 0 {
			continue
		}
		key := fmt.Sprintf("g%d", i)
		q := Question{Type: "choice", Instructions: groupQuestion(news[i].ID), Criteria: map[string]string{"none": groupNone}}
		for _, c := range ranked {
			q.Criteria[cands[c].ID] = groupOption(cands[c].ID)
		}
		units := func(cl *call) int {
			u := jsonUnits(news[i]) + 1 + jsonUnits(key) + 1 + jsonUnits(q) + 1
			for _, c := range ranked {
				if !cl.cands[c] {
					u += jsonUnits(cands[c]) + 1
				}
			}
			return u
		}
		var cl *call
		if n := len(calls); n > 0 && len(calls[n-1].questions) < l.questionLimit() && calls[n-1].size+units(calls[n-1]) <= maxJEVRequest {
			cl = calls[n-1]
		} else {
			cl = &call{state: linkState{New: []linkItem{}, Candidates: []linkItem{}}, questions: map[string]Question{}, cands: map[int]bool{}, size: jsLen(empty)}
			calls = append(calls, cl)
		}
		cl.size += units(cl)
		cl.state.New = append(cl.state.New, news[i])
		for _, c := range ranked {
			if !cl.cands[c] {
				cl.cands[c] = true
				cl.state.Candidates = append(cl.state.Candidates, cands[c])
			}
		}
		cl.questions[key] = q
		cl.groups = append(cl.groups, i)
	}
	at := map[string]int{}
	for c, item := range cands {
		at[item.ID] = c
	}
	for _, cl := range calls {
		answers, err := l.choose(ctx, cl.state, cl.questions, state, result)
		if err != nil {
			return nil, err
		}
		for _, i := range cl.groups {
			if a := answers[fmt.Sprintf("g%d", i)]; a.Option != "none" {
				chosen[i] = at[a.Option]
			}
		}
	}
	return chosen, nil
}

// choose makes one choice linking call and meters it as decide does.
func (l *linker) choose(ctx context.Context, s linkState, questions map[string]Question, state *timelineState, result *ConsolidateResult) (map[string]Choice, error) {
	chooser, ok := l.agent.models.Judge.(Chooser)
	if !ok {
		return nil, fmt.Errorf("neutral consolidation needs a decision model that answers choice questions")
	}
	start := time.Now()
	answers, usage, err := chooser.Choose(ctx, s, questions)
	if err == nil {
		foldJobOf(ctx).answer()
	}
	result.ModelMS += float64(time.Since(start).Microseconds()) / 1000
	result.Usage.add(usage)
	state.Usage.add(usage)
	if result.LinkUsage == nil {
		result.LinkUsage = &Usage{}
	}
	result.LinkUsage.add(usage)
	result.LinkCalls++
	return answers, err
}

// markUpdates asks, of every note of out from first on (the batch's, in
// their groups), whether it updates each of the latest LinkCandidates notes
// of its group before it, stored or earlier in the batch, and marks every
// note one updates (true at 0.5).
func (l *linker) markUpdates(ctx context.Context, out *timelineMemory, first int, state *timelineState, result *ConsolidateResult) error {
	titles := map[string]string{}
	for _, t := range out.Threads {
		titles[t.ID] = t.Title
	}
	item := func(id string, i int) linkItem {
		e := out.Events[i]
		return linkItem{ID: id, Date: e.Date, Thread: titles[e.Thread], Text: authorExcerpt(e.Text, simpleListing)}
	}
	news, cands, pairs, candOf, eventOf := []linkItem{}, []linkItem{}, []*linkPair{}, map[int]int{}, []int{}
	for i := first; i < len(out.Events); i++ {
		earlier := []int{}
		for j := i - 1; j >= 0 && len(earlier) < l.k; j-- {
			if out.Events[j].Thread == out.Events[i].Thread {
				earlier = append(earlier, j)
			}
		}
		if len(earlier) == 0 {
			continue
		}
		n := len(news)
		news = append(news, item(fmt.Sprintf("n%d", n+1), i))
		for _, j := range earlier {
			c, ok := candOf[j]
			if !ok {
				c = len(cands)
				candOf[j] = c
				cands = append(cands, item(fmt.Sprintf("c%d", c+1), j))
				eventOf = append(eventOf, j)
			}
			pairs = append(pairs, &linkPair{kind: linkUpdate, item: n, candidate: c})
		}
	}
	if len(pairs) == 0 {
		return nil
	}
	if err := l.judge(ctx, news, cands, pairs, state, result, &result.LinkCalls); err != nil {
		return err
	}
	for _, p := range pairs {
		if p.same >= 0.5 {
			out.Events[eventOf[p.candidate]].Updated = true
		}
	}
	return nil
}

// neutralLayerOrder is a layer's topic order in neutral mode: JEV's yes
// (needed ≥ 0.5) in the order the View's records light them, most anchors
// first, ties in candidate order, not by score; then the lit topics below
// 0.5 as layerOrder has them (order).
func neutralLayerOrder(topics []string, lit litSets, score map[string]float64, order []string) []string {
	yes := slices.DeleteFunc(slices.Clone(topics), func(id string) bool { return score[id] < 0.5 })
	slices.SortStableFunc(yes, func(a, b string) int { return cmp.Compare(lit.Counts[b], lit.Counts[a]) })
	return append(yes, order[min(len(yes), len(order)):]...)
}
