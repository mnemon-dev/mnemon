package memoryagent

import (
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
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

// foldJob is what the folds of one layered job share, which its context
// carries (withFoldJob): whether the job defers the links JEV cannot make
// for now (ConsolidateRequest.DeferLinks), and whether any extraction or
// linking call of the job was answered, which a timed-out extraction needs
// to count against its batch (counts).
type foldJob struct {
	deferLinks bool
	answered   atomic.Bool
}

type foldJobKey struct{}

// withFoldJob returns ctx carrying job.
func withFoldJob(ctx context.Context, job *foldJob) context.Context {
	return context.WithValue(ctx, foldJobKey{}, job)
}

// foldJobOf is the layered job whose context ctx is, or nil outside one:
// its methods take nil for a job that defers nothing and in which no call
// was answered.
func foldJobOf(ctx context.Context) *foldJob {
	job, _ := ctx.Value(foldJobKey{}).(*foldJob)
	return job
}

// defersLinks reports whether the job defers the links JEV cannot make.
func (j *foldJob) defersLinks() bool { return j != nil && j.deferLinks }

// answer records that a model call of the job was answered.
func (j *foldJob) answer() {
	if j != nil {
		j.answered.Store(true)
	}
}

// counts reports whether a fold's error counts against its batch for the
// drop rule: a counted model error (countedError), or, in a layered job, a
// call that timed out (ErrAttemptTimeout) while another call of the job was
// answered, so that the provider answers, only not this request. A timeout
// in a job none of whose calls was answered is an outage, as is any other
// unavailability, and counts nowhere. Decide it once the job's other calls
// that run alongside have ended (foldLayered, foldDropping).
func (j *foldJob) counts(ctx context.Context, err error) bool {
	return countedError(ctx, err) || j.timedOut(ctx, err) && j.answered.Load()
}

// mayCount reports whether a fold's error could count against its batch
// once the job's other calls have ended (counts): a counted model error, or
// in a layered job a timeout, which counts if another call is answered.
// Anything else never counts, however they end.
func (j *foldJob) mayCount(ctx context.Context, err error) bool {
	return countedError(ctx, err) || j.timedOut(ctx, err)
}

// timedOut reports whether, in a layered job, err is a call's timeout, not
// the job's cancellation or deadline.
func (j *foldJob) timedOut(ctx context.Context, err error) bool {
	return j != nil && errors.Is(err, ErrAttemptTimeout) && ctx.Err() == nil &&
		!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
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
// head batch as in timelines (foldJob.counts, once the job's extractions
// ended). A batch whose linking stops the job (linker.link: with
// DeferLinks) stays pending with those after it (linkStopped); the job
// keeps their extractions, waiting for those still running, for the job
// that links them (keepExtractions). The job's cancellation during linking
// leaves that batch pending; one that fails a batch's save leaves it
// linked, for the final save. foldErr is the error the job ends with after
// saving; err another save error, returned at once.
func (a *Agent) foldLayered(ctx context.Context, model Completer, req ConsolidateRequest, state *timelineState, token *string, flush bool, dirty *bool, result *ConsolidateResult) (foldErr, err error) {
	settings := a.config.Consolidation
	batches := layeredBatches(state.Pending, settings.BatchCharacters, req.MaxBatches, flush)
	if len(batches) == 0 {
		return nil, nil
	}
	*dirty = true
	run, keys := a.extractBatches(ctx, model, *state, batches, settings.Parallel)
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
	linked := 0
	var extractErr error
	for i, batch := range batches {
		x := run.wait(i)
		meter(x)
		if x.err != nil {
			foldErr, extractErr = x.err, x.err
			break
		}
		memory, counts, err := link.link(ctx, state, x.state.Memory, result)
		if err != nil {
			var counted bool
			if foldErr, counted = linkStopped(ctx, err, result); counted {
				a.countFailure(&state.LinkErrors, &state.FirstLinkError)
			}
			break
		}
		state.applyLinked(memory, x, counts, len(batch), result)
		linked++
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
	// A job that defers links keeps the extractions of the batches it
	// leaves pending, unless an extraction stopped it (keys is nil without
	// DeferLinks): those still running end first.
	keep := keys != nil && foldErr != nil && extractErr == nil
	if keep {
		run.drain()
	}
	// The extractions after the batches linked are discarded, or kept;
	// what they spent is counted here.
	if value := run.stop(); value != nil {
		panic(value)
	}
	for i := metered; i < len(batches); i++ {
		meter(&run.out[i])
	}
	if extractErr != nil {
		// Counted once every extraction of the job ended, so that its
		// calls answered meanwhile are known (foldJob.counts).
		a.countError(ctx, state, extractErr)
	}
	if keep {
		a.keepExtractions(run, keys, linked)
	}
	if foldErr != nil {
		*dirty = true
	}
	return foldErr, nil
}

// layeredBatches is a layered job's batches: up to max consecutive batches
// of the pending records (nextBatch), with flush the short rest too.
func layeredBatches(pending []pendingRecord, chars, max int, flush bool) [][]pendingRecord {
	batches := [][]pendingRecord{}
	for len(batches) < max {
		batch := nextBatch(pending, chars, flush)
		if batch == nil {
			break
		}
		batches, pending = append(batches, batch), pending[len(batch):]
	}
	return batches
}

// applyLinked takes the head batch, of n records, off the pending records,
// its extraction x linked into memory (counts): the memory, its folds and
// failures and the job's drops and links are the state's and the result's,
// and the head batch's model and linking errors are reset.
func (s *timelineState) applyLinked(memory timelineMemory, x *extraction, counts linkCounts, n int, result *ConsolidateResult) {
	s.Memory = memory
	s.Batches += x.state.Batches
	s.Failed += x.state.Failed
	for _, d := range x.result.Drops {
		result.drop(d.Records, d.Reason)
	}
	result.addLinks(counts)
	s.Pending = s.Pending[n:]
	s.ModelErrors, s.FirstModelError = 0, 0
	s.LinkErrors, s.FirstLinkError = 0, 0
	result.Folded++
}

// linkStopped is the error a job ends with when linking a batch stopped it
// (linker.link: the job's cancellation, or a failure keepsPending defers),
// and whether that error counts toward the batch's linking rule (linkDue).
// The cancellation stays as it is. An outage (an unavailable provider or a
// spent budget) defers the batch with ErrLinkDeferred around its cause,
// counted nowhere; the linking request's own failure (countedError) defers
// it with the error, counted. Either deferral counts in Deferred.
func linkStopped(ctx context.Context, err error, result *ConsolidateResult) (error, bool) {
	if ctx.Err() != nil {
		return err, false
	}
	result.Deferred++
	if outageError(err) {
		return fmt.Errorf("%w: %w", ErrLinkDeferred, err), false
	}
	return fmt.Errorf("linking failed: %w", err), countedError(ctx, err)
}

// outageError reports whether err says the provider cannot answer for now
// (ErrModelUnavailable) or its budget is spent (ErrBudgetCap), whatever
// the request.
func outageError(err error) bool {
	return errors.Is(err, ErrModelUnavailable) || errors.Is(err, ErrBudgetCap)
}

// keepExtractions keeps, by their keys, the extractions of the batches
// from linked on that ended without an error, for the job that links them
// (extractionCache), their spending cleared: this job counted it. The last
// batches go in first, so that the nearest the head are the last to go.
// The run's workers have stopped.
func (a *Agent) keepExtractions(run *extractionRun, keys []string, linked int) {
	for i := len(keys) - 1; i >= linked; i-- {
		if run.extracted(i) {
			a.extractions.put(keys[i], run.out[i].unmetered())
		}
	}
}

// extraction is one batch's L0: its batch-local memory and counts (state),
// its metering and drops (result), and the model error it ended in.
type extraction struct {
	state  timelineState
	result ConsolidateResult
	err    error
}

// unmetered is x without what its calls spent (the fields foldLayered's
// meter counts), as an extraction kept for a later job is.
func (x extraction) unmetered() extraction {
	x.state.Calls, x.state.Usage = 0, Usage{}
	x.result.Usage, x.result.ModelMS = Usage{}, 0
	return x
}

// extractionRun is the L0 of a job's batches, run by a pool of workers.
// Workers take batches in order while none before has failed and the run
// is not drained; a failed batch cancels the extractions after it.
type extractionRun struct {
	out      []extraction
	done     []chan struct{}
	cancels  []context.CancelFunc
	mu       sync.Mutex
	next     int
	failed   int  // the first failed batch, len(out) for none
	halted   bool // no batch is taken any more (drain)
	panicked any
	work     sync.WaitGroup
	once     sync.Once
}

var errExtractionPanic = errors.New("extraction panicked")

// extractBatches starts the L0 of batches, at most parallel at a time. The
// head batch is extracted in drop mode when head's errors make it due. In a
// job that defers links, keys are the digests the batches' extractions are
// kept by (extractionKey), and a batch whose extraction the agent keeps
// takes it, asking nothing; keys is nil otherwise.
func (a *Agent) extractBatches(ctx context.Context, model Completer, head timelineState, batches [][]pendingRecord, parallel int) (run *extractionRun, keys []string) {
	dropping, spec := a.dropDue(head), a.extractionFold()
	if foldJobOf(ctx).defersLinks() {
		keys = make([]string, len(batches))
		for i, batch := range batches {
			keys[i] = extractionKey(spec.system, i == 0 && dropping, batch)
		}
	}
	return startExtractions(ctx, len(batches), parallel, func(ctx context.Context, i int, x *extraction) error {
		if keys != nil {
			if kept, ok := a.extractions.take(keys[i]); ok {
				*x = kept
				return nil
			}
		}
		x.state = timelineState{Memory: emptyTimelineState().Memory, ModelErrors: head.ModelErrors, FirstModelError: head.FirstModelError}
		if i == 0 && dropping {
			return a.foldDropping(ctx, model, spec, &x.state, batches[i], &x.result)
		}
		return a.foldTimelines(ctx, model, spec, &x.state, batches[i], true, &x.result)
	}), keys
}

// extractionFold is the L0 fold, its prompt saying what the agent's role
// reading cuts (readFold).
func (a *Agent) extractionFold() foldSpec {
	return readFold(layeredFold, a.roleReading())
}

// startExtractions starts n extractions, extract(ctx, i, out[i]) for batch
// i, at most parallel at a time and in order.
func startExtractions(ctx context.Context, n, parallel int, extract func(ctx context.Context, i int, x *extraction) error) *extractionRun {
	run := &extractionRun{out: make([]extraction, n), done: make([]chan struct{}, n), cancels: make([]context.CancelFunc, n), failed: n}
	contexts := make([]context.Context, n)
	for i := range n {
		run.done[i] = make(chan struct{})
		contexts[i], run.cancels[i] = context.WithCancel(ctx)
	}
	for range min(parallel, n) {
		run.work.Add(1)
		go func() {
			defer run.work.Done()
			for {
				run.mu.Lock()
				i := run.next
				if i >= run.failed || run.halted {
					run.mu.Unlock()
					return
				}
				run.next++
				run.mu.Unlock()
				x := &run.out[i]
				x.err = run.guard(func() error { return extract(contexts[i], i, x) })
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

// drain stops the workers from taking more batches (halt) and waits for
// the extractions running to end, which it keeps (stop would cancel them).
func (r *extractionRun) drain() {
	r.halt()
	r.work.Wait()
}

// halt stops the workers from taking more batches; those extracting go on.
func (r *extractionRun) halt() {
	r.mu.Lock()
	r.halted = true
	r.mu.Unlock()
}

// extracted reports whether batch i was extracted without an error. The
// workers have stopped.
func (r *extractionRun) extracted(i int) bool {
	select {
	case <-r.done[i]:
		return r.out[i].err == nil
	default:
		return false
	}
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

// extractionCacheSize bounds the L0 extractions an agent keeps
// (extractionCache): as many batches as the largest job window holds
// (maxBatches is at most 64, 16 by default).
const extractionCacheSize = 64

// extractionCache keeps the L0 extractions of batches that jobs with
// DeferLinks extracted but left pending, by extractionKey, so that the job
// that links them later takes them instead of asking the model again: a
// retry during an outage pays for each extraction once. It keeps at most
// extractionCacheSize, the least recently stored going first, and taking
// one removes it. It lives in memory only: another process extracts again.
type extractionCache struct {
	mu      sync.Mutex
	order   *list.List // of *keptExtraction, the most recently stored first
	entries map[string]*list.Element
}

// keptExtraction is an extraction an extractionCache keeps, by key.
type keptExtraction struct {
	key string
	x   extraction
}

// take returns and removes the extraction kept by key, if any.
func (c *extractionCache) take(key string) (extraction, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return extraction{}, false
	}
	c.order.Remove(e)
	delete(c.entries, key)
	return e.Value.(*keptExtraction).x, true
}

// put keeps x by key, in place of what key kept, as the most recently
// stored, and lets the least recently stored go past the bound.
func (c *extractionCache) put(key string, x extraction) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.order, c.entries = list.New(), map[string]*list.Element{}
	}
	if e, ok := c.entries[key]; ok {
		c.order.Remove(e)
	}
	c.entries[key] = c.order.PushFront(&keptExtraction{key: key, x: x})
	for c.order.Len() > extractionCacheSize {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.entries, oldest.Value.(*keptExtraction).key)
	}
}

// extractionKey is the digest a batch's L0 extraction is kept by: all it
// depends on, the batch's records (ids, which its items cite, headers and
// texts, in order) under the fold's system prompt, and whether it runs in
// drop mode (foldDropping). An agent has one consolidating model.
func extractionKey(system string, dropping bool, batch []pendingRecord) string {
	raw, _ := json.Marshal(struct {
		System   string          `json:"system"`
		Dropping bool            `json:"dropping"`
		Records  []pendingRecord `json:"records"`
	}{system, dropping, batch})
	return reference("extraction", string(raw)).Hash
}
