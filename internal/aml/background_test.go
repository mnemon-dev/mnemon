package aml

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
	"github.com/mnemon-dev/mnemon/internal/records"
)

var (
	// parityRecord is a record of a fold's prompt: its batch-local id, its
	// header and its first line.
	parityRecord = regexp.MustCompile(`\[r(\d+)\] ([^\n]*)\n([^\n]*)`)
	// parityThread is a thread of a timelines fold's prompt, with its events.
	parityThread = regexp.MustCompile(`\((\d+) events, last `)
	// parityNote is a note's number (noteAdd).
	parityNote = regexp.MustCompile(`Note (\d+):`)
)

// parityReply folds a batch into one event and one fact that name the
// batch's size, the opening words of its first and last records and, in
// timelines mode, how many events the memory held before it: two runs fold
// the same memory only if they fold the same batches in the same order.
// Their date goes back and forth with the batch's first note (noteAdd), so
// that what reads items in date order sees them out of memory order.
func parityReply(_ context.Context, prompt string) (string, error) {
	shown := parityRecord.FindAllStringSubmatch(prompt, -1)
	if len(shown) == 0 {
		return `{"events":[],"facts":[],"directives":[]}`, nil
	}
	before := 0
	for _, match := range parityThread.FindAllStringSubmatch(prompt, -1) {
		n, _ := strconv.Atoi(match[1])
		before += n
	}
	refs := []string{}
	for _, match := range shown {
		refs = append(refs, "r"+match[1])
	}
	date := "2023/05/08"
	if match := parityNote.FindStringSubmatch(shown[0][3]); match != nil {
		n, _ := strconv.Atoi(match[1])
		date = fmt.Sprintf("2023/05/%02d", 10+n*7%9)
	}
	text := fmt.Sprintf("Caroline went to the LGBTQ support group (%d records after %d events: %s … %s).", len(refs), before, capBytes(shown[0][3], 40), capBytes(shown[len(shown)-1][3], 40))
	raw, _ := json.Marshal(map[string]any{
		"events":     []map[string]any{{"thread": "new: Support group", "date": date, "text": text, "entities": []string{"Caroline"}, "records": refs}},
		"facts":      []map[string]any{{"key": "support group notes", "value": fmt.Sprintf("%d more", len(refs)), "date": date, "entities": []string{"Caroline"}, "records": refs[:1], "conflict": false}},
		"directives": []any{},
	})
	return string(raw), nil
}

// noteAdd is the i-th Add of a scripted run: one record about the support
// group whose length varies with i, so that batches hold 2 to 4 records.
func noteAdd(i int, user string) AddRequest {
	content := fmt.Sprintf("Note %02d: Caroline went to the LGBTQ support group, part %d.", i, i) + strings.Repeat(" She talked with the others there.", i*7%5)
	return AddRequest{RequestID: fmt.Sprintf("n%02d", i), UserID: user, SessionID: fmt.Sprintf("s%d", i/3), Messages: []Message{{Role: "user", Content: content, Timestamp: at(i)}}}
}

// step is one request of a scripted run: an Add, or else a Search.
type step struct {
	add    *AddRequest
	search *SearchRequest
}

func addStep(req AddRequest) step { return step{add: &req} }

func searchStep(query, user string) step {
	return step{search: &SearchRequest{Query: query, UserID: user, TopK: 100}}
}

const otherQuestion = "What did Caroline talk about with the others?"

// replay sends steps through h, in order, and returns each response's status
// and body; with settle, a Search first waits until the scheduler is idle.
func replay(t *testing.T, h *harness, steps []step, settle bool) []string {
	t.Helper()
	out := []string{}
	for _, s := range steps {
		var code int
		var body string
		if s.add != nil {
			response := h.call("POST", "/add", *s.add)
			code, body = response.Code, response.Body.String()
		} else {
			if settle {
				h.settle(t)
			}
			response := h.call("POST", "/search", *s.search)
			code, body = response.Code, response.Body.String()
		}
		out = append(out, fmt.Sprintf("%d %s", code, body))
	}
	return out
}

// sameBodies fails at the first response that differs.
func sameBodies(t *testing.T, want, got []string) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatal(len(want), len(got))
	}
	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("response %d differs:\non-add:     %s\nbackground: %s", i, want[i], got[i])
		}
	}
}

// layered fails unless the Search responses of steps carry the parity
// consolidator's layer, so that the comparison covers consolidated memory.
func layered(t *testing.T, steps []step, bodies []string) {
	t.Helper()
	searched := 0
	for i, s := range steps {
		if s.search == nil {
			continue
		}
		searched++
		if !strings.HasPrefix(bodies[i], "200 ") || !strings.Contains(bodies[i], `"id":"index:`) || !strings.Contains(bodies[i], "records after") {
			t.Fatal("a Search without the layer:", bodies[i])
		}
	}
	if searched == 0 {
		t.Fatal("no Search")
	}
}

// consolidationHarness is a harness whose consolidation runs in mode with
// settings and folds with parityReply; in background mode its scheduler runs
// until the test ends.
func consolidationHarness(t *testing.T, mode ConsolidationMode, settings memoryagent.ConsolidationConfig) *harness {
	t.Helper()
	h := newHarness(t, func(config *ServerConfig) {
		config.MemoryConfig.Consolidation = &settings
		config.Consolidation = mode
	})
	h.consolidator.answer(parityReply)
	if mode == ConsolidationBackground {
		h.run(t)
	}
	return h
}

// run runs the server's background work until the test ends.
func (h *harness) run(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.server.Run(ctx, nil) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
}

// settle waits until the scheduler has nothing queued, running or backing
// off.
func (h *harness) settle(t *testing.T) {
	t.Helper()
	eventually(t, func() bool { return idle(h.server.sched) })
}

// backlog measures userID's consolidation backlog.
func (h *harness) backlog(t *testing.T, userID string) memoryagent.Backlog {
	t.Helper()
	ctx := context.Background()
	user, err := h.server.users.acquire(ctx, userID, false)
	if err != nil || user == nil {
		t.Fatal(err)
	}
	defer h.server.users.release(user)
	backlog, err := user.agent.Backlog(ctx, records.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	return backlog
}

// paritySettings are the consolidations parity is checked in: jobs of two
// batches, of one (whose window can end a batch early) and of the default
// sixteen (one job of a late worker folds every batch).
var paritySettings = []struct {
	name     string
	settings memoryagent.ConsolidationConfig
}{
	{"timelines", memoryagent.ConsolidationConfig{Mode: "timelines", BatchCharacters: 300, MaxBatches: 2}},
	{"layered", memoryagent.ConsolidationConfig{Mode: "layered", BatchCharacters: 300, MaxBatches: 2}},
	{"layered one batch a job", memoryagent.ConsolidationConfig{Mode: "layered", BatchCharacters: 300, MaxBatches: 1}},
	{"timelines default window", memoryagent.ConsolidationConfig{Mode: "timelines", BatchCharacters: 300}},
	{"layered default window", memoryagent.ConsolidationConfig{Mode: "layered", BatchCharacters: 300}},
}

func TestBackgroundAddMakesNoModelCall(t *testing.T) {
	settings := memoryagent.ConsolidationConfig{Mode: "timelines", BatchCharacters: 300}
	h := newHarness(t, func(config *ServerConfig) {
		config.MemoryConfig.Consolidation = &settings
		config.Consolidation = ConsolidationBackground
	})
	// Run is not started: no worker takes the Adds' work, and the Adds,
	// whose four records fill two batches, make no model call themselves.
	for i := 1; i <= 4; i++ {
		h.mustAdd(t, addRequest(fmt.Sprintf("r%d", i), "u1"))
		if n := h.consolidator.calls.Load(); n != 0 {
			t.Fatal("an Add consolidated", i, n)
		}
	}
	if len(h.planner.seen()) != 0 {
		t.Fatal("an Add called the planner")
	}
	// Nothing was read: every record waits, and the user is queued.
	if backlog := h.backlog(t, "u1"); backlog.Records != 4 {
		t.Fatal(backlog)
	}
	if status := h.server.sched.Status(); status.Queued != 1 || status.MaxBacklog == 0 {
		t.Fatal(status)
	}
	// Running, the scheduler folds the two full batches.
	h.run(t)
	h.settle(t)
	if n := h.consolidator.calls.Load(); n != 2 {
		t.Fatal(n)
	}
	if backlog := h.backlog(t, "u1"); backlog.Records != 0 {
		t.Fatal(backlog)
	}
	if logged := h.log.String(); strings.Contains(logged, "failed") {
		t.Fatal(logged)
	}
}

func TestBackgroundMatchesOnAdd(t *testing.T) {
	steps := []step{}
	for i := range 14 {
		steps = append(steps, addStep(noteAdd(i, "u1")))
	}
	steps = append(steps, searchStep(question, "u1"), searchStep(otherQuestion, "u1"), searchStep(question, "nobody"))
	for _, c := range paritySettings {
		t.Run(c.name, func(t *testing.T) {
			want := replay(t, consolidationHarness(t, ConsolidationOnAdd, c.settings), steps, false)
			layered(t, steps[:len(steps)-1], want[:len(want)-1])
			for _, settle := range []bool{false, true} {
				got := replay(t, consolidationHarness(t, ConsolidationBackground, c.settings), steps, settle)
				sameBodies(t, want, got)
			}
			// A worker that starts after every Add folds many batches a
			// job, where each Add's job folded one.
			h := newHarness(t, func(config *ServerConfig) {
				settings := c.settings
				config.MemoryConfig.Consolidation = &settings
				config.Consolidation = ConsolidationBackground
			})
			h.consolidator.answer(parityReply)
			h.server.addWindow = 10 * time.Millisecond // no worker to wait for
			got := replay(t, h, steps[:14], false)
			h.run(t)
			eventually(t, func() bool { return h.consolidator.calls.Load() > 0 })
			h.settle(t)
			sameBodies(t, want, append(got, replay(t, h, steps[14:], false)...))
		})
	}
}

func TestBackgroundMatchesOnAddInterleaved(t *testing.T) {
	// A streaming job: Adds, with the same questions asked at several steps.
	steps := []step{}
	for i := range 16 {
		steps = append(steps, addStep(noteAdd(i, "u1")))
		if i%3 == 1 {
			steps = append(steps, searchStep(question, "u1"))
		}
		if i%5 == 4 {
			steps = append(steps, searchStep(otherQuestion, "u1"))
		}
	}
	for _, c := range paritySettings {
		t.Run(c.name, func(t *testing.T) {
			want := replay(t, consolidationHarness(t, ConsolidationOnAdd, c.settings), steps, false)
			layered(t, steps[2:], want[2:])
			// The workers raced by every Search, then caught up before each.
			for _, settle := range []bool{false, true} {
				got := replay(t, consolidationHarness(t, ConsolidationBackground, c.settings), steps, settle)
				sameBodies(t, want, got)
			}
			// Folds that take a while: Searches meet jobs in flight.
			h := consolidationHarness(t, ConsolidationBackground, c.settings)
			h.consolidator.answer(func(ctx context.Context, prompt string) (string, error) {
				time.Sleep(3 * time.Millisecond)
				return parityReply(ctx, prompt)
			})
			sameBodies(t, want, replay(t, h, steps, false))
		})
	}
}

func TestSearchDuringABackgroundBatchWaitsForIt(t *testing.T) {
	// A job window large enough that no Add waits while the batch is held.
	settings := memoryagent.ConsolidationConfig{Mode: "layered", BatchCharacters: 300}
	steps := []step{}
	for i := range 12 {
		steps = append(steps, addStep(noteAdd(i, "u1")))
	}
	steps = append(steps, searchStep(question, "u1"))
	want := replay(t, consolidationHarness(t, ConsolidationOnAdd, settings), steps, false)
	layered(t, steps, want)

	h := consolidationHarness(t, ConsolidationBackground, settings)
	blocked, gate := make(chan struct{}, 1), make(chan struct{})
	var armed atomic.Bool
	h.consolidator.answer(func(ctx context.Context, prompt string) (string, error) {
		if armed.Load() && strings.Contains(prompt, "Note 06:") {
			blocked <- struct{}{}
			select {
			case <-gate:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		return parityReply(ctx, prompt)
	})
	got := replay(t, h, steps[:6], true)
	// The batch holding note 06 is extracted by a background job, which
	// waits there.
	armed.Store(true)
	got = append(got, replay(t, h, steps[6:12], false)...)
	select {
	case <-blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("no background job reached note 06")
	}
	// The Search waits for that batch, then folds the rest itself.
	answered := make(chan string, 1)
	go func() {
		response := h.call("POST", "/search", *steps[12].search)
		answered <- fmt.Sprintf("%d %s", response.Code, response.Body.String())
	}()
	select {
	case body := <-answered:
		t.Fatal("the Search did not wait for the batch in flight:", body)
	case <-time.After(100 * time.Millisecond):
	}
	if status := h.server.sched.Status(); status.Held != 1 || status.Running != 1 {
		t.Fatal(status)
	}
	close(gate)
	select {
	case body := <-answered:
		got = append(got, body)
	case <-time.After(5 * time.Second):
		t.Fatal("the Search did not answer")
	}
	sameBodies(t, want, got)
}

func TestSchedulerResumesAfterCutJob(t *testing.T) {
	// Twelve notes of ~91 units: three full batches of four.
	settings := memoryagent.ConsolidationConfig{Mode: "layered", BatchCharacters: 300}
	adds := []step{}
	for i := range 12 {
		req := noteAdd(i, "u1")
		req.Messages[0].Content = fmt.Sprintf("Note %02d: Caroline went to the LGBTQ support group and talked with the others there.", i)
		adds = append(adds, addStep(req))
	}
	want := replay(t, consolidationHarness(t, ConsolidationOnAdd, settings), append(adds, searchStep(question, "u1")), false)

	// The first server's job is cut while the third batch is in extraction.
	first := newHarness(t, func(config *ServerConfig) {
		config.MemoryConfig.Consolidation = &settings
		config.Consolidation = ConsolidationBackground
	})
	blocked := make(chan struct{}, 1)
	first.consolidator.answer(func(ctx context.Context, prompt string) (string, error) {
		if strings.Contains(prompt, "Note 08:") {
			blocked <- struct{}{}
			<-ctx.Done()
			return "", ctx.Err()
		}
		return parityReply(ctx, prompt)
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- first.server.Run(ctx, nil) }()
	replay(t, first, adds, false)
	<-blocked
	// The batches before it link and are saved one by one.
	eventually(t, func() bool { return first.backlog(t, "u1").Records == 4 })
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	first.server.Close()

	// A new server on the same data root resumes from the saved batches.
	second := newHarness(t, func(config *ServerConfig) {
		config.DataRoot = first.root
		config.MemoryConfig.Consolidation = &settings
		config.Consolidation = ConsolidationBackground
	})
	second.root = first.root
	second.consolidator.answer(parityReply)
	if backlog := second.backlog(t, "u1"); backlog.Records != 4 {
		t.Fatal("the cut job's saved state", backlog)
	}
	// It queues every user at start: the last batch is extracted once.
	second.run(t)
	eventually(t, func() bool { return second.backlog(t, "u1").Records == 0 })
	second.settle(t)
	if n := second.consolidator.calls.Load(); n != 1 {
		t.Fatal("extractions after the restart", n)
	}
	response := second.call("POST", "/search", SearchRequest{Query: question, UserID: "u1", TopK: 100})
	if got := fmt.Sprintf("%d %s", response.Code, response.Body.String()); got != want[len(want)-1] {
		t.Fatalf("after the restart:\n%s\nan uninterrupted run:\n%s", got, want[len(want)-1])
	}
}

func TestBackgroundJEVOutageDefers(t *testing.T) {
	settings := memoryagent.ConsolidationConfig{Mode: "layered", BatchCharacters: 300}
	adds := []step{}
	for i := 1; i <= 6; i++ {
		adds = append(adds, addStep(addRequest(fmt.Sprintf("r%d", i), "u1")))
	}
	want := replay(t, consolidationHarness(t, ConsolidationOnAdd, settings), append(adds, searchStep(question, "u1")), false)

	h := newHarness(t, func(config *ServerConfig) {
		config.MemoryConfig.Consolidation = &settings
		config.Consolidation = ConsolidationBackground
	})
	h.consolidator.answer(parityReply)
	clock := newFakeClock()
	h.server.sched.now = clock.now
	h.judge.outage.Store(true)
	h.run(t)
	// Six records, three full batches. The first links into the empty
	// memory without asking JEV; the second cannot be linked, and the job
	// defers it with the third and backs off.
	replay(t, h, adds, false)
	eventually(t, func() bool { return h.server.sched.Status().BackingOff == 1 })
	if backlog := h.backlog(t, "u1"); backlog.Records != 4 || h.server.sched.Status().MaxBacklog == 0 {
		t.Fatal(backlog, h.server.sched.Status())
	}
	// Searches answer meanwhile; their flush defers too.
	h.search(t, SearchRequest{Query: question, UserID: "u1", TopK: 100})
	// A retry during the outage defers again.
	clock.advance(time.Minute)
	h.server.sched.signal()
	eventually(t, func() bool { return strings.Count(h.log.String(), "consolidation retries in") >= 2 })
	eventually(t, func() bool { return h.server.sched.Status().BackingOff == 1 })
	if backlog := h.backlog(t, "u1"); backlog.Records != 4 {
		t.Fatal(backlog)
	}
	// JEV answers again: the next retry links the deferred batches.
	h.judge.outage.Store(false)
	clock.advance(10 * time.Minute)
	h.server.sched.signal()
	h.settle(t)
	if backlog := h.backlog(t, "u1"); backlog.Records != 0 {
		t.Fatal(backlog)
	}
	logged := h.log.String()
	if strings.Contains(logged, "without JEV") || !strings.Contains(logged, "consolidation failed (user "+userKey("u1")[:8]+"): model unavailable: linking deferred") {
		t.Fatal(logged)
	}
	// The memory is that of a run whose JEV never failed.
	response := h.call("POST", "/search", SearchRequest{Query: question, UserID: "u1", TopK: 100})
	if got := fmt.Sprintf("%d %s", response.Code, response.Body.String()); got != want[len(want)-1] {
		t.Fatalf("after the outage:\n%s\nwithout one:\n%s", got, want[len(want)-1])
	}
}

func TestBoundedLagHoldsAdds(t *testing.T) {
	// A job reads 600 units, the lag bound.
	settings := memoryagent.ConsolidationConfig{Mode: "layered", BatchCharacters: 300, MaxBatches: 2}
	steps := []step{}
	for i := range 24 {
		steps = append(steps, addStep(noteAdd(i, "u1")))
	}
	steps = append(steps, searchStep(question, "u1"))
	want := replay(t, consolidationHarness(t, ConsolidationOnAdd, settings), steps, false)
	layered(t, steps, want)

	// Every fold takes 100 ms, far slower than an Add.
	h := consolidationHarness(t, ConsolidationBackground, settings)
	h.consolidator.answer(func(ctx context.Context, prompt string) (string, error) {
		time.Sleep(100 * time.Millisecond)
		return parityReply(ctx, prompt)
	})
	if h.server.lagBound != 600 {
		t.Fatal(h.server.lagBound)
	}
	got, waited := []string{}, 0
	for _, s := range steps[:24] {
		start := time.Now()
		got = append(got, replay(t, h, []step{s}, false)...)
		if time.Since(start) >= 50*time.Millisecond {
			waited++
		}
		// An Add answers only once the backlog is within the bound.
		if backlog := h.backlog(t, "u1"); backlog.Characters > h.server.lagBound {
			t.Fatal("an Add answered ahead of consolidation", backlog)
		}
	}
	if waited == 0 {
		t.Fatal("no Add waited for consolidation")
	}
	// The first Search after the last Add folds at most one window plus the
	// tail: two full batches and the rest.
	calls := h.consolidator.calls.Load()
	got = append(got, replay(t, h, steps[24:], false)...)
	if n := h.consolidator.calls.Load() - calls; n > int64(settings.MaxBatches)+1 {
		t.Fatal("folds during the Search", n)
	}
	sameBodies(t, want, got)
	if strings.Contains(h.log.String(), "add outran consolidation") {
		t.Fatal(h.log.String())
	}
}

func TestAddOutranConsolidationAnswers(t *testing.T) {
	settings := memoryagent.ConsolidationConfig{Mode: "timelines", BatchCharacters: 300, MaxBatches: 1}
	h := newHarness(t, func(config *ServerConfig) {
		config.MemoryConfig.Consolidation = &settings
		config.Consolidation = ConsolidationBackground
	})
	// No worker runs: past the bound an Add waits out its window, then
	// answers; its records are stored and searchable.
	h.server.addWindow = 50 * time.Millisecond
	h.mustAdd(t, addRequest("r1", "u1"))
	if strings.Contains(h.log.String(), "add outran consolidation") {
		t.Fatal("an Add within the bound waited", h.log.String())
	}
	start := time.Now()
	h.mustAdd(t, addRequest("r2", "u1"))
	if time.Since(start) < 50*time.Millisecond || !strings.Contains(h.log.String(), "add outran consolidation (user "+userKey("u1")[:8]+")") {
		t.Fatal(time.Since(start), h.log.String())
	}
	if items := h.search(t, SearchRequest{Query: question, UserID: "u1", TopK: 100}); len(items) == 0 {
		t.Fatal("no items")
	}
}

func TestServerConsolidationModes(t *testing.T) {
	timelines := &memoryagent.ConsolidationConfig{Mode: "layered"}
	models := func(memoryagent.Config) (memoryagent.Models, error) {
		return memoryagent.Models{Planner: &fakePlanner{}, Judge: &fakeJudge{}, Consolidator: &fakeConsolidator{}}, nil
	}
	server := func(edit func(*ServerConfig)) (*Server, error) {
		config := ServerConfig{DataRoot: t.TempDir(), MemoryConfig: simpleConfig(), AuthKey: testKey, Models: models}
		config.MemoryConfig.Consolidation = timelines
		edit(&config)
		s, err := NewServer(config)
		if err == nil {
			t.Cleanup(func() { s.Close() })
		}
		return s, err
	}
	for name, edit := range map[string]func(*ServerConfig){
		"unknown mode":            func(c *ServerConfig) { c.Consolidation = "nightly" },
		"on-add flag, background": func(c *ServerConfig) { c.ConsolidateOnAdd, c.Consolidation = true, ConsolidationBackground },
		"background without timelines": func(c *ServerConfig) {
			c.Consolidation, c.MemoryConfig.Consolidation = ConsolidationBackground, nil
		},
		"background with insights": func(c *ServerConfig) {
			c.Consolidation, c.MemoryConfig.Consolidation = ConsolidationBackground, &memoryagent.ConsolidationConfig{Mode: "layered", Insights: true}
		},
		"negative workers": func(c *ServerConfig) { c.Consolidation, c.ConsolidationWorkers = ConsolidationBackground, -1 },
	} {
		if _, err := server(edit); err == nil {
			t.Fatal(name)
		}
	}
	// 0 workers is the default, so the error says what is refused (parked
	// T6-i).
	if _, err := server(func(c *ServerConfig) { c.Consolidation, c.ConsolidationWorkers = ConsolidationBackground, -1 }); err == nil || err.Error() != "consolidation workers must not be negative" {
		t.Fatal(err)
	}
	// The old flag alone is on-add; on-add and off have no scheduler.
	for _, edit := range []func(*ServerConfig){
		func(c *ServerConfig) { c.ConsolidateOnAdd = true },
		func(c *ServerConfig) { c.ConsolidateOnAdd, c.Consolidation = true, ConsolidationOnAdd },
	} {
		s, err := server(edit)
		if err != nil || s.mode != ConsolidationOnAdd || s.sched != nil {
			t.Fatal(err)
		}
	}
	if s, err := server(func(*ServerConfig) {}); err != nil || s.mode != ConsolidationOff || s.sched != nil {
		t.Fatal(err)
	}
	// Background mode: four workers by default, and the lag bound is the
	// job window of the effective settings, 160,000 by default.
	s, err := server(func(c *ServerConfig) { c.Consolidation = ConsolidationBackground })
	if err != nil || s.sched == nil || s.sched.workers != 4 || s.lagBound != 160000 {
		t.Fatal(err)
	}
}

func TestCloseStopsRun(t *testing.T) {
	settings := memoryagent.ConsolidationConfig{Mode: "timelines", BatchCharacters: 300}
	h := newHarness(t, func(config *ServerConfig) {
		config.MemoryConfig.Consolidation = &settings
		config.Consolidation = ConsolidationBackground
	})
	entered := make(chan struct{}, 1)
	h.consolidator.answer(func(ctx context.Context, prompt string) (string, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return "", ctx.Err()
	})
	done := make(chan error, 1)
	go func() { done <- h.server.Run(context.Background(), nil) }()
	h.mustAdd(t, addRequest("r1", "u1"))
	h.mustAdd(t, addRequest("r2", "u1"))
	<-entered
	// Run runs once at a time.
	if err := h.server.Run(context.Background(), nil); err == nil {
		t.Fatal("a second Run ran")
	}
	// Close stops the job in flight and Run before it closes the stores.
	closed := make(chan struct{})
	go func() { h.server.Close(); close(closed) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not stop Run")
	}
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return")
	}
	if strings.Contains(h.log.String(), "panicked") {
		t.Fatal(h.log.String())
	}
	// Nor does it run after Close.
	if err := h.server.Run(context.Background(), nil); err == nil {
		t.Fatal("Run ran after Close")
	}
}

// attemptClasses records the class of every model attempt it is shown.
type attemptClasses struct {
	mu   sync.Mutex
	seen []string
}

func (c *attemptClasses) ModelAttempt(event memoryagent.AttemptEvent) {
	c.mu.Lock()
	c.seen = append(c.seen, event.Class)
	c.mu.Unlock()
}

func (c *attemptClasses) all() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.seen)
}

// localConsolidator is a local provider whose endpoint folds with
// parityReply.
func localConsolidator(t *testing.T) *memoryagent.Provider {
	t.Helper()
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Messages []struct{ Content string } }
		json.NewDecoder(r.Body).Decode(&body)
		reply, _ := parityReply(r.Context(), body.Messages[len(body.Messages)-1].Content)
		json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": reply}, "finish_reason": "stop"}}})
	}))
	t.Cleanup(endpoint.Close)
	provider, err := memoryagent.NewProvider(memoryagent.ModelConfig{Kind: "local", Model: "m", BaseURL: endpoint.URL + "/v1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func TestSearchFlushCallsAreFlushClass(t *testing.T) {
	settings := memoryagent.ConsolidationConfig{Mode: "timelines", BatchCharacters: 300}
	for _, mode := range []ConsolidationMode{ConsolidationOnAdd, ConsolidationBackground} {
		t.Run(string(mode), func(t *testing.T) {
			consolidator := localConsolidator(t)
			h := newHarness(t, func(config *ServerConfig) {
				config.MemoryConfig.Consolidation = &settings
				config.Consolidation = mode
				config.Models = func(memoryagent.Config) (memoryagent.Models, error) {
					return memoryagent.Models{Planner: &fakePlanner{}, Judge: &fakeJudge{}, Consolidator: consolidator}, nil
				}
			})
			// The server shows the providers' attempts to its metrics; this
			// test watches them instead.
			classes := &attemptClasses{}
			memoryagent.Models{Consolidator: consolidator}.Observe(classes)
			if mode == ConsolidationBackground {
				h.run(t)
			}
			// Two records fill a batch, which a job folds as background
			// work; the Search folds the third as its flush.
			for _, id := range []string{"r1", "r2", "r3"} {
				h.mustAdd(t, addRequest(id, "u1"))
			}
			if mode == ConsolidationBackground {
				eventually(t, func() bool { return len(classes.all()) == 1 })
				h.settle(t)
			}
			h.search(t, SearchRequest{Query: question, UserID: "u1", TopK: 100})
			if got := classes.all(); !slices.Equal(got, []string{"background", "flush"}) {
				t.Fatal(got, h.log.String())
			}
		})
	}
}

func TestHeldUserJobStopsBetweenItsJobs(t *testing.T) {
	// A job reads 600 units, three records of 194: it folds one batch of
	// two, so six records take three jobs.
	settings := memoryagent.ConsolidationConfig{Mode: "timelines", BatchCharacters: 300, MaxBatches: 2}
	h := newHarness(t, func(config *ServerConfig) {
		config.MemoryConfig.Consolidation = &settings
		config.Consolidation = ConsolidationBackground
	})
	h.server.addWindow = 10 * time.Millisecond // the Adds outrun the bound
	for i := 1; i <= 6; i++ {
		h.mustAdd(t, addRequest(fmt.Sprintf("r%d", i), "u1"))
	}
	blocked, gate := make(chan struct{}, 1), make(chan struct{})
	var first atomic.Bool
	h.consolidator.answer(func(ctx context.Context, prompt string) (string, error) {
		if first.CompareAndSwap(false, true) {
			blocked <- struct{}{}
			select {
			case <-gate:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		return parityReply(ctx, prompt)
	})
	h.run(t)
	<-blocked
	// A Search holds the user during the first job: the jobs stop after it,
	// and the user waits in the queue for the hold to end.
	release := h.server.sched.Hold(userKey("u1"))
	close(gate)
	eventually(t, func() bool {
		status := h.server.sched.Status()
		return status.Running == 0 && status.Queued == 1
	})
	if n := h.consolidator.calls.Load(); n != 1 {
		t.Fatal("folds while held", n)
	}
	release()
	h.settle(t)
	if n := h.consolidator.calls.Load(); n != 3 {
		t.Fatal("folds after the hold", n)
	}
	// Before its first job too, a job of a held user folds nothing.
	release = h.server.sched.Hold(userKey("u1"))
	defer release()
	h.mustAdd(t, addRequest("r7", "u1"))
	h.mustAdd(t, addRequest("r8", "u1"))
	if _, err := h.server.consolidateUser(context.Background(), userKey("u1")); err != nil || h.consolidator.calls.Load() != 3 {
		t.Fatal(err, h.consolidator.calls.Load())
	}
}

// Drain stops a user's work between its jobs, as a Search's hold does, and
// leaves the user queued with the rest pending for the next start.
func TestDrainedUserJobStopsBetweenItsJobs(t *testing.T) {
	// Six records take three jobs (TestHeldUserJobStopsBetweenItsJobs).
	settings := memoryagent.ConsolidationConfig{Mode: "timelines", BatchCharacters: 300, MaxBatches: 2}
	h := newHarness(t, func(config *ServerConfig) {
		config.MemoryConfig.Consolidation = &settings
		config.Consolidation = ConsolidationBackground
	})
	h.server.addWindow = 10 * time.Millisecond // the Adds outrun the bound
	for i := 1; i <= 6; i++ {
		h.mustAdd(t, addRequest(fmt.Sprintf("r%d", i), "u1"))
	}
	blocked, gate := make(chan struct{}, 1), make(chan struct{})
	var first atomic.Bool
	h.consolidator.answer(func(ctx context.Context, prompt string) (string, error) {
		if first.CompareAndSwap(false, true) {
			blocked <- struct{}{}
			select {
			case <-gate:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		return parityReply(ctx, prompt)
	})
	h.run(t)
	<-blocked
	h.server.Drain()
	close(gate)
	// The first job ends uncut and saves its batch; no job follows it.
	if err := h.server.WaitDrained(within(t)); err != nil {
		t.Fatal(err)
	}
	if n := h.consolidator.calls.Load(); n != 1 {
		t.Fatal("folds after the drain", n)
	}
	if status := h.server.sched.Status(); status.Running != 0 || status.Queued != 1 {
		t.Fatal(status)
	}
	if backlog := h.backlog(t, "u1"); backlog.Records != 4 {
		t.Fatal("pending after the drain", backlog)
	}
	if strings.Contains(h.log.String(), "consolidation failed") {
		t.Fatal(h.log.String())
	}
}

// A background Add waiting at the lag bound answers once the scheduler
// drains: its records are stored, and no job would lower the backlog.
func TestDrainAnswersAnAddWaitingAtTheBound(t *testing.T) {
	settings := memoryagent.ConsolidationConfig{Mode: "timelines", BatchCharacters: 300, MaxBatches: 1}
	h := newHarness(t, func(config *ServerConfig) {
		config.MemoryConfig.Consolidation = &settings
		config.Consolidation = ConsolidationBackground
	})
	// No worker runs, so past the bound an Add would wait its whole window.
	h.server.addWindow = time.Minute
	h.mustAdd(t, addRequest("r1", "u1"))
	answered := make(chan *httptest.ResponseRecorder, 1)
	go func() { answered <- h.call("POST", "/add", addRequest("r2", "u1")) }()
	select {
	case response := <-answered:
		t.Fatal("answered above the bound", response.Code)
	case <-time.After(50 * time.Millisecond):
	}
	h.server.Drain()
	if response := received(t, answered); response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}
	if strings.Contains(h.log.String(), "add outran consolidation") {
		t.Fatal("a released Add counted as outrun", h.log.String())
	}
	// Without background consolidation there is nothing to drain.
	plain := newHarness(t, nil)
	plain.server.Drain()
	if err := plain.server.WaitDrained(within(t)); err != nil {
		t.Fatal(err)
	}
}

// Run calls ready once its background work runs: the startup scan has
// queued the users, and the heartbeat has ticked. A Run that is refused
// does not call it.
func TestRunReportsReadyAfterTheStartupScan(t *testing.T) {
	settings := memoryagent.ConsolidationConfig{Mode: "timelines", BatchCharacters: 300}
	background := func(config *ServerConfig) {
		config.MemoryConfig.Consolidation = &settings
		config.Consolidation = ConsolidationBackground
	}
	// An earlier process left a full batch pending.
	earlier := newHarness(t, background)
	earlier.server.addWindow = 10 * time.Millisecond
	earlier.mustAdd(t, addRequest("r1", "u1"))
	earlier.mustAdd(t, addRequest("r2", "u1"))
	earlier.server.Close()
	h := newHarness(t, func(config *ServerConfig) {
		background(config)
		config.DataRoot = earlier.root
	})
	// The fold blocks, so the user stays queued or running.
	h.consolidator.answer(func(ctx context.Context, _ string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	if !h.server.Heartbeat().IsZero() {
		t.Fatal("a heartbeat before Run")
	}
	ready := make(chan SchedulerStatus, 2)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- h.server.Run(ctx, func() {
			if h.server.Heartbeat().IsZero() {
				t.Error("ready before the first heartbeat")
			}
			ready <- h.server.sched.Status()
		})
	}()
	if status := received(t, ready); status.Queued+status.Running != 1 {
		t.Fatal("the startup scan had not queued the user", status)
	}
	if err := h.server.Run(ctx, func() { t.Error("ready for a second Run") }); err == nil {
		t.Fatal("a second Run ran")
	}
	cancel()
	if err := received(t, done); err != nil {
		t.Fatal(err)
	}
	h.server.Close()
	if err := h.server.Run(context.Background(), func() { t.Error("ready after Close") }); err == nil {
		t.Fatal("Run ran after Close")
	}
	if len(ready) != 0 {
		t.Fatal("ready called again")
	}
}

// The heartbeat ticks while Run runs, each tick once the scheduler and the
// store registry answered; it stops when Run returns.
func TestHeartbeatTicksWhileRunRuns(t *testing.T) {
	settings := memoryagent.ConsolidationConfig{Mode: "timelines", BatchCharacters: 300}
	h := newHarness(t, func(config *ServerConfig) {
		config.MemoryConfig.Consolidation = &settings
		config.Consolidation = ConsolidationBackground
	})
	h.server.heartbeatEvery = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.server.Run(ctx, nil) }()
	eventually(t, func() bool { return !h.server.Heartbeat().IsZero() })
	beats := func() bool {
		beat := h.server.Heartbeat()
		time.Sleep(30 * time.Millisecond)
		return h.server.Heartbeat().After(beat)
	}
	if !beats() {
		t.Fatal("the heartbeat does not tick")
	}
	// A tick waits for each lock: while one is held, the heartbeat stops.
	for name, lock := range map[string]sync.Locker{"scheduler": &h.server.sched.mu, "registry": &h.server.users.mu} {
		lock.Lock()
		time.Sleep(20 * time.Millisecond) // a tick past the lock lands
		stuck := beats()
		lock.Unlock()
		if stuck {
			t.Fatal("the heartbeat ticked while the", name, "was locked")
		}
		eventually(t, beats)
	}
	cancel()
	if err := received(t, done); err != nil {
		t.Fatal(err)
	}
	if beats() {
		t.Fatal("the heartbeat ticks after Run returned")
	}
}

func TestSearchPicksUpADroppedUser(t *testing.T) {
	settings := memoryagent.ConsolidationConfig{Mode: "timelines", BatchCharacters: 300}
	h := newHarness(t, func(config *ServerConfig) {
		config.MemoryConfig.Consolidation = &settings
		config.Consolidation = ConsolidationBackground
	})
	// The queue is full: the Adds' notifications are dropped, and the
	// Adds do not wait.
	h.server.sched.maxUsers = 0
	for _, id := range []string{"r1", "r2", "r3"} {
		h.mustAdd(t, addRequest(id, "u1"))
	}
	if status := h.server.sched.Status(); status.Queued != 0 || !strings.Contains(h.log.String(), "consolidation queue full") {
		t.Fatal(status, h.log.String())
	}
	// With room again, a Search whose flush fails leaves the work to a job.
	h.server.sched.maxUsers = 100
	h.consolidator.answer(func(context.Context, string) (string, error) {
		return "", errors.New("model HTTP 400")
	})
	h.search(t, SearchRequest{Query: question, UserID: "u1", TopK: 100})
	if status := h.server.sched.Status(); status.Queued != 1 || !strings.Contains(h.log.String(), "flush failed (user "+userKey("u1")[:8]+")") {
		t.Fatal(status, h.log.String())
	}
}

// Parked T6-a/T6-e (final review): until a job measured it, the scheduler
// knew nothing of a user's backlog after a start (or after a Search's hold
// made the user known), and an Add past the lag bound answered at once. An
// Add for a user the scheduler has no measure of now measures the backlog
// once, after its write: a backlog that piled up before (here consolidation
// was off) holds the Add at the bound.
func TestAddMeasuresABacklogTheSchedulerDoesNotKnow(t *testing.T) {
	settings := memoryagent.ConsolidationConfig{Mode: "timelines", BatchCharacters: 300, MaxBatches: 1}
	// Consolidation off: the records pile up, many jobs' windows of them.
	off := newHarness(t, func(config *ServerConfig) { config.MemoryConfig.Consolidation = &settings })
	for i := range 6 {
		off.mustAdd(t, noteAdd(i, "u1"))
	}
	off.server.Close()
	// The same data root in background mode, with no worker running: no job
	// measures the backlog.
	h := newHarness(t, func(config *ServerConfig) {
		config.DataRoot = off.root
		config.MemoryConfig.Consolidation = &settings
		config.Consolidation = ConsolidationBackground
	})
	h.server.addWindow = 50 * time.Millisecond
	start := time.Now()
	h.mustAdd(t, noteAdd(6, "u1"))
	if time.Since(start) < 50*time.Millisecond || !strings.Contains(h.log.String(), "add outran consolidation (user "+userKey("u1")[:8]+")") {
		t.Fatal("an Add over a backlog past the bound answered at once:", time.Since(start), h.log.String())
	}
	if backlog, known := h.server.sched.Backlog(userKey("u1")); !known || backlog <= h.server.lagBound {
		t.Fatal("the Add left no measure of the backlog", backlog, known, h.server.lagBound)
	}
}

// Parked T6-b (final review): an Add adds what it wrote to the user's backlog
// estimate in UTF-16 units, the units the backlog's measure and a job's
// window count, not in runes: an emoji is two. Two Adds alike but for 100
// characters of their text, ASCII in one and emoji in the other, differ by
// 100 there.
func TestAddCountsUTF16Units(t *testing.T) {
	settings := memoryagent.ConsolidationConfig{Mode: "timelines", BatchCharacters: 50000, MaxBatches: 64}
	h := newHarness(t, func(config *ServerConfig) {
		config.MemoryConfig.Consolidation = &settings
		config.Consolidation = ConsolidationBackground
	})
	say := func(id, text string) AddRequest {
		return AddRequest{RequestID: id, UserID: "u1", SessionID: id, Messages: []Message{{Role: "user", Content: text, Timestamp: at(0)}}}
	}
	estimate := func() int64 {
		t.Helper()
		backlog, known := h.server.sched.Backlog(userKey("u1"))
		if !known {
			t.Fatal("no estimate")
		}
		return backlog
	}
	h.mustAdd(t, say("r0", "The first note measures the backlog."))
	before := estimate()
	h.mustAdd(t, say("r1", "Plain: "+strings.Repeat("a", 100)))
	ascii := estimate() - before
	h.mustAdd(t, say("r2", "Plain: "+strings.Repeat("😀", 100)))
	emoji := estimate() - before - ascii
	if ascii <= 100 || emoji-ascii != 100 {
		t.Fatalf("the ASCII Add added %d, the emoji one %d: want 100 more", ascii, emoji)
	}
}

// Parked T7-b (final review): a notification dropped because the queue is
// full is logged with the request that made it: its request_id and its
// user.
func TestQueueFullLineCarriesTheRequestID(t *testing.T) {
	settings := memoryagent.ConsolidationConfig{Mode: "timelines", BatchCharacters: 50000, MaxBatches: 64}
	h := newHarness(t, func(config *ServerConfig) {
		config.MemoryConfig.Consolidation = &settings
		config.Consolidation = ConsolidationBackground
	})
	h.server.sched.maxUsers = 1 // no worker runs: u1 fills the queue
	h.mustAdd(t, addRequest("r1", "u1"))
	if response := h.call("POST", "/add", addRequest("r2", "u2"), withAuth("X-Request-Id", "full-1")...); response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}
	if lines := h.logLines("consolidation queue full", " request_id=full-1", " user="+userKey("u2")[:8]); len(lines) != 1 {
		t.Fatal(h.log.String())
	}
}
