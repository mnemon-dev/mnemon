package memoryagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// These tests hold the resilience layer to rulings R65 and R67, from Task
// 16's review: a provider that rejects the credential or the balance is
// counted, so that it pages (R65), and a 429 without Retry-After is waited
// out patiently by the calls a Search waits for (R67). The settlement of the
// panic path (R63) is here too.

// R65: a refusal of the credential or the balance (401, 402, 403: a wrong or
// revoked key, an empty balance) is a rejection. As before it is not
// retried and it is no breaker failure, however many come in a row; its
// attempt's outcome is "rejected", its reservation is released, and its
// error says the status, never the body. The providers count their
// rejections by kind (Models.Rejections, /admin/status's rejectedByKind),
// from 0 for every kind among the models.
func TestRejectedIsCountedAndNotRetried(t *testing.T) {
	// What DeepSeek answers when the balance is spent; the body is not read.
	const body = `{"error":{"message":"Insufficient Balance","type":"unknown_error","param":null,"code":"invalid_request_error"}}`
	var mu sync.Mutex
	status := http.StatusUnauthorized
	answer := func(_ int, w http.ResponseWriter) {
		mu.Lock()
		code := status
		mu.Unlock()
		w.WriteHeader(code)
		io.WriteString(w, body)
	}
	answerWith := func(code int) {
		mu.Lock()
		defer mu.Unlock()
		status = code
	}
	deepseekEndpoint, jevEndpoint := newEndpoint(t, answer), newEndpoint(t, answer)
	deepseek, deepseekBudget := deepseekOn(t, deepseekEndpoint, ModelConfig{}, nil)
	jev, jevBudget := jevOn(t, jevEndpoint, ModelConfig{})
	models := Models{Planner: deepseek, Judge: jev}
	log := &attemptLog{}
	models.Observe(log)
	if got := models.Rejections(); fmt.Sprint(got) != fmt.Sprint(map[string]int64{"deepseek": 0, "jev": 0}) {
		t.Fatal("before any call:", got)
	}
	questions := map[string]Question{"a": binary("Relevant?")}
	calls := 0
	// Each code in each class, twice: 18 rejections in a row per provider.
	for range 2 {
		for _, class := range []CallClass{Interactive, Flush, Background} {
			for _, code := range []int{http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusForbidden} {
				answerWith(code)
				ctx := WithCallClass(context.Background(), class)
				_, _, completionErr := deepseek.Complete(ctx, "s", "p")
				_, _, decisionErr := jev.Decide(ctx, "evidence", questions)
				calls++
				for kind, err := range map[string]error{"deepseek": completionErr, "jev": decisionErr} {
					if err == nil || !errors.Is(err, ErrModelUnavailable) || !strings.Contains(err.Error(), fmt.Sprintf("model HTTP %d", code)) ||
						strings.Contains(err.Error(), "Insufficient Balance") || strings.Contains(err.Error(), "attempts") {
						t.Fatal(kind, class, code, err)
					}
				}
				// One attempt each: nothing was retried.
				if deepseekEndpoint.requests() != calls || jevEndpoint.requests() != calls {
					t.Fatal(class, code, "requests", deepseekEndpoint.requests(), jevEndpoint.requests(), "for", calls, "calls")
				}
				if got := models.Rejections(); len(got) != 2 || got["deepseek"] != int64(calls) || got["jev"] != int64(calls) {
					t.Fatal(class, code, got, "after", calls, "calls")
				}
			}
		}
	}
	events := log.seen()
	if len(events) != 2*calls {
		t.Fatal(len(events), "events for", 2*calls, "attempts")
	}
	for _, e := range events {
		if e.Outcome != "rejected" || e.Attempt != 1 || e.Retried || e.Status < http.StatusUnauthorized || e.Status > http.StatusForbidden {
			t.Fatal(e)
		}
	}
	// Neither breaker moved: closed, no failure counted, no trip.
	for _, p := range []*Provider{deepseek, jev} {
		if p.breaker.state() != BreakerClosed || p.breaker.cb.Metrics().Failures() != 0 || p.breaker.trips.Load() != 0 {
			t.Fatal(p.config.Kind, p.breaker.state(), p.breaker.cb.Metrics().Failures(), p.breaker.trips.Load())
		}
	}
	// A refusal costs nothing: every reservation was released.
	for kind, budget := range map[string]*Budget{"deepseek": deepseekBudget, "jev": jevBudget} {
		if held, spent := accountHeld(t, budget, kind); !near(held, 0) || spent != 0 {
			t.Fatal(kind, "held", held, "spent", spent)
		}
	}
	// Another refusal is no rejection: a 400 is about the request.
	answerWith(http.StatusBadRequest)
	if _, _, err := deepseek.Complete(context.Background(), "s", "p"); err == nil || errors.Is(err, ErrModelUnavailable) {
		t.Fatal(err)
	}
	if events = log.seen(); events[len(events)-1].Outcome != "failed" || models.Rejections()["deepseek"] != int64(calls) {
		t.Fatal(events[len(events)-1], models.Rejections())
	}
	// A kind's providers add up; models of no provider count nothing (an
	// empty map, not nil).
	second, _ := deepseekOn(t, deepseekEndpoint, ModelConfig{}, nil)
	answerWith(http.StatusPaymentRequired)
	if _, _, err := second.Complete(context.Background(), "s", "p"); err == nil {
		t.Fatal("a 402 answered")
	}
	if got := (Models{Planner: deepseek, Judge: jev, Consolidator: second}).Rejections(); len(got) != 2 || got["deepseek"] != int64(calls)+1 || got["jev"] != int64(calls) {
		t.Fatal(got)
	}
	if got := (Models{Planner: completeFunc(nil)}).Rejections(); got == nil || len(got) != 0 {
		t.Fatalf("got %#v", got)
	}
}

// patiently scales p's patient throttling down by 40, in the classes that
// have it: the k-th throttled retry waits 50 ms × 2^(k−1) (50, 100, 200 and
// 400 ms for 2, 4, 8 and 16 s), 750 ms in all at most (for 30 s). The class's
// own backoff is a millisecond, so each wait is its floor.
func patiently(p *Provider) {
	quickRetries(p)
	for class := range p.policies {
		if p.policies[class].throttledRetries > 0 {
			p.policies[class].throttleFloor, p.policies[class].maxThrottleWait = 50*time.Millisecond, 750*time.Millisecond
		}
	}
}

// impatient makes p retry a 429 without Retry-After as one of its attempts,
// in every class, as before patient throttling (R67): a test of one attempt
// a call (MaxAttempts 1) that answers 429 makes one attempt.
func impatient(p *Provider) {
	for class := range p.policies {
		p.policies[class].throttledRetries = 0
	}
}

// throttleStorm is an endpoint that answers 429, without Retry-After, to
// every request that arrives within storm of its first, and deepseekReply
// after it.
func throttleStorm(t *testing.T, storm time.Duration) *endpoint {
	t.Helper()
	var mu sync.Mutex
	var first time.Time
	return newEndpointWith(t, func(_ int, w http.ResponseWriter, _ *http.Request, _ []byte) {
		mu.Lock()
		if first.IsZero() {
			first = time.Now()
		}
		within := time.Since(first) < storm
		mu.Unlock()
		if within {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		io.WriteString(w, deepseekReply)
	})
}

// R67: an Interactive or a Flush call refused with 429 and no Retry-After
// waits it out patiently. Its k-th throttled retry waits max(the class's
// backoff, 2 s × 2^(k−1)): 2, 4, 8 and 16 s. It makes up to 5 of them, which
// are not among the class's attempts, and waits 30 s in all at most, never
// past the caller's deadline. A Retry-After is waited as before, within the
// class's attempts, and Background calls keep their policy. The breaker never
// counts a 429 (R59).
func TestPatientThrottleWithoutRetryAfter(t *testing.T) {
	if throttledRetries != 5 || throttleFloor != 2*time.Second || maxThrottleWait != 30*time.Second {
		t.Fatal(throttledRetries, throttleFloor, maxThrottleWait)
	}
	// The policies: a model's maxAttempts leaves the throttled retries alone.
	p, err := NewProvider(ModelConfig{Kind: "local", Model: "m", BaseURL: "http://127.0.0.1:1/v1", MaxAttempts: 2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for class, want := range map[CallClass]struct {
		retries      int
		floor, total time.Duration
	}{
		Interactive: {5, 2 * time.Second, 30 * time.Second},
		Flush:       {5, 2 * time.Second, 30 * time.Second},
		Background:  {}, // as it was: a 429 is one of its attempts
	} {
		policy := p.policies[class]
		if policy.maxAttempts != 2 || policy.throttledRetries != want.retries || policy.throttleFloor != want.floor || policy.maxThrottleWait != want.total {
			t.Fatal(class, policy.maxAttempts, policy.throttledRetries, policy.throttleFloor, policy.maxThrottleWait)
		}
	}
	t.Run("the waits", func(t *testing.T) {
		// With the real constants. In a storm from the call's first attempt
		// the k-th throttled retry is the call's k-th retry: it waits 2, 4,
		// 8, 16 (and 32) s whatever the jitter, in both classes, whose backoff
		// (Interactive 0.5 s → 8 s, Flush 2 s → 60 s) is then at most that.
		for _, class := range []CallClass{Interactive, Flush} {
			policy := p.policies[class]
			for k, want := range []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second} {
				for _, random := range []float64{0, 0.5, 0.999} {
					if got := policy.throttleDelay(k+1, k+1, random); got != want {
						t.Error(class, "throttled retry", k+1, "random", random, got, "want", want)
					}
				}
			}
		}
		// Only the class's backoff is jittered, and never below the floor: a
		// Flush call whose first two attempts drew 503s, throttled at its
		// third, waits a random part of its third backoff (8 s) when that is
		// longer than the first floor, 2 s.
		flush := p.policies[Flush]
		for random, want := range map[float64]time.Duration{0: 2 * time.Second, 0.125: 2 * time.Second, 0.5: 4 * time.Second, 0.75: 6 * time.Second} {
			if got := flush.throttleDelay(3, 1, random); got != want {
				t.Error("random", random, got, "want", want)
			}
		}
	})

	// Calls, with the waits scaled down by 40 (patiently): a storm of 12 s
	// is one of 300 ms, the 30 s are 750 ms.
	closed := func(t *testing.T, p *Provider) {
		t.Helper()
		if p.breaker.state() != BreakerClosed || p.breaker.cb.Metrics().Failures() != 0 || p.breaker.trips.Load() != 0 {
			t.Fatal("the throttling moved the breaker:", p.breaker.state(), p.breaker.cb.Metrics().Failures(), p.breaker.trips.Load())
		}
	}
	// waited checks the requests' gaps: each the wait of its throttled retry
	// at least, never doubled.
	waited := func(t *testing.T, e *endpoint, waits ...time.Duration) {
		t.Helper()
		if e.requests() != len(waits)+1 {
			t.Fatal(e.requests(), "requests, want", len(waits)+1)
		}
		for i, wait := range waits {
			if gap := e.arrived(i + 1).Sub(e.arrived(i)); gap < wait || gap >= wait*3/2+50*time.Millisecond {
				t.Fatal("throttled retry", i+1, "waited", gap, "want", wait)
			}
		}
	}
	const ms = time.Millisecond
	for _, class := range []CallClass{Interactive, Flush} {
		t.Run(class.String(), func(t *testing.T) {
			ctx := WithCallClass(context.Background(), class)
			call := func(t *testing.T, e *endpoint, ctx context.Context) (*Provider, *Budget, *attemptLog, error, time.Duration) {
				t.Helper()
				p, budget := deepseekOn(t, e, ModelConfig{}, nil)
				patiently(p)
				log := &attemptLog{}
				Models{Planner: p}.Observe(log)
				start := time.Now()
				_, _, err := p.Complete(ctx, "s", "p")
				return p, budget, log, err, time.Since(start)
			}

			t.Run("a storm of 12 s passes after three throttled retries", func(t *testing.T) {
				e := throttleStorm(t, 300*ms)
				p, budget, log, err, _ := call(t, e, ctx)
				if err != nil || log.outcomes() != "throttled throttled throttled ok" {
					t.Fatal(err, log.outcomes())
				}
				waited(t, e, 50*ms, 100*ms, 200*ms)
				closed(t, p)
				if held, spent := accountHeld(t, budget, "deepseek"); !near(held, 0) || !near(spent, deepseekReplyCNY) {
					t.Fatal("held", held, "spent", spent)
				}
			})
			t.Run("four throttled retries, the 30 s, carry a call through a storm of 20 s", func(t *testing.T) {
				e := throttleStorm(t, 500*ms)
				p, _, log, err, _ := call(t, e, ctx)
				if err != nil || log.outcomes() != "throttled throttled throttled throttled ok" {
					t.Fatal(err, log.outcomes())
				}
				waited(t, e, 50*ms, 100*ms, 200*ms, 400*ms)
				closed(t, p)
			})
			t.Run("a storm of 40 s fails the call at the 30 s", func(t *testing.T) {
				e := throttleStorm(t, time.Second)
				p, budget, log, err, elapsed := call(t, e, ctx)
				// A fifth throttled retry would wait 800 ms, past the 750: the
				// call ends at its fourth, when the storm still lasts.
				if err == nil || !errors.Is(err, ErrModelUnavailable) || !strings.Contains(err.Error(), "model HTTP 429") || !strings.Contains(err.Error(), "(after 5 attempts)") ||
					strings.Contains(err.Error(), "gave up") || elapsed < 750*ms || elapsed >= 1500*ms {
					t.Fatal(err, elapsed)
				}
				if got := log.outcomes(); got != strings.TrimSpace(strings.Repeat("throttled ", 5)) {
					t.Fatal(got)
				}
				waited(t, e, 50*ms, 100*ms, 200*ms, 400*ms)
				closed(t, p)
				if held, spent := accountHeld(t, budget, "deepseek"); !near(held, 0) || spent != 0 {
					t.Fatal("held", held, "spent", spent)
				}
			})
			t.Run("a 429 is none of the class's attempts", func(t *testing.T) {
				attempts := map[CallClass]int{Interactive: interactiveAttempts, Flush: backgroundAttempts}[class] // 4, 3
				// Two 429s, then 503s: all of the class's attempts are left for
				// them, so its last one answers after attempts−1 of them, and
				// attempts of them fail the call.
				for _, failures := range []int{attempts - 1, attempts} {
					e := newEndpoint(t, func(n int, w http.ResponseWriter) {
						switch {
						case n <= 2:
							w.WriteHeader(http.StatusTooManyRequests)
						case n <= 2+failures:
							w.WriteHeader(http.StatusServiceUnavailable)
						default:
							io.WriteString(w, deepseekReply)
						}
					})
					_, _, log, err, _ := call(t, e, ctx)
					want := "throttled throttled " + strings.Repeat("retryable ", failures) + "ok"
					if failures == attempts {
						want = strings.TrimSuffix(want, " ok")
					}
					if (err == nil) != (failures < attempts) || log.outcomes() != want || e.requests() != 2+attempts {
						t.Fatal(failures, "503s:", err, log.outcomes(), e.requests())
					}
				}
			})
			t.Run("the caller's deadline", func(t *testing.T) {
				// 300 ms: the retries after 50 and 100 ms are made, the one
				// that would wait 200 ms more is not.
				e := throttleStorm(t, time.Hour)
				short, cancel := context.WithTimeout(ctx, 300*ms)
				defer cancel()
				p, _, log, err, elapsed := call(t, e, short)
				if err == nil || !strings.Contains(err.Error(), "model HTTP 429") || strings.Contains(err.Error(), "gave up") || elapsed >= 300*ms ||
					log.outcomes() != "throttled throttled throttled" {
					t.Fatal(err, elapsed, log.outcomes())
				}
				waited(t, e, 50*ms, 100*ms)
				closed(t, p)
			})
			t.Run("five throttled retries at most", func(t *testing.T) {
				e := throttleStorm(t, time.Hour)
				p, _ := deepseekOn(t, e, ModelConfig{}, nil)
				quickRetries(p)
				p.policies[class].throttleFloor, p.policies[class].maxThrottleWait = 5*ms, time.Hour
				log := &attemptLog{}
				Models{Planner: p}.Observe(log)
				if _, _, err := p.Complete(ctx, "s", "p"); err == nil || log.outcomes() != strings.TrimSpace(strings.Repeat("throttled ", 6)) {
					t.Fatal(err, log.outcomes())
				}
				waited(t, e, 5*ms, 10*ms, 20*ms, 40*ms, 80*ms)
				closed(t, p)
			})
		})
	}

	// With a Retry-After the 429 is waited as before, as one of the class's
	// attempts: two with maxAttempts 2.
	t.Run("Retry-After", func(t *testing.T) {
		e := newEndpoint(t, func(_ int, w http.ResponseWriter) {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
		})
		p, _ := deepseekOn(t, e, ModelConfig{MaxAttempts: 2}, nil)
		patiently(p)
		if _, _, err := p.Complete(context.Background(), "s", "p"); err == nil || e.requests() != 2 {
			t.Fatal(err, e.requests())
		}
		if gap := e.arrived(1).Sub(e.arrived(0)); gap < time.Second {
			t.Fatal("retried before its Retry-After", gap)
		}
	})
	// A Background call's 429s are its attempts, three, as before.
	t.Run("Background", func(t *testing.T) {
		e := throttleStorm(t, time.Hour)
		p, _ := deepseekOn(t, e, ModelConfig{}, nil)
		patiently(p)
		log := &attemptLog{}
		Models{Consolidator: p}.Observe(log)
		if _, _, err := p.Complete(WithCallClass(context.Background(), Background), "s", "p"); err == nil || e.requests() != 3 || log.outcomes() != "throttled throttled throttled" {
			t.Fatal(err, e.requests(), log.outcomes())
		}
		closed(t, p)
	})
}

// panicOnClose is a reply body whose Close panics.
type panicOnClose struct{ io.Reader }

func (panicOnClose) Close() error { panic("the body broke") }

// R63 on the panic path: a panic in a request's exchange fails its attempt,
// and the process goes on. A reservation the request left unresolved is
// settled at its input estimate; one it had settled is not settled again;
// and when the settlement panics too, the error says the reservation may
// still be held. Either way the flight gives its bulkhead permit back and
// ends, so Close does not wait for it.
func TestPanicPathSettles(t *testing.T) {
	// provider is a DeepSeek provider of one attempt a call whose requests
	// go to roundTrip instead of the network.
	provider := func(t *testing.T, roundTrip func(p *Provider, r *http.Request) (*http.Response, error)) (*Provider, *Budget, *attemptLog) {
		t.Helper()
		e := newEndpoint(t, func(int, http.ResponseWriter) { t.Error("a request reached the endpoint") })
		p, budget := deepseekOn(t, e, ModelConfig{MaxAttempts: 1}, nil)
		p.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) { return roundTrip(p, r) })
		log := &attemptLog{}
		Models{Planner: p}.Observe(log)
		return p, budget, log
	}
	ended := func(t *testing.T, p *Provider) {
		t.Helper()
		if free := freePermits(p.policies[Interactive].calls); free != interactiveCalls {
			t.Fatal("permits", free)
		}
		closed := make(chan struct{})
		go func() {
			p.Close()
			close(closed)
		}()
		select {
		case <-closed:
		case <-time.After(5 * time.Second):
			t.Fatal("Close waits for the flight")
		}
	}

	t.Run("before the reservation is resolved", func(t *testing.T) {
		var sent atomic.Int64
		p, budget, log := provider(t, func(_ *Provider, r *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(r.Body)
			sent.Store(int64(len(body)))
			panic("the transport broke")
		})
		_, _, err := p.Complete(context.Background(), "s", "p")
		if err == nil || !strings.Contains(err.Error(), "panic: the transport broke") || !strings.Contains(err.Error(), "; reservation settled at its input estimate") ||
			strings.Contains(err.Error(), "remains held") || log.outcomes() != "failed" {
			t.Fatal(err, log.outcomes())
		}
		ended(t, p)
		held, spent := accountHeld(t, budget, "deepseek")
		if attempts := budgetAttempts(t, budget); !near(held, 0) || sent.Load() == 0 || !near(spent, float64(sent.Load())*2/1e6) ||
			len(attempts) != 1 || attempts[0].actual == nil || !near(*attempts[0].actual, spent) {
			t.Fatal("held", held, "spent", spent, "for", sent.Load(), "bytes;", attempts)
		}
	})
	t.Run("after the reservation is settled", func(t *testing.T) {
		// The reply arrives and is settled at its metering; then closing its
		// body panics.
		p, budget, log := provider(t, func(_ *Provider, r *http.Request) (*http.Response, error) {
			io.Copy(io.Discard, r.Body)
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: panicOnClose{strings.NewReader(deepseekReply)}}, nil
		})
		_, _, err := p.Complete(context.Background(), "s", "p")
		if err == nil || !strings.Contains(err.Error(), "panic: the body broke") || strings.Contains(err.Error(), "reservation") || log.outcomes() != "failed" {
			t.Fatal(err, log.outcomes())
		}
		ended(t, p)
		held, spent := accountHeld(t, budget, "deepseek")
		if attempts := budgetAttempts(t, budget); !near(held, 0) || !near(spent, deepseekReplyCNY) || len(attempts) != 1 || attempts[0].actual == nil || !near(*attempts[0].actual, spent) {
			t.Fatal("held", held, "spent", spent, attempts)
		}
	})
	t.Run("when the settlement panics too", func(t *testing.T) {
		p, budget, _ := provider(t, func(p *Provider, r *http.Request) (*http.Response, error) {
			io.Copy(io.Discard, r.Body)
			// Without its budget the provider's settlement panics as well:
			// nothing can record the request then.
			p.budget = nil
			panic("the transport broke")
		})
		_, _, err := p.Complete(context.Background(), "s", "p")
		if err == nil || !strings.Contains(err.Error(), "panic: the transport broke") || !strings.Contains(err.Error(), "; any reservation remains held") ||
			strings.Contains(err.Error(), "input estimate") {
			t.Fatal(err)
		}
		ended(t, p)
		if held, spent := accountHeld(t, budget, "deepseek"); !near(held, reservations["deepseek"]) || spent != 0 {
			t.Fatal("held", held, "spent", spent)
		}
	})
}
