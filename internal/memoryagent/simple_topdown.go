package memoryagent

import (
	"cmp"
	"context"
	"slices"
	"strings"
)

// Top-down recall (view.topDown,
// docs/superpowers/specs/2026-10-26-neutral-memory-and-single-pass-search-design.md):
// the consolidation's topics and values recall the records they cite, beside
// the cue searches' words search of the records. The question's words find
// the topics (each one's title and every event) and the values (each one's
// key and every value) by BM25, the best 12 and 16 as the layer's nearest
// takes them; the records they cite rank by the question's own words search
// of the records (records.Store.LexicalScores: the index, statistics and
// rules the cue searches use), best first, those it does not rank after them
// in citation order, never left out for it. The first topDownLimit not
// pooled yet join the pool after the cue searches' mix, so that no cue
// search loses a slot, and JEV screens them like any other. It calls no
// model.

// topDownLimit is the records top-down recall pools: one cue search's first
// page.
const topDownLimit = simplePageSize

// topDown is the records top-down recall adds to pooled: at most
// topDownLimit, none of pooled, in rank order; none for an agent without an
// overlay (as the server's config check builds one).
func (r *simpleRun) topDown(ctx context.Context, pooled []string) ([]string, error) {
	if r.agent.overlay == nil {
		return nil, nil
	}
	state, _, err := r.agent.overlay.loadTimelines(ctx, r.req.Scope)
	if err != nil {
		return nil, err
	}
	m := state.Memory
	ids, by := eventThreads(m)
	titles := map[string]string{}
	for _, t := range m.Threads {
		titles[t.ID] = t.Title
	}
	topics := make([]string, len(ids))
	for i, id := range ids {
		parts := []string{titles[id]}
		for _, e := range by[id] {
			parts = append(parts, m.Events[e].Text)
		}
		topics[i] = strings.Join(parts, "\n")
	}
	values := make([]string, len(m.FactOrder))
	for i, key := range m.FactOrder {
		parts := []string{key}
		for _, v := range m.Facts[key] {
			parts = append(parts, v.Value)
		}
		values[i] = strings.Join(parts, "\n")
	}
	message := lastMessage(r.dialogue)
	in := map[string]bool{}
	for _, id := range pooled {
		in[id] = true
	}
	cited := []string{}
	cite := func(records []string) {
		for _, id := range records {
			if !in[id] {
				in[id] = true
				cited = append(cited, id)
			}
		}
	}
	for _, i := range bm25Top(topics, message, layerNearThreads) {
		for _, e := range by[ids[i]] {
			cite(m.Events[e].Records)
		}
	}
	for _, i := range bm25Top(values, message, layerNearValues) {
		for _, v := range m.Facts[m.FactOrder[i]] {
			cite(v.Records)
		}
	}
	if len(cited) == 0 {
		return nil, nil
	}
	scores, err := r.agent.store.LexicalScores(ctx, r.revision, r.req.Scope, []string{message}, [][]string{cited})
	if err != nil {
		return nil, err
	}
	order := make([]int, len(cited))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(scores[0][b], scores[0][a]) })
	out := []string{}
	for _, i := range order[:min(len(order), topDownLimit)] {
		out = append(out, cited[i])
	}
	return out, nil
}
