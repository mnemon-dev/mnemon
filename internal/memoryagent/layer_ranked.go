package memoryagent

import (
	"cmp"
	"context"
	"slices"
)

// The ranked layer (view.layerRanked,
// docs/superpowers/specs/2026-10-26-neutral-memory-and-single-pass-search-design.md).
// Candidates come two ways: the events and values the composed records light
// (every one, the events by the records they cite, then newest first), and
// the events and values the read's searches find in the consolidated items'
// index (layer_index.go; each search's first page, mixed as the searches'
// records are), the best layerNearThreads events and layerNearValues values
// not lit. JEV's needed question ranks them all, with no threshold, and the
// blocks fill their budgets in that order (composeRanked): a topic shows only
// its ranked events that fit, in time order. Directives are shown as the
// layer shows them. No record joins the View from the layer.

// layerRanked puts the ranked layer before the composed View's records; a
// state that will not load or a judgement that fails leaves the View as
// composed, degraded (layer-state, layer).
func (r *simpleRun) layerRanked(ctx context.Context, view View) (View, error) {
	state, _, err := r.agent.overlay.loadTimelines(ctx, r.req.Scope)
	if ctx.Err() != nil {
		return View{}, ctx.Err()
	}
	if err != nil {
		r.degrade("layer-state")
		view.Stats = r.statsNow()
		return view, nil
	}
	m := state.Memory
	if len(m.Events) == 0 && len(m.Directives) == 0 && len(m.Facts) == 0 {
		return view, nil
	}
	anchors := map[string]bool{}
	for _, record := range view.Records {
		anchors[record.ID] = true
	}
	events, values := litCandidates(m, anchors)
	near, err := r.nearItems(ctx)
	if ctx.Err() != nil {
		return View{}, ctx.Err()
	}
	if err != nil {
		r.degrade("layer-search")
	}
	nearEvents, nearValues := 0, 0
	for _, entry := range near {
		switch {
		case entry.Kind == "event" && nearEvents < layerNearThreads && !slices.Contains(events, entry.Index):
			events, nearEvents = append(events, entry.Index), nearEvents+1
		case entry.Kind == "value" && nearValues < layerNearValues && !slices.Contains(values, entry.Key):
			values, nearValues = append(values, entry.Key), nearValues+1
		}
	}
	titles := map[string]string{}
	for _, t := range m.Threads {
		titles[t.ID] = t.Title
	}
	candidates := []judgeCandidate{}
	for _, i := range events {
		e := m.Events[i]
		candidates = append(candidates, judgeCandidate{Source: "topic", Text: titles[e.Thread] + ": " + e.Date + " " + e.Text})
	}
	for _, key := range values {
		candidates = append(candidates, judgeCandidate{Source: "value", Text: valueText(key, m.Facts[key])})
	}
	scores, err := r.judgeNeeded(ctx, candidates)
	if ctx.Err() != nil {
		return View{}, ctx.Err()
	}
	if err != nil {
		r.degrade("layer")
	}
	eventScore, valueScore := map[int]float64{}, map[string]float64{}
	for j, i := range events {
		eventScore[i] = scores[j]
	}
	for j, key := range values {
		valueScore[key] = scores[len(events)+j]
	}
	slices.SortStableFunc(events, func(a, b int) int { return probabilityOrder(eventScore[a], eventScore[b]) })
	slices.SortStableFunc(values, func(a, b string) int { return probabilityOrder(valueScore[a], valueScore[b]) })
	blocks := composeRanked(m, events, values)
	view.Index = blocks
	view.Records, view.Truncated = fitRecords(blocks, view.Records, r.agent.config.View.MaxCharacters, view.Truncated)
	r.mu.Lock()
	r.stats.LayerRecalled = len(candidates)
	r.mu.Unlock()
	view.Stats = r.statsNow()
	return view, nil
}

// litCandidates is the events the anchors light, those citing more anchors
// first, then the newest, and the values they light, in memory order.
func litCandidates(m timelineMemory, anchors map[string]bool) (events []int, values []string) {
	cites := map[int]int{}
	for i, e := range m.Events {
		for _, id := range e.Records {
			if anchors[id] {
				cites[i]++
			}
		}
		if cites[i] > 0 {
			events = append(events, i)
		}
	}
	slices.SortStableFunc(events, func(a, b int) int {
		if c := cmp.Compare(cites[b], cites[a]); c != 0 {
			return c
		}
		return cmp.Compare(b, a)
	})
	_, _, _, values = lightUp(m, anchors)
	return events, values
}

// nearItems is the events and values the read's searches find in the
// consolidated items' index: each search's first page, mixed as mixCues
// mixes the searches' records.
func (r *simpleRun) nearItems(ctx context.Context) ([]layerEntry, error) {
	o := r.agent.overlay
	if err := o.syncLayerIndex(ctx, r.req.Scope); err != nil {
		return nil, err
	}
	searches := r.tried
	if len(searches) == 0 {
		searches = []string{lastMessage(r.dialogue)}
	}
	lists, entries := [][]string{}, map[string]layerEntry{}
	for _, text := range searches {
		found, err := o.searchLayer(ctx, r.req.Scope, text, simplePageSize)
		if err != nil {
			return nil, err
		}
		list := []string{}
		for _, entry := range found {
			list = append(list, entry.Item)
			entries[entry.Item] = entry
		}
		lists = append(lists, list)
	}
	out := []layerEntry{}
	for _, item := range mixCues(lists, layerNearThreads+layerNearValues+len(entries)) {
		out = append(out, entries[item])
	}
	return out, nil
}

// composeRanked is the layer's blocks from ranked events and values, each
// block within composeLayer's budgets:
//   - 1-instructions: the directives, oldest first, as composeLayer shows
//     them.
//   - 3-timelines: the topics in the order of their best ranked event; a
//     topic begins only with at least layerThreadFloor characters of room,
//     within min(layerThread, what is left of layerTimelines), and takes its
//     ranked events while they fit, shown in time order.
//   - 2-topics: the topics the timelines show, with their whole span, while
//     they fit layerDirectory, in memory order.
//   - 4-values: the ranked keys' histories while each fits layerValues.
func composeRanked(m timelineMemory, events []int, values []string) []IndexItem {
	blocks := []IndexItem{}
	add := func(id, text string, items, linked []string) {
		refs, seen := []Reference{}, map[string]bool{}
		for _, record := range linked {
			if !seen[record] {
				seen[record] = true
				refs = append(refs, Reference{ID: record})
			}
		}
		blocks = append(blocks, IndexItem{ID: id, Kind: id, Text: text, Records: refs, Items: items})
	}
	if directives := composeLayer(timelineMemory{Directives: m.Directives}, nil, nil, litSets{}); len(directives) > 0 {
		blocks = append(blocks, directives[0])
	}
	titles := map[string]string{}
	for _, t := range m.Threads {
		titles[t.ID] = t.Title
	}
	_, by := eventThreads(m)
	eventLine := func(i int) string {
		line := "\n- " + m.Events[i].Date + ": " + m.Events[i].Text
		if m.Events[i].Updated {
			line += " (later updated)"
		}
		return line
	}
	order, chosen := []string{}, map[string][]int{}
	for _, i := range events {
		if t := m.Events[i].Thread; !slices.Contains(order, t) {
			order = append(order, t)
		}
	}
	timelines, size := "", 0
	shown := []string{}
	for _, t := range order {
		room := min(layerThread, layerTimelines-size)
		if room < layerThreadFloor {
			break
		}
		used := jsLen("Thread: " + titles[t])
		for _, i := range events {
			if m.Events[i].Thread == t && used+jsLen(eventLine(i)) <= room {
				chosen[t], used = append(chosen[t], i), used+jsLen(eventLine(i))
			}
		}
		if len(chosen[t]) == 0 {
			continue
		}
		slices.Sort(chosen[t])
		if timelines != "" {
			timelines, size = timelines+"\n\n", size+2
		}
		part := "Thread: " + titles[t]
		for _, i := range chosen[t] {
			part += eventLine(i)
		}
		timelines, size = timelines+part, size+jsLen(part)
		shown = append(shown, t)
	}
	if len(shown) > 0 {
		head, size, text, items := "Topics of the conversation (first and last date):", 0, "", []string{}
		size = jsLen(head)
		for _, th := range m.Threads {
			all := by[th.ID]
			if !slices.Contains(shown, th.ID) || len(all) == 0 {
				continue
			}
			first, last := m.Events[all[0]].Date, m.Events[all[len(all)-1]].Date
			line := "\n- " + th.Title + " (" + first + ")"
			if first != last {
				line = "\n- " + th.Title + " (" + first + " to " + last + ")"
			}
			if size+jsLen(line) > layerDirectory {
				continue
			}
			text, size, items = text+line, size+jsLen(line), append(items, th.ID)
		}
		if text != "" {
			add("2-topics", head+text, items, nil)
		}
		linked, items := []string{}, []string{}
		for _, t := range shown {
			for _, i := range chosen[t] {
				linked = append(linked, m.Events[i].Records...)
				items = append(items, itemID("e", i))
			}
		}
		add("3-timelines", timelinesHead+timelines, items, linked)
	}
	text, linked, items := "", []string{}, []string{}
	size = 0
	for _, key := range values {
		list, ok := m.Facts[key]
		if !ok {
			continue
		}
		line := "\n- " + valueText(key, list)
		if size+jsLen(line) > layerValues {
			continue
		}
		text, size = text+line, size+jsLen(line)
		for _, v := range list {
			linked = append(linked, v.Records...)
		}
		items = append(items, key)
	}
	if text != "" {
		add("4-values", valuesHead+text, items, linked)
	}
	return blocks
}
