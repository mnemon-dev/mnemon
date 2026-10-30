package memoryagent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// These tests hold the providers' transport to ruling R88: a provider speaks
// HTTP/1.1, also to an endpoint that offers HTTP/2, so that its concurrent
// requests travel on connections of their own and a dropped connection fails
// one attempt, which a retry absorbs. Over HTTP/2 a provider's requests
// shared one connection, and its loss failed all of them at once: as many
// availability failures in a row, enough to open the breaker (the final
// review's Important 1). And the retry budget's floor counts a retry in the
// second it starts (Minor 5).

// openAIReply is an OpenAI completion reply with its metering.
const openAIReply = `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":10}}`

// connKey keys a request's connection in its context (tlsEndpoint).
type connKey struct{}

// tlsEndpoint is an httptest model endpoint over TLS that offers HTTP/2 and
// HTTP/1.1 (ALPN), as the providers' endpoints do. answer writes the reply
// to its n-th request (n from 1), and gets the request's connection.
type tlsEndpoint struct {
	*httptest.Server
	mu     sync.Mutex
	protos []string // each request's protocol, in arrival order
}

func newTLSEndpoint(t *testing.T, answer func(n int, w http.ResponseWriter, conn net.Conn)) *tlsEndpoint {
	t.Helper()
	return newTLSEndpointWith(t, answer, nil)
}

// newTLSEndpointWith is newTLSEndpoint whose server reports the state
// changes of its connections to onConn, when not nil.
func newTLSEndpointWith(t *testing.T, answer func(n int, w http.ResponseWriter, conn net.Conn), onConn func(net.Conn, http.ConnState)) *tlsEndpoint {
	t.Helper()
	e := &tlsEndpoint{}
	e.Server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		e.mu.Lock()
		e.protos = append(e.protos, r.Proto+" "+r.TLS.NegotiatedProtocol)
		n := len(e.protos)
		e.mu.Unlock()
		answer(n, w, r.Context().Value(connKey{}).(net.Conn))
	}))
	e.Config.ConnContext = func(ctx context.Context, conn net.Conn) context.Context {
		return context.WithValue(ctx, connKey{}, conn)
	}
	e.Config.ConnState = onConn
	e.EnableHTTP2 = true
	e.StartTLS()
	t.Cleanup(e.Close)
	return e
}

// protocols are the requests' protocols so far, each with the protocol
// ALPN chose for its connection: "HTTP/2.0 h2", or "HTTP/1.1 " when the
// client offered none, as an HTTP/1.1 client does not.
func (e *tlsEndpoint) protocols() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string{}, e.protos...)
}

// openAIOverTLS is a metered OpenAI provider whose requests go to e over
// TLS, trusting e's certificate, with a budget file of its own. The provider
// is closed when the test ends, before its budget.
func openAIOverTLS(t *testing.T, e *tlsEndpoint) (*Provider, *Budget) {
	t.Helper()
	budget, err := OpenBudget(filepath.Join(t.TempDir(), "budget.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { budget.Close() })
	t.Setenv("UNIT_OPENAI_CREDENTIAL", "test-only-placeholder")
	p, err := NewProvider(ModelConfig{Kind: "openai", Model: "gpt-4.1-mini", BaseURL: e.URL + "/v1", KeyEnv: "UNIT_OPENAI_CREDENTIAL"}, budget)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	roots := x509.NewCertPool()
	roots.AddCert(e.Certificate())
	p.client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{RootCAs: roots}
	return p, budget
}

// R88: a provider speaks HTTP/1.1 to an endpoint that offers HTTP/2: it
// does not offer h2 in ALPN, so the endpoint never chooses it.
func TestProvidersNegotiateHTTP1(t *testing.T) {
	e := newTLSEndpoint(t, func(_ int, w http.ResponseWriter, _ net.Conn) { io.WriteString(w, openAIReply) })
	p, _ := openAIOverTLS(t, e)
	for range 2 {
		if _, _, err := p.Complete(context.Background(), "s", "p"); err != nil {
			t.Fatal(err)
		}
	}
	if got := e.protocols(); len(got) != 2 || got[0] != "HTTP/1.1 " || got[1] != got[0] {
		t.Fatalf("the endpoint saw %q, want HTTP/1.1 with no protocol chosen by ALPN", got)
	}
}

// R88: twelve calls are in flight at once, and the connection of one of them
// drops while its request waits for the reply. Each call has a connection of
// its own, so the drop costs that one attempt, which is retried on a new
// connection: all twelve calls succeed, the breaker stays closed, and every
// reservation settles. A warm connection came first, which HTTP/2 would have
// given all twelve.
func TestOneDroppedConnectionCostsOneAttempt(t *testing.T) {
	const burst = 12
	inFlight := make(chan struct{})
	var all sync.Once
	e := newTLSEndpoint(t, func(n int, w http.ResponseWriter, conn net.Conn) {
		if n > 1 && n <= 1+burst {
			// The burst's requests wait until all of them are here.
			if n == 1+burst {
				all.Do(func() { close(inFlight) })
			}
			select {
			case <-inFlight:
			case <-time.After(10 * time.Second):
				t.Error("the burst's calls were not in flight at once")
			}
			if n == 1+burst {
				conn.Close() // the last one's connection drops
				return
			}
		}
		io.WriteString(w, openAIReply)
	})
	p, budget := openAIOverTLS(t, e)
	log := &attemptLog{}
	Models{Planner: p}.Observe(log)
	ctx := context.Background()
	if _, _, err := p.Complete(ctx, "s", "p"); err != nil {
		t.Fatal(err)
	}
	var calls sync.WaitGroup
	errs := make(chan error, burst)
	for range burst {
		calls.Add(1)
		go func() {
			defer calls.Done()
			_, _, err := p.Complete(ctx, "s", "p")
			errs <- err
		}()
	}
	calls.Wait()
	close(errs)
	failed := []string{}
	for err := range errs {
		if err != nil {
			failed = append(failed, err.Error())
		}
	}
	counts := log.counts()
	if len(failed) > 0 || counts["retryable"]+counts["failed"] != 1 || counts["ok"] != 1+burst || e.requests() != 2+burst {
		t.Fatalf("one dropped connection failed %d of %d calls (%q); attempts %v, requests %d", len(failed), burst, failed, counts, e.requests())
	}
	if state, trips := p.breaker.state(), p.breaker.trips.Load(); state != BreakerClosed || trips != 0 {
		t.Fatalf("one dropped connection left the breaker %s after %d trips", state, trips)
	}
	if held, _ := accountHeld(t, budget, "openai"); !near(held, 0) {
		t.Fatal("held after the burst:", held)
	}
}

// requests counts the requests e has read.
func (e *tlsEndpoint) requests() int { return len(e.protocols()) }

// R88, R1: over HTTP/1.1 a request in flight has a connection of its own,
// and a provider's pool keeps one for every permit of its three bulkheads:
// 32 Interactive, 8 Flush and 8 Background calls are in flight at once, and
// the next as many reuse their connections, dialing none.
func TestBulkheadsFitTheConnectionPool(t *testing.T) {
	const calls = interactiveCalls + flushCalls + backgroundCalls
	var dials atomic.Int32
	gates := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
	e := newTLSEndpointWith(t, func(n int, w http.ResponseWriter, _ net.Conn) {
		gate := gates[(n-1)/calls]
		if n%calls == 0 {
			close(gate) // the round's last call is here: all of them are in flight
		}
		select {
		case <-gate:
		case <-time.After(10 * time.Second):
			t.Error("a round's calls were not in flight at once")
		}
		io.WriteString(w, openAIReply)
	}, func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			dials.Add(1)
		}
	})
	p, _ := openAIOverTLS(t, e)
	classes := map[CallClass]int{Interactive: interactiveCalls, Flush: flushCalls, Background: backgroundCalls}
	for round := range 2 {
		var wg sync.WaitGroup
		for class, n := range classes {
			for range n {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if _, _, err := p.Complete(WithCallClass(context.Background(), class), "s", "p"); err != nil {
						t.Error(class, err)
					}
				}()
			}
		}
		wg.Wait()
		if got := dials.Load(); got != calls {
			t.Fatalf("after round %d the provider had opened %d connections, want %d", round+1, got, calls)
		}
	}
	if got := e.requests(); got != 2*calls {
		t.Fatal("requests", got)
	}
}

// Minor 5: the retry budget's floor, retryFloor retries a second however few
// calls there were, counts a retry in the second it starts, not the second it
// is decided in. Twelve Flush calls refused at once at low traffic (408,
// which may pass on a retry and is no breaker failure) retry after jittered
// waits that spread over two seconds: more than retryFloor of them retry, no
// more than retryFloor start in any second, and each call the floor refuses
// fails once, with its refusal shown as a "budget" attempt.
func TestRetryFloorCountsRetriesAsTheyStart(t *testing.T) {
	const burst = 12
	e := newEndpoint(t, func(n int, w http.ResponseWriter) {
		if n <= burst {
			w.WriteHeader(http.StatusRequestTimeout)
			return
		}
		io.WriteString(w, deepseekReply)
	})
	p, budget := deepseekOn(t, e, ModelConfig{MaxAttempts: 2}, nil)
	// The burst's waits spread evenly over the Flush class's first backoff,
	// 2 s: 2 s × (k + 0.5) / 12 for k = 0 … 11.
	var draws atomic.Int32
	p.random = func() float64 { return (float64((draws.Add(1)-1)%burst) + 0.5) / burst }
	log := &attemptLog{}
	Models{Planner: p}.Observe(log)
	// The burst is refused 50 ms into a second, within that second.
	time.Sleep(time.Until(time.Now().Truncate(time.Second).Add(time.Second + 50*time.Millisecond)))
	ctx := WithCallClass(context.Background(), Flush)
	var calls sync.WaitGroup
	errs := make(chan error, burst)
	for range burst {
		calls.Add(1)
		go func() {
			defer calls.Done()
			_, _, err := p.Complete(ctx, "s", "p")
			errs <- err
		}()
	}
	calls.Wait()
	close(errs)
	succeeded, spent := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			succeeded++
		case strings.Contains(err.Error(), "the provider's retry budget is spent"):
			spent++
		default:
			t.Error("a call failed otherwise:", err)
		}
	}
	retried := e.requests() - burst
	if retried <= retryFloor || succeeded != retried || spent != burst-retried || log.counts()["budget"] != spent {
		t.Fatalf("a burst of %d refusals: %d retried, %d succeeded, %d spent, attempts %v", burst, retried, succeeded, spent, log.counts())
	}
	perSecond := map[int64]int{}
	for i := burst; i < e.requests(); i++ {
		perSecond[e.arrived(i).Unix()]++
	}
	for second, n := range perSecond {
		if n > retryFloor {
			t.Fatalf("%d retries started in second %d, above the floor of %d", n, second, retryFloor)
		}
	}
	if held, _ := accountHeld(t, budget, "deepseek"); !near(held, 0) {
		t.Fatal("held after the burst:", held)
	}
}
