package memoryagent

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mnemon-dev/mnemon/internal/retrieval"
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
	// intent is the question's intent every search carries (view intent
	// "question"); "" lets each search detect its own.
	intent string
	// targeted is set when view.expand's target asked the expansions the
	// need table's question: compose then orders by it (ordered).
	targeted bool
	mu       sync.Mutex // guards stats during concurrent model calls
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
	// Expanded marks a judged record view.expand proposed (not pooled).
	Expanded bool
}

func (a *Agent) readSimple(ctx context.Context, req ReadRequest, revision string) (View, error) {
	if req.MaxItems == 0 {
		req.MaxItems = a.config.View.MaxItems
	}
	r := &simpleRun{agent: a, req: req, revision: revision, items: map[string]*simpleItem{}}
	if a.config.View.Intent == viewIntentQuestion {
		r.intent = string(retrieval.DetectIntent(intentText(req.Question)))
		r.stats.Intent = r.intent
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
	pages, err := r.runCues(ctx, cues)
	if err != nil {
		return View{}, err
	}
	pool, err := r.admit(ctx, mixCues(pages, simplePoolSize))
	if err != nil {
		return View{}, err
	}
	// JEV screens the bounded pool (N:928), then the loop reads on for the
	// needs.
	if err = r.screen(ctx, pool); err != nil {
		return View{}, err
	}
	stop, err := r.loop(ctx)
	if err != nil {
		return View{}, err
	}
	// JEV judges the shortlist (N:1002-1009). From here a judgement that
	// fails or finds the budget spent degrades the View to the screen; only
	// a canceled read fails.
	scored := shortlist(r.tableItems(), r.viewable(r.screened()), r.needs)
	if err = r.judgeAll(ctx, scored); err != nil {
		if ctx.Err() != nil {
			return View{}, ctx.Err()
		}
		r.degrade("judge")
	}
	// With view.expand, the needed items' neighbors are judged the same way
	// and the needed ones join the list (simple_expand.go).
	if a.config.View.Expand != nil {
		if scored, err = r.expandEvidence(ctx, scored, stop); err != nil {
			return View{}, err
		}
	}
	view, err := r.compose(ctx, stop, scored)
	if err != nil || !a.layerOn() {
		return view, err
	}
	// In timelines mode the consolidated layer goes before the records
	// (timelines_layer.go).
	return r.layer(ctx, view)
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
	stats.ExpandKinds = maps.Clone(r.stats.ExpandKinds)
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

// intentText is the question without leading "Current date: …" lines: a
// date header gives the reader context, not the question's intent, and its
// word "date" would make every question look like a WHEN question.
func intentText(question string) string {
	lines := strings.Split(question, "\n")
	for len(lines) > 1 && strings.HasPrefix(strings.ToLower(strings.TrimSpace(lines[0])), "current date:") {
		lines = lines[1:]
	}
	return strings.Join(lines, "\n")
}
