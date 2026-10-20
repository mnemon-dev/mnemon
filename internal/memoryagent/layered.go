package memoryagent

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
)

// Layered consolidation splits the author's fold in two
// (docs/superpowers/specs/2026-10-20-layered-consolidation-design.md). L0:
// a model reads each batch of records alone, with no index of the memory so
// far, and writes its events, facts and directives as timelines do, each
// naming the entities it is about. L1: JEV links each batch's items into
// the stored memory, in batch order, by yes/no decisions over a few
// retrieved candidates (layered_link.go). The state, its table and the read
// layer are those of timelines consolidation.

// layeredSystem is the author's consolidation prompt (timelineSystem) with
// the edits that remove its references to an index of the memory so far,
// and one sentence and JSON field that ask for entity names.
var layeredSystem = edited(timelineSystem,
	`Reuse an existing thread id when the event continues that sub-topic; otherwise start a new thread ("new: <short title>").`,
	`Name the thread with a short title ("new: <short title>") and use the same title for every event of that sub-topic in this batch.`,
	`When an existing key changes, repeat that exact key with the new value.`,
	`When a key's value changes within the batch, repeat that exact key with the new value.`,
	`contradicts an earlier one and`,
	`contradicts an earlier one in this batch and`,
	`Use only what the records say.`,
	`For each event and fact, list in "entities" the names of the people, organizations, places, products or documents it is about. Use only what the records say.`,
	`{"events":[{"thread":"T3","date":"YYYY/MM/DD","text":"...","records":["r12"]}],"facts":[{"key":"...","value":"...","date":"YYYY/MM/DD","records":["r15"],"conflict":false}],"directives":[{"text":"...","date":"YYYY/MM/DD","records":["r20"]}]}`,
	`{"events":[{"thread":"new: <short title>","date":"YYYY/MM/DD","text":"...","entities":["..."],"records":["r12"]}],"facts":[{"key":"...","value":"...","date":"YYYY/MM/DD","entities":["..."],"records":["r15"],"conflict":false}],"directives":[{"text":"...","date":"YYYY/MM/DD","records":["r20"]}]}`,
)

// layeredFold is L0: the batch alone, under layeredSystem, keeping the
// entity names of its items.
var layeredFold = foldSpec{system: layeredSystem, prompt: func(_ timelineMemory, batch []pendingRecord) string { return batchSection(batch) }, entities: true}

// edited replaces, in order, each old text of pairs (old, new, …) that s
// holds exactly once; it panics otherwise, so a changed prompt cannot lose
// an edit silently.
func edited(s string, pairs ...string) string {
	for i := 0; i+1 < len(pairs); i += 2 {
		if strings.Count(s, pairs[i]) != 1 {
			panic("prompt edit does not apply: " + pairs[i])
		}
		s = strings.Replace(s, pairs[i], pairs[i+1], 1)
	}
	return s
}

// foldLayered folds the job's batches in layered mode (for
// consolidateTimelines, which reads and saves the state as for timelines):
// up to req.MaxBatches consecutive batches, each extracted alone (L0,
// layeredFold, with the timelines retry, split, drop and too-large
// handling) with at most Parallel extractions in flight, then linked into
// the memory (L1a) strictly in batch order, as soon as a batch and those
// before it are extracted, and saved after each. Every extraction's calls
// and usage count. A batch whose extraction ends in a model error stops the
// job: the batches before it stay linked, it and those after it stay
// pending (their extractions discarded), and the error counts against the
// head batch as in timelines. The job's cancellation during linking leaves
// that batch pending; one that fails a batch's save leaves it linked, for
// the final save. foldErr is the error the job ends with after saving; err
// another save error, returned at once.
func (a *Agent) foldLayered(ctx context.Context, model Completer, req ConsolidateRequest, state *timelineState, token *string, flush bool, dirty *bool, result *ConsolidateResult) (foldErr, err error) {
	settings := a.config.Consolidation
	batches, rest := [][]pendingRecord{}, state.Pending
	for len(batches) < req.MaxBatches {
		batch := nextBatch(rest, settings.BatchCharacters, flush)
		if batch == nil {
			break
		}
		batches, rest = append(batches, batch), rest[len(batch):]
	}
	if len(batches) == 0 {
		return nil, nil
	}
	*dirty = true
	run := a.extractBatches(ctx, model, *state, batches, settings.Parallel)
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
			a.countError(ctx, state, foldErr)
			break
		}
		memory, counts, err := link.link(ctx, state, x.state.Memory, result)
		if err != nil {
			foldErr = err
			break
		}
		state.Memory = memory
		state.Batches += x.state.Batches
		state.Failed += x.state.Failed
		for _, d := range x.result.Drops {
			result.drop(d.Records, d.Reason)
		}
		result.addLinks(counts)
		state.Pending = state.Pending[len(batch):]
		state.ModelErrors, state.FirstModelError = 0, 0
		result.Folded++
		saved, err := a.overlay.saveTimelines(ctx, req.Scope, *token, *state)
		if err != nil {
			if ctx.Err() != nil {
				// The job was canceled: the save after the loop, which the
				// cancellation does not stop, keeps this batch and what the
				// job spent.
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

// extraction is one batch's L0: its batch-local memory and counts (state),
// its metering and drops (result), and the model error it ended in.
type extraction struct {
	state  timelineState
	result ConsolidateResult
	err    error
}

// extractionRun is the L0 of a job's batches, run by a pool of workers.
// Workers take batches in order while none before has failed; a failed
// batch cancels the extractions after it.
type extractionRun struct {
	out      []extraction
	done     []chan struct{}
	cancels  []context.CancelFunc
	mu       sync.Mutex
	next     int
	failed   int // the first failed batch, len(out) for none
	panicked any
	work     sync.WaitGroup
	once     sync.Once
}

var errExtractionPanic = errors.New("extraction panicked")

// extractBatches starts the L0 of batches, at most parallel at a time. The
// head batch is extracted in drop mode when head's errors make it due.
func (a *Agent) extractBatches(ctx context.Context, model Completer, head timelineState, batches [][]pendingRecord, parallel int) *extractionRun {
	run := &extractionRun{out: make([]extraction, len(batches)), done: make([]chan struct{}, len(batches)), cancels: make([]context.CancelFunc, len(batches)), failed: len(batches)}
	contexts := make([]context.Context, len(batches))
	for i := range batches {
		run.done[i] = make(chan struct{})
		contexts[i], run.cancels[i] = context.WithCancel(ctx)
	}
	dropping := a.dropDue(head)
	for range min(parallel, len(batches)) {
		run.work.Add(1)
		go func() {
			defer run.work.Done()
			for {
				run.mu.Lock()
				i := run.next
				if i >= run.failed {
					run.mu.Unlock()
					return
				}
				run.next++
				run.mu.Unlock()
				x := &run.out[i]
				x.state = timelineState{Memory: emptyTimelineState().Memory, ModelErrors: head.ModelErrors, FirstModelError: head.FirstModelError}
				x.err = run.guard(func() error {
					if i == 0 && dropping {
						return a.foldDropping(contexts[i], model, layeredFold, &x.state, batches[i], &x.result)
					}
					return a.foldTimelines(contexts[i], model, layeredFold, &x.state, batches[i], true, &x.result)
				})
				if x.err != nil {
					run.mu.Lock()
					if i < run.failed {
						run.failed = i
						for _, cancel := range run.cancels[i+1:] {
							cancel()
						}
					}
					run.mu.Unlock()
				}
				close(run.done[i])
			}
		}()
	}
	return run
}

// guard runs f; a panic in it is kept for stop to raise again in the job's
// goroutine, and fails the batch.
func (r *extractionRun) guard(f func() error) (err error) {
	defer func() {
		if value := recover(); value != nil {
			r.mu.Lock()
			if r.panicked == nil {
				r.panicked = fmt.Sprintf("%v\n%s", value, debug.Stack())
			}
			r.mu.Unlock()
			err = errExtractionPanic
		}
	}()
	return f()
}

// wait returns batch i's extraction once it is done; the batches before it
// must have been extracted without an error.
func (r *extractionRun) wait(i int) *extraction {
	<-r.done[i]
	return &r.out[i]
}

// stop cancels the extractions still running, waits for the workers and
// returns the first panic of one, if any.
func (r *extractionRun) stop() any {
	r.once.Do(func() {
		for _, cancel := range r.cancels {
			cancel()
		}
		r.work.Wait()
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.panicked
}
