package memoryagent

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// These tests hold the resilience layer to rulings R59–R63, which come from
// the 12a fault drill: model services that are unstable but not down.
// Throttling is not unavailability, the breaker isolates only a run of
// availability failures, an invalid reply is retried once, stalls are cut,
// and every reservation settles when its attempt ends.

const (
	// jevReply is a JEV decision reply with its metering: a noul answer to
	// the question "a".
	jevReply = `{"model":"jev-1.13.0","answers":{"a":{"type":"noul","noul":0.8}},"usage":{"input_tokens":1000,"output_tokens":1}}`
	// deepseekReplyCNY and jevReplyUSD are what deepseekReply and jevReply
	// cost at the providers' prices.
	deepseekReplyCNY = 100*2/1e6 + 10*8/1e6
	jevReplyUSD      = 1000 * 0.042 / 1e6
)

// completionWith is a DeepSeek completion reply whose content is content,
// metered as deepseekReply is.
func completionWith(content string) string {
	raw, err := json.Marshal(map[string]any{
		"choices": []map[string]any{{"message": map[string]string{"content": content}, "finish_reason": "stop"}},
		"usage":   map[string]int{"prompt_tokens": 100, "completion_tokens": 10},
	})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// newEndpointWith is newEndpoint whose answer also gets the request and its
// body, read in full.
func newEndpointWith(t *testing.T, answer func(n int, w http.ResponseWriter, r *http.Request, body []byte)) *endpoint {
	t.Helper()
	e := &endpoint{}
	e.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		e.mu.Lock()
		e.arrivals = append(e.arrivals, time.Now())
		n := len(e.arrivals)
		e.mu.Unlock()
		answer(n, w, r, body)
	}))
	t.Cleanup(e.Close)
	return e
}

// localOn is a local (unmetered) provider whose requests go to e, with
// config's resilience settings.
func localOn(t *testing.T, e *endpoint, config ModelConfig) *Provider {
	t.Helper()
	config.Kind, config.Model, config.BaseURL = "local", "m", e.URL+"/v1"
	p, err := NewProvider(config, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

// counts are the logged attempts by outcome.
func (l *attemptLog) counts() map[string]int {
	out := map[string]int{}
	for _, e := range l.seen() {
		out[e.Outcome]++
	}
	return out
}

// quickRetries makes p's retries wait at most a millisecond in every class:
// what a test then looks at is which attempts are retried, not the backoff.
func quickRetries(p *Provider) {
	for class := range p.policies {
		p.policies[class].backoff, p.policies[class].maxBackoff = time.Millisecond, time.Millisecond
	}
}

// stallMidBody answers 200 with its headers and the first half of reply,
// then sends nothing more until the client leaves.
func stallMidBody(reply string) func(int, http.ResponseWriter, *http.Request, []byte) {
	return func(_ int, w http.ResponseWriter, r *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, reply[:len(reply)/2])
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}
}

// R59: a 429 is throttling, not unavailability. A storm of them, with and
// without Retry-After, longer than the breaker's ten attempts, leaves the
// breaker closed. Its attempts count as throttled and wait max(backoff,
// Retry-After). The calls succeed once the storm has passed them, within
// their attempts and their deadline, and the refusals cost nothing.
func TestThrottleNeverOpensTheBreaker(t *testing.T) {
	for _, retryAfter := range []string{"1", ""} {
		t.Run("Retry-After "+cmp.Or(retryAfter, "absent"), func(t *testing.T) {
			// Each call's first three requests are refused: four calls at once
			// make a storm of twelve 429s.
			var mu sync.Mutex
			arrivals := map[string][]time.Time{}
			e := newEndpointWith(t, func(_ int, w http.ResponseWriter, _ *http.Request, body []byte) {
				mu.Lock()
				arrivals[string(body)] = append(arrivals[string(body)], time.Now())
				n := len(arrivals[string(body)])
				mu.Unlock()
				if n <= 3 {
					if retryAfter != "" {
						w.Header().Set("Retry-After", retryAfter)
					}
					w.WriteHeader(http.StatusTooManyRequests)
					return
				}
				io.WriteString(w, deepseekReply)
			})
			p, budget := deepseekOn(t, e, ModelConfig{}, nil)
			// A minute of steady traffic leaves the provider's retry budget
			// room for the storm's twelve retries.
			for range 100 {
				p.retries.called(time.Now())
			}
			log := &attemptLog{}
			Models{Planner: p}.Observe(log)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			errs := make([]error, 4)
			var calls sync.WaitGroup
			for i := range errs {
				calls.Add(1)
				go func() {
					defer calls.Done()
					_, _, errs[i] = p.Complete(ctx, "s", fmt.Sprint("p", i))
				}()
			}
			calls.Wait()
			for i, err := range errs {
				if err != nil {
					t.Fatal("call", i, err, log.counts())
				}
			}
			if got := log.counts(); got["throttled"] != 12 || got["ok"] != 4 || len(log.seen()) != 16 {
				t.Fatal(got)
			}
			if state := p.breaker.state(); state != BreakerClosed || p.breaker.trips.Load() != 0 || p.breaker.cb.Metrics().Failures() != 0 {
				t.Fatal("the storm moved the breaker:", state, p.breaker.trips.Load(), p.breaker.cb.Metrics().Failures())
			}
			if retryAfter != "" {
				mu.Lock()
				for _, times := range arrivals {
					for i := 1; i < len(times); i++ {
						if gap := times[i].Sub(times[i-1]); gap < time.Second {
							t.Error("retried before its Retry-After", i, gap)
						}
					}
				}
				mu.Unlock()
			}
			if held, spent := accountHeld(t, budget, "deepseek"); !near(held, 0) || !near(spent, 4*deepseekReplyCNY) {
				t.Fatal("held", held, "spent", spent)
			}
		})
	}
	// A Retry-After whose wait would end past the caller's deadline ends the
	// call at once, as before (an Interactive call degrades), and opens
	// nothing.
	t.Run("Retry-After past the deadline", func(t *testing.T) {
		e := newEndpoint(t, func(_ int, w http.ResponseWriter) {
			w.Header().Set("Retry-After", "5")
			w.WriteHeader(http.StatusTooManyRequests)
		})
		p, _ := deepseekOn(t, e, ModelConfig{}, nil)
		log := &attemptLog{}
		Models{Planner: p}.Observe(log)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		start := time.Now()
		_, _, err := p.Complete(ctx, "s", "p")
		if elapsed := time.Since(start); elapsed > time.Second || !errors.Is(err, ErrModelUnavailable) || !strings.Contains(err.Error(), "429") || e.requests() != 1 {
			t.Fatal(elapsed, err, e.requests())
		}
		if got := log.outcomes(); got != "throttled" || p.breaker.state() != BreakerClosed || p.breaker.cb.Metrics().Failures() != 0 {
			t.Fatal(got, p.breaker.state())
		}
	})
}

// R60: the breaker opens after 10 availability failures in a row
// (failsafe-go's consecutive threshold) for its 20 s delay, then admits 2
// probes; anything else starts the count again. A stream of 30% 503s, which
// the retries are left to absorb, never opens it.
func TestBreakerOpensOnTenConsecutiveFailures(t *testing.T) {
	ctx := context.Background()
	// 503 to every request but the 10th and the 20th.
	e := newEndpoint(t, func(n int, w http.ResponseWriter) {
		if n != 10 && n != 20 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		io.WriteString(w, deepseekReply)
	})
	p, _ := deepseekOn(t, e, ModelConfig{MaxAttempts: 1}, nil)
	for n := 1; n <= 29; n++ {
		p.Complete(ctx, "s", "p")
		if state := p.breaker.state(); state != BreakerClosed {
			t.Fatal("open after request", n, state)
		}
	}
	// The tenth 503 in a row opens it, for 20 s.
	p.Complete(ctx, "s", "p")
	if p.breaker.state() != BreakerOpen || p.breaker.trips.Load() != 1 {
		t.Fatal("ten 503s in a row did not open it:", p.breaker.state())
	}
	if delay := p.breaker.cb.RemainingDelay(); delay <= 19*time.Second || delay > 20*time.Second {
		t.Fatal(delay)
	}
	if _, _, err := p.Complete(ctx, "s", "p"); !errors.Is(err, ErrBreakerOpen) || e.requests() != 30 {
		t.Fatal("the open breaker sent:", err, e.requests())
	}
	// Past its delay it lets two probes through and closes after two
	// successes; a failed probe opens it again.
	b := newBreaker()
	b.cb.HalfOpen()
	if b.open() || !b.permit() || !b.permit() || b.permit() {
		t.Fatal("half-open admits two probes")
	}
	b.record(false)
	b.record(false)
	if !b.cb.IsClosed() {
		t.Fatal(b.cb.State())
	}
	b.cb.HalfOpen()
	if !b.permit() {
		t.Fatal("no probe")
	}
	b.record(true)
	if !b.open() {
		t.Fatal("a failed probe did not open it")
	}

	// 30% of 400 attempts answered 503, from a fixed seed: no run of ten.
	random := rand.New(rand.NewPCG(2026, 1024))
	stream := make([]bool, 400)
	failures, run, longest := 0, 0, 0
	for i := range stream {
		if stream[i] = random.Float64() < 0.3; stream[i] {
			failures, run = failures+1, run+1
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	if longest >= 10 || failures < 100 || failures > 140 {
		t.Fatal("the fixture", failures, longest)
	}
	e = newEndpoint(t, func(n int, w http.ResponseWriter) {
		if stream[n-1] {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	})
	local := localOn(t, e, ModelConfig{MaxAttempts: 1})
	failed := 0
	for i := range stream {
		if _, _, err := local.Complete(ctx, "s", "p"); err != nil {
			failed++
		}
		if state := local.breaker.state(); state != BreakerClosed {
			t.Fatal("open after attempt", i+1, "of the 30% stream:", state)
		}
	}
	if failed != failures || local.breaker.trips.Load() != 0 || e.requests() != len(stream) {
		t.Fatal(failed, failures, local.breaker.trips.Load(), e.requests())
	}
}

// R61: a 200 reply that does not parse, or parses but fails validation, is
// invalid. It is retried once, within the class's attempts, in every class,
// JEV included. It is not a breaker failure: the provider answered.
func TestInvalidReplyIsRetriedOnce(t *testing.T) {
	questions := map[string]Question{"a": binary("Relevant?")}
	decide := func(ctx context.Context, p *Provider) error {
		_, _, err := p.Decide(ctx, "evidence", questions)
		return err
	}
	complete := func(ctx context.Context, p *Provider) error {
		_, _, err := p.Complete(ctx, "s", "p")
		return err
	}
	for _, c := range []struct {
		name           string
		jev            bool
		invalid, valid string
		call           func(context.Context, *Provider) error
		message        string // the failure says so
	}{
		{"jev reply in prose", true, "upstream answered: decision service temporarily degraded", jevReply, decide, "invalid JEV response"},
		{"jev reply cut in half", true, jevReply[:len(jevReply)/2], jevReply, decide, "invalid JEV response"},
		{"jev answers empty", true, `{"model":"jev-1.13.0","answers":{},"usage":{"input_tokens":1000,"output_tokens":1}}`, jevReply, decide, "cardinality mismatch"},
		{"jev answer of another type", true, `{"model":"jev-1.13.0","answers":{"a":{"type":"boolean","value":true}},"usage":{"input_tokens":1000,"output_tokens":1}}`, jevReply, decide, "noul"},
		{"deepseek reply in prose", false, "upstream answered: completion service temporarily degraded", deepseekReply, complete, "invalid completion response"},
		{"deepseek reply without choices", false, `{"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":10}}`, deepseekReply, complete, "invalid completion response"},
		{"deepseek decision in prose", false, completionWith("yes, it is relevant"), completionWith(`{"a":0.7}`), decide, "invalid decision JSON"},
		{"deepseek decision of another cardinality", false, completionWith(`{"a":0.7,"b":0.1}`), completionWith(`{"a":0.7}`), decide, "cardinality mismatch"},
	} {
		for _, class := range []CallClass{Interactive, Flush, Background} {
			t.Run(c.name+"/"+class.String(), func(t *testing.T) {
				ctx := WithCallClass(context.Background(), class)
				provider := func(e *endpoint, config ModelConfig) (*Provider, *attemptLog) {
					var p *Provider
					if c.jev {
						p, _ = jevOn(t, e, config)
					} else {
						p, _ = deepseekOn(t, e, config, nil)
					}
					quickRetries(p)
					log := &attemptLog{}
					Models{Judge: p}.Observe(log)
					return p, log
				}
				invalidThenValid := func(n int, w http.ResponseWriter) {
					if n == 1 {
						io.WriteString(w, c.invalid)
						return
					}
					io.WriteString(w, c.valid)
				}
				alwaysInvalid := func(_ int, w http.ResponseWriter) { io.WriteString(w, c.invalid) }

				// One invalid reply, then a valid one: the call succeeds.
				e := newEndpoint(t, invalidThenValid)
				p, log := provider(e, ModelConfig{})
				if err := c.call(ctx, p); err != nil || e.requests() != 2 {
					t.Fatal(err, e.requests(), log.counts())
				}
				if got := log.counts(); got["invalid"] != 1 || got["ok"] != 1 || len(log.seen()) != 2 {
					t.Fatal(got)
				}
				if failures := p.breaker.cb.Metrics().Failures(); failures != 0 {
					t.Fatal("an invalid reply counted against the provider", failures)
				}
				// Two invalid replies: the call fails as before, about the
				// request, not an unavailable provider.
				e = newEndpoint(t, alwaysInvalid)
				p, log = provider(e, ModelConfig{})
				err := c.call(ctx, p)
				if err == nil || errors.Is(err, ErrModelUnavailable) || !strings.Contains(err.Error(), c.message) || e.requests() != 2 {
					t.Fatal(err, e.requests())
				}
				if got := log.counts(); got["invalid"] != 2 || len(log.seen()) != 2 {
					t.Fatal(got)
				}
				// The retry is one of the class's attempts: with one, none.
				e = newEndpoint(t, alwaysInvalid)
				p, log = provider(e, ModelConfig{MaxAttempts: 1})
				if err := c.call(ctx, p); err == nil || e.requests() != 1 || log.outcomes() != "invalid" {
					t.Fatal(err, e.requests(), log.outcomes())
				}
			})
		}
	}
}

// R62: a Flush attempt waits at most 180 s, bounded by the hard timeout
// (here shortened): past it, the attempt stops waiting and is retried, while
// its request runs on and settles by itself.
func TestFlushAttemptTimeout(t *testing.T) {
	if flushAttemptTimeout != 180*time.Second {
		t.Fatal(flushAttemptTimeout)
	}
	for hard, want := range map[int]time.Duration{600: 180 * time.Second, 0: 90 * time.Second, 120: 120 * time.Second, 30: 30 * time.Second} {
		p, err := NewProvider(ModelConfig{Kind: "local", Model: "m", BaseURL: "http://127.0.0.1:1/v1", TimeoutSeconds: hard}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := p.policies[Flush].attemptTimeout; got != want || p.policies[Background].attemptTimeout != p.client.Timeout {
			t.Fatal(hard, got, p.policies[Background].attemptTimeout)
		}
		p.Close()
	}
	// The first request stalls before its headers until the client leaves.
	e := newEndpointWith(t, func(n int, w http.ResponseWriter, r *http.Request, _ []byte) {
		if n == 1 {
			<-r.Context().Done()
			return
		}
		io.WriteString(w, deepseekReply)
	})
	p, budget := deepseekOn(t, e, ModelConfig{TimeoutSeconds: 3}, nil)
	quickRetries(p)
	p.policies[Flush].attemptTimeout = 300 * time.Millisecond
	log := &attemptLog{}
	Models{Consolidator: p}.Observe(log)
	start := time.Now()
	text, _, err := p.Complete(WithCallClass(context.Background(), Flush), "s", "p")
	if elapsed := time.Since(start); err != nil || text != "ok" || e.requests() != 2 || elapsed < 300*time.Millisecond || elapsed > 2*time.Second {
		t.Fatal(text, err, e.requests(), elapsed)
	}
	if events := log.seen(); log.outcomes() != "retryable ok" || events[0].Duration < 300*time.Millisecond || events[0].Duration > time.Second {
		t.Fatal(log.outcomes(), events)
	}
	// The stalled request ran on to its hard timeout; Close waits for it,
	// and then every reservation is settled.
	p.Close()
	if held, _ := accountHeld(t, budget, "deepseek"); !near(held, 0) {
		t.Fatal("held", held)
	}
	if attempts := budgetAttempts(t, budget); len(attempts) != 2 || !settled(attempts) {
		t.Fatal(attempts)
	}
}

// R62: every attempt of every class fails as an attempt timeout once its
// reply, its headers arrived, has sent no byte for 60 s (here shortened).
// Bytes that keep coming, DeepSeek's keep-alive blank lines among them, keep
// it alive.
func TestBodyIdleTimeout(t *testing.T) {
	if bodyIdleTimeout != 60*time.Second {
		t.Fatal(bodyIdleTimeout)
	}
	p, err := NewProvider(ModelConfig{Kind: "local", Model: "m", BaseURL: "http://127.0.0.1:1/v1", TimeoutSeconds: 600}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for class := range p.policies {
		if p.policies[class].bodyIdle != bodyIdleTimeout {
			t.Fatal(CallClass(class), p.policies[class].bodyIdle)
		}
	}
	p.Close()
	const idle = 300 * time.Millisecond
	for _, class := range []CallClass{Interactive, Flush, Background} {
		t.Run(class.String(), func(t *testing.T) {
			ctx := WithCallClass(context.Background(), class)
			// The first reply stalls mid-body; the second answers.
			stalled := stallMidBody(deepseekReply)
			e := newEndpointWith(t, func(n int, w http.ResponseWriter, r *http.Request, body []byte) {
				if n == 1 {
					stalled(n, w, r, body)
					return
				}
				io.WriteString(w, deepseekReply)
			})
			p, budget := deepseekOn(t, e, ModelConfig{TimeoutSeconds: 10, MaxAttempts: 1}, nil)
			p.policies[class].bodyIdle = idle
			start := time.Now()
			_, _, err := p.Complete(ctx, "s", "p")
			if elapsed := time.Since(start); !errors.Is(err, ErrAttemptTimeout) || elapsed < idle || elapsed > 2*time.Second {
				t.Fatal(err, elapsed)
			}
			// An availability failure, which the breaker counts.
			if failures := p.breaker.cb.Metrics().Failures(); failures != 1 {
				t.Fatal("breaker failures", failures)
			}
			if text, _, err := p.Complete(ctx, "s", "p"); err != nil || text != "ok" {
				t.Fatal(text, err)
			}
			if held, _ := accountHeld(t, budget, "deepseek"); !near(held, 0) {
				t.Fatal("held", held)
			}
		})
	}
	// Blank lines every 75 ms for 600 ms, twice the idle timeout, then the
	// reply: it is not cut.
	t.Run("keep-alive", func(t *testing.T) {
		e := newEndpointWith(t, func(_ int, w http.ResponseWriter, _ *http.Request, _ []byte) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			for range 8 {
				io.WriteString(w, "\n")
				w.(http.Flusher).Flush()
				time.Sleep(75 * time.Millisecond)
			}
			io.WriteString(w, deepseekReply)
		})
		for _, class := range []CallClass{Interactive, Flush, Background} {
			p, _ := deepseekOn(t, e, ModelConfig{TimeoutSeconds: 10, MaxAttempts: 1}, nil)
			p.policies[class].bodyIdle = idle
			before := e.requests()
			if text, _, err := p.Complete(WithCallClass(context.Background(), class), "s", "p"); err != nil || text != "ok" || e.requests() != before+1 {
				t.Fatal(class, text, err)
			}
		}
	})
}

// R63: every reservation settles when its attempt ends. A reply whose
// envelope parses settles at its usage, also when its content is invalid; a
// request cut after it was sent, or answered without metering, settles at
// its input estimate: its input tokens at the input price, no output. A
// refusal costs nothing. Once the calls end, nothing is held.
func TestEveryReservationSettles(t *testing.T) {
	questions := map[string]Question{"a": binary("Relevant?")}
	reply := func(body string) func(int, http.ResponseWriter, *http.Request, []byte) {
		return func(_ int, w http.ResponseWriter, _ *http.Request, _ []byte) { io.WriteString(w, body) }
	}
	status := func(code int) func(int, http.ResponseWriter, *http.Request, []byte) {
		return func(_ int, w http.ResponseWriter, _ *http.Request, _ []byte) { w.WriteHeader(code) }
	}
	hangUp := func(_ int, w http.ResponseWriter, _ *http.Request, _ []byte) {
		if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
			conn.Close()
		}
	}
	resetMidBody := func(_ int, w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, deepseekReply[:20])
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	}
	stallBeforeHeaders := func(_ int, _ http.ResponseWriter, r *http.Request, _ []byte) { <-r.Context().Done() }
	type settlement int
	const (
		atUsage settlement = iota
		atEstimate
		released
	)
	for _, c := range []struct {
		name   string
		jev    bool
		answer func(int, http.ResponseWriter, *http.Request, []byte)
		// decide makes the call a decision (for DeepSeek, a completer's).
		decide bool
		// config: its resilience settings; prepare shortens its timeouts.
		config  ModelConfig
		prepare func(*Provider)
		// cancelAfter, when positive, is when the caller gives up.
		cancelAfter time.Duration
		settles     settlement
	}{
		{name: "parse failure: no envelope", answer: reply("upstream answered: completion service temporarily degraded"), settles: atEstimate},
		{name: "parse failure: an envelope without choices", answer: reply(`{"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":10}}`), settles: atUsage},
		{name: "parse failure: a JEV reply in prose", jev: true, decide: true, answer: reply("upstream answered: decision service temporarily degraded"), settles: atEstimate},
		{name: "parse failure: a JEV reply cut in half", jev: true, decide: true, answer: reply(jevReply[:len(jevReply)/2]), settles: atEstimate},
		{name: "a reply without metering", answer: reply(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`), settles: atEstimate},
		{name: "a reply whose metering the budget refuses", answer: reply(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":0,"completion_tokens":0}}`), settles: atEstimate},
		{name: "a 2xx reply other than 200", answer: status(http.StatusAccepted), settles: atEstimate},
		{name: "an oversized reply", answer: reply(strings.Repeat(" ", maxReplyBytes+16)), settles: atEstimate},
		{name: "a JEV reply without metering", jev: true, decide: true, answer: reply(`{"model":"jev-1.13.0","answers":{"a":{"type":"noul","noul":0.8}}}`), settles: atEstimate},
		{name: "validation failure: JEV answers empty", jev: true, decide: true, answer: reply(`{"model":"jev-1.13.0","answers":{},"usage":{"input_tokens":1000,"output_tokens":1}}`), settles: atUsage},
		{name: "validation failure: a DeepSeek decision of another cardinality", decide: true, answer: reply(completionWith(`{"a":0.7,"b":0.1}`)), settles: atUsage},
		{name: "reset after send", answer: hangUp, settles: atEstimate},
		{name: "reset mid-body", answer: resetMidBody, settles: atEstimate},
		{name: "stall mid-body", answer: stallMidBody(deepseekReply), config: ModelConfig{TimeoutSeconds: 10},
			prepare: func(p *Provider) { p.policies[Interactive].bodyIdle = 300 * time.Millisecond }, settles: atEstimate},
		{name: "stall before headers", answer: stallBeforeHeaders, config: ModelConfig{TimeoutSeconds: 1}, settles: atEstimate},
		{name: "an abandoned attempt's stall", answer: stallBeforeHeaders, config: ModelConfig{TimeoutSeconds: 2},
			prepare: func(p *Provider) { p.policies[Interactive].attemptTimeout = 200 * time.Millisecond }, settles: atEstimate},
		{name: "the caller gives up after send", answer: stallBeforeHeaders, config: ModelConfig{TimeoutSeconds: 10}, cancelAfter: 200 * time.Millisecond, settles: atEstimate},
		{name: "429", answer: status(http.StatusTooManyRequests), settles: released},
		{name: "5xx", answer: status(http.StatusServiceUnavailable), settles: released},
		{name: "a JEV 5xx", jev: true, decide: true, answer: status(http.StatusBadGateway), settles: released},
	} {
		t.Run(c.name, func(t *testing.T) {
			var sentBytes int
			var mu sync.Mutex
			e := newEndpointWith(t, func(n int, w http.ResponseWriter, r *http.Request, body []byte) {
				mu.Lock()
				sentBytes = len(body)
				mu.Unlock()
				c.answer(n, w, r, body)
			})
			config := c.config
			config.MaxAttempts = 1 // what one attempt's ending settles is the subject
			var p *Provider
			var budget *Budget
			kind := "deepseek"
			if c.jev {
				kind = "jev"
				p, budget = jevOn(t, e, config)
			} else {
				p, budget = deepseekOn(t, e, config, nil)
			}
			if c.prepare != nil {
				c.prepare(p)
			}
			ctx := context.Background()
			if c.cancelAfter > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, c.cancelAfter)
				defer cancel()
			}
			var err error
			if c.decide {
				_, _, err = p.Decide(ctx, "evidence", questions)
			} else {
				_, _, err = p.Complete(ctx, "s", "p")
			}
			if err == nil {
				t.Fatal("the fault did not fail the call")
			}
			// The calls have ended; Close waits for the requests that run on.
			p.Close()
			mu.Lock()
			n := sentBytes
			mu.Unlock()
			held, spent := accountHeld(t, budget, kind)
			attempts := budgetAttempts(t, budget)
			if !near(held, 0) || len(attempts) != 1 || attempts[0].actual == nil || !near(*attempts[0].actual, spent) {
				t.Fatal("held", held, "spent", spent, "attempts", attempts, err)
			}
			var want float64
			switch {
			case c.settles == released:
				want = 0
			case c.settles == atUsage && c.jev:
				want = jevReplyUSD
			case c.settles == atUsage:
				want = deepseekReplyCNY
			case c.jev:
				want = float64(n) * 0.042 / 1e6
			default:
				want = float64(n) * 2 / 1e6
			}
			if n == 0 || !near(spent, want) {
				t.Fatal("spent", spent, "want", want, "for a body of", n, "bytes;", err)
			}
		})
	}
}
