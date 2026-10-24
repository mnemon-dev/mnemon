package memoryagent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/bulkhead"
	"github.com/failsafe-go/failsafe-go/circuitbreaker"
	"github.com/failsafe-go/failsafe-go/retrypolicy"
)

// Model calls go through one resilience layer, inside Provider so that the
// optional interfaces callers type-assert (CompleterWithMax, JSONCompleter,
// MaxQuestions) stay. After the model cache's lookup, which answers without
// any attempt, a call (send) is a sequence of metered attempts under the
// policies of its class, outermost first:
//
//   - retries (failsafe-go's retry policy) of transport failures, timeouts and
//     the statuses retryableStatus lists, with full-jitter exponential backoff
//     that waits at least an endpoint's Retry-After, never into the caller's
//     deadline, within the provider's retry budget;
//   - the provider's circuit breaker (failsafe-go's), shared by its classes;
//   - the class's bulkhead (failsafe-go's), whose permit a request holds until
//     it ends;
//   - the attempt timeout: past it an attempt stops waiting for its request,
//     which runs on to its hard timeout (the client's) and settles, releases
//     or holds its reservation on its own.
//
// The breaker and the bulkhead are used through their permits rather than
// composed: a request keeps its bulkhead permit after its attempt stopped
// waiting, and the breaker judges only attempts that sent, or tried to send,
// a request.

// CallClass says whose model call it is, which picks the call's policies. A
// context carries it (WithCallClass); one without is Interactive.
type CallClass uint8

const (
	// Interactive calls read for a Search: its planner and its judge.
	Interactive CallClass = iota
	// Flush calls fold what a Search finds pending before it reads.
	Flush
	// Background calls are consolidation jobs that no request waits for.
	Background
)

func (c CallClass) String() string {
	switch c {
	case Flush:
		return "flush"
	case Background:
		return "background"
	}
	return "interactive"
}

type callClassKey struct{}

// WithCallClass returns ctx whose model calls are of class c.
func WithCallClass(ctx context.Context, c CallClass) context.Context {
	return context.WithValue(ctx, callClassKey{}, c)
}

// callClass is the class of ctx's model calls: Interactive unless
// WithCallClass set another.
func callClass(ctx context.Context) CallClass {
	if c, ok := ctx.Value(callClassKey{}).(CallClass); ok && c <= Background {
		return c
	}
	return Interactive
}

// consolidationCalls marks a consolidation job's model calls Background,
// unless they fold a Search's tail (Flush).
func consolidationCalls(ctx context.Context) context.Context {
	if callClass(ctx) == Flush {
		return ctx
	}
	return WithCallClass(ctx, Background)
}

var (
	// ErrBreakerOpen is the error of a call the provider's circuit breaker
	// refused after the provider's failures; nothing was reserved or sent.
	ErrBreakerOpen = fmt.Errorf("%w: circuit breaker open", ErrModelUnavailable)
	// ErrBulkheadFull is the error of a call whose class had as many requests
	// in flight to the provider as it may, none ending within the attempt
	// timeout; nothing was reserved or sent.
	ErrBulkheadFull = fmt.Errorf("%w: too many calls in flight", ErrModelUnavailable)
	// ErrAttemptTimeout wraps the error of an attempt that ran out of time:
	// abandoned past its attempt timeout, or cut by the hard timeout after it
	// was sent.
	ErrAttemptTimeout = fmt.Errorf("%w: attempt timed out", ErrModelUnavailable)
	// ErrBackgroundReserve is the error of a Background call whose
	// reservation would fill the last 10% of its account's cap, which is kept
	// for Searches and their flushes.
	ErrBackgroundReserve = fmt.Errorf("%w: kept for searches", ErrBudgetCap)
	// errProviderClosed is the error of an attempt after Provider.Close.
	errProviderClosed = fmt.Errorf("%w: provider closed", ErrModelUnavailable)
)

// The calls of each class a provider runs at once (its bulkheads); together
// they size its connection pool (providerConns).
const (
	interactiveCalls = 32
	flushCalls       = 8
	backgroundCalls  = 8
)

// The classes' policies. An Interactive call makes up to 4 attempts of 45 s
// (completions) or 20 s (JEV) each, at most the hard timeout, retried after
// 0.5 s → 8 s, an endpoint's Retry-After waited up to 10 s. Flush and
// Background calls make up to 3 attempts of the hard timeout each, for every
// kind, so they are never abandoned, retried after 2 s → 60 s, a Retry-After
// waited up to 5 min. A model's attemptTimeoutSeconds replaces the
// Interactive attempt timeout, its maxAttempts every class's attempts.
const (
	interactiveAttempts          = 4
	interactiveCompletionTimeout = 45 * time.Second
	interactiveDecisionTimeout   = 20 * time.Second
	interactiveBackoff           = 500 * time.Millisecond
	interactiveMaxBackoff        = 8 * time.Second
	interactiveMaxRetryAfter     = 10 * time.Second
	backgroundAttempts           = 3
	backgroundBackoff            = 2 * time.Second
	backgroundMaxBackoff         = 60 * time.Second
	backgroundMaxRetryAfter      = 5 * time.Minute
	// backgroundShare is the part of an account's cap Background calls may
	// fill: the rest is kept for Searches and their flushes.
	backgroundShare = 0.9
	// maxModelAttempts bounds a model's maxAttempts.
	maxModelAttempts = 10
)

// classPolicy is how a provider makes the calls of one class.
type classPolicy struct {
	class CallClass
	// attemptTimeout bounds an attempt's wait for its request: at most the
	// hard timeout, below it only for Interactive attempts, which are
	// abandoned past it (wait).
	attemptTimeout time.Duration
	maxAttempts    int
	// A retry waits a random part of backoff×2^(n−1), at most maxBackoff,
	// and at least an endpoint's Retry-After, up to maxRetryAfter.
	backoff, maxBackoff, maxRetryAfter time.Duration
	// calls is the class's bulkhead. A request holds a permit until it ends,
	// an abandoned one included; an attempt waits for one up to its timeout.
	calls bulkhead.Bulkhead[any]
	// share is the part of an account's cap the class reserves within.
	share float64
}

// checkResilience checks a model's resilience settings: an attempt timeout
// of at most hard, the hard timeout; 1–maxModelAttempts attempts; and a test
// endpoint only for the jev and deepseek kinds, an http URL on a loopback
// host.
func checkResilience(config ModelConfig, hard time.Duration) error {
	if seconds := int(hard / time.Second); config.AttemptTimeoutSeconds < 0 || config.AttemptTimeoutSeconds > seconds {
		return fmt.Errorf("attemptTimeoutSeconds must be 1–%d (timeoutSeconds)", seconds)
	}
	if config.MaxAttempts < 0 || config.MaxAttempts > maxModelAttempts {
		return fmt.Errorf("maxAttempts must be 1–%d", maxModelAttempts)
	}
	if config.TestEndpoint == "" {
		return nil
	}
	u, err := url.Parse(config.TestEndpoint)
	if config.Kind != "jev" && config.Kind != "deepseek" || err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !isLoopbackHost(u.Hostname()) {
		return errors.New("testEndpoint must be an http URL on a loopback host, for the jev and deepseek kinds")
	}
	return nil
}

// classPolicies are a provider's policies, indexed by CallClass, for its
// kind and resilience settings; hard is its hard timeout.
func classPolicies(config ModelConfig, hard time.Duration) [3]classPolicy {
	interactive := interactiveCompletionTimeout
	if config.Kind == "jev" {
		interactive = interactiveDecisionTimeout
	}
	if config.AttemptTimeoutSeconds > 0 {
		interactive = time.Duration(config.AttemptTimeoutSeconds) * time.Second
	}
	policies := [3]classPolicy{
		{class: Interactive, attemptTimeout: min(interactive, hard), maxAttempts: interactiveAttempts, backoff: interactiveBackoff, maxBackoff: interactiveMaxBackoff, maxRetryAfter: interactiveMaxRetryAfter, calls: bulkhead.New[any](interactiveCalls), share: 1},
		{class: Flush, attemptTimeout: hard, maxAttempts: backgroundAttempts, backoff: backgroundBackoff, maxBackoff: backgroundMaxBackoff, maxRetryAfter: backgroundMaxRetryAfter, calls: bulkhead.New[any](flushCalls), share: 1},
		{class: Background, attemptTimeout: hard, maxAttempts: backgroundAttempts, backoff: backgroundBackoff, maxBackoff: backgroundMaxBackoff, maxRetryAfter: backgroundMaxRetryAfter, calls: bulkhead.New[any](backgroundCalls), share: backgroundShare},
	}
	if config.MaxAttempts > 0 {
		for i := range policies {
			policies[i].maxAttempts = config.MaxAttempts
		}
	}
	return policies
}

// retryDelay is the wait before the class's retry number retry (1 for the
// first): a random part of backoff×2^(retry−1), at most maxBackoff (full
// jitter), and at least retryAfter, an endpoint's Retry-After, up to
// maxRetryAfter. random is in [0, 1).
func (c classPolicy) retryDelay(retry int, retryAfter time.Duration, random float64) time.Duration {
	ceiling := c.maxBackoff
	if shift := retry - 1; shift >= 0 && shift < 16 {
		ceiling = min(c.maxBackoff, c.backoff<<shift)
	}
	return max(time.Duration(random*float64(ceiling)), min(retryAfter, c.maxRetryAfter))
}

// parseRetryAfter is the wait a Retry-After header asks for at now, in delta
// seconds or as an HTTP date; 0 when it is absent, invalid or past.
func parseRetryAfter(header string, now time.Time) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(header, 10, 64); err == nil {
		return time.Duration(max(0, min(seconds, 86400))) * time.Second
	}
	if at, err := http.ParseTime(header); err == nil && at.After(now) {
		return at.Sub(now)
	}
	return 0
}

// statusError is an endpoint's answer outside 2xx: its status, and the wait
// its Retry-After asks for.
type statusError struct {
	code       int
	retryAfter time.Duration
}

func (e *statusError) Error() string { return fmt.Sprintf("model HTTP %d", e.code) }

// retryableStatus reports whether a refusal with the status may pass on a
// retry: a request timeout, too early, rate limited, or a server or gateway
// failing for now (524 a gateway's timeout, 529 an overloaded provider).
func retryableStatus(code int) bool {
	switch code {
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout, 524, 529:
		return true
	}
	return false
}

// breaker is a provider's circuit breaker, shared by its classes:
// failsafe-go's count-based breaker opens once breakerFailures of the last
// breakerWindow attempts failed, half-opens after breakerDelay and closes
// again after breakerSuccesses; it also opens after breakerConsecutive
// failures in a row. An attempt takes its permit (permit) once the bulkhead
// and the budget admitted it, and returns it with how it ended (record).
type breaker struct {
	cb circuitbreaker.CircuitBreaker[any]
	// mu orders the records and guards consecutive. It is held across the
	// breaker's own calls, which take its lock and call no listener but the
	// trip counter's.
	mu          sync.Mutex
	consecutive int
	// trips counts the times the breaker opened, from closed or from
	// half-open (a failed probe), since it was made. It only grows.
	trips atomic.Int64
}

const (
	breakerFailures    = 10
	breakerWindow      = 20
	breakerConsecutive = 5
	breakerDelay       = 20 * time.Second
	breakerSuccesses   = 2
)

func newBreaker() *breaker {
	b := &breaker{}
	b.cb = circuitbreaker.NewBuilder[any]().
		WithFailureThresholdRatio(breakerFailures, breakerWindow).
		WithDelay(breakerDelay).
		WithSuccessThreshold(breakerSuccesses).
		// The listener runs on the goroutine that tripped the breaker, once
		// the breaker has changed state; it only adds to a counter.
		OnOpen(func(circuitbreaker.StateChangedEvent) { b.trips.Add(1) }).
		Build()
	return b
}

// open reports whether the breaker refuses calls for now: it is open and its
// delay is not over. It takes no permit.
func (b *breaker) open() bool { return b.cb.RemainingDelay() > 0 }

// The states a breaker is reported in (BreakerStatus).
const (
	BreakerClosed   = "closed"
	BreakerOpen     = "open"
	BreakerHalfOpen = "half-open"
)

// state is the breaker's state as callers act on it: BreakerOpen while it
// refuses calls, BreakerHalfOpen once its delay is over (the next call takes
// a permit and probes the provider, so nothing is refused any more), else
// BreakerClosed. It takes no permit.
func (b *breaker) state() string {
	switch {
	case b.open():
		return BreakerOpen
	case b.cb.IsClosed():
		return BreakerClosed
	}
	return BreakerHalfOpen
}

// BreakerStatus is the state of one provider's circuit breaker: the
// provider's kind and BreakerClosed, BreakerOpen or BreakerHalfOpen.
type BreakerStatus struct{ Kind, State string }

// Breakers lists the circuit breaker of every provider among the models, once
// each, in role order (planner, judge, consolidator). It changes nothing, and
// is empty for models that are not providers.
func (m Models) Breakers() []BreakerStatus {
	out := []BreakerStatus{}
	for _, p := range m.providers() {
		out = append(out, BreakerStatus{Kind: p.config.Kind, State: p.breaker.state()})
	}
	return out
}

// BreakerTrips counts, by provider kind, the times the circuit breakers of
// the models' providers opened since they were made: a failed probe of a
// half-open breaker opens it again and counts. Unlike the states of Breakers,
// which are the moment of the call, a count only grows, so a breaker that
// opened and closed again between two readings still moved it. A kind with
// several providers sums their counts. The map has a key for each kind among
// the models, and is empty for models that are not providers.
func (m Models) BreakerTrips() map[string]int64 {
	trips := map[string]int64{}
	for _, p := range m.providers() {
		trips[p.config.Kind] += p.breaker.trips.Load()
	}
	return trips
}

// permit takes the breaker's permission to send one request, which record
// returns.
func (b *breaker) permit() bool { return b.cb.TryAcquirePermit() }

// record returns a permit with how its attempt ended: failed when the
// provider failed it (flightResult.failed).
func (b *breaker) record(failed bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !failed {
		b.consecutive = 0
		b.cb.RecordSuccess()
		return
	}
	b.consecutive++
	b.cb.RecordFailure()
	if b.consecutive >= breakerConsecutive && b.cb.IsClosed() {
		b.cb.Open()
	}
}

// retryBudget bounds a provider's retries to retryShare of the calls it
// began over the last retryWindow seconds, and allows retryFloor a second
// however few calls there were, so that an outage cannot multiply the load
// on it (the SRE book's retry budget). It counts by second, in a ring.
type retryBudget struct {
	mu    sync.Mutex
	slots [retryWindow]retrySlot
}

// retrySlot counts the calls begun and the retries taken in one second.
type retrySlot struct {
	second         int64
	calls, retries int
}

const (
	retryWindow = 60
	retryShare  = 0.2
	retryFloor  = 5
)

// slot is now's slot, emptied when it counted an older second; b.mu is held.
func (b *retryBudget) slot(now time.Time) *retrySlot {
	second := now.Unix()
	s := &b.slots[second%retryWindow]
	if s.second != second {
		*s = retrySlot{second: second}
	}
	return s
}

// called counts a call begun at now.
func (b *retryBudget) called(now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.slot(now).calls++
}

// allow takes a retry at now from the budget, reporting whether one was left.
func (b *retryBudget) allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	current := b.slot(now)
	calls, retries := 0, 0
	for _, s := range b.slots {
		if now.Unix()-s.second < retryWindow {
			calls, retries = calls+s.calls, retries+s.retries
		}
	}
	if current.retries >= retryFloor && float64(retries+1) > retryShare*float64(calls) {
		return false
	}
	current.retries++
	return true
}

// meteredReply is a parsed model reply: what it says, and its metering.
type meteredReply interface{ metered() Usage }

// flightResult is how an attempt ended.
type flightResult[T meteredReply] struct {
	reply  T
	raw    []byte
	status int // the HTTP status answered, 0 without an answer
	err    error
	// retry: a retry may not meet the failure (a transport failure, a
	// timeout, a status retryableStatus lists).
	retry bool
	// failed: the provider failed the attempt, which its breaker counts.
	failed bool
	// unparsed: a 200 reply that did not parse.
	unparsed bool
}

// outcome is the result's AttemptEvent.Outcome.
func (r flightResult[T]) outcome() string {
	switch {
	case r.err == nil:
		return "ok"
	case errors.Is(r.err, ErrBreakerOpen):
		return "breaker-open"
	case errors.Is(r.err, ErrBulkheadFull):
		return "bulkhead-full"
	case errors.Is(r.err, ErrBudgetCap):
		return "budget"
	case r.retry:
		return "retryable"
	}
	return "failed"
}

// send makes one model call of the encoded request body: attempts under the
// policies of the context's call class. Every attempt is metered as a call
// was before: it reserves amount (at least the kind's flat reservation),
// sends, then settles, releases or holds its reservation by how its request
// ended (flight.exchange); parse reads a 200 reply and its metering. send
// returns the reply of the attempt that answered with its raw body, else the
// last attempt's error, whose category says what failed it: the provider
// (ErrModelUnavailable), the budget (ErrBudgetCap) or the request itself (no
// category). A reply that parsed but could not be settled comes with that
// error. When the caller's context ended, the error wraps its error.
func send[T meteredReply](ctx context.Context, p *Provider, body []byte, amount float64, parse func([]byte) (T, error)) (T, []byte, error) {
	c := &modelCall[T]{ctx: ctx, p: p, policy: &p.policies[callClass(ctx)], body: body, hash: reference("request", string(body)).Hash, amount: amount, parse: parse}
	retry := retrypolicy.NewBuilder[flightResult[T]]().
		HandleIf(func(r flightResult[T], err error) bool { return err != nil && r.retry }).
		AbortIf(func(flightResult[T], error) bool { return c.stop() }).
		WithMaxAttempts(c.policy.maxAttempts).
		WithDelayFunc(func(failsafe.ExecutionAttempt[flightResult[T]]) time.Duration { return c.delay }).
		ReturnLastFailure().
		Build()
	_, err := failsafe.With[flightResult[T]](retry).WithContext(ctx).GetWithExecution(c.attempt)
	return c.result(err)
}

// modelCall is one call across its attempts. failsafe-go runs the attempts
// and the retry policy's functions one at a time on the caller's goroutine,
// so it needs no lock.
type modelCall[T meteredReply] struct {
	ctx    context.Context // the caller's
	p      *Provider
	policy *classPolicy
	body   []byte
	hash   string // of body, for the budget's attempt rows
	amount float64
	parse  func([]byte) (T, error)

	attempts int
	last     flightResult[T] // the last attempt's
	reparsed bool            // an unparsed reply was retried
	delay    time.Duration   // the wait before the next retry
}

// attempt is one attempt of the call (the function the retry policy runs),
// shown to the provider's observer.
func (c *modelCall[T]) attempt(exec failsafe.Execution[flightResult[T]]) (flightResult[T], error) {
	c.attempts++
	start := time.Now()
	res := c.try(exec.Context())
	// A read-path completion whose 200 reply does not parse is retried once.
	if res.unparsed && c.policy.class == Interactive && c.p.config.Kind != "jev" && !c.reparsed {
		res.retry, c.reparsed = true, true
	}
	c.last = res
	c.report(res.outcome(), c.attempts, time.Since(start), res.status)
	return res, res.err
}

// report shows an attempt to the provider's observer.
func (c *modelCall[T]) report(outcome string, attempt int, took time.Duration, status int) {
	c.p.observe(AttemptEvent{Kind: c.p.config.Kind, Class: c.policy.class.String(), Outcome: outcome, Attempt: attempt, Duration: took, Status: status, Retried: attempt > 1})
}

// stop decides, after an attempt a retry may pass, whether the call stops
// there, and sets the wait before the retry. A call with no attempt left is
// left to the retry policy, which ends it; one whose wait would end at or
// past the caller's deadline stops, as does one the provider's retry budget
// has no retry left for (shown to the observer as a "budget" attempt).
func (c *modelCall[T]) stop() bool {
	if c.attempts >= c.policy.maxAttempts {
		return false
	}
	var retryAfter time.Duration
	var status *statusError
	if errors.As(c.last.err, &status) {
		retryAfter = status.retryAfter
	}
	c.delay = c.policy.retryDelay(c.attempts, retryAfter, rand.Float64())
	now := time.Now()
	if deadline, ok := c.ctx.Deadline(); ok && !now.Add(c.delay).Before(deadline) {
		return true
	}
	if !c.p.retries.allow(now) {
		c.last.err = fmt.Errorf("%w; the provider's retry budget is spent", c.last.err)
		c.report("budget", c.attempts+1, 0, 0)
		return true
	}
	return false
}

// result is the call's answer once the policies are done: the last
// attempt's, also when the caller gave up as its reply arrived (the reply is
// paid for). A failure after retries says how many attempts the call made,
// and names the caller's error when it ended the call between attempts. The
// policies' own error answers only when no attempt ran.
func (c *modelCall[T]) result(err error) (T, []byte, error) {
	if c.attempts == 0 {
		var zero T
		return zero, nil, err
	}
	res := c.last
	if res.err == nil {
		return res.reply, res.raw, nil
	}
	if c.attempts > 1 {
		res.err = fmt.Errorf("%w (after %d attempts)", res.err, c.attempts)
	}
	if gaveUp := c.ctx.Err(); gaveUp != nil && !errors.Is(res.err, gaveUp) {
		res.err = fmt.Errorf("%w; the caller gave up: %w", res.err, gaveUp)
	}
	return res.reply, res.raw, res.err
}

// try makes one attempt on its context (the execution's: the caller's, or a
// child that ends with the attempt): once admitted (admit), its request
// flies in a goroutine of its own while the attempt waits for it (wait), and
// the breaker counts how it ended.
func (c *modelCall[T]) try(ctx context.Context) flightResult[T] {
	if err := ctx.Err(); err != nil {
		return flightResult[T]{err: err}
	}
	f, err := c.admit(ctx)
	if err != nil {
		return flightResult[T]{err: err}
	}
	if c.attempts == 1 {
		c.p.retries.called(time.Now())
	}
	go f.run(c.p)
	res := c.wait(ctx, f)
	c.p.breaker.record(res.failed)
	return res
}

// admit readies the attempt's flight, in order: its request (with the
// credential), the breaker unless it is open, the provider's flights unless
// it is closed, a permit of the class's bulkhead within the attempt timeout,
// the budget's reservation within the class's share of the cap, and the
// breaker's permit. A step that refuses undoes the ones before it.
func (c *modelCall[T]) admit(ctx context.Context) (*flight[T], error) {
	p, policy := c.p, c.policy
	request, err := p.newRequest(ctx, c.body)
	if err != nil {
		return nil, err
	}
	if p.breaker.open() {
		return nil, ErrBreakerOpen
	}
	if !p.begin() {
		return nil, errProviderClosed
	}
	f := &flight[T]{request: request, parse: c.parse, done: make(chan flightResult[T], 1)}
	if err := policy.calls.AcquirePermitWithMaxWait(ctx, policy.attemptTimeout); err != nil {
		if errors.Is(err, bulkhead.ErrFull) {
			err = ErrBulkheadFull
		}
		return nil, f.abort(p, err)
	}
	f.permit = policy.calls
	if f.id, err = p.budget.reserveWithin(ctx, p.config.Kind, c.hash, c.amount, policy.share); err != nil {
		return nil, f.abort(p, err)
	}
	if !p.breaker.permit() {
		return nil, f.abort(p, ErrBreakerOpen)
	}
	// The request flies on the attempt's context without its cancellation:
	// wait cuts it when the caller gives up, not when the attempt times out.
	flying, cut := context.WithCancelCause(request.Context())
	f.trace, f.cut = &connTrace{traced: tracesConnections(p.client.Transport)}, cut
	f.request = request.WithContext(httptrace.WithClientTrace(flying, f.trace.hooks()))
	return f, nil
}

// wait waits for the flight's result on the attempt's context ctx. Past the
// attempt timeout, when that is below the hard timeout (an Interactive
// attempt's), the attempt stops waiting and abandons its request, which runs
// on and ends on its own; so does an attempt whose own context ends while the
// caller's goes on. When the caller gives up, the attempt cuts its request
// and waits the moment that takes it to end. A result ready by then wins over
// both.
func (c *modelCall[T]) wait(ctx context.Context, f *flight[T]) flightResult[T] {
	var expired <-chan time.Time
	if c.policy.attemptTimeout < c.p.client.Timeout {
		timer := time.NewTimer(c.policy.attemptTimeout)
		defer timer.Stop()
		expired = timer.C
	}
	select {
	case res := <-f.done:
		return res
	case <-expired:
	case <-ctx.Done():
	}
	select {
	case res := <-f.done:
		return res
	default:
	}
	gaveUp := c.ctx.Err()
	if gaveUp == nil {
		return flightResult[T]{err: fmt.Errorf("%w after %v; its request runs on", ErrAttemptTimeout, c.policy.attemptTimeout), retry: true, failed: true}
	}
	f.cut(gaveUp)
	return <-f.done
}

// flight is an attempt's HTTP request, run by a goroutine of its own (run)
// so that an attempt past its timeout can stop waiting without stopping it.
// The goroutine is the provider's: its flights count it from the attempt's
// admission, and Provider.Close waits for it. Until the request ends it owns
// what the attempt was admitted with, the bulkhead permit and the budget
// reservation, which it settles, releases or holds. The client's hard
// timeout and the budget file's busy timeout bound its work, and the caller
// giving up cuts it (cut). It writes its result once to done, never closed.
type flight[T meteredReply] struct {
	request *http.Request
	trace   *connTrace
	id      string // the budget reservation; "" for a local model
	permit  bulkhead.Bulkhead[any]
	parse   func([]byte) (T, error)
	// cut cuts the request, the caller's error its cause.
	cut context.CancelCauseFunc
	// done hands the result to the attempt; its room for one lets the
	// goroutine end with nobody waiting for it.
	done chan flightResult[T]
}

// abort ends a flight that will not fly, returning what it was admitted
// with, and returns cause, saying whether a reservation was released.
func (f *flight[T]) abort(p *Provider, cause error) error {
	if f.id != "" {
		cause = p.release(f.request.Context(), f.id, cause)
	}
	if f.permit != nil {
		f.permit.ReleasePermit()
	}
	p.flights.Done()
	return cause
}

// run is the flight's goroutine: it ends the request (exchange), returns the
// permit and hands over the result. A panic leaves the request's outcome
// unknown, so its reservation stays held, and fails the attempt.
func (f *flight[T]) run(p *Provider) {
	defer p.flights.Done()
	var res flightResult[T]
	if err := recovered(func() error { res = f.exchange(p); return nil }); err != nil {
		res = flightResult[T]{err: fmt.Errorf("model request failed; any reservation remains held: %w", err)}
	}
	f.cut(nil)
	f.permit.ReleasePermit()
	f.done <- res
}

// exchange sends the request and meters what came back. A request that
// never obtained a connection (it failed before or while connecting, or
// waited for one until it was cut or timed out) or that the endpoint refused
// (a status outside 2xx) cost nothing: its reservation is released, recorded
// at 0. One that may have been processed (it failed once connected, or its
// reply is unreadable or does not parse) keeps its reservation held. A 200
// reply that parses is settled at its metering, also when the caller gave up
// meanwhile.
func (f *flight[T]) exchange(p *Provider) flightResult[T] {
	response, err := p.client.Do(f.request)
	if err != nil {
		return f.broken(p, err, f.trace.sent(err))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return f.refused(p, response)
	}
	if response.StatusCode != http.StatusOK {
		return flightResult[T]{status: response.StatusCode, err: fmt.Errorf("model HTTP %d; any reservation remains held", response.StatusCode)}
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
	if err != nil {
		res := f.broken(p, err, true)
		res.status = http.StatusOK
		return res
	}
	if len(raw) > 8<<20 {
		return flightResult[T]{status: http.StatusOK, err: errors.New("invalid or oversized model response")}
	}
	reply, err := f.parse(raw)
	if err != nil {
		return flightResult[T]{status: http.StatusOK, err: err, unparsed: true}
	}
	if err := p.settle(f.request.Context(), f.id, reply.metered()); err != nil {
		return flightResult[T]{status: http.StatusOK, reply: reply, err: err}
	}
	return flightResult[T]{status: http.StatusOK, reply: reply, raw: raw}
}

// broken is a request that failed before its whole answer arrived; sent says
// whether it may have reached the endpoint. One the caller cut says so with
// the caller's error. Any other failure is the provider's, and a retry may
// not meet it: unsent, an unreachable endpoint (released); sent, a timeout or
// a failed exchange (held).
func (f *flight[T]) broken(p *Provider, err error, sent bool) flightResult[T] {
	ctx := f.request.Context()
	if cause := context.Cause(ctx); cause != nil {
		if !sent {
			return flightResult[T]{err: p.release(ctx, f.id, fmt.Errorf("model request canceled before it was sent: %w", cause))}
		}
		return flightResult[T]{err: fmt.Errorf("model request failed; any reservation remains held: %w", cause)}
	}
	res := flightResult[T]{retry: true, failed: true}
	switch {
	case !sent:
		res.err = p.release(ctx, f.id, fmt.Errorf("%w: endpoint unreachable", ErrModelUnavailable))
	case timedOut(err):
		res.err = fmt.Errorf("%w; any reservation remains held", ErrAttemptTimeout)
	default:
		res.err = fmt.Errorf("%w: model request failed; any reservation remains held", ErrModelUnavailable)
	}
	return res
}

// refused is a request the endpoint answered outside 2xx: it cost nothing,
// so its reservation is released. A refusal that holds for every request
// alike is the provider's failure (unavailableStatus); one a retry may pass
// (retryableStatus) is retried after the endpoint's Retry-After.
func (f *flight[T]) refused(p *Provider, response *http.Response) flightResult[T] {
	status := &statusError{code: response.StatusCode, retryAfter: parseRetryAfter(response.Header.Get("Retry-After"), time.Now())}
	var err error = status
	if unavailableStatus(status.code) {
		err = fmt.Errorf("%w: %w", ErrModelUnavailable, status)
	}
	return flightResult[T]{status: status.code, err: p.release(f.request.Context(), f.id, err), retry: retryableStatus(status.code), failed: unavailableStatus(status.code)}
}

// timedOut reports whether err is a timeout: the client's hard timeout, or a
// connection's.
func timedOut(err error) bool {
	var timeout interface{ Timeout() bool }
	return errors.As(err, &timeout) && timeout.Timeout()
}

// connTrace is what a request's trace tells of its connection: traced, the
// round tripper reports the connections it hands requests (an
// http.Transport), and got, the request obtained one.
type connTrace struct {
	traced bool
	got    atomic.Bool
}

func (t *connTrace) hooks() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { t.got.Store(true) }}
}

// sent reports whether a failed request may have reached the endpoint. An
// http.Transport reports the connection it hands a request (GotConn, for
// HTTP/1 and HTTP/2 alike) before it writes any of it, so a request that
// obtained none sent nothing, however it failed. Another round tripper is
// judged by its error (unsent).
func (t *connTrace) sent(err error) bool {
	if t.traced {
		return t.got.Load()
	}
	return !unsent(err)
}

// tracesConnections reports whether requests sent over rt report their
// connections: rt is net/http's Transport (nil is http.DefaultTransport).
func tracesConnections(rt http.RoundTripper) bool {
	if rt == nil {
		rt = http.DefaultTransport
	}
	_, ok := rt.(*http.Transport)
	return ok
}

// newRequest is an attempt's request of body, with the credential, on ctx
// without its cancellation (the attempt cuts it, see wait).
func (p *Provider) newRequest(ctx context.Context, body []byte) (*http.Request, error) {
	request, err := http.NewRequestWithContext(context.WithoutCancel(ctx), http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	if p.config.KeyEnv != "" {
		key := os.Getenv(p.config.KeyEnv)
		if key == "" {
			return nil, errors.New("model credential is unavailable")
		}
		request.Header.Set("Authorization", "Bearer "+key)
	}
	return request, nil
}

// begin counts an attempt into the provider's flights, unless the provider
// is closed. The flight's goroutine, or its abort, ends the count.
func (p *Provider) begin() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false
	}
	p.flights.Add(1)
	return true
}

// Close waits for the provider's requests in flight, abandoned ones included
// (each ends by its hard timeout), then closes its idle connections. Calls
// after Close fail without a request.
func (p *Provider) Close() error {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	p.flights.Wait()
	p.client.CloseIdleConnections()
	return nil
}

// observerRef publishes a provider's observer.
type observerRef struct{ observer ModelObserver }

// observe shows an attempt to the provider's observer, if any.
func (p *Provider) observe(e AttemptEvent) {
	if ref := p.observer.Load(); ref != nil && ref.observer != nil {
		ref.observer.ModelAttempt(e)
	}
}
