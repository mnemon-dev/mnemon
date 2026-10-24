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

func (l *logLines) logf(format string, args ...any) {
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
		s.Notify(keys[i%3], 171)
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
	s.Notify(x, 1)
	if key := next(t, ran); key != x {
		t.Fatal(key)
	}
	s.Notify(a, 1)
	s.Notify(b, 1)
	s.Notify(c, 1)
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
	s.Notify(a, 1)
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
	s.Notify(a, 200000)
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
	s.Notify(a, 0)
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
	s.Notify(a, 5000)
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
	s.Notify(a, 3000)
	next(t, ran)
	eventually(t, func() bool { return s.Status().BackingOff == 1 })
	if status := s.Status(); status.Queued != 0 || status.Running != 0 || status.MaxBacklog != 3000 {
		t.Fatal(status)
	}
	// The first retry comes 30 s after the failure, not before; a
	// notification meanwhile does not bring it forward.
	s.Notify(a, 0)
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
	s.Notify(a, 1)
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
	s.Notify(a, 3000)
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
		{6, 0, 10 * time.Minute},
		{40, 0.5, 10 * time.Minute},
	} {
		if got := retryDelay(tc.failures, tc.jitter); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}

func TestSchedulerStopsAndWaits(t *testing.T) {
	before := runtime.NumGoroutine()
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
	s.Notify(a, 200000)
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
	// No worker is left: no goroutine outlives Run, and nothing runs. The
	// cut job's user is queued again.
	eventually(t, func() bool { return runtime.NumGoroutine() <= before })
	s.Notify(userKey("b"), 1)
	time.Sleep(20 * time.Millisecond)
	if status := s.Status(); status.Running != 0 || status.Queued != 2 {
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
	s.Notify(a, 200000)
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
	s.Notify(a, 1)
	s.Notify(b, 1)
	s.Notify(c, 1)
	if status := s.Status(); status.Queued != 2 || logs.count("consolidation queue full") != 1 || logs.count("(user "+c[:8]+")") != 1 {
		t.Fatal(status, logs)
	}
	// Logged once per minute; a user already queued is no new entry.
	s.Notify(d, 1)
	s.Notify(a, 1)
	if logs.count("consolidation queue full") != 1 {
		t.Fatal(logs)
	}
	clock.advance(time.Minute)
	s.Notify(d, 1)
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
	s.Notify(c, 1)
	if key := next(t, ran); key != c {
		t.Fatal(key)
	}
}
