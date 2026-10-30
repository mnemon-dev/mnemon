package aml

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
)

// fakeClock is a clock the test moves by hand.
type fakeClock struct {
	mu sync.Mutex
	at time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{at: time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.at = c.at.Add(d)
	c.mu.Unlock()
}

// logLines collects a scheduler's log lines.
type logLines struct {
	mu    sync.Mutex
	lines []string
}

func (l *logLines) logf(_ context.Context, format string, args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

// count is the number of lines that contain text.
func (l *logLines) count(text string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, line := range l.lines {
		if strings.Contains(line, text) {
			n++
		}
	}
	return n
}

func (l *logLines) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

// startScheduler runs s until the test ends.
func startScheduler(t *testing.T, s *scheduler) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

// idle reports whether s has nothing queued, running or backing off.
func idle(s *scheduler) bool {
	status := s.Status()
	return status.Queued == 0 && status.Running == 0 && status.BackingOff == 0
}

// never fails if ran receives within 50 ms.
func never(t *testing.T, ran <-chan string, what string) {
	t.Helper()
	select {
	case key := <-ran:
		t.Fatal(what, key)
	case <-time.After(50 * time.Millisecond):
	}
}

// received waits up to five seconds for a value on ch.
func received[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("nothing received")
		var zero T
		return zero
	}
}

// next is the next key on ran, within five seconds.
func next(t *testing.T, ran <-chan string) string {
	t.Helper()
	select {
	case key := <-ran:
		return key
	case <-time.After(5 * time.Second):
		t.Fatal("no job ran")
		return ""
	}
}

func TestSchedulerRunsEachUserOnceAtATime(t *testing.T) {
	const workers = 2
	var mu sync.Mutex
	running, runs := map[string]int{}, map[string]int{}
	inFlight, maxInFlight, maxPerUser := 0, 0, 0
	started, release := make(chan string, 100), make(chan struct{})
	s := newScheduler(workers, 100, func(ctx context.Context, key string) (int64, error) {
		mu.Lock()
		running[key]++
		runs[key]++
		inFlight++
		maxInFlight, maxPerUser = max(maxInFlight, inFlight), max(maxPerUser, running[key])
		mu.Unlock()
		started <- key
		select {
		case <-release:
		case <-ctx.Done():
		}
		mu.Lock()
		running[key]--
		inFlight--
		mu.Unlock()
		return 0, nil
	}, (&logLines{}).logf, time.Now)
	startScheduler(t, s)
	keys := []string{userKey("a"), userKey("b"), userKey("c")}
	for i := range 100 {
		s.Notify(context.Background(), keys[i%3], 171)
	}
	// Both workers take a user; the third waits.
	next(t, started)
	next(t, started)
	never(t, started, "a third job ran with two workers busy")
	// Released one at a time, each user runs once, and once more at most
	// for the notifications that came while it ran.
	for deadline := time.Now().Add(5 * time.Second); !idle(s); {
		if time.Now().After(deadline) {
			t.Fatal("the jobs did not drain", s.Status())
		}
		select {
		case release <- struct{}{}:
		case <-time.After(5 * time.Millisecond):
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if maxPerUser != 1 || maxInFlight != workers {
		t.Fatal("concurrent jobs per user", maxPerUser, "in all", maxInFlight)
	}
	for _, key := range keys {
		if runs[key] < 1 || runs[key] > 2 {
			t.Fatal("runs", runs)
		}
	}
}

func TestSchedulerHoldBlocksJobs(t *testing.T) {
	x, a, b, c := userKey("x"), userKey("a"), userKey("b"), userKey("c")
	ran := make(chan string, 10)
	gates := map[string]chan struct{}{x: make(chan struct{}), b: make(chan struct{})}
	s := newScheduler(1, 100, func(ctx context.Context, key string) (int64, error) {
		ran <- key
		if gate := gates[key]; gate != nil {
			select {
			case <-gate:
			case <-ctx.Done():
			}
		}
		return 0, nil
	}, (&logLines{}).logf, time.Now)
	startScheduler(t, s)
	// One worker, busy with x: a, b and c queue in that order.
	s.Notify(context.Background(), x, 1)
	if key := next(t, ran); key != x {
		t.Fatal(key)
	}
	s.Notify(context.Background(), a, 1)
	s.Notify(context.Background(), b, 1)
	s.Notify(context.Background(), c, 1)
	release := s.Hold(a)
	if status := s.Status(); status.Held != 1 || status.Queued != 3 {
		t.Fatal(status)
	}
	// a is held: b runs first.
	close(gates[x])
	if key := next(t, ran); key != b {
		t.Fatal("ran", key, "instead of b while a was held")
	}
	// Released, a runs before c: it kept its place in the queue.
	release()
	release() // a second call does nothing
	close(gates[b])
	if first, second := next(t, ran), next(t, ran); first != a || second != c {
		t.Fatal("after the hold", first, second)
	}
	eventually(t, func() bool { return idle(s) })
	// A held user that is the only one queued does not run until released.
	release = s.Hold(a)
	s.Notify(context.Background(), a, 1)
	never(t, ran, "a held user ran")
	if status := s.Status(); status.Held != 1 || status.Queued != 1 {
		t.Fatal(status)
	}
	release()
	if key := next(t, ran); key != a {
		t.Fatal(key)
	}
	eventually(t, func() bool { return idle(s) && s.Status().Held == 0 })
}

func TestWaitBacklogReleasesAtLimit(t *testing.T) {
	a := userKey("a")
	started, results := make(chan struct{}, 10), make(chan int64)
	s := newScheduler(1, 100, func(ctx context.Context, key string) (int64, error) {
		started <- struct{}{}
		select {
		case backlog := <-results:
			return backlog, nil
		case <-ctx.Done():
			return -1, ctx.Err()
		}
	}, (&logLines{}).logf, time.Now)
	ctx := context.Background()
	// A user the scheduler does not know has no backlog.
	if err := s.WaitBacklog(ctx, userKey("nobody"), 160000); err != nil {
		t.Fatal(err)
	}
	s.Notify(context.Background(), a, 200000)
	waited := make(chan error, 1)
	go func() { waited <- s.WaitBacklog(ctx, a, 160000) }()
	// Above the limit with no job run yet, the wait goes on; a context that
	// ends stops it.
	select {
	case err := <-waited:
		t.Fatal("returned above the limit", err)
	case <-time.After(50 * time.Millisecond):
	}
	short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if err := s.WaitBacklog(short, a, 160000); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	startScheduler(t, s)
	received(t, started)
	// More written while the job runs: it runs again after, and the backlog
	// it measures stays above the limit.
	s.Notify(context.Background(), a, 0)
	results <- 170000
	received(t, started)
	select {
	case err := <-waited:
		t.Fatal("returned at", s.Status().MaxBacklog, err)
	case <-time.After(50 * time.Millisecond):
	}
	if backlog := s.Status().MaxBacklog; backlog != 170000 {
		t.Fatal(backlog)
	}
	// The second job measures 150,000, and 5,000 more were written while it
	// ran, which its measure may not hold: 155,000, at or below the limit.
	s.Notify(context.Background(), a, 5000)
	results <- 150000
	select {
	case err := <-waited:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the wait did not end at", s.Status().MaxBacklog)
	}
	if backlog := s.Status().MaxBacklog; backlog != 155000 {
		t.Fatal(backlog)
	}
	// Written during the job, those 5,000 bring a third job.
	received(t, started)
	results <- 0
	eventually(t, func() bool { return idle(s) && s.Status().MaxBacklog == 0 })
}

func TestSchedulerBacksOffAndRecovers(t *testing.T) {
	clock, logs := newFakeClock(), &logLines{}
	a := userKey("a")
	ran := make(chan string, 10)
	var calls atomic.Int64
	failures := []error{errors.New("model HTTP 503"), fmt.Errorf("%w: %w", memoryagent.ErrLinkDeferred, memoryagent.ErrModelUnavailable)}
	s := newScheduler(1, 100, func(_ context.Context, key string) (int64, error) {
		n := int(calls.Add(1))
		ran <- key
		if n <= len(failures) {
			return 3000, failures[n-1]
		}
		return 0, nil
	}, logs.logf, clock.now)
	s.jitter = func() float64 { return 0.5 } // no jitter: exactly 30 s × 2ⁿ
	startScheduler(t, s)
	s.Notify(context.Background(), a, 3000)
	next(t, ran)
	eventually(t, func() bool { return s.Status().BackingOff == 1 })
	if status := s.Status(); status.Queued != 0 || status.Running != 0 || status.MaxBacklog != 3000 {
		t.Fatal(status)
	}
	// The first retry comes 30 s after the failure, not before; a
	// notification meanwhile does not bring it forward.
	s.Notify(context.Background(), a, 0)
	clock.advance(29 * time.Second)
	s.signal()
	never(t, ran, "retried before 30 s")
	clock.advance(time.Second)
	s.signal()
	next(t, ran)
	// The second, 60 s after the second failure.
	eventually(t, func() bool { return s.Status().BackingOff == 1 })
	clock.advance(59 * time.Second)
	s.signal()
	never(t, ran, "retried before 60 s")
	clock.advance(time.Second)
	s.signal()
	next(t, ran)
	eventually(t, func() bool { return idle(s) })
	if status := s.Status(); status.BackingOff != 0 || status.MaxBacklog != 0 {
		t.Fatal(status)
	}
	prefix := a[:8]
	if logs.count("consolidation retries in 30s (user "+prefix+")") != 1 || logs.count("consolidation retries in 1m0s (user "+prefix+")") != 1 {
		t.Fatal(logs)
	}
	// After a success the failures start over: 30 s again.
	calls.Store(0)
	s.Notify(context.Background(), a, 1)
	next(t, ran)
	eventually(t, func() bool { return s.Status().BackingOff == 1 })
	if logs.count("consolidation retries in 30s (user "+prefix+")") != 2 {
		t.Fatal(logs)
	}
	clock.advance(30 * time.Second)
	s.signal()
	next(t, ran)
	eventually(t, func() bool { return s.Status().BackingOff == 1 })
	clock.advance(time.Minute)
	s.signal()
	next(t, ran)
	eventually(t, func() bool { return idle(s) })
}

func TestSchedulerPausesForTheBudgetReserve(t *testing.T) {
	clock, logs := newFakeClock(), &logLines{}
	a := userKey("a")
	ran := make(chan string, 10)
	var calls atomic.Int64
	s := newScheduler(1, 100, func(_ context.Context, key string) (int64, error) {
		ran <- key
		if calls.Add(1) == 1 {
			return 3000, fmt.Errorf("%w: %w", memoryagent.ErrLinkDeferred, memoryagent.ErrBackgroundReserve)
		}
		return 0, nil
	}, logs.logf, clock.now)
	startScheduler(t, s)
	s.Notify(context.Background(), a, 3000)
	next(t, ran)
	eventually(t, func() bool { return s.Status().BackingOff == 1 })
	if logs.count("consolidation paused: budget reserve (user "+a[:8]+")") != 1 || logs.count("retries in") != 0 {
		t.Fatal(logs)
	}
	clock.advance(10*time.Minute - time.Second)
	s.signal()
	never(t, ran, "resumed within the 10-minute pause")
	clock.advance(time.Second)
	s.signal()
	next(t, ran)
	eventually(t, func() bool { return idle(s) })
}

func TestRetryDelayJitterAndCap(t *testing.T) {
	for _, tc := range []struct {
		failures int
		jitter   float64
		want     time.Duration
	}{
		{1, 0.5, 30 * time.Second},
		{1, 0, 24 * time.Second},
		{1, 1, 36 * time.Second},
		{2, 0.5, time.Minute},
		{5, 0.5, 8 * time.Minute},
		{5, 0, 384 * time.Second},
		{5, 1, 576 * time.Second},
		// At the cap the jitter stays (parked T6-d): retryMax × (0.8 + 0.2
		// × jitter), so users that keep failing do not retry in step.
		{6, 0, 8 * time.Minute},
		{6, 0.5, 9 * time.Minute},
		{40, 0.5, 9 * time.Minute},
		{40, 1, 10 * time.Minute},
	} {
		if got := retryDelay(tc.failures, tc.jitter); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}

// schedulerGoroutines counts the goroutines running one of the scheduler's
// functions: a count of all goroutines (runtime.NumGoroutine) can hide a
// leaked worker behind another test's goroutine that ended meanwhile.
func schedulerGoroutines() int {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	count := 0
	for _, stack := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(stack, "aml.(*scheduler).") {
			count++
		}
	}
	return count
}

func TestSchedulerStopsAndWaits(t *testing.T) {
	a := userKey("a")
	entered := make(chan struct{})
	var once sync.Once
	var returned atomic.Bool
	s := newScheduler(2, 100, func(ctx context.Context, key string) (int64, error) {
		once.Do(func() { close(entered) })
		<-ctx.Done()
		// The job takes a moment to stop.
		time.Sleep(50 * time.Millisecond)
		returned.Store(true)
		return -1, ctx.Err()
	}, (&logLines{}).logf, time.Now)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	s.Notify(context.Background(), a, 200000)
	<-entered
	waited := make(chan error, 1)
	go func() { waited <- s.WaitBacklog(context.Background(), a, 160000) }()
	cancel()
	select {
	case err := <-done:
		if err != nil || !returned.Load() {
			t.Fatal("Run returned before its job", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
	// A stopped scheduler keeps no Add waiting.
	select {
	case err := <-waited:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("an Add still waits after the scheduler stopped")
	}
	if err := s.WaitBacklog(context.Background(), a, 160000); err != nil {
		t.Fatal(err)
	}
	// No worker is left: no goroutine of the scheduler outlives Run, and
	// nothing runs. The cut job's user is queued again.
	eventually(t, func() bool { return schedulerGoroutines() == 0 })
	s.Notify(context.Background(), userKey("b"), 1)
	time.Sleep(20 * time.Millisecond)
	if status := s.Status(); status.Running != 0 || status.Queued != 2 {
		t.Fatal(status)
	}
}

// within is a context that ends after five seconds, the longest a test waits.
func within(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// Drain is the first step of a graceful shutdown: no job starts any more,
// the job running goes on to its end, and WaitDrained waits for it.
func TestSchedulerDrainStartsNoJob(t *testing.T) {
	a, b := userKey("a"), userKey("b")
	ran, gate := make(chan string, 10), make(chan struct{})
	var cut atomic.Bool
	s := newScheduler(1, 100, func(ctx context.Context, key string) (int64, error) {
		ran <- key
		select {
		case <-gate:
			return 0, nil
		case <-ctx.Done():
			cut.Store(true)
			return -1, ctx.Err()
		}
	}, (&logLines{}).logf, time.Now)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	s.Notify(context.Background(), a, 1)
	if key := next(t, ran); key != a {
		t.Fatal(key)
	}
	s.Notify(context.Background(), b, 1)
	s.Drain()
	s.Drain() // a second call does nothing
	// a's job goes on, uncut: a wait for it that ends first says so.
	short, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	if err := s.WaitDrained(short); !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "consolidation jobs still running: 1") {
		t.Fatal(err)
	}
	if status := s.Status(); status.Running != 1 || cut.Load() {
		t.Fatal("the drain cut the running job", status)
	}
	close(gate)
	if err := s.WaitDrained(within(t)); err != nil {
		t.Fatal(err)
	}
	// b stays queued and no job starts, nor after a notification: the
	// workers have exited, so Run returns before its context ends.
	s.Notify(context.Background(), userKey("c"), 1)
	never(t, ran, "a job started after the drain")
	if err := received(t, done); err != nil {
		t.Fatal(err)
	}
	if status := s.Status(); status.Running != 0 || status.Queued != 2 {
		t.Fatal(status)
	}
	if err := s.WaitDrained(within(t)); err != nil {
		t.Fatal(err)
	}
}

// Once draining, no job will lower a backlog: the Adds waiting on one answer,
// and a later Add does not wait.
func TestSchedulerDrainEndsTheAddsWait(t *testing.T) {
	a := userKey("a")
	ran := make(chan string, 10)
	s := newScheduler(1, 100, func(ctx context.Context, key string) (int64, error) {
		ran <- key
		<-ctx.Done()
		return -1, ctx.Err()
	}, (&logLines{}).logf, time.Now)
	// Run has not started, so nothing would lower a's backlog.
	s.Notify(context.Background(), a, 200000)
	waited := make(chan error, 1)
	go func() { waited <- s.WaitBacklog(context.Background(), a, 160000) }()
	select {
	case err := <-waited:
		t.Fatal("returned above the limit", err)
	case <-time.After(50 * time.Millisecond):
	}
	s.Drain()
	if err := received(t, waited); err != nil {
		t.Fatal(err)
	}
	if err := s.WaitBacklog(context.Background(), a, 160000); err != nil {
		t.Fatal(err)
	}
	// Nothing runs, so WaitDrained returns at once; and a Run that starts
	// after the drain starts no job.
	if err := s.WaitDrained(within(t)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Run(context.Background()) }()
	if err := received(t, done); err != nil {
		t.Fatal(err)
	}
	never(t, ran, "a job ran after the drain")
	if status := s.Status(); status.Queued != 1 || status.MaxBacklog != 200000 {
		t.Fatal(status)
	}
}

func TestWaitBacklogOnAnIdleUserQueuesAJob(t *testing.T) {
	a := userKey("a")
	ran, results := make(chan string, 10), make(chan int64)
	s := newScheduler(1, 100, func(ctx context.Context, key string) (int64, error) {
		ran <- key
		select {
		case backlog := <-results:
			return backlog, nil
		case <-ctx.Done():
			return -1, ctx.Err()
		}
	}, (&logLines{}).logf, time.Now)
	startScheduler(t, s)
	// A job leaves the user idle above the limit (nothing was written while
	// it ran).
	s.Notify(context.Background(), a, 200000)
	next(t, ran)
	results <- 170000
	eventually(t, func() bool { return idle(s) && s.Status().MaxBacklog == 170000 })
	// Waiting on it queues a job, which measures the backlog again.
	waited := make(chan error, 1)
	go func() { waited <- s.WaitBacklog(context.Background(), a, 160000) }()
	next(t, ran)
	results <- 150000
	select {
	case err := <-waited:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the wait did not end")
	}
}

func TestSchedulerQueueBound(t *testing.T) {
	clock, logs := newFakeClock(), &logLines{}
	a, b, c, d := userKey("a"), userKey("b"), userKey("c"), userKey("d")
	ran := make(chan string, 10)
	s := newScheduler(1, 2, func(_ context.Context, key string) (int64, error) {
		ran <- key
		return 0, nil
	}, logs.logf, clock.now)
	// Not running yet: the notifications queue, two users at most.
	s.Notify(context.Background(), a, 1)
	s.Notify(context.Background(), b, 1)
	s.Notify(context.Background(), c, 1)
	if status := s.Status(); status.Queued != 2 || logs.count("consolidation queue full") != 1 || logs.count("(user "+c[:8]+")") != 1 {
		t.Fatal(status, logs)
	}
	// Logged once per minute; a user already queued is no new entry.
	s.Notify(context.Background(), d, 1)
	s.Notify(context.Background(), a, 1)
	if logs.count("consolidation queue full") != 1 {
		t.Fatal(logs)
	}
	clock.advance(time.Minute)
	s.Notify(context.Background(), d, 1)
	if logs.count("consolidation queue full") != 2 {
		t.Fatal(logs)
	}
	// The dropped users have no backlog to wait for.
	if err := s.WaitBacklog(context.Background(), c, 0); err != nil {
		t.Fatal(err)
	}
	startScheduler(t, s)
	got := []string{next(t, ran), next(t, ran)}
	if !slices.Equal(got, []string{a, b}) {
		t.Fatal(got)
	}
	never(t, ran, "a dropped user ran")
	// Their next notification, with room again, picks them up.
	eventually(t, func() bool { return idle(s) })
	s.Notify(context.Background(), c, 1)
	if key := next(t, ran); key != c {
		t.Fatal(key)
	}
}

// oldestQueued asserts the age of the oldest work not yet consolidated.
func oldestQueued(t *testing.T, s *scheduler, want time.Duration) {
	t.Helper()
	if got := s.Status().OldestQueued; got != want {
		t.Fatalf("oldest queued %s, want %s (%+v)", got, want, s.Status())
	}
}

// OldestQueued is the age of the oldest notification no successful job has
// covered. It starts at the first notification and is not restarted by later
// ones, it keeps growing while the user runs, backs off after a failed job and
// is queued again after a job the scheduler's end cut, and it ends with a job
// that succeeds.
func TestSchedulerOldestQueuedAge(t *testing.T) {
	clock := newFakeClock()
	a, b := userKey("a"), userKey("b")
	started, results := make(chan string, 10), make(chan error)
	s := newScheduler(1, 100, func(ctx context.Context, key string) (int64, error) {
		started <- key
		select {
		case err := <-results:
			return 0, err
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}, (&logLines{}).logf, clock.now)
	s.jitter = func() float64 { return 0.5 } // a retry exactly 30 s after the failure

	// Nothing waiting has no age. a's work ages from its first notification,
	// whatever comes after; the oldest of the users counts.
	oldestQueued(t, s, 0)
	s.Notify(context.Background(), a, 1)
	clock.advance(2 * time.Minute)
	s.Notify(context.Background(), b, 1)
	clock.advance(time.Minute)
	s.Notify(context.Background(), a, 1)
	oldestQueued(t, s, 3*time.Minute)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	// a runs first, and its work is as old as before while it does.
	if key := next(t, started); key != a {
		t.Fatal(key)
	}
	clock.advance(time.Minute)
	oldestQueued(t, s, 4*time.Minute)

	// Its job fails: it backs off with its work as old as ever, and b runs.
	results <- errors.New("model HTTP 503")
	if key := next(t, started); key != b {
		t.Fatal(key)
	}
	eventually(t, func() bool { return s.Status().BackingOff == 1 })
	oldestQueued(t, s, 4*time.Minute)
	clock.advance(10 * time.Second)
	oldestQueued(t, s, 4*time.Minute+10*time.Second)

	// b's job covers b's work, so b no longer counts; a is still backing off.
	results <- nil
	eventually(t, func() bool { return s.Status().Running == 0 })
	if status := s.Status(); status.BackingOff != 1 || status.Queued != 0 {
		t.Fatal(status)
	}
	oldestQueued(t, s, 4*time.Minute+10*time.Second)

	// a's retry is due 30 s after its failure, and runs.
	clock.advance(20 * time.Second)
	s.signal()
	if key := next(t, started); key != a {
		t.Fatal(key)
	}
	oldestQueued(t, s, 4*time.Minute+30*time.Second)

	// The scheduler stops with a's job running: the job is cut, a is queued
	// again, and its work keeps its age.
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if status := s.Status(); status.Queued != 1 || status.Running != 0 {
		t.Fatal(status)
	}
	oldestQueued(t, s, 4*time.Minute+30*time.Second)

	// A scheduler that runs again takes the work up, and a job that succeeds
	// ends the age.
	startScheduler(t, s)
	if key := next(t, started); key != a {
		t.Fatal(key)
	}
	results <- nil
	eventually(t, func() bool { return idle(s) })
	oldestQueued(t, s, 0)
}

// A notification that comes while a job runs may be work the job did not
// cover: when the job succeeds, the user's age runs from that notification,
// not from the first.
func TestSchedulerOldestQueuedRestartsAfterACoveredJob(t *testing.T) {
	clock := newFakeClock()
	a := userKey("a")
	started, finish := make(chan string, 10), make(chan struct{})
	s := newScheduler(1, 100, func(ctx context.Context, key string) (int64, error) {
		started <- key
		select {
		case <-finish:
		case <-ctx.Done():
		}
		return 0, nil
	}, (&logLines{}).logf, clock.now)
	startScheduler(t, s)
	s.Notify(context.Background(), a, 1)
	next(t, started)
	clock.advance(time.Minute)
	oldestQueued(t, s, time.Minute)

	s.Notify(context.Background(), a, 1)
	clock.advance(30 * time.Second)
	oldestQueued(t, s, 90*time.Second) // until the job that may not cover it ends
	finish <- struct{}{}
	next(t, started) // the user runs again
	oldestQueued(t, s, 30*time.Second)
	finish <- struct{}{}
	eventually(t, func() bool { return idle(s) })
	oldestQueued(t, s, 0)
}

// The scheduler reports how each job ended, while the job still counts as
// running, so before the state that tells of it can be seen: ok, failed, paused (the budget reserve stopped it) or canceled
// (the scheduler's end cut it).
func TestSchedulerReportsJobOutcomes(t *testing.T) {
	clock := newFakeClock()
	a, b, c, d := userKey("a"), userKey("b"), userKey("c"), userKey("d")
	failures := map[string]error{
		b: errors.New("model HTTP 503"),
		c: fmt.Errorf("%w: %w", memoryagent.ErrLinkDeferred, memoryagent.ErrBackgroundReserve),
	}
	ran := make(chan string, 10)
	s := newScheduler(1, 100, func(ctx context.Context, key string) (int64, error) {
		ran <- key
		if key == d {
			<-ctx.Done()
			return -1, ctx.Err()
		}
		return 0, failures[key]
	}, (&logLines{}).logf, clock.now)
	var mu sync.Mutex
	outcomes, running := []string{}, []int{}
	s.jobDone = func(outcome string) {
		// Told before the job stops counting as running, and so before its
		// user is seen queued, backing off or idle.
		status := s.Status()
		mu.Lock()
		outcomes, running = append(outcomes, outcome), append(running, status.Running)
		mu.Unlock()
	}
	reported := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(outcomes)
	}
	for _, key := range []string{a, b, c, d} {
		s.Notify(context.Background(), key, 1)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	for _, key := range []string{a, b, c, d} {
		if got := next(t, ran); got != key {
			t.Fatal(got, key)
		}
	}
	// Once b and c are seen backing off, their outcomes have been reported.
	eventually(t, func() bool { return s.Status().BackingOff == 2 })
	if got := reported(); !slices.Equal(got, []string{"ok", "failed", "paused"}) {
		t.Fatal(got)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := reported(); !slices.Equal(got, []string{"ok", "failed", "paused", "canceled"}) {
		t.Fatal(got)
	}
	mu.Lock()
	if !slices.Equal(running, []int{1, 1, 1, 1}) {
		t.Fatal("a job was told as done after its state had changed:", running)
	}
	mu.Unlock()
	if !slices.Equal(jobOutcomes, []string{"ok", "failed", "paused", "canceled"}) {
		t.Fatal(jobOutcomes)
	}
}

// The scheduler's estimate of a user's backlog is known once a job has ended
// and measured it; before that it is only what Adds have been told to it,
// and a user it has dropped (idle, nothing pending) has none.
func TestSchedulerBacklogIsKnownOnceMeasured(t *testing.T) {
	a, b, c := userKey("a"), userKey("b"), userKey("c")
	started := make(chan string, 10)
	measure := make(chan int64)
	s := newScheduler(1, 100, func(ctx context.Context, key string) (int64, error) {
		started <- key
		select {
		case backlog := <-measure:
			return backlog, nil
		case <-ctx.Done():
			return -1, ctx.Err()
		}
	}, (&logLines{}).logf, time.Now)
	unknown := func(key, what string) {
		t.Helper()
		if chars, known := s.Backlog(key); known || chars != 0 {
			t.Fatalf("%s: %d, known %v", what, chars, known)
		}
	}
	known := func(key string, want int64, what string) {
		t.Helper()
		if chars, known := s.Backlog(key); !known || chars != want {
			t.Fatalf("%s: %d, known %v, want %d", what, chars, known, want)
		}
	}
	unknown(a, "a user the scheduler has never heard of")

	// Notified of 500 characters, the scheduler has a number it has not
	// measured: not an estimate to show.
	s.Notify(context.Background(), a, 500)
	if status := s.Status(); status.MaxBacklog != 500 {
		t.Fatal(status)
	}
	unknown(a, "notified, no job yet")
	startScheduler(t, s)
	if key := next(t, started); key != a {
		t.Fatal(key)
	}
	unknown(a, "the first job running")

	// The job measures 120: known from then on, and Adds add to it.
	measure <- 120
	eventually(t, func() bool { _, known := s.Backlog(a); return known })
	known(a, 120, "after the first job")
	s.Notify(context.Background(), a, 30)
	known(a, 150, "after a notification")
	if key := next(t, started); key != a {
		t.Fatal(key)
	}
	known(a, 150, "the second job running")

	// A job that finds nothing pending: the scheduler drops the user.
	measure <- 0
	eventually(t, func() bool { _, known := s.Backlog(a); return !known })
	unknown(a, "nothing pending")

	// A job that cannot measure does not make a number known.
	s.Notify(context.Background(), b, 10)
	if key := next(t, started); key != b {
		t.Fatal(key)
	}
	measure <- -1
	eventually(t, func() bool { return idle(s) })
	unknown(b, "a job that could not measure")

	// A user held by a Search is known to the scheduler and has no estimate.
	release := s.Hold(c)
	unknown(c, "held")
	release()
}

// Parked T6-a: a measure taken outside a job, by an Add of a user the
// scheduler had no measure of, is the estimate from then on, with what Adds
// add after it. A user the scheduler has measured keeps its measure, one
// whose job runs keeps the job's to come, and an unknown user gains none.
func TestSchedulerTakesAnAddsMeasure(t *testing.T) {
	a, b, stranger := userKey("a"), userKey("b"), userKey("stranger")
	started := make(chan string, 10)
	measure := make(chan int64)
	s := newScheduler(1, 100, func(ctx context.Context, key string) (int64, error) {
		started <- key
		select {
		case backlog := <-measure:
			return backlog, nil
		case <-ctx.Done():
			return -1, ctx.Err()
		}
	}, (&logLines{}).logf, time.Now)
	s.Measured(stranger, 100)
	if _, known := s.Backlog(stranger); known {
		t.Fatal("a measure made a user the scheduler does not know")
	}
	s.Notify(context.Background(), a, 50)
	s.Measured(a, 900)
	if chars, known := s.Backlog(a); !known || chars != 900 {
		t.Fatal("after the measure:", chars, known)
	}
	s.Measured(a, 10)
	s.Notify(context.Background(), a, 30)
	if chars, known := s.Backlog(a); !known || chars != 930 {
		t.Fatal("a second measure, then an Add:", chars, known)
	}
	// While b's job runs, the job's own measure wins.
	startScheduler(t, s)
	if key := next(t, started); key != a {
		t.Fatal(key)
	}
	measure <- 0
	s.Notify(context.Background(), b, 40)
	if key := next(t, started); key != b {
		t.Fatal(key)
	}
	s.Measured(b, 5000)
	if _, known := s.Backlog(b); known {
		t.Fatal("a measure taken while b's job ran")
	}
	measure <- 70
	eventually(t, func() bool { chars, known := s.Backlog(b); return known && chars == 70 })
}

// The scheduler remembers which users it has measured since it started, a
// job or an Add, also once it has forgotten one whose backlog measured 0: an
// Add measures a user only once a start, not on every Add of a user that
// keeps catching up.
func TestSchedulerRemembersWhomItMeasured(t *testing.T) {
	a, b := userKey("a"), userKey("b")
	started := make(chan string, 10)
	measure := make(chan int64)
	s := newScheduler(1, 100, func(ctx context.Context, key string) (int64, error) {
		started <- key
		select {
		case backlog := <-measure:
			return backlog, nil
		case <-ctx.Done():
			return -1, ctx.Err()
		}
	}, (&logLines{}).logf, time.Now)
	if s.HasMeasured(a) || s.HasMeasured(b) {
		t.Fatal("measured before anything")
	}
	s.Notify(context.Background(), a, 50)
	s.Measured(a, 900)
	if !s.HasMeasured(a) || s.HasMeasured(b) {
		t.Fatal("after an Add's measure:", s.HasMeasured(a), s.HasMeasured(b))
	}
	startScheduler(t, s)
	if key := next(t, started); key != a {
		t.Fatal(key)
	}
	measure <- 0 // a caught up: forgotten
	eventually(t, func() bool { _, known := s.Backlog(a); return !known && idle(s) })
	s.Notify(context.Background(), b, 10)
	if key := next(t, started); key != b {
		t.Fatal(key)
	}
	measure <- 0
	eventually(t, func() bool { return idle(s) })
	if !s.HasMeasured(a) || !s.HasMeasured(b) {
		t.Fatal("forgotten, so measured no more:", s.HasMeasured(a), s.HasMeasured(b))
	}
}
