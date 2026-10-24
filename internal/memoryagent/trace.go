package memoryagent

import (
	"context"
	"slices"
	"sync"
	"time"
)

// The read trace records what a simple read does, stage by stage, for the
// AML inspect service (aml-inspect): the plan, the searches, JEV's answers,
// the need loop's rounds and the consolidated layer. A read finds its trace
// in its context (WithReadTrace). Without one, a read records nothing: each
// hook is behind a nil check, and the read runs exactly as it does without
// tracing (TestReadPathWithoutTracerIsUnchanged). With one, the hooks run on
// the read's goroutine, after the work they describe, and copy what they
// record: they make no call, no I/O and no change to the read.

// The stages of a trace, in the order a read first enters them; a read
// enters judge, candidates and compose again as it goes.
const (
	traceCues       = "cues"
	traceCandidates = "candidates"
	traceJudge      = "judge"
	traceNeed       = "need"
	traceCompose    = "compose"
)

// ReadTrace is a simple read's trace. Give an empty one to a read with
// WithReadTrace, and read its fields once Read has returned, whether it
// failed or not: a failed read leaves what it did until it failed.
type ReadTrace struct {
	mu sync.Mutex
	// Revision is the records revision the read pinned.
	Revision string
	// Stages are the time and the model calls of each stage, in the order
	// the read first entered them, each summed over every time it did.
	Stages []TraceStage
	// Cues and Needs are the plan; Needless says it named no need.
	Cues     []TraceCue
	Needs    []Need
	Needless bool
	// Searches are every search of the read in order: the cues' first, cue
	// i being search i, then the need loop's rewritten ones.
	Searches []TraceSearch
	// Pool is the records the cues' first pages pooled, in pool order.
	Pool []string
	// Rounds are the need loop's rounds that acted; NeedStates each need's
	// state when it stopped, and Stop why (covered, given up or rounds).
	Rounds     []TraceRound
	NeedStates []string
	Stop       string
	// Items are every pooled record as the read left it, in pool order.
	Items []TraceItem
	// Layer is the consolidated layer's candidates and JEV's answers;
	// Linked the records linked from its blocks that JEV judged.
	Layer  []TraceLayerItem
	Linked []TraceLinked

	open  string    // the stage being timed, "" for none
	since time.Time // when it was entered
	stats ReadStats // the read's stats then
}

// TraceStage is a stage's wall time and the model calls it made, with their
// use (the providers' metering).
type TraceStage struct {
	Name                        string
	Duration                    time.Duration
	PlannerCalls, DecisionCalls int
	Usage                       Usage
}

// TraceCue is one cue of the plan: message, note or search.
type TraceCue struct{ Text, Kind string }

// TraceSearch is a search of the read: its text and kind, how many records
// it ranked, and the first page it read.
type TraceSearch struct {
	Text, Kind string
	Ranked     int
	First      []string
}

// TraceRound is one round of the need loop: the step each need took, the
// next pages it read (Search is a search's index), the indexes of the
// searches it had the planner write, and the records it pooled.
type TraceRound struct {
	Round    int
	Steps    []string
	Pages    []TracePage
	Searches []int
	Fresh    []string
}

// TracePage is a page a round read from a search.
type TracePage struct {
	Search int
	IDs    []string
}

// TraceItem is a pooled record and JEV's answers about it: its screen (with
// HasScreen), its need table row (nil when untabled), and its judgement
// (Judged; when the judgement did not reach it, Needed is its screen and
// Superseded 0). Expanded marks a record view.expand proposed.
type TraceItem struct {
	ID, Text           string
	Screen             float64
	HasScreen          bool
	Row                []float64
	Needed, Superseded float64
	Judged, Expanded   bool
}

// TraceLayerItem is a candidate of the consolidated layer: a thread (Kind
// topic, ID its id) or a fact key (Kind value, ID the key), the text JEV
// read, how many of the View's records link to it (Lit), its rank among the
// candidates nearest the question (-1 when it is not among them), and JEV's
// answer whether the reply needs it. Events are a thread's events, by their
// Consolidation ids ("e<i>", the items the timelines block names); none for
// a key.
type TraceLayerItem struct {
	ID, Kind, Text string
	Lit, Near      int
	Needed         float64
	Events         []string
}

// TraceLinked is a record linked from the layer's blocks that JEV judged,
// its answer, and whether it joined the View.
type TraceLinked struct {
	ID, Text string
	Needed   float64
	Joined   bool
}

type readTraceKey struct{}

// WithReadTrace returns ctx whose simple reads record into t.
func WithReadTrace(ctx context.Context, t *ReadTrace) context.Context {
	return context.WithValue(ctx, readTraceKey{}, t)
}

// readTraceFrom is the trace ctx carries, nil for none.
func readTraceFrom(ctx context.Context) *ReadTrace {
	t, _ := ctx.Value(readTraceKey{}).(*ReadTrace)
	return t
}

// enter closes the stage being timed, giving it the time and the calls since
// it was entered, and opens stage: stats are the read's now.
func (t *ReadTrace) enter(stage string, stats ReadStats) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closeStage(stats)
	t.open, t.since, t.stats = stage, time.Now(), stats
}

// closeStage gives the stage being timed what the read did since it was
// entered. t.mu is held.
func (t *ReadTrace) closeStage(stats ReadStats) {
	if t.open == "" {
		return
	}
	i := slices.IndexFunc(t.Stages, func(s TraceStage) bool { return s.Name == t.open })
	if i < 0 {
		t.Stages, i = append(t.Stages, TraceStage{Name: t.open}), len(t.Stages)
	}
	s := &t.Stages[i]
	s.Duration += time.Since(t.since)
	s.PlannerCalls += stats.PlannerCalls - t.stats.PlannerCalls
	s.DecisionCalls += stats.DecisionCalls - t.stats.DecisionCalls
	s.Usage.add(Usage{
		InputTokens: stats.Usage.InputTokens - t.stats.Usage.InputTokens, OutputTokens: stats.Usage.OutputTokens - t.stats.Usage.OutputTokens,
		USD: stats.Usage.USD - t.stats.Usage.USD, CNY: stats.Usage.CNY - t.stats.Usage.CNY, Cached: stats.Usage.Cached - t.stats.Usage.Cached,
	})
	t.open = ""
}

// stage marks the read entering a stage of its trace; without a trace it
// does nothing.
func (r *simpleRun) stage(name string) {
	if r.trace != nil {
		r.trace.enter(name, r.statsNow())
	}
}

// traceStart opens the trace's first stage.
func (r *simpleRun) traceStart() {
	r.trace.mu.Lock()
	r.trace.Revision = r.revision
	r.trace.mu.Unlock()
	r.stage(traceCues)
}

// traceEnd closes the trace when the read returns, however it returns: the
// stage being timed, the needs' states and every pooled record.
func (r *simpleRun) traceEnd() {
	stats := r.statsNow()
	items := make([]TraceItem, 0, len(r.order))
	for _, id := range r.order {
		item := r.items[id]
		traced := TraceItem{ID: id, Text: item.Evidence.Text, Screen: item.Screen, HasScreen: item.HasScreen, Row: slices.Clone(item.Row),
			Needed: item.Needed, Superseded: item.Superseded, Judged: item.Judged, Expanded: item.Expanded}
		if !item.Judged {
			traced.Needed, traced.Superseded = item.Screen, 0
		}
		items = append(items, traced)
	}
	t := r.trace
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closeStage(stats)
	t.Items, t.NeedStates = items, slices.Clone(r.needStates)
}

// tracePlan records the plan.
func (r *simpleRun) tracePlan(cues []cue) {
	traced := []TraceCue{}
	for _, c := range cues {
		traced = append(traced, TraceCue{Text: c.Text, Kind: c.Kind})
	}
	t := r.trace
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Cues, t.Needs, t.Needless = traced, slices.Clone(r.needs), r.needless
}

// traceSearches records the searches run since the trace last did, each
// with the first page it read, and returns their indexes.
func (r *simpleRun) traceSearches() []int {
	t := r.trace
	t.mu.Lock()
	defer t.mu.Unlock()
	added := []int{}
	for i := len(t.Searches); i < len(r.searches); i++ {
		s := r.searches[i]
		t.Searches = append(t.Searches, TraceSearch{Text: s.Cue.Text, Kind: s.Cue.Kind, Ranked: len(s.IDs), First: slices.Clone(s.IDs[:s.Offset])})
		added = append(added, i)
	}
	return added
}

// tracePool records the first pool.
func (r *simpleRun) tracePool(pool []*simpleItem) {
	ids := []string{}
	for _, item := range pool {
		ids = append(ids, item.ID)
	}
	r.trace.mu.Lock()
	r.trace.Pool = ids
	r.trace.mu.Unlock()
}

// traceRound records one round of the need loop.
func (r *simpleRun) traceRound(round int, steps []string, pages []TracePage, fresh []string) {
	searches := r.traceSearches()
	t := r.trace
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Rounds = append(t.Rounds, TraceRound{Round: round, Steps: slices.Clone(steps), Pages: pages, Searches: searches, Fresh: slices.Clone(fresh)})
}

// traceStop records why the need loop stopped.
func (r *simpleRun) traceStop(stop string) {
	r.trace.mu.Lock()
	r.trace.Stop = stop
	r.trace.mu.Unlock()
}

// layerTrace is what the layer's candidates were made from: the memory, the
// View's records (anchors) and what they lit, the threads that have events
// (threadIDs), the candidates (topics, then values), the indexes nearest
// the question, the texts JEV read and its answers (topics first).
type layerTrace struct {
	memory                  timelineMemory
	anchors                 map[string]bool
	lit                     litSets
	threadIDs               []string
	topics, values          []string
	nearThreads, nearValues []int
	threadTexts, valueTexts map[string]string
	scores                  []float64
}

// traceLayer records the layer's candidates and JEV's answers.
func (r *simpleRun) traceLayer(in layerTrace) {
	near := func(indexes []int, names []string, id string) int {
		return slices.IndexFunc(indexes, func(i int) bool { return names[i] == id })
	}
	litValue := func(key string) int {
		n := 0
		for _, v := range in.memory.Facts[key] {
			for _, id := range v.Records {
				if in.anchors[id] {
					n++
				}
			}
		}
		return n
	}
	_, threadEvents := eventThreads(in.memory)
	items := []TraceLayerItem{}
	for i, id := range in.topics {
		events := []string{}
		for _, e := range threadEvents[id] {
			events = append(events, itemID("e", e))
		}
		items = append(items, TraceLayerItem{ID: id, Kind: "topic", Text: in.threadTexts[id], Lit: in.lit.Counts[id], Near: near(in.nearThreads, in.threadIDs, id), Needed: in.scores[i], Events: events})
	}
	for i, key := range in.values {
		items = append(items, TraceLayerItem{ID: key, Kind: "value", Text: in.valueTexts[key], Lit: litValue(key), Near: near(in.nearValues, in.memory.FactOrder, key), Needed: in.scores[len(in.topics)+i]})
	}
	r.trace.mu.Lock()
	r.trace.Layer = items
	r.trace.mu.Unlock()
}

// traceLinked records the linked records JEV judged, and which joined.
func (r *simpleRun) traceLinked(found []Evidence, scores []float64, kept []Evidence) {
	items := []TraceLinked{}
	for i, value := range found {
		joined := slices.ContainsFunc(kept, func(k Evidence) bool { return k.ID == value.ID })
		items = append(items, TraceLinked{ID: value.ID, Text: value.Text, Needed: scores[i], Joined: joined})
	}
	r.trace.mu.Lock()
	r.trace.Linked = items
	r.trace.mu.Unlock()
}
