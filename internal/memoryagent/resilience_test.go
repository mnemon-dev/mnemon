package memoryagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/failsafe-go/failsafe-go/bulkhead"
	"github.com/failsafe-go/failsafe-go/circuitbreaker"
)

// endpoint is an httptest model endpoint: answer writes the reply to its
// n-th request (n from 1). It keeps when each request arrived.
type endpoint struct {
	*httptest.Server
	mu       sync.Mutex
	arrivals []time.Time
}

func newEndpoint(t *testing.T, answer func(n int, w http.ResponseWriter)) *endpoint {
	t.Helper()
	e := &endpoint{}
	e.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		e.mu.Lock()
		e.arrivals = append(e.arrivals, time.Now())
		n := len(e.arrivals)
		e.mu.Unlock()
		answer(n, w)
	}))
	t.Cleanup(e.Close)
	return e
}

func (e *endpoint) requests() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.arrivals)
}

func (e *endpoint) arrived(i int) time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.arrivals[i]
}

// deepseekReply is a DeepSeek completion reply with its metering.
const deepseekReply = `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":10}}`

// deepseekOn is a metered DeepSeek provider whose requests go to e
// (TestEndpoint), with config's resilience settings and a budget file of its
// own (caps nil for the defaults). The provider is closed when the test ends,
// before its budget.
func deepseekOn(t *testing.T, e *endpoint, config ModelConfig, caps map[string]float64) (*Provider, *Budget) {
	t.Helper()
	budget, err := OpenBudgetWithCaps(filepath.Join(t.TempDir(), "budget.db"), caps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { budget.Close() })
	t.Setenv("UNIT_DEEPSEEK_CREDENTIAL", "test-only-placeholder")
	config.Kind, config.Model, config.KeyEnv, config.TestEndpoint = "deepseek", "deepseek-flash", "UNIT_DEEPSEEK_CREDENTIAL", e.URL
	p, err := NewProvider(config, budget)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p, budget
}

// jevOn is deepseekOn for a JEV provider.
func jevOn(t *testing.T, e *endpoint, config ModelConfig) (*Provider, *Budget) {
	t.Helper()
	budget, err := OpenBudget(filepath.Join(t.TempDir(), "budget.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { budget.Close() })
	t.Setenv("UNIT_JEV_CREDENTIAL", "test-only-placeholder")
	config.Kind, config.Model, config.KeyEnv, config.TestEndpoint = "jev", "jev-1.13.0", "UNIT_JEV_CREDENTIAL", e.URL
	p, err := NewProvider(config, budget)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p, budget
}

// attemptLog is a ModelObserver that keeps the attempts it is shown.
type attemptLog struct {
	mu     sync.Mutex
	events []AttemptEvent
}

func (l *attemptLog) ModelAttempt(e AttemptEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
}

func (l *attemptLog) seen() []AttemptEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]AttemptEvent{}, l.events...)
}

// outcomes are the logged attempts' outcomes, space separated.
func (l *attemptLog) outcomes() string {
	out := []string{}
	for _, e := range l.seen() {
		out = append(out, e.Outcome)
	}
	return strings.Join(out, " ")
}

// freePermits counts the permits b has free, taking none for good.
func freePermits(b bulkhead.Bulkhead[any]) int {
	n := 0
	for b.TryAcquirePermit() {
		n++
	}
	for range n {
		b.ReleasePermit()
	}
	return n
}

// settled reports whether every attempt settled at a positive price.
func settled(attempts []attempt) bool {
	for _, a := range attempts {
		if a.actual == nil || *a.actual <= 0 {
			return false
		}
	}
	return true
}

func TestRetriesOn503ThenSucceeds(t *testing.T) {
	e := newEndpoint(t, func(n int, w http.ResponseWriter) {
		if n <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		io.WriteString(w, deepseekReply)
	})
	p, budget := deepseekOn(t, e, ModelConfig{}, nil)
	log := &attemptLog{}
	Models{Planner: p}.Observe(log)
	text, usage, err := p.Complete(context.Background(), "s", "p")
	if err != nil || text != "ok" || usage.CNY <= 0 || e.requests() != 3 {
		t.Fatal(text, usage, err, e.requests())
	}
	// Every attempt reserved: the two refused ones were released at 0, the
	// one that answered settled at its metering.
	attempts := budgetAttempts(t, budget)
	if len(attempts) != 3 || attempts[0].actual == nil || *attempts[0].actual != 0 || attempts[1].actual == nil || *attempts[1].actual != 0 || !settled(attempts[2:]) || !near(*attempts[2].actual, usage.CNY) {
		t.Fatal(attempts)
	}
	if held, _ := accountHeld(t, budget, "deepseek"); !near(held, 0) {
		t.Fatal("held", held)
	}
	if got := log.outcomes(); got != "retryable retryable ok" {
		t.Fatal(got)
	}
	for i, event := range log.seen() {
		if event.Kind != "deepseek" || event.Class != "interactive" || event.Attempt != i+1 || event.Retried != (i > 0) || event.Status != []int{503, 503, 200}[i] {
			t.Fatal(i, event)
		}
	}
}

func TestHonoursRetryAfter(t *testing.T) {
	e := newEndpoint(t, func(n int, w http.ResponseWriter) {
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		io.WriteString(w, deepseekReply)
	})
	p, _ := deepseekOn(t, e, ModelConfig{}, nil)
	if _, _, err := p.Complete(context.Background(), "s", "p"); err != nil || e.requests() != 2 {
		t.Fatal(err, e.requests())
	}
	if gap := e.arrived(1).Sub(e.arrived(0)); gap < time.Second {
		t.Fatal("retried before its Retry-After", gap)
	}
	// Retry-After is delta seconds or an HTTP date. Asked to wait 30 s, an
	// Interactive call waits 10 s, its most; a Background one the 30 s, up to
	// 5 min. The backoff never shortens it.
	now := time.Now()
	thirty := parseRetryAfter("30", now)
	date := parseRetryAfter(now.Add(30*time.Second).UTC().Format(http.TimeFormat), now)
	if thirty != 30*time.Second || date <= 29*time.Second || date > 30*time.Second {
		t.Fatal(thirty, date)
	}
	for _, header := range []string{"", "soon", "-5", now.Add(-time.Minute).UTC().Format(http.TimeFormat)} {
		if wait := parseRetryAfter(header, now); wait != 0 {
			t.Fatal(header, wait)
		}
	}
	for _, random := range []float64{0, 0.5, 0.999} {
		interactive, background := p.policies[Interactive], p.policies[Background]
		if wait := interactive.retryDelay(1, thirty, random); wait != 10*time.Second {
			t.Fatal("interactive", random, wait)
		}
		if wait := background.retryDelay(1, thirty, random); wait != 30*time.Second {
			t.Fatal("background", random, wait)
		}
		if wait := background.retryDelay(1, time.Hour, random); wait != 5*time.Minute {
			t.Fatal("background cap", random, wait)
		}
	}
}

func TestRetryDelayBacksOffWithFullJitter(t *testing.T) {
	p, err := NewProvider(ModelConfig{Kind: "local", Model: "m", BaseURL: "http://127.0.0.1:1/v1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	// Retry n waits a random part of first×2^(n−1), at most the class's
	// longest backoff: 0.5 s → 8 s for Interactive calls, 2 s → 60 s for
	// Flush and Background ones.
	for class, ceilings := range map[CallClass][]time.Duration{
		Interactive: {500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second},
		Flush:       {2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, 60 * time.Second},
		Background:  {2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, 60 * time.Second},
	} {
		policy := p.policies[class]
		for i, ceiling := range ceilings {
			retry := i + 1
			if wait := policy.retryDelay(retry, 0, 0); wait != 0 {
				t.Fatal(class, retry, wait)
			}
			if wait := policy.retryDelay(retry, 0, 0.5); wait != ceiling/2 {
				t.Fatal(class, retry, wait)
			}
			if wait := policy.retryDelay(retry, 0, 0.999); wait >= ceiling || wait < ceiling*99/100 {
				t.Fatal(class, retry, wait)
			}
		}
	}
}

func TestNoRetryOn400And401(t *testing.T) {
	for _, code := range []int{http.StatusBadRequest, http.StatusUnauthorized} {
		e := newEndpoint(t, func(_ int, w http.ResponseWriter) { w.WriteHeader(code) })
		p, budget := deepseekOn(t, e, ModelConfig{}, nil)
		_, _, err := p.Complete(context.Background(), "s", "p")
		if err == nil || !strings.Contains(err.Error(), fmt.Sprint(code)) || e.requests() != 1 || len(budgetAttempts(t, budget)) != 1 {
			t.Fatal(code, err, e.requests())
		}
		// A refused credential refuses every request alike (unavailable); a
		// 400 is about this one.
		if errors.Is(err, ErrModelUnavailable) != (code == http.StatusUnauthorized) {
			t.Fatal(code, err)
		}
	}
}

func TestAttemptTimeoutRetries(t *testing.T) {
	e := newEndpoint(t, func(n int, w http.ResponseWriter) {
		if n == 1 {
			time.Sleep(3 * time.Second)
		}
		io.WriteString(w, deepseekReply)
	})
	p, budget := deepseekOn(t, e, ModelConfig{AttemptTimeoutSeconds: 1, TimeoutSeconds: 5}, nil)
	start := time.Now()
	text, _, err := p.Complete(context.Background(), "s", "p")
	if elapsed := time.Since(start); err != nil || text != "ok" || e.requests() != 2 || elapsed < time.Second || elapsed > 2500*time.Millisecond {
		t.Fatal(text, err, e.requests(), elapsed)
	}
	// The first attempt was abandoned, not canceled: its request runs on,
	// holding its reservation and its bulkhead permit.
	if free := freePermits(p.policies[Interactive].calls); free != interactiveCalls-1 {
		t.Fatal("the abandoned request returned its permit", free)
	}
	if held, _ := accountHeld(t, budget, "deepseek"); held < 0.25 {
		t.Fatal("the abandoned request released its reservation", held)
	}
	// Close waits for it; then both attempts are settled and nothing is held.
	if err := p.Close(); err != nil || time.Since(start) < 3*time.Second {
		t.Fatal("Close did not wait for the abandoned request", err, time.Since(start))
	}
	if attempts := budgetAttempts(t, budget); len(attempts) != 2 || !settled(attempts) {
		t.Fatal(attempts)
	}
	if held, _ := accountHeld(t, budget, "deepseek"); !near(held, 0) {
		t.Fatal("held", held)
	}
	if free := freePermits(p.policies[Interactive].calls); free != interactiveCalls {
		t.Fatal("permits", free)
	}
}

// A Background attempt is not abandoned: it waits its hard timeout, whatever
// the Interactive attempt timeout.
func TestBackgroundAttemptsWaitTheirHardTimeout(t *testing.T) {
	e := newEndpoint(t, func(_ int, w http.ResponseWriter) {
		time.Sleep(1500 * time.Millisecond)
		io.WriteString(w, deepseekReply)
	})
	p, budget := deepseekOn(t, e, ModelConfig{AttemptTimeoutSeconds: 1, TimeoutSeconds: 5}, nil)
	log := &attemptLog{}
	Models{Consolidator: p}.Observe(log)
	start := time.Now()
	text, _, err := p.Complete(WithCallClass(context.Background(), Background), "s", "p")
	if elapsed := time.Since(start); err != nil || text != "ok" || e.requests() != 1 || elapsed < 1500*time.Millisecond {
		t.Fatal(text, err, e.requests(), elapsed)
	}
	if got := log.outcomes(); got != "ok" || !settled(budgetAttempts(t, budget)) {
		t.Fatal(got, budgetAttempts(t, budget))
	}
}

// reserveFixture is a DeepSeek provider whose account has a cap of 1.0 with
// 0.70 spent: a call's flat 0.25 reservation fits the cap (0.95), not the
// 0.9 of it a Background call may fill.
func reserveFixture(t *testing.T) (*endpoint, *Provider, *Budget) {
	t.Helper()
	e := newEndpoint(t, func(_ int, w http.ResponseWriter) { io.WriteString(w, deepseekReply) })
	p, budget := deepseekOn(t, e, ModelConfig{}, map[string]float64{"deepseek": 1})
	if _, err := budget.db.Exec(`INSERT INTO accounts(kind,cap,spent) VALUES('deepseek',1,0.7)`); err != nil {
		t.Fatal(err)
	}
	return e, p, budget
}

func TestFlushClassSkipsReserve(t *testing.T) {
	e, p, _ := reserveFixture(t)
	ctx := context.Background()
	if _, _, err := p.Complete(WithCallClass(ctx, Background), "s", "p"); !errors.Is(err, ErrBackgroundReserve) || e.requests() != 0 {
		t.Fatal("a Background call reserved into the reserve", err, e.requests())
	}
	if text, _, err := p.Complete(WithCallClass(ctx, Flush), "s", "p"); err != nil || text != "ok" || e.requests() != 1 {
		t.Fatal("a Flush call kept the reserve", err, e.requests())
	}
}

func TestBackgroundKeepsReserve(t *testing.T) {
	e, p, budget := reserveFixture(t)
	log := &attemptLog{}
	Models{Consolidator: p}.Observe(log)
	ctx := context.Background()
	_, _, err := p.Complete(WithCallClass(ctx, Background), "s", "p")
	if !errors.Is(err, ErrBackgroundReserve) || !errors.Is(err, ErrBudgetCap) || e.requests() != 0 || len(budgetAttempts(t, budget)) != 0 {
		t.Fatal(err, e.requests(), budgetAttempts(t, budget))
	}
	if events := log.seen(); len(events) != 1 || events[0].Outcome != "budget" || events[0].Class != "background" {
		t.Fatal(events)
	}
	if text, _, err := p.Complete(ctx, "s", "p"); err != nil || text != "ok" || e.requests() != 1 {
		t.Fatal("an Interactive call was refused", err)
	}
	// Past the cap itself the refusal is the cap's.
	if _, err := budget.db.Exec(`UPDATE accounts SET spent=0.9`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.Complete(WithCallClass(ctx, Background), "s", "p"); !errors.Is(err, ErrBudgetCap) || errors.Is(err, ErrBackgroundReserve) {
		t.Fatal(err)
	}
}

func TestRespectsCallerDeadline(t *testing.T) {
	e := newEndpoint(t, func(_ int, w http.ResponseWriter) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	p, _ := deepseekOn(t, e, ModelConfig{}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	_, _, err := p.Complete(ctx, "s", "p")
	// Each retry waits a second; the one that would start past the deadline
	// is not made, and the call says what the provider answered.
	if elapsed := time.Since(start); elapsed > 2500*time.Millisecond || !errors.Is(err, ErrModelUnavailable) || !strings.Contains(err.Error(), "503") {
		t.Fatal(elapsed, err)
	}
	if elapsed := time.Since(start); elapsed > 1900*time.Millisecond || e.requests() != 2 {
		t.Fatal("the call waited for its deadline", elapsed, e.requests())
	}
}

func TestBreakerOpensAndFailsFast(t *testing.T) {
	e := newEndpoint(t, func(_ int, w http.ResponseWriter) { w.WriteHeader(http.StatusServiceUnavailable) })
	p, budget := deepseekOn(t, e, ModelConfig{MaxAttempts: 1}, nil)
	log := &attemptLog{}
	Models{Planner: p}.Observe(log)
	ctx := context.Background()
	for i := range breakerConsecutive {
		if _, _, err := p.Complete(ctx, "s", "p"); !errors.Is(err, ErrModelUnavailable) || errors.Is(err, ErrBreakerOpen) {
			t.Fatal(i, err)
		}
	}
	start := time.Now()
	_, _, err := p.Complete(ctx, "s", "p")
	if elapsed := time.Since(start); !errors.Is(err, ErrBreakerOpen) || elapsed > 10*time.Millisecond {
		t.Fatal(err, elapsed)
	}
	if e.requests() != breakerConsecutive || len(budgetAttempts(t, budget)) != breakerConsecutive {
		t.Fatal("the open breaker sent or reserved", e.requests(), len(budgetAttempts(t, budget)))
	}
	if got := log.outcomes(); got != strings.Repeat("retryable ", breakerConsecutive)+"breaker-open" {
		t.Fatal(got)
	}
}

func TestBulkheadFullFailsAfterAttemptTimeout(t *testing.T) {
	e := newEndpoint(t, func(_ int, w http.ResponseWriter) { io.WriteString(w, deepseekReply) })
	p, budget := deepseekOn(t, e, ModelConfig{AttemptTimeoutSeconds: 1}, nil)
	log := &attemptLog{}
	Models{Planner: p}.Observe(log)
	interactive, background := p.policies[Interactive].calls, p.policies[Background].calls
	for range interactiveCalls {
		interactive.TryAcquirePermit()
	}
	for range backgroundCalls {
		background.TryAcquirePermit()
	}
	defer func() {
		for range interactiveCalls {
			interactive.ReleasePermit()
		}
		for range backgroundCalls {
			background.ReleasePermit()
		}
	}()
	// An Interactive call waits for a permit up to its attempt timeout, then
	// fails unsent; it is not retried and says nothing about the provider.
	start := time.Now()
	_, _, err := p.Complete(context.Background(), "s", "p")
	if elapsed := time.Since(start); !errors.Is(err, ErrBulkheadFull) || elapsed < time.Second || elapsed > 2*time.Second {
		t.Fatal(err, elapsed)
	}
	if e.requests() != 0 || len(budgetAttempts(t, budget)) != 0 || log.outcomes() != "bulkhead-full" || p.breaker.cb.Metrics().Executions() != 0 {
		t.Fatal(e.requests(), log.outcomes(), p.breaker.cb.Metrics().Executions())
	}
	// A full Background bulkhead leaves Flush calls their own.
	if _, _, err := p.Complete(WithCallClass(context.Background(), Flush), "s", "p"); err != nil || e.requests() != 1 {
		t.Fatal(err, e.requests())
	}
}

func TestRetryBudgetBoundsRetries(t *testing.T) {
	b := &retryBudget{}
	now := time.Unix(1_000_000, 0)
	// Without calls, 5 retries a second are allowed.
	for i := range 5 {
		if !b.allow(now) {
			t.Fatal(i)
		}
	}
	if b.allow(now) {
		t.Fatal("a sixth retry in one second")
	}
	if !b.allow(now.Add(time.Second)) {
		t.Fatal("the floor did not renew the next second")
	}
	// 200 calls over the minute allow retries up to 20% of them, 40.
	for range 200 {
		b.called(now.Add(2 * time.Second))
	}
	allowed := 0
	for b.allow(now.Add(2 * time.Second)) {
		allowed++
	}
	if allowed != 40-6 {
		t.Fatal(allowed)
	}
	// A minute later the window holds none of them.
	for i := range 5 {
		if !b.allow(now.Add(63 * time.Second)) {
			t.Fatal(i)
		}
	}
	if b.allow(now.Add(63 * time.Second)) {
		t.Fatal("expired calls still allow retries")
	}
}

func TestQueuedRequestNeverSentIsReleased(t *testing.T) {
	gate := make(chan struct{})
	e := newEndpoint(t, func(_ int, w http.ResponseWriter) {
		<-gate
		io.WriteString(w, deepseekReply)
	})
	p, budget := deepseekOn(t, e, ModelConfig{}, nil)
	p.client.Transport = newTransport(2)
	ctx := context.Background()
	var calls sync.WaitGroup
	defer calls.Wait()
	opened := false
	defer func() {
		if !opened {
			close(gate)
		}
	}()
	for i := range 2 {
		calls.Add(1)
		go func() {
			defer calls.Done()
			if _, _, err := p.Complete(ctx, "s", fmt.Sprint("p", i)); err != nil {
				t.Error(err)
			}
		}()
	}
	for deadline := time.Now().Add(5 * time.Second); e.requests() < 2; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the two calls did not take both connections")
		}
	}
	// A third call waits inside the transport for one of the two connections
	// until its own deadline: it never reached the endpoint, so its
	// reservation is released.
	short, cancel := context.WithTimeout(ctx, 400*time.Millisecond)
	defer cancel()
	_, _, err := p.Complete(short, "s", "p2")
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "reservation released") || e.requests() != 2 {
		t.Fatal(err, e.requests())
	}
	close(gate)
	opened = true
	calls.Wait()
	attempts := budgetAttempts(t, budget)
	if len(attempts) != 3 || !settled(attempts[:2]) || attempts[2].actual == nil || *attempts[2].actual != 0 {
		t.Fatal(attempts)
	}
	if held, _ := accountHeld(t, budget, "deepseek"); !near(held, 0) {
		t.Fatal("held", held)
	}
}

// Over the provider's own http.Transport, a request that never obtained a
// connection sent nothing, however it failed: here at its proxy, before the
// transport asked for a connection.
func TestRequestFailingBeforeAConnectionIsReleased(t *testing.T) {
	e := newEndpoint(t, func(_ int, w http.ResponseWriter) { io.WriteString(w, deepseekReply) })
	p, budget := deepseekOn(t, e, ModelConfig{MaxAttempts: 1}, nil)
	transport := newTransport(providerConns)
	transport.Proxy = func(*http.Request) (*url.URL, error) { return nil, errors.New("malformed proxy setting") }
	p.client.Transport = transport
	_, _, err := p.Complete(context.Background(), "s", "p")
	if !errors.Is(err, ErrModelUnavailable) || !strings.Contains(err.Error(), "reservation released") || e.requests() != 0 {
		t.Fatal(err, e.requests())
	}
	if attempts := budgetAttempts(t, budget); len(attempts) != 1 || attempts[0].actual == nil || *attempts[0].actual != 0 {
		t.Fatal(attempts)
	}
	if held, _ := accountHeld(t, budget, "deepseek"); !near(held, 0) {
		t.Fatal("held", held)
	}
}

func TestCacheHitMakesNoAttempt(t *testing.T) {
	e := newEndpoint(t, func(_ int, w http.ResponseWriter) { io.WriteString(w, deepseekReply) })
	p, budget := deepseekOn(t, e, ModelConfig{}, nil)
	p.cache = openTestCache(t, filepath.Join(t.TempDir(), "model-cache.db"))
	log := &attemptLog{}
	Models{Planner: p}.Observe(log)
	ctx := context.Background()
	if _, usage, err := p.Complete(ctx, "s", "p"); err != nil || usage.Cached != 0 || e.requests() != 1 {
		t.Fatal(usage, err)
	}
	// The recorded reply answers: nothing is sent, reserved or attempted.
	text, usage, err := p.Complete(ctx, "s", "p")
	if err != nil || text != "ok" || usage.Cached != 1 || e.requests() != 1 || len(budgetAttempts(t, budget)) != 1 || len(log.seen()) != 1 {
		t.Fatal(text, usage, err, e.requests(), len(log.seen()))
	}
}

func TestTestEndpointRejectsNonLoopback(t *testing.T) {
	budget, err := OpenBudget(filepath.Join(t.TempDir(), "budget.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer budget.Close()
	t.Setenv("UNIT_DEEPSEEK_CREDENTIAL", "test-only-placeholder")
	t.Setenv("UNIT_JEV_CREDENTIAL", "test-only-placeholder")
	deepseek := ModelConfig{Kind: "deepseek", Model: "deepseek-flash", KeyEnv: "UNIT_DEEPSEEK_CREDENTIAL"}
	jev := ModelConfig{Kind: "jev", Model: "jev-1.13.0", KeyEnv: "UNIT_JEV_CREDENTIAL"}
	for _, test := range []string{"http://example.com", "https://127.0.0.1:1", "http://user@127.0.0.1:1", "http://127.0.0.1:1/?x=1", "127.0.0.1:1", "http://10.0.0.1:80"} {
		config := deepseek
		config.TestEndpoint = test
		if _, err := NewProvider(config, budget); err == nil {
			t.Fatal("test endpoint accepted", test)
		}
	}
	if _, err := NewProvider(ModelConfig{Kind: "local", Model: "m", BaseURL: "http://127.0.0.1:1/v1", TestEndpoint: "http://127.0.0.1:2"}, nil); err == nil {
		t.Fatal("a local model took a test endpoint")
	}
	// A loopback one replaces the official endpoint.
	for _, config := range []ModelConfig{deepseek, jev} {
		config.TestEndpoint = "http://127.0.0.1:9/v1"
		p, err := NewProvider(config, budget)
		if err != nil || p.endpoint != "http://127.0.0.1:9/v1" {
			t.Fatal(config.Kind, err)
		}
		p.Close()
	}
}

func TestClassPoliciesByKindAndConfig(t *testing.T) {
	budget, err := OpenBudget(filepath.Join(t.TempDir(), "budget.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer budget.Close()
	t.Setenv("UNIT_JEV_CREDENTIAL", "test-only-placeholder")
	if providerConns != interactiveCalls+flushCalls+backgroundCalls || interactiveCalls != 32 || flushCalls != 8 || backgroundCalls != 8 {
		t.Fatal(providerConns, interactiveCalls, flushCalls, backgroundCalls)
	}
	type want struct {
		timeout  [3]time.Duration // Interactive, Flush, Background
		attempts [3]int
	}
	for _, c := range []struct {
		config ModelConfig
		want   want
	}{
		{ModelConfig{Kind: "local", Model: "m", BaseURL: "http://127.0.0.1:1/v1"}, want{[3]time.Duration{45 * time.Second, 90 * time.Second, 90 * time.Second}, [3]int{4, 3, 3}}},
		// A Flush attempt waits at most 180 s (R62), within the hard timeout.
		{ModelConfig{Kind: "local", Model: "m", BaseURL: "http://127.0.0.1:1/v1", TimeoutSeconds: 600}, want{[3]time.Duration{45 * time.Second, 180 * time.Second, 600 * time.Second}, [3]int{4, 3, 3}}},
		{ModelConfig{Kind: "local", Model: "m", BaseURL: "http://127.0.0.1:1/v1", TimeoutSeconds: 30}, want{[3]time.Duration{30 * time.Second, 30 * time.Second, 30 * time.Second}, [3]int{4, 3, 3}}},
		// Background attempts take the hard timeout, for every kind: they are
		// never abandoned. attemptTimeoutSeconds is the Interactive one.
		{ModelConfig{Kind: "jev", Model: "jev-1.13.0", KeyEnv: "UNIT_JEV_CREDENTIAL"}, want{[3]time.Duration{20 * time.Second, 90 * time.Second, 90 * time.Second}, [3]int{4, 3, 3}}},
		{ModelConfig{Kind: "jev", Model: "jev-1.13.0", KeyEnv: "UNIT_JEV_CREDENTIAL", TimeoutSeconds: 45}, want{[3]time.Duration{20 * time.Second, 45 * time.Second, 45 * time.Second}, [3]int{4, 3, 3}}},
		{ModelConfig{Kind: "jev", Model: "jev-1.13.0", KeyEnv: "UNIT_JEV_CREDENTIAL", AttemptTimeoutSeconds: 5, MaxAttempts: 2}, want{[3]time.Duration{5 * time.Second, 90 * time.Second, 90 * time.Second}, [3]int{2, 2, 2}}},
	} {
		p, err := NewProvider(c.config, budget)
		if err != nil {
			t.Fatal(c.config, err)
		}
		for class := range 3 {
			policy := p.policies[class]
			if policy.class != CallClass(class) || policy.attemptTimeout != c.want.timeout[class] || policy.maxAttempts != c.want.attempts[class] {
				t.Fatal(c.config, CallClass(class), policy.attemptTimeout, policy.maxAttempts)
			}
		}
		if n := []int{freePermits(p.policies[Interactive].calls), freePermits(p.policies[Flush].calls), freePermits(p.policies[Background].calls)}; n[0] != 32 || n[1] != 8 || n[2] != 8 {
			t.Fatal("bulkheads", n)
		}
		p.Close()
	}
	for _, config := range []ModelConfig{
		{Kind: "local", Model: "m", BaseURL: "http://127.0.0.1:1/v1", AttemptTimeoutSeconds: 91},
		{Kind: "local", Model: "m", BaseURL: "http://127.0.0.1:1/v1", AttemptTimeoutSeconds: -1},
		{Kind: "local", Model: "m", BaseURL: "http://127.0.0.1:1/v1", MaxAttempts: 11},
		{Kind: "local", Model: "m", BaseURL: "http://127.0.0.1:1/v1", MaxAttempts: -1},
	} {
		if _, err := NewProvider(config, nil); err == nil {
			t.Fatal("resilience settings accepted", config)
		}
	}
}

func TestModelsCloseAndObserveEachProviderOnce(t *testing.T) {
	var calls atomic.Int32
	local := func() *Provider {
		p, err := NewProvider(ModelConfig{Kind: "local", Model: "m", BaseURL: "http://127.0.0.1:1/v1"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		p.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return mockResponse(`{"choices":[{"message":{"content":"ok"}}]}`), nil
		})
		return p
	}
	a, b := local(), local()
	models := Models{Planner: a, Judge: a, Consolidator: b}
	log := &attemptLog{}
	models.Observe(log)
	ctx := context.Background()
	if _, _, err := a.Complete(ctx, "s", "p"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Complete(ctx, "s", "p"); err != nil {
		t.Fatal(err)
	}
	if len(log.seen()) != 2 {
		t.Fatal("each provider reports its attempts once", log.seen())
	}
	if err := models.Close(); err != nil {
		t.Fatal(err)
	}
	// A closed provider sends nothing.
	if _, _, err := a.Complete(ctx, "s", "p"); !errors.Is(err, ErrModelUnavailable) || calls.Load() != 2 {
		t.Fatal(err, calls.Load())
	}
}

func TestConsolidateCallsAreBackgroundUnlessFlush(t *testing.T) {
	s, dir := agentStore(t)
	seedNote(t, s, "answer", "codename ORBIT")
	overlay, err := OpenOverlay(filepath.Join(dir, "memory-index.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer overlay.Close()
	var mu sync.Mutex
	classes := []CallClass{}
	planner := completeFunc(func(ctx context.Context, _, _ string) (string, Usage, error) {
		mu.Lock()
		defer mu.Unlock()
		classes = append(classes, callClass(ctx))
		return `{"items":[{"kind":"value","title":"password","text":"ORBIT","records":["r0"]}]}`, Usage{}, nil
	})
	a, err := New(s, Config{Version: 1, Policy: &ReadPolicy{Consolidation: true}}, Models{Planner: planner, Judge: decisionFunc(fixtureJudge)}, overlay)
	if err != nil {
		t.Fatal(err)
	}
	for _, ctx := range []context.Context{context.Background(), WithCallClass(context.Background(), Interactive), WithCallClass(context.Background(), Flush)} {
		if _, err := a.Consolidate(ctx, ConsolidateRequest{Reset: true}); err != nil {
			t.Fatal(err)
		}
	}
	if len(classes) != 3 || classes[0] != Background || classes[1] != Background || classes[2] != Flush {
		t.Fatal(classes)
	}
}

// A timed-out call wraps ErrModelUnavailable, so it no longer counts toward
// a batch's drop mode (countedError).
func TestAttemptTimeoutIsUnavailable(t *testing.T) {
	if !errors.Is(ErrAttemptTimeout, ErrModelUnavailable) || countedError(context.Background(), fmt.Errorf("%w; any reservation remains held", ErrAttemptTimeout)) {
		t.Fatal("a timeout counts toward drop mode")
	}
}

// Breakers lists each distinct provider's breaker once, in role order, by the
// state callers act on: open while it refuses calls, half-open once its delay
// is over (the next calls probe the provider), else closed.
func TestModelsBreakersReportEachProvidersState(t *testing.T) {
	deepseek := &Provider{config: ModelConfig{Kind: "deepseek"}, breaker: newBreaker()}
	jev := &Provider{config: ModelConfig{Kind: "jev"}, breaker: newBreaker()}
	models := Models{Planner: deepseek, Judge: jev, Consolidator: deepseek}
	want := func(states ...BreakerStatus) {
		t.Helper()
		if got := models.Breakers(); fmt.Sprint(got) != fmt.Sprint(states) {
			t.Fatalf("got %v, want %v", got, states)
		}
	}
	want(BreakerStatus{"deepseek", BreakerClosed}, BreakerStatus{"jev", BreakerClosed})

	// A run of failures opens the provider's breaker, and only its own.
	for range breakerConsecutive {
		deepseek.breaker.record(true)
	}
	want(BreakerStatus{"deepseek", BreakerOpen}, BreakerStatus{"jev", BreakerClosed})

	// Half-open admits probes; closed again once they succeed.
	deepseek.breaker.cb.HalfOpen()
	want(BreakerStatus{"deepseek", BreakerHalfOpen}, BreakerStatus{"jev", BreakerClosed})
	deepseek.breaker.cb.Close()
	want(BreakerStatus{"deepseek", BreakerClosed}, BreakerStatus{"jev", BreakerClosed})

	// An open breaker whose delay is over no longer refuses: the next call
	// takes its permit and probes, so it is reported half-open, not open.
	short := &breaker{cb: circuitbreaker.NewBuilder[any]().WithDelay(300 * time.Millisecond).WithSuccessThreshold(1).Build()}
	short.cb.Open()
	if got := short.state(); got != BreakerOpen {
		t.Fatal("a breaker inside its delay is", got)
	}
	for start := time.Now(); short.state() == BreakerOpen; time.Sleep(10 * time.Millisecond) {
		if time.Since(start) > 10*time.Second {
			t.Fatal("a breaker past its delay is still", short.state())
		}
	}
	if got := short.state(); got != BreakerHalfOpen || short.open() {
		t.Fatal("a breaker past its delay is", got)
	}

	// Models of no provider (scripted roles) have none to list.
	if got := (Models{Planner: completeFunc(nil)}).Breakers(); got == nil || len(got) != 0 {
		t.Fatalf("got %#v", got)
	}
}

// BreakerTrips counts, by kind, the times breakers opened: a breaker that
// opened and closed again between two readings has still moved its count,
// which the states of Breakers would not show.
func TestModelsBreakerTripsCountOpenings(t *testing.T) {
	deepseek := &Provider{config: ModelConfig{Kind: "deepseek"}, breaker: newBreaker()}
	other := &Provider{config: ModelConfig{Kind: "deepseek"}, breaker: newBreaker()}
	jev := &Provider{config: ModelConfig{Kind: "jev"}, breaker: newBreaker()}
	// Two roles share a provider (counted once); another deepseek provider
	// adds to the kind.
	models := Models{Planner: deepseek, Judge: jev, Consolidator: other}
	shared := Models{Planner: deepseek, Judge: jev, Consolidator: deepseek}
	want := func(m Models, trips map[string]int64) {
		t.Helper()
		if got := m.BreakerTrips(); fmt.Sprint(got) != fmt.Sprint(trips) {
			t.Fatalf("got %v, want %v", got, trips)
		}
	}
	want(models, map[string]int64{"deepseek": 0, "jev": 0})

	// A run of failures opens a breaker: one trip, however long it stays open.
	for range breakerConsecutive {
		deepseek.breaker.record(true)
	}
	want(models, map[string]int64{"deepseek": 1, "jev": 0})
	want(shared, map[string]int64{"deepseek": 1, "jev": 0})
	deepseek.breaker.record(true) // refused calls are not recorded, but even so it is already open
	want(models, map[string]int64{"deepseek": 1, "jev": 0})

	// A failed probe opens it again: a second trip.
	deepseek.breaker.cb.HalfOpen()
	if !deepseek.breaker.permit() {
		t.Fatal("no probe")
	}
	deepseek.breaker.record(true)
	want(models, map[string]int64{"deepseek": 2, "jev": 0})

	// Closing it again, however it closes, takes nothing back…
	deepseek.breaker.cb.HalfOpen()
	for range breakerSuccesses {
		if !deepseek.breaker.permit() {
			t.Fatal("no probe")
		}
		deepseek.breaker.record(false)
	}
	if got := deepseek.breaker.state(); got != BreakerClosed {
		t.Fatal(got)
	}
	want(models, map[string]int64{"deepseek": 2, "jev": 0})

	// …and the kind's count is the sum of its providers'.
	for range breakerConsecutive {
		other.breaker.record(true)
		jev.breaker.record(true)
	}
	want(models, map[string]int64{"deepseek": 3, "jev": 1})
	want(shared, map[string]int64{"deepseek": 2, "jev": 1})

	// Models of no provider have no kinds: an empty map, not nil.
	if got := (Models{Planner: completeFunc(nil)}).BreakerTrips(); got == nil || len(got) != 0 {
		t.Fatalf("got %#v", got)
	}
}
