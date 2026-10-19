package memoryagent

import (
	"context"
	"encoding/json"
	"errors"
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
	stats    ReadStats
	// pageReads counts search pages read, first pages included, against
	// simplePageBudget.
	pageReads int
	mu        sync.Mutex // guards stats during concurrent model calls
}

// simpleItem is one pooled record and what the models said about it.
type simpleItem struct {
	ID        string
	Evidence  Evidence
	Screen    float64 // JEV's "needed?" screen
	HasScreen bool
	Row       []float64 // per need: does this item give it?
	Needed    float64
	// Superseded is JEV's "no longer current" answer.
	Superseded float64
	Judged     bool
	FirstSeen  int // position in pool order
}

func (a *Agent) readSimple(ctx context.Context, req ReadRequest, revision string) (View, error) {
	if req.MaxItems == 0 {
		req.MaxItems = a.config.View.MaxItems
	}
	r := &simpleRun{agent: a, req: req, revision: revision, items: map[string]*simpleItem{}}
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
	if _, err = r.admit(ctx, mixCues(pages, simplePoolSize)); err != nil {
		return View{}, err
	}
	return r.poolView(), nil
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

// poolView returns the pooled records in pool order within the View bounds.
func (r *simpleRun) poolView() View {
	view := View{Revision: r.revision, Policy: r.policy(), Records: []Evidence{}, Needs: r.needs, StopReason: "pool"}
	used := 0
	for _, id := range r.order {
		item := r.items[id].Evidence
		size := jsLen(item.Text)
		if len(view.Records) == r.req.MaxItems || used+size > r.agent.config.View.MaxCharacters {
			view.Truncated = true
			continue
		}
		used += size
		view.Records = append(view.Records, item)
	}
	view.Stats = r.stats
	return view
}
