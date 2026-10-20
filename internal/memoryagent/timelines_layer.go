package memoryagent

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/mnemon-dev/mnemon/internal/records"
)

// The timelines layer is the read side of timelines consolidation: the
// author's consolidated part of a View (consolidation.ts C:183-267 and
// consolidatedLayer, natural.ts N:1129-1197). The records a simple read shows
// light up the topics and values linked to them (bottom-up); JEV judges those
// and the topics and values nearest the message (top-down). Before the
// records, the View then shows, coarse to fine, the standing instructions, a
// directory of the topics, the chosen timelines and the value histories, as
// View.Index blocks. The records give way from the end when the View would
// pass its characters, and records linked from what is shown may use the
// room left. Lengths are JavaScript string lengths (UTF-16 units).

const (
	// The budgets of the blocks (LAYER, C:183). A timeline is begun only
	// with at least layerThreadFloor characters of room.
	layerInstructions = 1500
	layerDirectory    = 1500
	layerTimelines    = 3500
	layerThread       = 1500
	layerThreadFloor  = 300
	layerValues       = 1500
	layerNearThreads  = 12
	layerNearValues   = 16
	// layerEmbedText is the units of a text the embedder reads (C:283).
	layerEmbedText = 1500
	// layerEmbedBatch keeps a DerivedVectors call within its 512 texts and
	// 2 MiB (256 texts of 1500 units are at most 1.2 MB).
	layerEmbedBatch = 256
	// Linked records (N:1172-1189): at most 24 not shown are judged, only
	// while 400 characters are left, and at most 8 join the View.
	layerLinked = 24
	layerSlack  = 400
	layerExpand = 8
)

// eventThreads is the author's threadsOf (C:184-188) as event indexes, and
// the threads that have events, in memory order.
func eventThreads(m timelineMemory) (ids []string, by map[string][]int) {
	ids, by = []string{}, map[string][]int{}
	for i, e := range m.Events {
		by[e.Thread] = append(by[e.Thread], i)
	}
	for _, t := range m.Threads {
		if len(by[t.ID]) > 0 {
			ids = append(ids, t.ID)
		}
	}
	return ids, by
}

// threadText is what JEV and the embedder read of a topic (C:190-192): its
// title, span and latest three events. events is not empty.
func threadText(title string, m timelineMemory, events []int) string {
	latest := []string{}
	for _, i := range events[max(0, len(events)-3):] {
		latest = append(latest, m.Events[i].Date+" "+m.Events[i].Text)
	}
	return fmt.Sprintf("%s: %d events, %s to %s. Latest: %s", title, len(events), m.Events[events[0]].Date, m.Events[events[len(events)-1]].Date, strings.Join(latest, " / "))
}

// valueText is a key's history of values (C:193).
func valueText(key string, values []factValue) string {
	parts := []string{}
	for _, v := range values {
		part := v.Date + " " + v.Value
		if v.Conflict {
			part += " (contradicts the earlier value)"
		}
		parts = append(parts, part)
	}
	return key + ": " + strings.Join(parts, "; ")
}

// layerTexts is the author's layerTexts (C:195-199): the text of every topic
// that has events, by thread id, and of every value's history, by key.
func layerTexts(m timelineMemory) (threadTexts map[string]string, valueTexts map[string]string) {
	ids, by := eventThreads(m)
	titles := map[string]string{}
	for _, t := range m.Threads {
		titles[t.ID] = t.Title
	}
	threadTexts, valueTexts = map[string]string{}, map[string]string{}
	for _, id := range ids {
		threadTexts[id] = threadText(titles[id], m, by[id])
	}
	for _, key := range m.FactOrder {
		valueTexts[key] = valueText(key, m.Facts[key])
	}
	return threadTexts, valueTexts
}

// lightUp is the author's lightUp (C:202-210): the threads of the events
// linked to an anchor, in the order they first light up, with the count of
// anchor links over their events; those events (indexes); and the keys with
// a value linked to an anchor, in key order.
func lightUp(m timelineMemory, anchors map[string]bool) (threads []string, threadCounts map[string]int, events map[int]bool, values []string) {
	threads, threadCounts, events, values = []string{}, map[string]int{}, map[int]bool{}, []string{}
	anchored := func(id string) bool { return anchors[id] }
	for i, e := range m.Events {
		n := 0
		for _, id := range e.Records {
			if anchors[id] {
				n++
			}
		}
		if n == 0 {
			continue
		}
		if _, ok := threadCounts[e.Thread]; !ok {
			threads = append(threads, e.Thread)
		}
		threadCounts[e.Thread] += n
		events[i] = true
	}
	for _, key := range m.FactOrder {
		if slices.ContainsFunc(m.Facts[key], func(v factValue) bool { return slices.ContainsFunc(v.Records, anchored) }) {
			values = append(values, key)
		}
	}
	return threads, threadCounts, events, values
}

// litSets is what a View's records light up (lightUp's results).
type litSets struct {
	Threads []string
	Counts  map[string]int
	Events  map[int]bool
	Values  []string
}

// layerOrder is the order of the chosen topics and values (N:1159-1163):
// JEV's yes (needed ≥ 0.5) by score, ties in candidate order, then the lit
// threads below 0.5 by anchor count (ties in lit order) and the lit values
// below 0.5 in lit order. Candidates only near the message and below 0.5 are
// not chosen.
func layerOrder(topics, values []string, lit litSets, threadScore, valueScore map[string]float64) (threads, vals []string) {
	yes := func(list []string, score map[string]float64) []string {
		out := slices.DeleteFunc(slices.Clone(list), func(id string) bool { return score[id] < 0.5 })
		slices.SortStableFunc(out, func(a, b string) int { return probabilityOrder(score[a], score[b]) })
		return out
	}
	below := func(list []string, score map[string]float64) []string {
		return slices.DeleteFunc(slices.Clone(list), func(id string) bool { return score[id] >= 0.5 })
	}
	litThreads := below(lit.Threads, threadScore)
	slices.SortStableFunc(litThreads, func(a, b string) int { return cmp.Compare(lit.Counts[b], lit.Counts[a]) })
	return append(yes(topics, threadScore), litThreads...), append(yes(values, valueScore), below(lit.Values, valueScore)...)
}

// layerCandidates is what JEV judges for the layer (N:1151-1152): the lit
// threads, then the near ones (indexes into threadIDs), each once; the lit
// values, then the near ones (indexes into keys), each once.
func layerCandidates(lit litSets, threadIDs, keys []string, nearThreads, nearValues []int) (topics, values []string) {
	topics, values = slices.Clone(lit.Threads), slices.Clone(lit.Values)
	for _, i := range nearThreads {
		if !slices.Contains(topics, threadIDs[i]) {
			topics = append(topics, threadIDs[i])
		}
	}
	for _, i := range nearValues {
		if !slices.Contains(values, keys[i]) {
			values = append(values, keys[i])
		}
	}
	return topics, values
}

// composeLayer is the author's composeLayer (C:218-267): the blocks of the
// consolidated layer, each within its budget, as View.Index items.
//   - 1-instructions: the directives, oldest first, until one would pass
//     1500 with the header.
//   - 2-topics: the chosen threads, then the threads by event count, each
//     line kept if it fits 1500 with the header; listed in memory order.
//   - 3-timelines: the chosen threads in order, each within
//     min(1500, 3500 - the timelines so far), none begun in less than 300.
//     A thread over its room keeps its anchor events (lit.Events), then the
//     newest that fit, shown in their order.
//   - 4-values: the chosen keys' histories while each fits 1500; the header
//     is not counted.
//
// Records name the records linked from what each block shows, in order,
// each once, by id only; the read keeps those its View shows.
func composeLayer(m timelineMemory, threadOrder, valueOrder []string, lit litSets) []IndexItem {
	_, by := eventThreads(m)
	blocks := []IndexItem{}
	add := func(id, text string, linked []string) {
		refs, seen := []Reference{}, map[string]bool{}
		for _, record := range linked {
			if !seen[record] {
				seen[record] = true
				refs = append(refs, Reference{ID: record})
			}
		}
		blocks = append(blocks, IndexItem{ID: id, Kind: id, Text: text, Records: refs})
	}

	text, linked := "Standing instructions and preferences the user stated:", []string{}
	size, shown := jsLen(text), 0
	for _, d := range m.Directives {
		line := "\n- (" + d.Date + ") " + d.Text
		if size+jsLen(line) > layerInstructions {
			break
		}
		text, size, shown = text+line, size+jsLen(line), shown+1
		linked = append(linked, d.Records...)
	}
	if shown > 0 {
		add("1-instructions", text, linked)
	}

	type span struct {
		id, title, first, last string
		events                 int
	}
	spans := []span{}
	for _, t := range m.Threads {
		if events := by[t.ID]; len(events) > 0 {
			spans = append(spans, span{t.ID, t.Title, m.Events[events[0]].Date, m.Events[events[len(events)-1]].Date, len(events)})
		}
	}
	topicLine := func(s span) string {
		if s.first == s.last {
			return "\n- " + s.title + " (" + s.first + ")"
		}
		return "\n- " + s.title + " (" + s.first + " to " + s.last + ")"
	}
	largest := slices.Clone(spans)
	slices.SortStableFunc(largest, func(a, b span) int { return cmp.Compare(b.events, a.events) })
	ids := slices.Clone(threadOrder)
	for _, s := range largest {
		ids = append(ids, s.id)
	}
	head, keep := "Topics of the conversation (first and last date):", map[string]bool{}
	size = jsLen(head)
	for _, id := range ids {
		i := slices.IndexFunc(spans, func(s span) bool { return s.id == id })
		if i < 0 || keep[id] || size+jsLen(topicLine(spans[i])) > layerDirectory {
			continue
		}
		keep[id] = true
		size += jsLen(topicLine(spans[i]))
	}
	if len(keep) > 0 {
		text = head
		for _, s := range spans {
			if keep[s.id] {
				text += topicLine(s)
			}
		}
		add("2-topics", text, nil)
	}

	eventLine := func(i int) string { return "\n- " + m.Events[i].Date + ": " + m.Events[i].Text }
	timelines, linked := "", []string{}
	size = 0
	for _, id := range threadOrder {
		room := min(layerThread, layerTimelines-size)
		if room < layerThreadFloor {
			break
		}
		title := "Thread: " + id
		if i := slices.IndexFunc(m.Threads, func(t thread) bool { return t.ID == id }); i >= 0 {
			title = "Thread: " + m.Threads[i].Title
		}
		events, used := by[id], jsLen(title)
		for _, i := range events {
			used += jsLen(eventLine(i))
		}
		chosen := events
		if used > room {
			// The anchor events, then the others newest first, while they fit.
			pick, order := map[int]bool{}, []int{}
			for _, i := range events {
				if lit.Events[i] {
					order = append(order, i)
				}
			}
			for _, i := range slices.Backward(events) {
				if !lit.Events[i] {
					order = append(order, i)
				}
			}
			used = jsLen(title)
			for _, i := range order {
				if n := jsLen(eventLine(i)); used+n <= room {
					pick[i] = true
					used += n
				}
			}
			chosen = slices.DeleteFunc(slices.Clone(events), func(i int) bool { return !pick[i] })
		}
		if len(chosen) == 0 {
			continue
		}
		part := title
		for _, i := range chosen {
			part += eventLine(i)
			linked = append(linked, m.Events[i].Records...)
		}
		if timelines != "" {
			timelines, size = timelines+"\n\n", size+2
		}
		timelines, size = timelines+part, size+jsLen(part)
	}
	if timelines != "" {
		add("3-timelines", "Timelines of the topics this concerns:\n"+timelines, linked)
	}

	values, linked := "", []string{}
	size = 0
	for _, key := range valueOrder {
		list, ok := m.Facts[key]
		if !ok {
			continue
		}
		line := "\n- " + valueText(key, list)
		if size+jsLen(line) > layerValues {
			continue
		}
		values, size = values+line, size+jsLen(line)
		for _, v := range list {
			linked = append(linked, v.Records...)
		}
	}
	if values != "" {
		add("4-values", "History of values:"+values, linked)
	}
	return blocks
}

// layerOn reports whether simple reads add the timelines layer: in
// timelines or layered mode unless the policy turns consolidation off.
func (a *Agent) layerOn() bool {
	return a.config.Consolidation.Timelines() && a.config.Policy.Consolidation
}

// layer puts the consolidated layer before the composed View's records
// (consolidatedLayer, N:1135-1197). The scope's state is read; with no state,
// or nothing consolidated, the View is unchanged. Anchors are the View's
// records. The candidates are the lit topics and the 12 nearest the raw last
// user message, the lit values and the 16 nearest; JEV judges them, the
// order picks what the blocks show. The blocks' characters count toward
// View.MaxCharacters, not toward the items: walking the records in order,
// one that would pass the characters is dropped. Then linked records may
// join (expand). A block's Records name the linked records the View shows.
// The layer degrades and never fails the read (a state that will not load,
// an embedder that fails, a judgement that fails or finds the budget spent;
// each named in ReadStats.Degraded), unless the read is canceled.
func (r *simpleRun) layer(ctx context.Context, view View) (View, error) {
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
	var lit litSets
	lit.Threads, lit.Counts, lit.Events, lit.Values = lightUp(m, anchors)
	ids, _ := eventThreads(m)
	threadTexts, valueTexts := layerTexts(m)
	threadList, valueList := []string{}, []string{}
	for _, id := range ids {
		threadList = append(threadList, threadTexts[id])
	}
	for _, key := range m.FactOrder {
		valueList = append(valueList, valueTexts[key])
	}
	message := ""
	for _, value := range slices.Backward(r.dialogue) {
		if value.Role == "user" {
			message = value.Text
			break
		}
	}
	nearThreads, nearValues, _ := r.nearest(ctx, message, threadList, valueList)
	if ctx.Err() != nil {
		return View{}, ctx.Err()
	}
	topics, values := layerCandidates(lit, ids, m.FactOrder, nearThreads, nearValues)
	candidates := []judgeCandidate{}
	for _, id := range topics {
		candidates = append(candidates, judgeCandidate{Source: "topic", Text: threadTexts[id]})
	}
	for _, key := range values {
		candidates = append(candidates, judgeCandidate{Source: "value", Text: valueTexts[key]})
	}
	scores, err := r.judgeNeeded(ctx, candidates)
	if err != nil {
		if ctx.Err() != nil {
			return View{}, ctx.Err()
		}
		r.degrade("layer")
	}
	threadScore, valueScore := map[string]float64{}, map[string]float64{}
	for i, id := range topics {
		threadScore[id] = scores[i]
	}
	for i, key := range values {
		valueScore[key] = scores[len(topics)+i]
	}
	threadOrder, valueOrder := layerOrder(topics, values, lit, threadScore, valueScore)
	blocks := composeLayer(m, threadOrder, valueOrder, lit)

	used, kept, cut := 0, []Evidence{}, false
	for _, block := range blocks {
		used += jsLen(block.Text)
	}
	for _, record := range view.Records {
		if n := jsLen(record.Text); used+n <= r.agent.config.View.MaxCharacters {
			kept, used = append(kept, record), used+n
		} else {
			cut = true
		}
	}
	if kept, err = r.expand(ctx, blocks, kept, used); err != nil {
		return View{}, err
	}
	shown := map[string]Reference{}
	for _, record := range kept {
		shown[record.ID] = record.Reference
	}
	for i := range blocks {
		refs := []Reference{}
		for _, ref := range blocks[i].Records {
			if found, ok := shown[ref.ID]; ok {
				refs = append(refs, found)
			}
		}
		blocks[i].Records = refs
	}
	view.Index, view.Records = blocks, kept
	view.Truncated = view.Truncated || cut
	view.Needs = r.viewNeeds(kept)
	view.Stats = r.statsNow()
	return view, nil
}

// expand adds to the kept records those linked from the shown blocks that
// JEV calls needed (N:1172-1189): of the linked records not shown, the first
// 24 are read (evidence(), proved against their originals) and judged, if
// the View has 400 characters left; those needed (≥ 0.5) join by score,
// best first, while fewer than 8 joined, the records are fewer than the
// View's items, and the record fits the characters. The author judges even
// when the records are already full; that call is skipped here, with the
// same View. Only a canceled read is an error.
func (r *simpleRun) expand(ctx context.Context, blocks []IndexItem, kept []Evidence, used int) ([]Evidence, error) {
	maxItems, maxChars := r.req.MaxItems, r.agent.config.View.MaxCharacters
	shown, seen, linked := map[string]bool{}, map[string]bool{}, []string{}
	for _, record := range kept {
		shown[record.ID] = true
	}
	for _, block := range blocks {
		for _, ref := range block.Records {
			if !seen[ref.ID] && !shown[ref.ID] && len(linked) < layerLinked {
				linked = append(linked, ref.ID)
			}
			seen[ref.ID] = true
		}
	}
	if len(linked) == 0 || used >= maxChars-layerSlack || len(kept) >= maxItems {
		return kept, nil
	}
	values, err := evidence(ctx, r.agent.store, r.revision, r.req.Scope, linked)
	if err != nil {
		if ctx.Err() == nil {
			r.degrade("expand")
		}
		return kept, ctx.Err()
	}
	byID := map[string]Evidence{}
	for _, value := range values {
		byID[value.ID] = value
	}
	found, candidates := []Evidence{}, []judgeCandidate{}
	for _, id := range linked {
		if value, ok := byID[id]; ok {
			found = append(found, value)
			candidates = append(candidates, judgeCandidate{Source: judgeSource, Text: value.Text})
		}
	}
	scores, err := r.judgeNeeded(ctx, candidates)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		r.degrade("expand")
	}
	order := []int{}
	for i := range found {
		if scores[i] >= 0.5 {
			order = append(order, i)
		}
	}
	slices.SortStableFunc(order, func(a, b int) int { return probabilityOrder(scores[a], scores[b]) })
	expanded := 0
	for _, i := range order {
		n := jsLen(found[i].Text)
		if expanded >= layerExpand || len(kept) >= maxItems || used+n > maxChars {
			continue
		}
		found[i].Score = scores[i]
		kept, used, expanded = append(kept, found[i]), used+n, expanded+1
	}
	return kept, nil
}

// neededRequest is batchRequest with the needed question n<i> alone.
func neededRequest(dialogue []Message, candidates []judgeCandidate) (batchState, map[string]Question) {
	state, questions := batchRequest(dialogue, candidates)
	for i := range candidates {
		delete(questions, fmt.Sprintf("s%d", i))
	}
	return state, questions
}

// judgeNeeded asks JEV whether the reply should use each candidate, in
// sequential batches of 40 (N:1154-1157 and 1181-1184), and returns each
// candidate's answer. A spent budget (errCallBudget) or a failed call (its
// error) ends the judging: the candidates it did not judge score 0.
func (r *simpleRun) judgeNeeded(ctx context.Context, candidates []judgeCandidate) ([]float64, error) {
	scores := make([]float64, len(candidates))
	for start := 0; start < len(candidates); start += judgeBatch {
		part := candidates[start:min(start+judgeBatch, len(candidates))]
		state, questions := neededRequest(r.dialogue, part)
		answers, err := r.decide(ctx, state, questions)
		if err != nil {
			return scores, err
		}
		for i := range part {
			scores[start+i] = answers[fmt.Sprintf("n%d", i)]
		}
	}
	return scores, nil
}

// nearest ranks the topic and value texts by nearness to the message (the
// author's nearest, C:269-277, as N:1143-1150 calls it): by meaning when an
// embedder is configured, the dot product of each text's document vector
// with the message's query vector (texts and message cut to 1500 units),
// best first, ties in order; else, or when embedding fails (a degraded
// stage, layer-lexical), by shared words (BM25, positive scores only). It
// returns the best 12 topics and 16 values as indexes, and whether shared
// words ranked them.
func (r *simpleRun) nearest(ctx context.Context, message string, threadTexts, valueTexts []string) (threads, values []int, lexical bool) {
	if query, threadVectors, valueVectors, ok := r.layerVectors(ctx, message, threadTexts, valueTexts); ok {
		return byDot(query, threadVectors, layerNearThreads), byDot(query, valueVectors, layerNearValues), false
	}
	if r.agent.config.Embedding != nil && ctx.Err() == nil {
		r.degrade("layer-lexical")
	}
	return bm25Top(threadTexts, message, layerNearThreads), bm25Top(valueTexts, message, layerNearValues), true
}

// layerVectors embeds the message and the texts; ok is false without an
// embedder, when it fails, or when a vector does not match the query's size.
func (r *simpleRun) layerVectors(ctx context.Context, message string, threadTexts, valueTexts []string) (query []float32, threads, values [][]float32, ok bool) {
	config := r.agent.config.Embedding
	if config == nil {
		return nil, nil, nil, false
	}
	query, err := r.agent.store.QueryVector(ctx, jsSlice(message, 0, layerEmbedText), *config)
	if err != nil || len(query) == 0 {
		return nil, nil, nil, false
	}
	documents := func(texts []string) [][]float32 {
		vectors, err := r.agent.layerDocuments(ctx, texts)
		if err != nil || slices.ContainsFunc(vectors, func(v []float32) bool { return len(v) != len(query) }) {
			return nil
		}
		return vectors
	}
	if threads = documents(threadTexts); threads == nil {
		return nil, nil, nil, false
	}
	if values = documents(valueTexts); values == nil {
		return nil, nil, nil, false
	}
	return query, threads, values, true
}

// layerDocuments is the document vectors of the layer texts, each cut to
// layerEmbedText units, in batches of layerEmbedBatch; missing vectors are
// embedded and stored, so a later read or warm-up finds them.
func (a *Agent) layerDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	return a.documentVectors(ctx, texts, true)
}

// documentVectors is layerDocuments; without store, every text is embedded
// and its vector is not stored (texts read once).
func (a *Agent) documentVectors(ctx context.Context, texts []string, store bool) ([][]float32, error) {
	out := [][]float32{}
	for start := 0; start < len(texts); start += layerEmbedBatch {
		part := []string{}
		for _, text := range texts[start:min(start+layerEmbedBatch, len(texts))] {
			part = append(part, jsSlice(text, 0, layerEmbedText))
		}
		var vectors [][]float32
		var err error
		if store {
			vectors, err = a.store.DerivedVectors(ctx, part, *a.config.Embedding, true)
		} else {
			vectors, err = a.store.EmbedDocuments(ctx, part, *a.config.Embedding)
		}
		if err != nil {
			return nil, err
		}
		out = append(out, vectors...)
	}
	return out, nil
}

// WarmLayer embeds and stores the scope's timelines layer texts, its topics
// and value histories, which the first read after a consolidation would
// otherwise embed while it answers (the reference system embeds them when
// its consolidation catches up). It does nothing without an embedder, the
// timelines layer or a consolidated state; an embedding error is returned
// for the caller to log, and the read then embeds or falls back as before.
func (a *Agent) WarmLayer(ctx context.Context, scope records.Scope) error {
	if !a.layerOn() || a.config.Embedding == nil {
		return nil
	}
	state, _, err := a.overlay.loadTimelines(ctx, scope)
	if err != nil {
		return err
	}
	m := state.Memory
	ids, _ := eventThreads(m)
	threadTexts, valueTexts := layerTexts(m)
	texts := []string{}
	for _, id := range ids {
		texts = append(texts, threadTexts[id])
	}
	for _, key := range m.FactOrder {
		texts = append(texts, valueTexts[key])
	}
	_, err = a.layerDocuments(ctx, texts)
	return err
}

// byDot is the indexes of the best limit vectors by dot product with query,
// best first; ties keep their order.
func byDot(query []float32, vectors [][]float32, limit int) []int {
	order, score := []int{}, make([]float64, len(vectors))
	for i, vector := range vectors {
		order, score[i] = append(order, i), records.VectorDot(query, vector)
	}
	slices.SortStableFunc(order, func(a, b int) int { return probabilityOrder(score[a], score[b]) })
	return order[:min(limit, len(order))]
}
