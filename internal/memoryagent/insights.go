package memoryagent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// L2 insights (consolidation.insights, layered mode) consolidate the
// consolidated memory one level up. L0 reads one batch of records alone and
// so cannot write a conclusion that only several items together support (a
// pattern that recurs, a change and what it led to, the reason for a
// decision, the overall state of an effort). When enough consolidated
// items have accumulated, L2 reads them, never the raw records, each item
// exactly once, in batches, and the consolidating model writes such
// conclusions, each citing the items it rests on; JEV then merges each new
// conclusion into a stored one that states the same (as L1 links L0's
// items). Simple reads show the conclusions as the layer's fifth block
// (insights_layer.go). Lengths are UTF-16 units.

// insight is a conclusion L2 drew from two or more consolidated items: its
// sentence, its date (the latest it rests on) and the records of the items
// it rests on, in the order they were first cited, each once.
type insight struct {
	Text    string   `json:"text"`
	Date    string   `json:"date"`
	Records []string `json:"records"`
}

// insightRead is L2's read position in the memory: how many of its events,
// of each key's values and of its directives L2 has read, always the first
// ones (consolidation only appends to these lists), so that no item is
// read twice. ModelErrors and FirstModelError count the jobs whose L2 of
// the head batch ended in a model error, as timelineState's count L0's,
// for L2's own drop mode.
type insightRead struct {
	Events          int            `json:"events"`
	Values          map[string]int `json:"values"`
	Directives      int            `json:"directives"`
	ModelErrors     int            `json:"modelErrors,omitempty"`
	FirstModelError int64          `json:"firstModelError,omitempty"`
}

// The kinds of consolidated item.
const (
	itemEvent = iota
	itemValue
	itemDirective
)

// insightItem is a consolidated item as L2 reads it: its id (E<i>, the i-th
// event; V<k>.<j>, the j-th value of the k-th key in FactOrder; D<i>, the
// i-th directive; all from 1), its line, its date and records, and where it
// is in the memory (its kind, index in its list, and a value's key).
type insightItem struct {
	id, line, date, key string
	kind, index         int
	records             []string
}

// insightSystem is the L2 consolidation prompt.
const insightSystem = `You consolidate a person's long-term memory one level up. The input lists items already extracted from their conversations: events of topics, values of facts and standing instructions, each with an id and a date. Write the conclusions that follow only from two or more of these items together and that no single item states: a pattern that recurs, a change over time and what it led to, the reason behind a decision, the overall state of an effort, how the person's preferences or situation evolved. Each conclusion is one sentence, dated with the latest date it rests on, and lists the ids of the items it rests on (at least two). Do not repeat what one item already says, do not go beyond what the items say, and return an empty list when nothing follows. Reply with JSON only: {"insights":[{"text":"...","date":"YYYY/MM/DD","items":["E12","V3.2"]}]}`

// insightStream is the consolidated items L2 has not read, in memory order:
// the events after those read, then each key's values after those read
// (keys in FactOrder), then the directives after those read. Lines are
// "E<i> (<date>) [<thread title>] <text>", "V<k>.<j> (<date>) <key>:
// <value>" and "D<i> (<date>) <text>"; an event whose thread is not stored
// shows the thread's id.
func insightStream(m timelineMemory, read insightRead) []insightItem {
	titles := map[string]string{}
	for _, t := range m.Threads {
		if _, ok := titles[t.ID]; !ok {
			titles[t.ID] = t.Title
		}
	}
	out := []insightItem{}
	for i := min(read.Events, len(m.Events)); i < len(m.Events); i++ {
		e := m.Events[i]
		title, ok := titles[e.Thread]
		if !ok {
			title = e.Thread
		}
		id := fmt.Sprintf("E%d", i+1)
		out = append(out, insightItem{id: id, line: id + " (" + e.Date + ") [" + title + "] " + e.Text, date: e.Date, kind: itemEvent, index: i, records: e.Records})
	}
	for k, key := range m.FactOrder {
		values := m.Facts[key]
		for j := min(read.Values[key], len(values)); j < len(values); j++ {
			v := values[j]
			id := fmt.Sprintf("V%d.%d", k+1, j+1)
			out = append(out, insightItem{id: id, line: id + " (" + v.Date + ") " + key + ": " + v.Value, date: v.Date, key: key, kind: itemValue, index: j, records: v.Records})
		}
	}
	for i := min(read.Directives, len(m.Directives)); i < len(m.Directives); i++ {
		d := m.Directives[i]
		id := fmt.Sprintf("D%d", i+1)
		out = append(out, insightItem{id: id, line: id + " (" + d.Date + ") " + d.Text, date: d.Date, kind: itemDirective, index: i, records: d.Records})
	}
	return out
}

// advance moves the read position past items, read in stream order.
func (r *insightRead) advance(items []insightItem) {
	for _, item := range items {
		switch item.kind {
		case itemEvent:
			r.Events = item.index + 1
		case itemValue:
			if r.Values == nil {
				r.Values = map[string]int{}
			}
			r.Values[item.key] = item.index + 1
		default:
			r.Directives = item.index + 1
		}
	}
}

// insightBatches cuts the stream into at most max batches, in order, by
// nextBatch's rule over the items' lines: a batch ends with the item at
// which its lines reach chars UTF-16 units, never inside an item; with
// flush the short rest is a batch too.
func insightBatches(stream []insightItem, chars, max int, flush bool) [][]insightItem {
	lines := make([]pendingRecord, len(stream))
	for i, item := range stream {
		lines[i] = pendingRecord{Text: item.line}
	}
	out := [][]insightItem{}
	for start := 0; len(out) < max; {
		batch := nextBatch(lines[start:], chars, flush)
		if batch == nil {
			break
		}
		out = append(out, stream[start:start+len(batch):start+len(batch)])
		start += len(batch)
	}
	return out
}

// insightPrompt is the user message of an L2 call: the batch's lines.
func insightPrompt(batch []insightItem) string {
	return strings.Join(itemLines(batch), "\n")
}

// itemLines is the items' lines.
func itemLines(items []insightItem) []string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = item.line
	}
	return out
}

// isDay reports whether s is a date written YYYY/MM/DD.
func isDay(s string) bool {
	_, err := time.Parse("2006/01/02", s)
	return err == nil
}

// parseInsights is the insights of an L2 reply about batch, in reply order;
// !ok when the reply holds no JSON object (parseTimelineReply). An insight
// is kept when its text is not empty and it cites at least two items of the
// batch (by id, trimmed, in any case; other ids and repeats are ignored).
// Its records are the cited items' records, in the order of first
// citation, each once. Its date is the model's when that is a day
// (YYYY/MM/DD), else the latest date of the cited items: of those that are
// days, else of any.
func parseInsights(reply string, batch []insightItem) ([]insight, bool) {
	value, ok := parseTimelineReply(reply)
	if !ok {
		return nil, false
	}
	known := map[string]int{}
	for i, item := range batch {
		known[item.id] = i
	}
	out := []insight{}
	for _, raw := range jsonList(value["insights"]) {
		o := jsonObject(raw)
		text := jsonText(o["text"])
		if text == "" {
			continue
		}
		cited := []int{}
		for _, ref := range jsonList(o["items"]) {
			if i, ok := known[strings.ToUpper(jsonText(ref))]; ok && !slices.Contains(cited, i) {
				cited = append(cited, i)
			}
		}
		if len(cited) < 2 {
			continue
		}
		records, latest, latestDay := []string{}, "", ""
		for _, i := range cited {
			for _, record := range batch[i].records {
				if !slices.Contains(records, record) {
					records = append(records, record)
				}
			}
			date := batch[i].date
			if isDay(date) && date > latestDay {
				latestDay = date
			}
			if date > latest {
				latest = date
			}
		}
		date := jsonText(o["date"])
		if !isDay(date) {
			date = latestDay
			if date == "" {
				date = latest
			}
		}
		out = append(out, insight{Text: text, Date: date, Records: records})
	}
	return out, true
}

// The L2 linking question, in the style of the linking questions
// (layered_link.go).

func sameInsightQuestion(n, c string) string {
	return "Does the new insight whose `id` is \"" + n + "\" state the same conclusion as the candidate whose `id` is \"" + c + "\"?"
}

const (
	sameInsightYes = "Both draw the same conclusion about the same thing, even if worded differently."
	sameInsightNo  = "They concern different things, or draw different conclusions."
)

// insightCounts is what linking one L2 batch did: its insights added and
// merged into stored ones, and whether JEV failed (Fallback).
type insightCounts struct {
	New, Merged int
	Fallback    bool
}

// insightItemOf is an insight as JEV reads it.
func insightItemOf(id string, in insight) linkItem {
	return linkItem{ID: id, Date: in.Date, Text: authorExcerpt(in.Text, simpleListing)}
}

// linkInsights returns the stored insights (state.Memory.Insights) with an
// L2 batch's new insights linked into them, in batch order, leaving the
// stored ones unchanged, and what linking did. Each new insight, n1, n2, …,
// is compared with up to LinkCandidates stored insights, I<j> (the j-th,
// from 1), nearest to it by the texts (rank: embeddings, else shared
// words); JEV answers whether each pair states the same conclusion, the
// questions packed into few calls (judge). A new insight merges into its
// best yes (bestLink: at least 0.5, ties to the nearer): the stored one
// keeps its text, takes the later date and appends the new records it
// lacks; otherwise it is added. A failed or unaffordable JEV call adds
// every new insight (Fallback). Every JEV call's usage is the state's and
// the job's (decide); only a canceled job is an error.
func (l *linker) linkInsights(ctx context.Context, state *timelineState, fresh []insight, result *ConsolidateResult) ([]insight, insightCounts, error) {
	stored := state.Memory.Insights
	out, counts := slices.Clone(stored), insightCounts{}
	queries, docs := make([]string, len(fresh)), make([]string, len(stored))
	news, cands := make([]linkItem, len(fresh)), make([]linkItem, len(stored))
	for i, in := range fresh {
		queries[i], news[i] = in.Text, insightItemOf(fmt.Sprintf("n%d", i+1), in)
	}
	for j, in := range stored {
		docs[j], cands[j] = in.Text, insightItemOf(fmt.Sprintf("I%d", j+1), in)
	}
	ranks, _, err := l.rank(ctx, queries, docs)
	if err != nil {
		return nil, insightCounts{}, err
	}
	pairs, all := make([][]*linkPair, len(fresh)), []*linkPair{}
	for i, ranked := range ranks {
		for _, j := range ranked {
			pairs[i] = append(pairs[i], &linkPair{kind: linkInsight, item: i, candidate: j})
		}
		all = append(all, pairs[i]...)
	}
	if len(all) > 0 {
		if err := l.judge(ctx, news, cands, all, state, result, &result.InsightLinkCalls); err != nil {
			if ctx.Err() != nil {
				return nil, insightCounts{}, err
			}
			counts.Fallback = true
		}
	}
	for i, in := range fresh {
		answers := []float64{}
		for _, p := range pairs[i] {
			answers = append(answers, p.same)
		}
		if b := bestLink(answers); b >= 0 && !counts.Fallback {
			merged := out[pairs[i][b].candidate]
			if in.Date > merged.Date {
				merged.Date = in.Date
			}
			merged.Records = slices.Clone(merged.Records)
			for _, record := range in.Records {
				if !slices.Contains(merged.Records, record) {
					merged.Records = append(merged.Records, record)
				}
			}
			out[pairs[i][b].candidate] = merged
			counts.Merged++
			continue
		}
		out = append(out, in)
		counts.New++
	}
	return out, counts, nil
}

// insightsOn reports whether the configuration has L2 insights
// (consolidation.insights).
func (a *Agent) insightsOn() bool {
	return a.config.Consolidation != nil && a.config.Consolidation.Insights
}

// insightPosition is the state's L2 read position: nothing read without
// one.
func (s *timelineState) insightPosition() insightRead {
	if s.InsightRead == nil {
		return insightRead{}
	}
	return *s.InsightRead
}

// insightsUnread counts the consolidated items L2 has not read.
func insightsUnread(state timelineState) int {
	return len(insightStream(state.Memory, state.insightPosition()))
}

// foldInsights is L2 in a layered job (consolidateTimelines, after L0 and
// L1, which it never changes): up to req.MaxBatches batches of the items
// it has not read (insightBatches; the short rest only with rest), each
// extracted alone (extractInsights, with L0's retry, split, drop and
// too-large handling) with at most Parallel extractions in flight, then
// linked into the stored insights (linkInsights) strictly in batch order,
// as soon as a batch and those before it are extracted; after each, the
// read position moves past the batch's items and the state is saved. A
// batch whose extraction ends in a model error stops L2: the batches
// before it stay read, it and those after it stay unread, and the error,
// unless it is the caller's cancellation, a spent budget or an unavailable
// provider, counts against L2's head batch: after dropErrors such jobs over
// dropAfter, the head batch is extracted in drop mode
// (extractInsightsDropping). foldErr is the error the job ends with after
// saving; err another save error, returned at once.
func (a *Agent) foldInsights(ctx context.Context, model Completer, req ConsolidateRequest, state *timelineState, token *string, rest bool, dirty *bool, result *ConsolidateResult) (foldErr, err error) {
	if state.InsightRead == nil {
		state.InsightRead = &insightRead{Values: map[string]int{}}
	}
	head := *state.InsightRead
	batches := insightBatches(insightStream(state.Memory, head), a.config.Consolidation.BatchCharacters, req.MaxBatches, rest)
	if len(batches) == 0 {
		return nil, nil
	}
	*dirty = true
	dropping := a.dropDue(timelineState{ModelErrors: head.ModelErrors, FirstModelError: head.FirstModelError})
	run := startExtractions(ctx, len(batches), a.config.Consolidation.Parallel, func(ctx context.Context, i int, x *extraction) error {
		x.state = timelineState{}
		if i == 0 && dropping {
			return a.extractInsightsDropping(ctx, model, &x.state, batches[i], &x.result)
		}
		return a.extractInsights(ctx, model, &x.state, batches[i], true, &x.result)
	})
	defer run.stop()
	metered := 0
	meter := func(x *extraction) {
		state.Calls += x.state.Calls
		state.Usage.add(x.state.Usage)
		result.Usage.add(x.result.Usage)
		result.ModelMS += x.result.ModelMS
		metered++
	}
	link := a.newLinker()
	for i, batch := range batches {
		x := run.wait(i)
		meter(x)
		if x.err != nil {
			foldErr = x.err
			a.countInsightError(ctx, state.InsightRead, foldErr)
			break
		}
		insights, counts, err := link.linkInsights(ctx, state, x.state.Memory.Insights, result)
		if err != nil {
			foldErr = err
			break
		}
		state.Memory.Insights = insights
		state.InsightRead.advance(batch)
		state.InsightRead.ModelErrors, state.InsightRead.FirstModelError = 0, 0
		result.InsightBatches++
		result.InsightsDropped += x.result.InsightsDropped
		result.InsightsNew += counts.New
		result.InsightsMerged += counts.Merged
		if counts.Fallback {
			result.InsightFallbacks++
		}
		saved, err := a.overlay.saveTimelines(ctx, req.Scope, *token, *state)
		if err != nil {
			if ctx.Err() != nil {
				// The job was canceled: the save after it keeps this batch.
				foldErr = err
				break
			}
			if value := run.stop(); value != nil {
				panic(value)
			}
			return nil, err
		}
		*token, *dirty = saved, false
	}
	// The extractions after the batches linked are discarded; what they
	// spent is kept.
	if value := run.stop(); value != nil {
		panic(value)
	}
	for i := metered; i < len(batches); i++ {
		meter(&run.out[i])
	}
	if foldErr != nil {
		*dirty = true
	}
	return foldErr, nil
}

// countInsightError counts an L2 extraction's error against L2's head
// batch when it is a counted model error (countedError).
func (a *Agent) countInsightError(ctx context.Context, read *insightRead, err error) {
	if countedError(ctx, err) {
		if read.ModelErrors == 0 {
			read.FirstModelError = a.now().UnixMilli()
		}
		read.ModelErrors++
	}
}

// extractInsights is the L2 of one batch, or part of one, as foldTimelines
// is L0's: one consolidator call (insightSystem, the batch's lines, 8192
// output tokens), two attempts, then, when split is allowed, each half
// with two attempts of its own; a part that still does not parse is
// dropped (its items are read without conclusions: InsightsDropped). A
// request the provider refuses as too large before sending it splits in
// halves at once, down to single items; a single item still too large is
// dropped. The insights go to state.Memory.Insights, batch-local; calls
// and usage to the state and the result.
func (a *Agent) extractInsights(ctx context.Context, model Completer, state *timelineState, batch []insightItem, split bool, result *ConsolidateResult) error {
	for attempt := 0; attempt < 2; attempt++ {
		reply, err := a.completeFold(ctx, model, insightSystem, insightPrompt(batch), state, result)
		if errors.Is(err, ErrRequestTooLarge) {
			if len(batch) == 1 {
				result.InsightsDropped++
				return nil
			}
			half := (len(batch) + 1) / 2
			if err := a.extractInsights(ctx, model, state, batch[:half], split, result); err != nil {
				return err
			}
			return a.extractInsights(ctx, model, state, batch[half:], split, result)
		}
		if err != nil {
			return err
		}
		if insights, ok := parseInsights(reply, batch); ok {
			state.Memory.Insights = append(state.Memory.Insights, insights...)
			return nil
		}
	}
	if split && len(batch) > 1 {
		half := (len(batch) + 1) / 2
		if err := a.extractInsights(ctx, model, state, batch[:half], false, result); err != nil {
			return err
		}
		return a.extractInsights(ctx, model, state, batch[half:], false, result)
	}
	result.InsightsDropped += len(batch)
	return nil
}

// extractInsightsDropping extracts L2's head batch after repeated model
// errors, as foldDropping folds L0's: split once, each half extracted on
// its own without a further split; a half whose extraction fails with a
// counted model error is dropped whole (InsightsDropped), a half that is
// extracted is kept. Any other error ends the job, dropping nothing.
func (a *Agent) extractInsightsDropping(ctx context.Context, model Completer, state *timelineState, batch []insightItem, result *ConsolidateResult) error {
	parts := [][]insightItem{batch}
	if len(batch) > 1 {
		half := (len(batch) + 1) / 2
		parts = [][]insightItem{batch[:half], batch[half:]}
	}
	for _, part := range parts {
		kept, dropped := len(state.Memory.Insights), result.InsightsDropped
		err := a.extractInsights(ctx, model, state, part, false, result)
		if err == nil {
			continue
		}
		if !countedError(ctx, err) {
			return err
		}
		state.Memory.Insights, result.InsightsDropped = state.Memory.Insights[:kept], dropped+len(part)
	}
	return nil
}
