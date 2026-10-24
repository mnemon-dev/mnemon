package memoryagent

import (
	"cmp"
	"context"
	"slices"
	"strconv"
)

// The insights block is the read side of L2 (consolidation.insights,
// insights.go): after the layer's four blocks, the conclusions drawn from
// the consolidated memory that JEV finds the reply needs, and those the
// View's records light up. Its records name where the conclusions come
// from; unlike the other blocks', they never join the View (expand).

const (
	// layerInsights is the insights block's budget, its header not
	// counted, as the values'.
	layerInsights = 1500
	// layerNearInsights is the insights nearest the message that JEV judges.
	layerNearInsights = 12
	insightsHead      = "Conclusions drawn from the memory:"
	// insightSource is the source of the insights' judgement candidates.
	insightSource = "insight"
)

// layerBound is the most characters the layer's blocks can take in the
// agent's Views: layerBlocksMax, and with insights their block, its budget
// and header.
func (a *Agent) layerBound() int {
	if a.insightsOn() {
		return layerBlocksMax + layerInsights + jsLen(insightsHead)
	}
	return layerBlocksMax
}

// insightLine is an insight as the block shows it, after "- ", and as JEV
// reads it: "(<date>) <text>".
func insightLine(in insight) string {
	return "(" + in.Date + ") " + in.Text
}

// insights is the block 5-insights of a read with consolidation.insights,
// or nil. The candidates are the insights whose records include a View
// record (anchors), most such records first (ties in memory order), then
// the 12 nearest the message (nearestInsights), each once. JEV judges them
// in a judgeNeeded call of their own (source "insight"), so the topics' and
// values' call is the same with insights and without. The chosen are those
// it calls needed (≥ 0.5), by its answer (ties in candidate order), then
// the lit ones below 0.5 by their View records, as layerOrder chooses
// topics. A judgement that fails, or that the read's decision budget cannot
// pay for whole, shows no insights and degrades the read (layer-insights).
// No stored insight, or no candidate, asks nothing. Only a canceled read
// is an error.
func (r *simpleRun) insights(ctx context.Context, m timelineMemory, anchors map[string]bool, message string, lexical bool) (*IndexItem, error) {
	if len(m.Insights) == 0 {
		return nil, nil
	}
	ids, texts, lit := make([]string, len(m.Insights)), make([]string, len(m.Insights)), litSets{Counts: map[string]int{}}
	for i, in := range m.Insights {
		ids[i], texts[i] = strconv.Itoa(i), in.Text
		n := 0
		for _, record := range in.Records {
			if anchors[record] {
				n++
			}
		}
		if n > 0 {
			lit.Threads, lit.Counts[ids[i]] = append(lit.Threads, ids[i]), n
		}
	}
	slices.SortStableFunc(lit.Threads, func(a, b string) int { return cmp.Compare(lit.Counts[b], lit.Counts[a]) })
	near := r.nearestInsights(ctx, message, texts, lexical)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	candidates, _ := layerCandidates(lit, ids, nil, near, nil)
	if len(candidates) == 0 {
		return nil, nil
	}
	judged := []judgeCandidate{}
	for _, id := range candidates {
		i, _ := strconv.Atoi(id)
		judged = append(judged, judgeCandidate{Source: insightSource, Text: insightLine(m.Insights[i])})
	}
	if calls := (len(judged) + judgeBatch - 1) / judgeBatch; r.decisionsLeft() < calls {
		r.degrade("layer-insights")
		return nil, nil
	}
	scores, err := r.judgeNeeded(ctx, judged)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		r.degrade("layer-insights")
		return nil, nil
	}
	score := map[string]float64{}
	for i, id := range candidates {
		score[id] = scores[i]
	}
	order, _ := layerOrder(candidates, nil, lit, score, nil)
	block, shown := composeInsights(m, order)
	if shown == 0 {
		return nil, nil
	}
	r.mu.Lock()
	r.stats.LayerInsights = shown
	r.mu.Unlock()
	return &block, nil
}

// nearestInsights is the indexes of the 12 insight texts nearest the
// message, best first, as nearest ranks the topics: by meaning with an
// embedder (the texts' document vectors, kept like the topics'; ties in
// memory order), else, or when that fails (layer-lexical), by shared words
// (positive scores only). When the topics fell back to shared words
// (lexical), so do the insights, embedding nothing.
func (r *simpleRun) nearestInsights(ctx context.Context, message string, texts []string, lexical bool) []int {
	if !lexical {
		if query, vectors, _, ok := r.layerVectors(ctx, message, texts, nil); ok {
			return byDot(query, vectors, layerNearInsights)
		}
		if r.agent.config.Embedding != nil && ctx.Err() == nil {
			r.degrade("layer-lexical")
		}
	}
	return bm25Top(texts, message, layerNearInsights)
}

// composeInsights is the block 5-insights: the header, then the insights in
// order (ids are indexes into m.Insights), each line "- (<date>) <text>"
// kept while it fits layerInsights characters (the header not counted, a
// line that does not fit skipped), and how many it shows. Records name the
// shown insights' records, in order, each once, by id only; Items the shown
// insights ("i<index>").
func composeInsights(m timelineMemory, order []string) (IndexItem, int) {
	text, size, shown, linked, items := "", 0, 0, []string{}, []string{}
	for _, id := range order {
		i, err := strconv.Atoi(id)
		if err != nil || i < 0 || i >= len(m.Insights) {
			continue
		}
		line := "\n- " + insightLine(m.Insights[i])
		if size+jsLen(line) > layerInsights {
			continue
		}
		text, size, shown = text+line, size+jsLen(line), shown+1
		linked = append(linked, m.Insights[i].Records...)
		items = append(items, itemID("i", i))
	}
	refs, seen := []Reference{}, map[string]bool{}
	for _, record := range linked {
		if !seen[record] {
			seen[record] = true
			refs = append(refs, Reference{ID: record})
		}
	}
	return IndexItem{ID: "5-insights", Kind: "5-insights", Text: insightsHead + text, Records: refs, Items: items}, shown
}
