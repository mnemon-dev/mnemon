package memoryagent

import (
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
