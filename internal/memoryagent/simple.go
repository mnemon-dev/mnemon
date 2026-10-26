package memoryagent

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"sync"
	"time"
)

// The simple strategy ports the reference system's ("author") final read
// path, the cue recall of plugins/dsh-mnemon-strategy-jev-optmem/src/natural.ts
// ("N") in simple mode: the planner writes searches and names what the reply
// needs (simple_plan.go), each search reads pages of 20 records, and their
// first pages mix into a pool of 48 candidates (simple_pool.go) that JEV then
// screens, tables against the needs and judges.

// simpleRun is the state of one simple read; every mutable value of the read
// lives here.
type simpleRun struct {
	agent    *Agent
	req      ReadRequest // MaxItems is the effective View size
	revision string
	dialogue []Message
	needs    []Need
	needless bool // the plan named no need: the one need is the message
	items    map[string]*simpleItem
	order    []string // item ids in the order they were first pooled
	searches []*cueSearch
	tried    []string // every search text run, for rewrites
	// rows is the need table: the ids of tabled items (Row set), in the
	// order they entered it.
	rows   []string
	rounds int // loop rounds that acted
	// needStates is the loop's state of each need (open, met, done or given
	// up); nil when the loop ended before it began.
	needStates []string
	stats      ReadStats
	// pageReads counts search pages read, first pages included, against
	// simplePageBudget.
	pageReads int
	// trace records the read for the inspect service (trace.go); nil, the
	// read records nothing.
	trace *ReadTrace
	// layerNeeded reports that JEV called a block of the consolidated layer
	// needed.
	layerNeeded bool
	mu          sync.Mutex // guards stats during concurrent model calls
}

// simpleItem is one pooled record and what the models said about it.
type simpleItem struct {
	ID        string
	Evidence  Evidence
	Screen    float64 // JEV's "needed?" screen
	HasScreen bool
	Row       []float64 // per need: does this item give it? nil until tabled
	Needed    float64
	// Superseded is JEV's "no longer current" answer.
	Superseded float64
	Judged     bool
	FirstSeen  int // position in pool order
}

func (a *Agent) readSimple(ctx context.Context, req ReadRequest, revision string, lexicalBehind bool) (View, error) {
	if req.MaxItems == 0 {
		req.MaxItems = a.config.View.MaxItems
	}
	r := &simpleRun{agent: a, req: req, revision: revision, items: map[string]*simpleItem{}, trace: readTraceFrom(ctx)}
	if lexicalBehind {
		r.degrade("lexical-index")
	}
	if r.trace != nil {
		r.traceStart()
		defer r.traceEnd()
	}
	// The author's prompts read at most the last six messages (N:348).
	for _, message := range req.Recent[max(0, len(req.Recent)-5):] {
		if message.Role != "user" && message.Role != "assistant" {
			return View{}, errors.New("recent dialogue roles must be user or assistant")
		}
		r.dialogue = append(r.dialogue, message)
	}
	r.dialogue = append(r.dialogue, Message{Role: "user", Text: req.Question})
	cues, err := r.plan(ctx)
	if err != nil {
		return View{}, err
	}
	if r.trace != nil {
		r.tracePlan(cues)
	}
	r.stage(traceCandidates)
	pages, err := r.runCues(ctx, cues)
	if err != nil {
		return View{}, err
	}
	pool, err := r.admit(ctx, mixCues(pages, simplePoolSize))
	if err != nil {
		return View{}, err
	}
	if r.trace != nil {
		r.traceSearches()
		r.tracePool(pool)
	}
	// JEV screens the bounded pool (N:928), then the loop reads on for the
	// needs.
	r.stage(traceJudge)
	if err = r.screen(ctx, pool); err != nil {
		return View{}, err
	}
	r.stage(traceNeed)
	stop, err := r.loop(ctx)
	if err != nil {
		return View{}, err
	}
	if r.trace != nil {
		r.traceStop(stop)
	}
	// JEV judges the shortlist (N:1002-1009). From here a judgement that
	// fails or finds the budget spent degrades the View to the screen; only
	// a canceled read fails.
	r.stage(traceJudge)
	scored := shortlist(r.tableItems(), r.viewable(r.screened()), r.needs)
	if err = r.judgeAll(ctx, scored); err != nil {
		if ctx.Err() != nil {
			return View{}, ctx.Err()
		}
		r.degrade("judge")
	}
	r.stage(traceCompose)
	view, err := r.compose(ctx, stop, scored)
	if err == nil && a.layerOn() {
		// In timelines mode the consolidated layer goes before the records
		// (timelines_layer.go).
		view, err = r.layer(ctx, view)
	}
	if err != nil {
		return View{}, err
	}
	view.NoEvidence = r.noEvidence(scored)
	if view.NoEvidence && a.config.Safety != nil && a.config.Safety.Abstain {
		view.Records, view.Index = []Evidence{}, nil
		view.Needs = r.viewNeeds(view.Records)
		return view, nil
	}
	// With view.order "time" the composed View is listed in time order.
	return r.orderView(ctx, view)
}

// noEvidence reports whether the read, run whole, found nothing the reply
// needs: no stage degraded, every shortlisted record judged, none of them
// current and needed, and no layer block needed. A read that fell back
// somewhere has not found that nothing is needed.
func (r *simpleRun) noEvidence(scored []*simpleItem) bool {
	if len(r.statsNow().Degraded) > 0 || r.layerNeeded {
		return false
	}
	return !slices.ContainsFunc(scored, func(item *simpleItem) bool { return !item.Judged || current(item) && item.Needed >= 0.5 })
}

// screen gives the pool JEV's screen. A spent JEV budget is not an error:
// the loop then stops, and the read is degraded (scan-budget). (The pool
// scan cannot spend 128 calls today; this guards a shared budget.)
func (r *simpleRun) screen(ctx context.Context, pool []*simpleItem) error {
	err := r.scan(ctx, pool)
	if errors.Is(err, errCallBudget) {
		r.degrade("scan-budget")
		return nil
	}
	return err
}

// degrade names a stage of the read that fell back (ReadStats.Degraded),
// once.
func (r *simpleRun) degrade(stage string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !slices.Contains(r.stats.Degraded, stage) {
		r.stats.Degraded = append(r.stats.Degraded, stage)
	}
}

// statsNow is a copy of the read's stats for a View.
func (r *simpleRun) statsNow() ReadStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	stats := r.stats
	stats.Degraded = slices.Clone(r.stats.Degraded)
	stats.IndexRoutes = maps.Clone(r.stats.IndexRoutes)
	return stats
}

// complete runs one planner call with the author's output limit for it; a
// planner without per-call limits uses its own.
func (r *simpleRun) complete(ctx context.Context, system, prompt string, maxTokens int) (string, error) {
	start := time.Now()
	var text string
	var usage Usage
	var err error
	if planner, ok := r.agent.models.Planner.(CompleterWithMax); ok {
		text, usage, err = planner.CompleteWith(ctx, system, prompt, maxTokens)
	} else {
		text, usage, err = r.agent.models.Planner.Complete(ctx, system, prompt)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stats.PlannerCalls++
	r.stats.ModelMS += float64(time.Since(start).Microseconds()) / 1000
	r.stats.Usage.add(usage)
	return text, err
}

// policy names the simple strategy and its whole configuration.
func (r *simpleRun) policy() string {
	config, _ := json.Marshal(r.agent.config)
	return "memory-agent/simple/" + reference("policy", string(config)).Hash
}
