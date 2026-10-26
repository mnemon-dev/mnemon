package memoryagent

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/mnemon-dev/mnemon/internal/records"
)

// Timelines consolidation ports the reference system's ("author") background
// consolidation: plugins/dsh-mnemon-strategy-jev-optmem/src/consolidation.ts
// ("C") and its job in natural.ts ("N"). The raw records stay the only
// evidence. A model reads each record once, in order and in batches, and
// writes an index over them: events on topic threads, the history of values
// that can change, and the directives the user gave the assistant, every item
// linked to the records it came from. The read side renders this index.

const (
	timelineFormat = "mnemon-consolidation/v1"
	// timelineMaxTokens is the output limit of every fold (N:1098).
	timelineMaxTokens = 8192
	// After dropErrors model errors over at least dropAfter, the head batch
	// is folded in drop mode (foldDropping): a provider that keeps refusing
	// one batch must not stop every later job. Errors that hit every request
	// alike (ErrModelUnavailable: outage, credentials, balance, rate limit)
	// never count, so an outage of any length drops nothing; a layered
	// job's timeout counts only when another of its calls was answered
	// (foldJob.counts). The same thresholds make a layered head batch's
	// linking rule due (linkDue), counted apart and never splitting it.
	dropErrors = 3
	dropAfter  = 10 * time.Minute
)

// pendingRecord is a record read but not yet folded: what the model reads of
// it (C:31).
type pendingRecord struct {
	ID     string `json:"id"`
	Header string `json:"header"`
	Text   string `json:"text"`
}

type thread struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// Entities, of an event or a fact value, are the names of what it is about,
// as layered extraction lists them; timelines consolidation never sets them.
type event struct {
	Thread   string   `json:"thread"`
	Date     string   `json:"date"`
	Text     string   `json:"text"`
	Records  []string `json:"records"`
	Entities []string `json:"entities,omitempty"`
}

type factValue struct {
	Value    string   `json:"value"`
	Date     string   `json:"date"`
	Records  []string `json:"records"`
	Conflict bool     `json:"conflict"`
	Entities []string `json:"entities,omitempty"`
	// Forget marks the user's request to forget the key (safety.forget).
	Forget bool `json:"forget,omitempty"`
}

type directive struct {
	Text    string   `json:"text"`
	Date    string   `json:"date"`
	Records []string `json:"records"`
}

// timelineMemory is the author's Consolidated (C:30). Facts keeps each key's
// value history; FactOrder lists the keys in the order they were first added,
// as the author's object keys are.
type timelineMemory struct {
	Threads    []thread               `json:"threads"`
	Events     []event                `json:"events"`
	Facts      map[string][]factValue `json:"facts"`
	FactOrder  []string               `json:"factOrder"`
	Directives []directive            `json:"directives"`
}

// timelineState is one scope's consolidation (C:32-39). Watermark is the
// records rowid (records.Head.Version) of the newest record read: the next
// job reads the records after it, so none is read twice. ModelErrors counts
// the jobs whose fold of the head batch ended in a model error since a batch
// last left the pending records, and FirstModelError is when the first of
// them was (Unix milliseconds; 0 for none). LinkErrors and FirstLinkError
// count likewise the jobs (with DeferLinks) in which linking the head batch
// failed for the linking request itself, for its linking rule (linkDue),
// apart from its extraction's drop mode. Mode is the consolidation mode
// of the job that last wrote the state: "" for timelines, or "layered" (a
// state a deleted mode saved keeps that mode's name until a job saves it).
// A stored state's fields beyond these, in the state or in its memory,
// such as those the deleted native edges and the deleted L2 consolidation
// wrote and the "updated" marks of a deleted mode's events, are ignored
// when it loads (loadTimelines decodes it with plain json.Unmarshal, which
// must stay lenient) and dropped when it is next saved.
type timelineState struct {
	Format          string          `json:"format"`
	Mode            string          `json:"mode,omitempty"`
	Watermark       int64           `json:"watermark"`
	Pending         []pendingRecord `json:"pending"`
	Memory          timelineMemory  `json:"memory"`
	Usage           Usage           `json:"usage"`
	Calls           int             `json:"calls"`
	Batches         int             `json:"batches"`
	Failed          int             `json:"failed"`
	ModelErrors     int             `json:"modelErrors,omitempty"`
	FirstModelError int64           `json:"firstModelError,omitempty"`
	LinkErrors      int             `json:"linkErrors,omitempty"`
	FirstLinkError  int64           `json:"firstLinkError,omitempty"`
}

func emptyTimelineState() timelineState {
	return timelineState{Format: timelineFormat, Pending: []pendingRecord{}, Memory: timelineMemory{
		Threads: []thread{}, Events: []event{}, Facts: map[string][]factValue{}, FactOrder: []string{}, Directives: []directive{},
	}}
}

// normalize checks a loaded state and gives its lists non-nil values.
func (s *timelineState) normalize() error {
	if s.Format != timelineFormat {
		return fmt.Errorf("unsupported timelines format %q", s.Format)
	}
	m := &s.Memory
	if s.Pending == nil {
		s.Pending = []pendingRecord{}
	}
	if m.Threads == nil {
		m.Threads = []thread{}
	}
	if m.Events == nil {
		m.Events = []event{}
	}
	if m.Facts == nil {
		m.Facts = map[string][]factValue{}
	}
	if m.FactOrder == nil {
		m.FactOrder = []string{}
	}
	if m.Directives == nil {
		m.Directives = []directive{}
	}
	seen := map[string]bool{}
	for _, key := range m.FactOrder {
		if seen[key] || len(m.Facts[key]) == 0 {
			return errors.New("timelines fact order does not match its facts")
		}
		seen[key] = true
	}
	if len(seen) != len(m.Facts) {
		return errors.New("timelines fact order does not match its facts")
	}
	return nil
}

// ------------------------------------------------------------------ reading

var (
	// The author's transcript heading, and the one WriteConversation writes.
	authorSessionHeading = regexp.MustCompile(`^Conversation #(\d+) on (\d{4}/\d{2}/\d{2})[^\n]*\n?`)
	sessionHeading       = regexp.MustCompile(`^Conversation (\S+) on (\d{4}/\d{2}/\d{2})[^\n]*\n?`)
)

// readable is what a consolidating model reads of a record under a role
// reading, or !ok for nothing (C:50-63). A conversation chunk whose data.lines
// say whose each line is (WriteConversation) is read line by line, as the
// reading reads the role of the line's message: whole, the part within the
// first Excerpt units of the message's text, or not at all; a chunk none of
// whose lines is read is not read. A conversation chunk written before the
// lines were kept is read whole: its text does not say whose a wrapped line
// is. Other records are read whole, up to 2000 units, under the first 40
// units of their title. A last line that is a JSON object is the author's
// data line and is not read.
//
// tally, when given, adds what the reading read and passed over of each role.
func readable(reading RoleReading, title, content string, data map[string]any, tally map[string]ReadUnits) (header, body string, ok bool) {
	if owners := chunkLines(data); owners != nil {
		heading, rest, found := strings.Cut(content, "\n")
		if lines := strings.Split(rest, "\n"); found && len(lines) == len(owners) {
			body, ok = readLines(reading, lines, owners, tally)
			if !ok {
				return "", "", false
			}
			return chunkHeader(heading), body, true
		}
	}
	if lines := strings.Split(content, "\n"); isDataLine(lines[len(lines)-1]) {
		content = strings.Join(lines[:len(lines)-1], "\n")
	}
	session := authorSessionHeading.FindStringSubmatch(content)
	if session == nil {
		session = sessionHeading.FindStringSubmatch(content)
	}
	if session == nil {
		if strings.TrimFunc(content, isJSSpace) == "" {
			return "", "", false
		}
		return jsSlice(title, 0, 40), jsSlice(content, 0, 2000), true
	}
	header, body = session[2]+" (session "+session[1]+")", content[len(session[0]):]
	if strings.TrimFunc(body, isJSSpace) == "" {
		return "", "", false
	}
	return header, body, true
}

// lineOwner is an entry of a conversation chunk's data.lines: the role of the
// message its line belongs to, and the units of the message's text before
// the line.
type lineOwner struct {
	role   string
	offset int
}

// chunkLines is a record's data.lines, or nil when it has none or they are
// not the format.
func chunkLines(data map[string]any) []lineOwner {
	raw, ok := data["lines"].([]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	owners := make([]lineOwner, len(raw))
	for i, item := range raw {
		entry, _ := item.(map[string]any)
		role, isRole := entry["role"].(string)
		offset, isOffset := entry["offset"].(float64)
		if !isRole || !isOffset || offset < 0 {
			return nil
		}
		owners[i] = lineOwner{role: role, offset: int(offset)}
	}
	return owners
}

// readLines is what reading reads of a chunk's lines, owners[i] saying whose
// lines[i] is: the lines it reads, an excerpted message's last line cut with
// " …", joined by newlines; !ok when it reads none. The "role: " prefix of a
// message's first line does not count against the excerpt.
func readLines(reading RoleReading, lines []string, owners []lineOwner, tally map[string]ReadUnits) (string, bool) {
	var kept []string
	for i, line := range lines {
		owner, units, read := owners[i], jsLen(line), 0
		switch reading.modeOf(owner.role) {
		case ReadFull:
			kept, read = append(kept, line), units
		case ReadExcerpt:
			room := reading.Excerpt - owner.offset
			if room <= 0 {
				break
			}
			prefix := ""
			if owner.offset == 0 && strings.HasPrefix(line, owner.role+": ") {
				prefix, line = owner.role+": ", line[len(owner.role)+2:]
			}
			read = jsLen(prefix) + min(jsLen(line), room)
			if jsLen(line) > room {
				line = jsSlice(line, 0, room) + " …"
			}
			kept = append(kept, prefix+line)
		}
		if tally != nil {
			bucket := reading.Bucket(owner.role)
			counted := tally[bucket]
			counted.Read += read
			counted.Skipped += units - read
			tally[bucket] = counted
		}
	}
	return strings.Join(kept, "\n"), len(kept) > 0
}

// chunkHeader is what a conversation chunk's heading line shows the model:
// the date and session as readable has them, or the heading itself when it
// has no date.
func chunkHeader(heading string) string {
	if session := sessionHeading.FindStringSubmatch(heading + "\n"); session != nil {
		return session[2] + " (session " + session[1] + ")"
	}
	return strings.TrimSuffix(heading, ":")
}

// isDataLine is the author's /^\s*\{.*\}\s*$/: JavaScript whitespace around
// braces that enclose no line terminator.
func isDataLine(line string) bool {
	t := strings.TrimFunc(line, isJSSpace)
	return len(t) >= 2 && t[0] == '{' && t[len(t)-1] == '}' && !strings.ContainsAny(t, "\r\n  ")
}

// nextBatch is the records until their text reaches chars UTF-16 units, or,
// when flush, whatever is pending; nil when there is no batch (C:65-73).
func nextBatch(pending []pendingRecord, chars int, flush bool) []pendingRecord {
	size := 0
	for i, record := range pending {
		size += jsLen(record.Text)
		if size >= chars {
			return pending[: i+1 : i+1]
		}
	}
	if flush && len(pending) > 0 {
		return pending[:len(pending):len(pending)]
	}
	return nil
}

// ------------------------------------------------------------------ folding

// timelineSystem is the author's CONSOLIDATION_SYSTEM (C:75-82), verbatim.
const timelineSystem = `You keep a structured memory of a long conversation between a user and an assistant. The conversation arrives in batches of records, in order; each record shows its id, the session date and the text (assistant messages are cut short). For each batch, write down, with the ids of the records each item comes from:

1. events: what happened, was decided, planned, changed, finished, reported or asked about in the user's projects and life. One event per distinct development, one sentence of at most 30 words, keeping exact names, numbers, amounts, versions and dates. Include what the assistant proposed only when the user relied on it or may ask about it later. Put each event in a thread: one specific sub-topic that later messages may return to (a feature, component, problem, decision, document, exam, event, relationship or goal). A conversation about one project is split into its parts; a thread that would hold the whole conversation is too broad. Reuse an existing thread id when the event continues that sub-topic; otherwise start a new thread ("new: <short title>").
2. facts: the value of something about the user's situation that can change later: a number, count, amount, date, time, deadline, place, choice, status, version, tool or name. Use a short key "thing · attribute". When an existing key changes, repeat that exact key with the new value. Set "conflict" to true when a value contradicts an earlier one and the user does not say it changed.
3. directives: instructions the user gives the assistant for how to answer from now on (format, style, what to always or never include), and lasting preferences the user states. Give their substance briefly, and do not repeat one already listed.

Use only what the records say. Dates are the session dates shown with the records. Reply with JSON only, in this form:
{"events":[{"thread":"T3","date":"YYYY/MM/DD","text":"...","records":["r12"]}],"facts":[{"key":"...","value":"...","date":"YYYY/MM/DD","records":["r15"],"conflict":false}],"directives":[{"text":"...","date":"YYYY/MM/DD","records":["r20"]}]}`

// indexLimit caps how many threads, keys and directives a fold's prompt
// lists (the author's IndexLimit, C:88).
type indexLimit struct{ Threads, Values, Directives int }

// timelineIndexLimit is the author's limit for very long memories
// (scripts/lib/jev-runtime.ts:237). Every prompt applies it; a memory within it
// is listed whole, word for word as without a limit.
var timelineIndexLimit = indexLimit{Threads: 120, Values: 240, Directives: 60}

// timelinePrompt is the user message of one fold: the index so far within
// timelineIndexLimit, then the batch's records under batch-local ids.
func timelinePrompt(m timelineMemory, batch []pendingRecord) string {
	return renderTimelinePrompt(m, batch, &timelineIndexLimit)
}

// renderTimelinePrompt is the author's consolidationPrompt (C:98-115); a nil
// limit lists the whole index.
func renderTimelinePrompt(m timelineMemory, batch []pendingRecord, limit *indexLimit) string {
	type count struct {
		n    int
		last string
	}
	counts := map[string]*count{}
	lastEvent := map[string]int{}
	for i, e := range m.Events {
		c := counts[e.Thread]
		if c == nil {
			c = &count{}
			counts[e.Thread] = c
		}
		c.n++
		c.last = e.Date
		lastEvent[e.Thread] = i
	}
	keys := []string{}
	for _, key := range m.FactOrder {
		if len(m.Facts[key]) > 0 {
			keys = append(keys, key)
		}
	}
	latest := func(key string) factValue { values := m.Facts[key]; return values[len(values)-1] }
	// Without a limit every list is shown (limit -1) and no query is needed.
	query, threadLimit, valueLimit, directiveLimit := "", -1, -1, -1
	if limit != nil {
		texts := make([]string, len(batch))
		for i, record := range batch {
			texts[i] = record.Text
		}
		query, threadLimit, valueLimit, directiveLimit = strings.Join(texts, "\n"), limit.Threads, limit.Values, limit.Directives
	}
	// Threads are recent by their latest event, keys by the date of their
	// latest value, directives by order; every list keeps the memory's order.
	threadSet := shownItems(len(m.Threads), threadLimit, func(i int) string { return m.Threads[i].Title }, func(a, b int) int {
		return cmp.Compare(recentEvent(lastEvent, m.Threads[a].ID), recentEvent(lastEvent, m.Threads[b].ID))
	}, query)
	keySet := shownItems(len(keys), valueLimit, func(i int) string { return keys[i] + " " + latest(keys[i]).Value }, func(a, b int) int {
		return jsCompare(latest(keys[a]).Date, latest(keys[b]).Date)
	}, query)
	directiveSet := shownItems(len(m.Directives), directiveLimit, func(i int) string { return m.Directives[i].Text }, cmp.Compare[int], query)
	threads := []string{}
	for i, t := range m.Threads {
		if !threadSet[i] {
			continue
		}
		n, last := 0, "-"
		if c := counts[t.ID]; c != nil {
			n, last = c.n, c.last
		}
		threads = append(threads, fmt.Sprintf("%s %s (%d events, last %s)", t.ID, t.Title, n, last))
	}
	facts := []string{}
	for i, key := range keys {
		if keySet[i] {
			facts = append(facts, "- "+key+": "+latest(key).Value)
		}
	}
	directives := []string{}
	for i, d := range m.Directives {
		if directiveSet[i] {
			directives = append(directives, "- "+d.Text)
		}
	}
	return "Threads so far" + shownOf(len(threads), len(m.Threads)) + ":\n" + linesOrNone(threads) +
		"\n\nState keys so far, with the latest value" + shownOf(len(facts), len(keys)) + ":\n" + linesOrNone(facts) +
		"\n\nDirectives so far" + shownOf(len(directives), len(m.Directives)) + ":\n" + linesOrNone(directives) +
		"\n\n" + batchSection(batch)
}

// batchSection is the batch part of a fold's prompt (C:113-114): the
// records under batch-local ids r0, r1, …, each with its header.
func batchSection(batch []pendingRecord) string {
	shown := []string{}
	for i, record := range batch {
		shown = append(shown, fmt.Sprintf("[r%d] %s\n%s", i, record.Header, record.Text))
	}
	return "New records:\n\n" + strings.Join(shown, "\n\n")
}

// shownOf names a cut list (C:112).
func shownOf(shown, total int) string {
	if shown < total {
		return fmt.Sprintf(" (the %d of %d most related to these records or most recent; the rest keep their ids and keys)", shown, total)
	}
	return ""
}

// recentEvent is the index of a thread's latest event, -1 when it has none.
func recentEvent(last map[string]int, id string) int {
	if i, ok := last[id]; ok {
		return i
	}
	return -1
}

// shownItems is the author's shown() (C:91-97): which of n items a prompt
// lists. All of them when they fit the limit (or limit < 0); else up to
// ceil(limit/2) that share the most words with query by BM25, then the most
// recent by a stable descending sort on recency (ties keep the memory's order)
// until limit are kept.
func shownItems(n, limit int, text func(int) string, recency func(a, b int) int, query string) []bool {
	keep := make([]bool, n)
	if limit < 0 || n <= limit {
		for i := range keep {
			keep[i] = true
		}
		return keep
	}
	texts := make([]string, n)
	for i := range texts {
		texts[i] = text(i)
	}
	size := 0
	for _, i := range bm25Top(texts, query, (limit+1)/2) {
		keep[i] = true
		size++
	}
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return recency(b, a) })
	for _, i := range order {
		if size >= limit {
			break
		}
		if !keep[i] {
			keep[i] = true
			size++
		}
	}
	return keep
}

var (
	lexicalLatin = regexp.MustCompile(`[a-z0-9]+(?:[-_:][a-z0-9]+)*`)
	lexicalHan   = regexp.MustCompile(`\p{Han}+`)
)

// lexicalTokens is the author's lexicalTokens (src/sdk/source/lexical.ts):
// lowercase Latin words and codes, then each run of Han characters as UTF-16
// unit bigrams, or the unit itself when the run is one unit long. A bigram
// that splits a surrogate pair cannot be a Go string; it gets a key of its
// two units that no other token can equal.
func lexicalTokens(text string) []string {
	out := lexicalLatin.FindAllString(strings.ToLower(strings.ReplaceAll(text, "\u0130", "i\u0307")), -1)
	if out == nil {
		out = []string{}
	}
	for _, run := range lexicalHan.FindAllString(text, -1) {
		units := utf16.Encode([]rune(run))
		if len(units) == 1 {
			out = append(out, run)
		}
		for i := 0; i+1 < len(units); i++ {
			pair := units[i : i+2]
			if utf16.IsSurrogate(rune(pair[0])) != utf16.IsSurrogate(rune(pair[1])) || utf16.IsSurrogate(rune(pair[0])) && (pair[0] >= 0xdc00 || pair[1] < 0xdc00) {
				out = append(out, fmt.Sprintf("\x00%04x%04x", pair[0], pair[1]))
			} else {
				out = append(out, string(utf16.Decode(pair)))
			}
		}
	}
	return out
}

// bm25Top is the author's LexicalIndex.search over texts with ids String(i)
// and one query of weight 1: BM25 (k1 1.2, b 0.75) with
// idf ln(1+(N-df+0.5)/(df+0.5)), positive scores only, the best limit by
// score, then id as a string. The arithmetic rounds as JavaScript's does.
func bm25Top(texts []string, query string, limit int) []int {
	if len(texts) == 0 {
		return []int{}
	}
	return newBM25(texts).top(query, limit)
}

// bm25Index is bm25Top's index of texts, built once for many queries.
type bm25Index struct {
	docs    []bm25Doc
	df      map[string]int
	average float64
}

type bm25Doc struct {
	tf     map[string]int
	length int
}

// newBM25 indexes texts, at least one.
func newBM25(texts []string) *bm25Index {
	x, total := &bm25Index{docs: make([]bm25Doc, len(texts)), df: map[string]int{}}, 0
	for i, text := range texts {
		tokens := lexicalTokens(text)
		x.docs[i] = bm25Doc{tf: map[string]int{}, length: len(tokens)}
		for _, token := range tokens {
			if x.docs[i].tf[token] == 0 {
				x.df[token]++
			}
			x.docs[i].tf[token]++
		}
		total += len(tokens)
	}
	x.average = float64(total) / float64(len(x.docs))
	return x
}

// top is bm25Top's ranking of the indexed texts for query.
func (x *bm25Index) top(query string, limit int) []int {
	docs, df, average := x.docs, x.df, x.average
	terms, seen := []string{}, map[string]bool{}
	for _, token := range lexicalTokens(query) {
		if !seen[token] {
			terms, seen[token] = append(terms, token), true
		}
	}
	n := float64(len(docs))
	type hit struct {
		i     int
		id    string
		score float64
	}
	hits := []hit{}
	for i, d := range docs {
		score := 0.0
		for _, token := range terms {
			tf := float64(d.tf[token])
			if tf == 0 {
				continue
			}
			count := float64(df[token])
			idf := records.JSLog(1 + (n-count+0.5)/(count+0.5))
			// The conversions keep Go from fusing operations JavaScript rounds.
			scaled := float64(1.2 * (0.25 + 0.75*float64(d.length)/average))
			score += float64(1*idf) * float64(tf*2.2) / (tf + scaled)
		}
		if score > 0 {
			hits = append(hits, hit{i, strconv.Itoa(i), score})
		}
	}
	slices.SortFunc(hits, func(a, b hit) int {
		if a.score != b.score {
			return cmp.Compare(b.score, a.score)
		}
		return strings.Compare(a.id, b.id)
	})
	out := []int{}
	for _, h := range hits[:min(limit, len(hits))] {
		out = append(out, h.i)
	}
	return out
}

// jsCompare orders strings as JavaScript's < does: by UTF-16 code units.
func jsCompare(a, b string) int {
	return slices.Compare(utf16.Encode([]rune(a)), utf16.Encode([]rune(b)))
}

func linesOrNone(lines []string) string {
	if joined := strings.Join(lines, "\n"); joined != "" {
		return joined
	}
	return "(none)"
}

// parseTimelineReply is the JSON object in a reply, fenced or not (C:118-122).
func parseTimelineReply(text string) (map[string]json.RawMessage, bool) {
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return nil, false
	}
	var value map[string]json.RawMessage
	if json.Unmarshal([]byte(text[start:end+1]), &value) != nil || value == nil {
		return nil, false
	}
	return value, true
}

var batchRef = regexp.MustCompile(`^r?([0-9]+)$`)

// applyReply adds one batch's items to m (C:128-157); it fails, changing
// nothing, when the reply holds no JSON object. Record references outside the
// batch are dropped. Events join a thread by id, else by title ignoring case,
// else start thread T<n+1>; facts append to their key's history; a directive
// that restates one already kept is skipped. Unlike the author, lists that are
// not arrays and entries that are not objects add nothing instead of throwing.
func applyReply(m *timelineMemory, batch []pendingRecord, reply string) error {
	return applyReplyWith(m, batch, reply, false)
}

// applyReplyWith is applyReply; with entities, the "entities" an event or a
// fact names are kept with it (layered extraction).
func applyReplyWith(m *timelineMemory, batch []pendingRecord, reply string, entities bool) error {
	value, ok := parseTimelineReply(reply)
	if !ok {
		return errors.New("consolidation reply holds no JSON object")
	}
	if m.Facts == nil {
		m.Facts = map[string][]factValue{}
	}
	refs := func(raw json.RawMessage) []string {
		out := []string{}
		for _, item := range jsonList(raw) {
			match := batchRef.FindStringSubmatch(strings.TrimFunc(jsString(item), isJSSpace))
			if match == nil {
				continue
			}
			if i, err := strconv.Atoi(match[1]); err == nil && i < len(batch) {
				out = append(out, batch[i].ID)
			}
		}
		return out
	}
	// names is an item's entity names: its strings, trimmed, each once;
	// nil when it names none, or when they are not asked for.
	names := func(raw json.RawMessage) []string {
		if !entities {
			return nil
		}
		var out []string
		for _, item := range jsonList(raw) {
			if name := jsonText(item); name != "" && !slices.Contains(out, name) {
				out = append(out, name)
			}
		}
		return out
	}
	for _, raw := range jsonList(value["events"]) {
		e := jsonObject(raw)
		said := jsonText(e["text"])
		if said == "" {
			continue
		}
		label := jsonText(e["thread"])
		at := slices.IndexFunc(m.Threads, func(t thread) bool { return t.ID == label })
		if at < 0 {
			title := label
			if len(title) >= 4 && strings.EqualFold(title[:4], "new:") {
				title = title[4:]
			}
			if title = strings.TrimFunc(title, isJSSpace); title == "" {
				title = "Other"
			}
			at = slices.IndexFunc(m.Threads, func(t thread) bool { return strings.ToLower(t.Title) == strings.ToLower(title) })
			if at < 0 {
				m.Threads = append(m.Threads, thread{ID: fmt.Sprintf("T%d", len(m.Threads)+1), Title: title})
				at = len(m.Threads) - 1
			}
		}
		m.Events = append(m.Events, event{Thread: m.Threads[at].ID, Date: jsonText(e["date"]), Text: said, Records: refs(e["records"]), Entities: names(e["entities"])})
	}
	for _, raw := range jsonList(value["facts"]) {
		f := jsonObject(raw)
		key, said := strings.ToLower(jsonText(f["key"])), jsonText(f["value"])
		if key == "" || said == "" {
			continue
		}
		var conflict, forget bool
		if json.Unmarshal(f["conflict"], &conflict) != nil {
			conflict = false
		}
		if json.Unmarshal(f["forget"], &forget) != nil {
			forget = false
		}
		if _, known := m.Facts[key]; !known {
			m.FactOrder = append(m.FactOrder, key)
		}
		m.Facts[key] = append(m.Facts[key], factValue{Value: said, Date: jsonText(f["date"]), Records: refs(f["records"]), Conflict: conflict, Entities: names(f["entities"]), Forget: forget})
	}
	for _, raw := range jsonList(value["directives"]) {
		d := jsonObject(raw)
		said := jsonText(d["text"])
		if said == "" || slices.ContainsFunc(m.Directives, func(old directive) bool { return restates(old.Text, said) }) {
			continue
		}
		m.Directives = append(m.Directives, directive{Text: said, Date: jsonText(d["date"]), Records: refs(d["records"])})
	}
	return nil
}

var wordPattern = regexp.MustCompile(`[\p{L}\p{N}]+`)

// restates reports whether most words of the shorter text appear in the
// other: the same directive in other words (C:125-126).
func restates(a, b string) bool {
	words := func(text string) map[string]bool {
		out := map[string]bool{}
		for _, word := range wordPattern.FindAllString(strings.ToLower(text), -1) {
			out[word] = true
		}
		return out
	}
	x, y := words(a), words(b)
	common := 0
	for word := range x {
		if y[word] {
			common++
		}
	}
	return float64(common)/float64(max(1, min(len(x), len(y)))) >= 0.8
}

// jsonList is raw as an array, or nil.
func jsonList(raw json.RawMessage) []json.RawMessage {
	var out []json.RawMessage
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return out
}

// jsonObject is raw as an object, or nil, whose fields then read as absent.
func jsonObject(raw json.RawMessage) map[string]json.RawMessage {
	var out map[string]json.RawMessage
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return out
}

// jsonText is the author's text(): a string trimmed, anything else "".
func jsonText(raw json.RawMessage) string {
	var out string
	if json.Unmarshal(raw, &out) != nil {
		return ""
	}
	return strings.TrimFunc(out, isJSSpace)
}

// jsString is JavaScript's String() of a JSON value, as far as a record
// reference can tell: numbers print as JavaScript prints them below 1e21.
func jsString(raw json.RawMessage) string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	var format func(any, bool) string
	format = func(value any, inArray bool) string {
		switch v := value.(type) {
		case string:
			return v
		case float64:
			if v == 0 {
				return "0"
			}
			if v > -1e21 && v < 1e21 && v == math.Trunc(v) {
				return strconv.FormatFloat(v, 'f', -1, 64)
			}
			return strconv.FormatFloat(v, 'g', -1, 64)
		case bool:
			return strconv.FormatBool(v)
		case nil:
			if inArray {
				return ""
			}
			return "null"
		case []any:
			parts := make([]string, len(v))
			for i, item := range v {
				parts[i] = format(item, true)
			}
			return strings.Join(parts, ",")
		default:
			return "[object Object]"
		}
	}
	return format(value, false)
}

// ---------------------------------------------------------------------- job

// consolidateTimelines is the author's background job (N:1071-1127): read the
// records written after the watermark, oldest first, fold full batches, and
// the short rest once a job finds nothing new and nothing left unread (unless
// req.Hold). The pending records are bounded (readNewRecords): records past
// the bound stay unread, and when the bound leaves no full batch the pending
// records fold as one, since more follow. The state is saved after every
// batch. A batch whose reply will not parse is asked twice, then each half
// twice; a half that still fails is dropped. A request too large to send
// splits until it fits; a single record still too large is dropped. A model
// error withdraws the batch's items, keeps its records pending, ends the job
// with the error and, unless it is the caller's cancellation or a spent
// budget, counts against the head batch: after dropErrors such jobs over
// dropAfter, the head batch is folded in drop mode (foldDropping). A
// layered job's folds share a foldJob (req.DeferLinks, and whether a call
// of the job was answered, for its timeouts).
func (a *Agent) consolidateTimelines(ctx context.Context, req ConsolidateRequest) (ConsolidateResult, error) {
	settings := a.config.Consolidation
	model := a.models.Consolidator
	if model == nil {
		model = a.models.Planner
	}
	if a.overlay == nil || model == nil {
		return ConsolidateResult{}, errors.New("timelines consolidation requires an overlay store and a completion model")
	}
	if a.store.ReadOnly() || a.overlay.readonly {
		return ConsolidateResult{}, errors.New("consolidation is unavailable with readonly storage")
	}
	layered := settings.Layered()
	if layered && a.models.Judge == nil {
		return ConsolidateResult{}, errors.New("layered consolidation requires a decision model")
	}
	if req.MaxBatches == 0 {
		req.MaxBatches = settings.MaxBatches
	}
	if req.MaxBatches < 1 || req.MaxBatches > 64 {
		return ConsolidateResult{}, errors.New("maxBatches must be 1–64")
	}
	unlock, err := a.lockTimelines(ctx, req.Scope)
	if err != nil {
		return ConsolidateResult{}, err
	}
	defer unlock()
	state, token, err := a.overlay.loadTimelines(ctx, req.Scope)
	if req.Reset {
		state, err = emptyTimelineState(), nil
	}
	if err != nil {
		return ConsolidateResult{}, err
	}
	state.Mode = ""
	if layered {
		state.Mode = settings.Mode
	}
	reading, tally := a.roleReading(), map[string]ReadUnits{}
	revision, read, unread, err := a.readNewRecords(ctx, req.Scope, &state, reading, settings.MaxBatches*settings.BatchCharacters, tally)
	if err != nil {
		return ConsolidateResult{}, err
	}
	result := ConsolidateResult{Revision: revision, Visited: read}
	if len(tally) > 0 {
		result.Reading = tally
	}
	before := memorySize(state.Memory)
	// When every record was read and nothing new found, the author's
	// caughtUp holds: fold the short rest too (N:1108). When the bound
	// stopped reading and left no full batch, the pending records are not
	// the conversation's tail: fold them as one batch, or no job goes on.
	flush := !req.Hold && read == 0 && unread == 0 ||
		unread > 0 && nextBatch(state.Pending, settings.BatchCharacters, false) == nil
	// A reset replaces the stored state even when no record is left.
	dirty := read > 0 || req.Reset
	var foldErr error
	if layered {
		ctx = withFoldJob(ctx, &foldJob{deferLinks: req.DeferLinks})
		if foldErr, err = a.foldLayered(ctx, model, req, &state, &token, flush, &dirty, &result); err != nil {
			return result, err
		}
	} else {
		for n := 0; n < req.MaxBatches; n++ {
			batch := nextBatch(state.Pending, settings.BatchCharacters, flush)
			if batch == nil {
				break
			}
			memory, batches, failed, drops := cloneMemory(state.Memory), state.Batches, state.Failed, len(result.Drops)
			dirty = true
			if n == 0 && a.dropDue(state) {
				foldErr = a.foldDropping(ctx, model, readFold(timelinesFold, reading), &state, batch, &result)
			} else {
				foldErr = a.foldTimelines(ctx, model, readFold(timelinesFold, reading), &state, batch, true, &result)
			}
			if foldErr != nil {
				state.Memory, state.Batches, state.Failed = memory, batches, failed
				result.keepDrops(drops)
				a.countError(ctx, &state, foldErr)
				break
			}
			state.Pending = state.Pending[len(batch):]
			state.ModelErrors, state.FirstModelError = 0, 0
			result.Folded++
			if token, err = a.overlay.saveTimelines(ctx, req.Scope, token, state); err != nil {
				return result, err
			}
			dirty = false
		}
	}
	if dirty {
		// After a model error, a canceled job still keeps what it read and
		// spent; the fold error is what it returns.
		saveCtx := ctx
		if foldErr != nil {
			saveCtx = context.WithoutCancel(ctx)
		}
		if _, err = a.overlay.saveTimelines(saveCtx, req.Scope, token, state); err != nil {
			return result, err
		}
	}
	result.Added = memorySize(state.Memory) - before
	result.Remaining = len(state.Pending) + unread
	return result, foldErr
}

// countedError reports whether a fold's error counts toward drop mode: a
// model error about this request (a refusal of its content, a malformed
// reply), not the caller's cancellation or deadline, a spent budget or an
// unavailable provider, which say nothing about the batch. A failed or
// timed-out exchange (ErrAttemptTimeout) is the provider's unavailability;
// foldJob.counts adds a layered job's timeouts that are the request's own.
func countedError(ctx context.Context, err error) bool {
	return ctx.Err() == nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, ErrBudgetCap) && !errors.Is(err, ErrModelUnavailable)
}

// countError counts a fold's error against the head batch when it is a
// counted model error (countedError), or, in a layered job, a timeout that
// counts (foldJob.counts).
func (a *Agent) countError(ctx context.Context, state *timelineState, err error) {
	if foldJobOf(ctx).counts(ctx, err) {
		a.countFailure(&state.ModelErrors, &state.FirstModelError)
	}
}

// countFailure counts one more failure in n, dating the first of them
// (first, Unix milliseconds).
func (a *Agent) countFailure(n *int, first *int64) {
	if *n == 0 {
		*first = a.now().UnixMilli()
	}
	(*n)++
}

// dropDue reports whether the head batch is folded in drop mode: dropErrors
// counted model errors, the first of them at least dropAfter ago.
func (a *Agent) dropDue(state timelineState) bool {
	return a.ruleDue(state.ModelErrors, state.FirstModelError)
}

// linkDue reports whether a layered head batch whose linking fails for the
// request itself links without JEV (its linking rule): dropErrors such
// failures counted, the first of them at least dropAfter ago.
func (a *Agent) linkDue(state timelineState) bool {
	return a.ruleDue(state.LinkErrors, state.FirstLinkError)
}

// ruleDue reports whether n counted failures, the first at first (Unix
// milliseconds), are due: dropErrors of them, the first at least dropAfter
// ago.
func (a *Agent) ruleDue(n int, first int64) bool {
	return n >= dropErrors && a.now().Sub(time.UnixMilli(first)) >= dropAfter
}

// foldDropping folds the head batch after repeated model errors: split once,
// each half folded on its own (foldTimelines without a further split). A half
// whose fold fails with a counted model error (in a layered job, also a
// timeout that counts: foldJob.counts) is dropped whole (Failed, and a Drop
// naming the error); a half that folds is kept. Whether a half's error
// counts is decided once both halves ran (the final review's parked T5-a),
// as foldLayered decides an extraction's once the job's extractions ended:
// a half that timed out before any call of the job was answered counts when
// the other half is answered after it. An error that can never count (the
// caller's cancellation, a spent budget, an outage) ends the job at once, as
// in foldTimelines, and so does one that does not count once both ran: the
// job then drops nothing.
func (a *Agent) foldDropping(ctx context.Context, model Completer, spec foldSpec, state *timelineState, batch []pendingRecord, result *ConsolidateResult) error {
	cause := fmt.Sprintf("%d failed jobs over %s", state.ModelErrors, a.now().Sub(time.UnixMilli(state.FirstModelError)).Round(time.Second))
	parts := [][]pendingRecord{batch}
	if len(batch) > 1 {
		half := (len(batch) + 1) / 2
		parts = [][]pendingRecord{batch[:half], batch[half:]}
	}
	job := foldJobOf(ctx)
	var failures []halfFailure
	for _, part := range parts {
		memory, batches, failed, drops := cloneMemory(state.Memory), state.Batches, state.Failed, len(result.Drops)
		err := a.foldTimelines(ctx, model, spec, state, part, false, result)
		if err == nil {
			continue
		}
		// The half contributes nothing, whether it is dropped or not.
		state.Memory, state.Batches, state.Failed = memory, batches, failed
		result.keepDrops(drops)
		if !job.mayCount(ctx, err) {
			return err
		}
		failures = append(failures, halfFailure{len(part), err})
	}
	for _, f := range failures {
		if !job.counts(ctx, f.err) {
			return f.err
		}
	}
	for _, f := range failures {
		state.Failed++
		result.drop(f.records, fmt.Sprintf("model error after %s: %v", cause, f.err))
	}
	return nil
}

// halfFailure is a drop-mode half that failed: its size, in records, and
// its error.
type halfFailure struct {
	records int
	err     error
}

// lockTimelines waits until no other job of this agent consolidates scope.
func (a *Agent) lockTimelines(ctx context.Context, scope records.Scope) (func(), error) {
	value, _ := a.timelineLocks.LoadOrStore(scopeKey(scope), make(chan struct{}, 1))
	lock := value.(chan struct{})
	select {
	case lock <- struct{}{}:
		return func() { <-lock }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// readNewRecords appends what the model reads of the records after the
// watermark, in rowid order, to the pending records and moves the watermark
// past them, until the pending text (headers and texts, UTF-16 units) would
// pass bound: the records from there on stay unread, below the watermark, for
// a job after the pending records drain. A record longer than bound is read
// when nothing is pending, so none can block. It returns the revision read,
// the number of records read and the number left unread.
func (a *Agent) readNewRecords(ctx context.Context, scope records.Scope, state *timelineState, reading RoleReading, bound int, tally map[string]ReadUnits) (string, int, int, error) {
	revision, heads, err := a.store.Heads(ctx, "", scope)
	if err != nil {
		return "", 0, 0, err
	}
	fresh := []records.Head{}
	for _, head := range heads {
		if head.Version > state.Watermark {
			fresh = append(fresh, head)
		}
	}
	slices.SortFunc(fresh, func(x, y records.Head) int { return cmp.Compare(x.Version, y.Version) })
	size := 0
	for _, record := range state.Pending {
		size += jsLen(record.Header) + jsLen(record.Text)
	}
	read := 0
	for start := 0; start < len(fresh); start += 500 {
		part := fresh[start:min(start+500, len(fresh))]
		ids := make([]string, len(part))
		for i, head := range part {
			ids[i] = head.ID
		}
		snapshot, err := a.store.Get(ctx, records.GetRequest{Revision: revision, IDs: ids})
		if err != nil {
			return "", 0, 0, err
		}
		values := map[string]recordValue{}
		for _, raw := range snapshot.Records {
			var value recordValue
			if json.Unmarshal([]byte(raw), &value) == nil {
				values[value.ID] = value
			}
		}
		for _, head := range part {
			if value, ok := values[head.ID]; ok {
				if header, body, ok := readable(reading, value.Title, value.Content, value.Data, tally); ok {
					units := jsLen(header) + jsLen(body)
					if len(state.Pending) > 0 && size+units > bound {
						return revision, read, len(fresh) - read, nil
					}
					state.Pending = append(state.Pending, pendingRecord{ID: head.ID, Header: header, Text: body})
					size += units
				}
			}
			state.Watermark = head.Version
			read++
		}
	}
	return revision, read, 0, nil
}

// foldSpec is what a fold sends the consolidating model: its system prompt
// and the user message for a batch, or a part of one, given the memory so
// far; entities keeps the entity names the replies give (applyReplyWith).
type foldSpec struct {
	system   string
	prompt   func(m timelineMemory, batch []pendingRecord) string
	entities bool
}

// timelinesFold is the author's fold: the index so far, then the batch.
var timelinesFold = foldSpec{system: timelineSystem, prompt: timelinePrompt}

// foldTimelines folds one batch (N:1095-1105): two attempts, then, when split
// is allowed, each half with two attempts of its own; a part that still does
// not parse is dropped. A request the provider refuses as too large before
// sending it splits in halves at once, each folded like the batch, down to
// single records; a single record still too large is dropped. So a long
// batch never stops every later job. spec says what each call sends.
func (a *Agent) foldTimelines(ctx context.Context, model Completer, spec foldSpec, state *timelineState, batch []pendingRecord, split bool, result *ConsolidateResult) error {
	for attempt := 0; attempt < 2; attempt++ {
		reply, err := a.completeFold(ctx, model, spec.system, spec.prompt(state.Memory, batch), state, result)
		if errors.Is(err, ErrRequestTooLarge) {
			if len(batch) == 1 {
				state.Failed++
				result.drop(1, "the fold request is too large to send")
				return nil
			}
			half := (len(batch) + 1) / 2
			if err := a.foldTimelines(ctx, model, spec, state, batch[:half], split, result); err != nil {
				return err
			}
			return a.foldTimelines(ctx, model, spec, state, batch[half:], split, result)
		}
		if err != nil {
			return err
		}
		if applyReplyWith(&state.Memory, batch, reply, spec.entities) == nil {
			state.Batches++
			return nil
		}
	}
	// A reply that will not parse is usually cut off at the token limit.
	if split && len(batch) > 1 {
		half := (len(batch) + 1) / 2
		if err := a.foldTimelines(ctx, model, spec, state, batch[:half], false, result); err != nil {
			return err
		}
		return a.foldTimelines(ctx, model, spec, state, batch[half:], false, result)
	}
	state.Failed++
	result.drop(len(batch), "the model's reply did not parse twice")
	return nil
}

// completeFold makes one consolidating model call and meters it. An
// answered call is the job's (foldJob.answer).
func (a *Agent) completeFold(ctx context.Context, model Completer, system, prompt string, state *timelineState, result *ConsolidateResult) (string, error) {
	start := time.Now()
	var reply string
	var usage Usage
	var err error
	if limited, ok := model.(CompleterWithMax); ok {
		reply, usage, err = limited.CompleteWith(ctx, system, prompt, timelineMaxTokens)
	} else {
		reply, usage, err = model.Complete(ctx, system, prompt)
	}
	result.ModelMS += float64(time.Since(start).Microseconds()) / 1000
	result.Usage.add(usage)
	state.Usage.add(usage)
	if err != nil {
		return "", err
	}
	state.Calls++
	foldJobOf(ctx).answer()
	return reply, nil
}

func cloneMemory(m timelineMemory) timelineMemory {
	out := timelineMemory{Threads: slices.Clone(m.Threads), Events: slices.Clone(m.Events), Facts: make(map[string][]factValue, len(m.Facts)),
		FactOrder: slices.Clone(m.FactOrder), Directives: slices.Clone(m.Directives)}
	for key, values := range m.Facts {
		out.Facts[key] = slices.Clone(values)
	}
	return out
}

// memorySize counts the items of m: events, fact values and directives.
func memorySize(m timelineMemory) int {
	n := len(m.Events) + len(m.Directives)
	for _, values := range m.Facts {
		n += len(values)
	}
	return n
}
