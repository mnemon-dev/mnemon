package memoryagent

import (
	"context"
	"slices"
)

// Layer recall (view.layerRecall,
// docs/superpowers/specs/2026-10-26-neutral-memory-and-single-pass-search-design.md):
// the layer is what the question finds in the consolidation, not what the
// composed records light. The question's words search the consolidated
// events and value histories (layer_index.go), the best simplePoolSize are
// judged by JEV's needed question, and those it calls needed (≥ 0.5) are
// shown, best first, within the layer's budgets (composeLayer): each topic
// with only its needed events, in time order. Directives are shown as the
// layer shows them, whatever the question, since they hold for every reply.

// recallLayer returns the layer's blocks for the read's question; none
// without an overlay.
func (r *simpleRun) recallLayer(ctx context.Context) ([]IndexItem, error) {
	o := r.agent.overlay
	if o == nil {
		return nil, nil
	}
	if err := o.syncLayerIndex(ctx, r.req.Scope); err != nil {
		return nil, err
	}
	found, err := o.searchLayer(ctx, r.req.Scope, lastMessage(r.dialogue), simplePoolSize)
	if err != nil {
		return nil, err
	}
	directives, err := o.layerDirectives(ctx, r.req.Scope)
	if err != nil {
		return nil, err
	}
	candidates := []judgeCandidate{}
	for _, entry := range found {
		source := "topic"
		if entry.Kind == "value" {
			source = "value"
		}
		candidates = append(candidates, judgeCandidate{Source: source, Text: entry.judgeText()})
	}
	scores, err := r.judgeNeeded(ctx, candidates)
	if err != nil {
		return nil, err
	}
	kept := []int{}
	for i := range found {
		if scores[i] >= 0.5 {
			kept = append(kept, i)
		}
	}
	slices.SortStableFunc(kept, func(a, b int) int { return probabilityOrder(scores[a], scores[b]) })
	m := timelineMemory{Facts: map[string][]factValue{}}
	threadOrder, valueOrder, events := []string{}, []string{}, []layerEntry{}
	for _, i := range kept {
		entry := found[i]
		switch entry.Kind {
		case "event":
			if !slices.Contains(threadOrder, entry.Event.Thread) {
				threadOrder = append(threadOrder, entry.Event.Thread)
				m.Threads = append(m.Threads, thread{ID: entry.Event.Thread, Title: entry.Title})
			}
			events = append(events, entry)
		case "value":
			valueOrder = append(valueOrder, entry.Key)
			m.Facts[entry.Key] = entry.Values
			m.FactOrder = append(m.FactOrder, entry.Key)
		}
	}
	slices.SortFunc(events, func(a, b layerEntry) int { return a.Index - b.Index })
	for _, entry := range events {
		m.Events = append(m.Events, *entry.Event)
	}
	for _, entry := range directives {
		m.Directives = append(m.Directives, *entry.Directive)
	}
	r.mu.Lock()
	r.stats.LayerRecalled = len(kept)
	r.mu.Unlock()
	return composeLayer(m, threadOrder, valueOrder, litSets{}), nil
}

// layerRecalled puts the recalled layer before the composed View's records,
// the records kept while the characters fit; a recall that fails leaves the
// View as composed, degraded (layer).
func (r *simpleRun) layerRecalled(ctx context.Context, view View) (View, error) {
	blocks, err := r.recallLayer(ctx)
	if ctx.Err() != nil {
		return View{}, ctx.Err()
	}
	if err != nil {
		r.degrade("layer")
		view.Stats = r.statsNow()
		return view, nil
	}
	view.Index = blocks
	view.Records, view.Truncated = fitRecords(blocks, view.Records, r.agent.config.View.MaxCharacters, view.Truncated)
	view.Stats = r.statsNow()
	return view, nil
}

// fitRecords keeps the records, in order, while the blocks and the kept
// records stay within maxChars; truncated reports a record left out.
func fitRecords(blocks []IndexItem, list []Evidence, maxChars int, truncated bool) ([]Evidence, bool) {
	used := 0
	for _, block := range blocks {
		used += jsLen(block.Text)
	}
	kept := []Evidence{}
	for _, record := range list {
		if n := jsLen(record.Text); used+n <= maxChars {
			kept, used = append(kept, record), used+n
		} else {
			truncated = true
		}
	}
	return kept, truncated
}
