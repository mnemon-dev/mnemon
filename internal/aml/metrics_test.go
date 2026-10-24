package aml

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
)

// adminCall sends a request to the admin handler.
func (h *harness) adminCall(method, path string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	h.server.AdminHandler().ServeHTTP(recorder, httptest.NewRequest(method, path, nil))
	return recorder
}

// adminStatus is /admin/status, decoded.
func (h *harness) adminStatus(t *testing.T) AdminStatus {
	t.Helper()
	response := h.adminCall("GET", "/admin/status")
	var status AdminStatus
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &status) != nil {
		t.Fatal(response.Code, response.Body.String())
	}
	return status
}

// samples is one scrape of /metrics: each sample's series, spelled as the
// exposition spells it (the metric's name, then its labels sorted by name),
// to its value.
type samples map[string]float64

// seriesKey spells a series from its name and label name/value pairs. The
// exposition sorts the labels by name, except a bucket's le, which comes last.
func seriesKey(name string, labels ...string) string {
	if len(labels) == 0 {
		return name
	}
	pairs, bound := []string{}, ""
	for i := 0; i+1 < len(labels); i += 2 {
		pair := labels[i] + `="` + labels[i+1] + `"`
		if labels[i] == "le" {
			bound = pair
			continue
		}
		pairs = append(pairs, pair)
	}
	slices.Sort(pairs)
	if bound != "" {
		pairs = append(pairs, bound)
	}
	return name + "{" + strings.Join(pairs, ",") + "}"
}

func (h *harness) scrape(t *testing.T) samples {
	t.Helper()
	response := h.adminCall("GET", "/metrics")
	if response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}
	out := samples{}
	for _, line := range strings.Split(response.Body.String(), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cut := strings.LastIndex(line, " ")
		value, err := strconv.ParseFloat(line[cut+1:], 64)
		if cut < 0 || err != nil {
			t.Fatalf("not a sample: %q", line)
		}
		out[line[:cut]] = value
	}
	return out
}

// is fails unless the series exists with the value want.
func (s samples) is(t *testing.T, want float64, name string, labels ...string) {
	t.Helper()
	key := seriesKey(name, labels...)
	if got, ok := s[key]; !ok || got != want {
		t.Errorf("%s = %v (present %v), want %v", key, got, ok, want)
	}
}

// absent fails if the series exists.
func (s samples) absent(t *testing.T, name string, labels ...string) {
	t.Helper()
	if got, ok := s[seriesKey(name, labels...)]; ok {
		t.Errorf("%s = %v, want no such series", seriesKey(name, labels...), got)
	}
}

// bucketBounds are the upper bounds (le) of the histogram name's buckets for
// the series with the given labels, in increasing order, +Inf last.
func (s samples) bucketBounds(name string, labels ...string) []string {
	bounds := []string{}
	for key := range s {
		if !strings.HasPrefix(key, name+"_bucket{") {
			continue
		}
		matches := true
		for i := 0; i+1 < len(labels); i += 2 {
			matches = matches && (strings.Contains(key, "{"+labels[i]+`="`+labels[i+1]+`"`) || strings.Contains(key, ","+labels[i]+`="`+labels[i+1]+`"`))
		}
		if _, after, found := strings.Cut(key, `le="`); matches && found {
			bound, _, _ := strings.Cut(after, `"`)
			bounds = append(bounds, bound)
		}
	}
	number := func(bound string) float64 {
		value, _ := strconv.ParseFloat(bound, 64) // "+Inf" parses as infinity
		return value
	}
	slices.SortFunc(bounds, func(a, b string) int {
		switch {
		case number(a) < number(b):
			return -1
		case number(a) > number(b):
			return 1
		}
		return 0
	})
	return bounds
}

func TestMetricsCountRequestsAndDegraded(t *testing.T) {
	h := newHarness(t, nil)
	h.mustAdd(t, addRequest("r1", "u1"))
	h.search(t, SearchRequest{Query: question, UserID: "u1", TopK: 10})
	// A Search whose planner crashes is answered by the raw ranking.
	h.planner.panics.Store(true)
	h.search(t, SearchRequest{Query: question, UserID: "u1", TopK: 10})
	h.planner.panics.Store(false)
	// Every other answer is counted under its route and code.
	h.call("GET", "/health", nil, "X-None", "")
	h.call("GET", "/nope-123", nil)
	h.call("POST", "/search", `{"query":`)
	h.call("POST", "/add", addRequest("r2", "u2"), "X-None", "")

	m := h.scrape(t)
	for _, want := range []struct {
		value  float64
		name   string
		labels []string
	}{
		{1, "mnemon_aml_requests_total", []string{"route", "add", "code", "200"}},
		{1, "mnemon_aml_requests_total", []string{"route", "add", "code", "401"}},
		{2, "mnemon_aml_requests_total", []string{"route", "search", "code", "200"}},
		{1, "mnemon_aml_requests_total", []string{"route", "search", "code", "400"}},
		{1, "mnemon_aml_requests_total", []string{"route", "health", "code", "200"}},
		{1, "mnemon_aml_requests_total", []string{"route", "other", "code", "404"}},
		// Every request is timed, whatever its answer.
		{2, "mnemon_aml_request_duration_seconds_count", []string{"route", "add"}},
		{3, "mnemon_aml_request_duration_seconds_count", []string{"route", "search"}},
		{1, "mnemon_aml_request_duration_seconds_count", []string{"route", "health"}},
		{1, "mnemon_aml_request_duration_seconds_count", []string{"route", "other"}},
		{3, "mnemon_aml_request_duration_seconds_bucket", []string{"route", "search", "le", "+Inf"}},
		// Nothing is in flight between requests.
		{0, "mnemon_aml_in_flight", []string{"route", "add"}},
		{0, "mnemon_aml_in_flight", []string{"route", "search"}},
		{0, "mnemon_aml_in_flight", []string{"route", "health"}},
		{0, "mnemon_aml_in_flight", []string{"route", "other"}},
		// One Search degraded to the raw ranking, none in part; nothing shed.
		{1, "mnemon_aml_search_degraded_total", nil},
		{0, "mnemon_aml_shed_total", []string{"route", "add"}},
		{0, "mnemon_aml_shed_total", []string{"route", "search"}},
		// The one user whose store the requests opened.
		{1, "mnemon_aml_open_stores", nil},
	} {
		m.is(t, want.value, want.name, want.labels...)
	}
	m.absent(t, "mnemon_aml_search_partial_total", "stage", "judge")
	// Latencies fall in buckets from 5 ms to 10 minutes.
	want := []string{"0.005", "0.01", "0.025", "0.05", "0.1", "0.25", "0.5", "1", "2.5", "5", "10", "30", "60", "120", "300", "600", "+Inf"}
	if got := m.bucketBounds("mnemon_aml_request_duration_seconds", "route", "search"); !slices.Equal(got, want) {
		t.Errorf("request duration buckets %v, want %v", got, want)
	}
	// The Go runtime and the process report on the same registry.
	if m["go_goroutines"] <= 0 || m["process_start_time_seconds"] <= 0 {
		t.Errorf("go_goroutines %v, process_start_time_seconds %v", m["go_goroutines"], m["process_start_time_seconds"])
	}
	// The counts the status line prints are the ones in the metrics.
	if h.server.metrics.adds.Load() != 1 || h.server.metrics.searches.Load() != 2 || h.server.DegradedSearches() != 1 {
		t.Error(h.server.metrics.adds.Load(), h.server.metrics.searches.Load(), h.server.DegradedSearches())
	}
}

// The metrics live in the server's own registry: nothing of them (nor the
// runtime's) is added to the process-wide default one, so servers made in one
// process, as these tests make them, do not collide.
func TestMetricsUseAPrivateRegistry(t *testing.T) {
	first, second := newHarness(t, nil), newHarness(t, nil)
	first.mustAdd(t, addRequest("r1", "u1"))
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if strings.HasPrefix(family.GetName(), "mnemon_aml_") {
			t.Errorf("the default registry carries %s", family.GetName())
		}
	}
	// Each server counts for itself.
	first.scrape(t).is(t, 1, "mnemon_aml_requests_total", "route", "add", "code", "200")
	second.scrape(t).absent(t, "mnemon_aml_requests_total", "route", "add", "code", "200")
}

// A user id, a user's store key, a request id or an unknown path never labels
// a series, and none of them reaches /admin/status either.
func TestMetricsCarryNoUserOrRequestIDs(t *testing.T) {
	h := newHarness(t, nil)
	const user, request = "alice@example.test", "req-7f3a9c"
	h.mustAdd(t, addRequest("r1", user))
	h.call("POST", "/search", SearchRequest{Query: question, UserID: user, TopK: 5}, withAuth("X-Request-Id", request)...)
	h.call("GET", "/users/"+user+"/"+userKey(user), nil, withAuth("X-Request-Id", request)...)
	for name, body := range map[string]string{"/metrics": h.adminCall("GET", "/metrics").Body.String(), "/admin/status": h.adminCall("GET", "/admin/status").Body.String()} {
		for _, leak := range []string{user, "alice", userKey(user), userKey(user)[:8], request, "/users/"} {
			if strings.Contains(body, leak) {
				t.Errorf("%s carries %q", name, leak)
			}
		}
	}
}

func TestMetricsCountPartialSearchesByStage(t *testing.T) {
	h := newHarness(t, nil)
	addSessions(t, h, "u1", 2)
	h.judge.failJudgement.Store(true)
	h.search(t, SearchRequest{Query: question, UserID: "u1", TopK: 10})
	h.judge.failJudgement.Store(false)
	h.search(t, SearchRequest{Query: question, UserID: "u1", TopK: 10})

	m := h.scrape(t)
	m.is(t, 1, "mnemon_aml_search_partial_total", "stage", "judge")
	m.is(t, 0, "mnemon_aml_search_degraded_total")
	status := h.adminStatus(t)
	if status.Partial != 1 || status.Degraded != 0 || status.Searches != 2 || status.PartialLast10m != 1 || status.SearchesLast10m != 2 {
		t.Fatalf("%+v", status)
	}
}

// A request shed for want of a slot is counted as shed and as a 503, and a
// request in a handler (queued for a slot included) is in flight; a request
// whose client left while it waited is no shed.
func TestMetricsCountShedRequests(t *testing.T) {
	h := newHarness(t, func(config *ServerConfig) { config.MaxConcurrentSearches = 1 })
	h.server.admitWait = 100 * time.Millisecond
	addSessions(t, h, "u1", 2)
	h.judge.block.Store(true)
	leave, left := context.WithCancel(context.Background())
	defer left()
	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- h.callContext(leave, "POST", "/search", searchBody("u1"), withAuth()...) }()
	eventually(t, func() bool { return h.judge.blocked.Load() > 0 })
	h.scrape(t).is(t, 1, "mnemon_aml_in_flight", "route", "search")

	if shed := h.call("POST", "/search", searchBody("u1")); shed.Code != http.StatusServiceUnavailable {
		t.Fatal(shed.Code)
	}
	gone, goes := context.WithCancel(context.Background())
	goes()
	h.callContext(gone, "POST", "/search", searchBody("u1"), withAuth()...)

	m := h.scrape(t)
	m.is(t, 1, "mnemon_aml_shed_total", "route", "search")
	m.is(t, 0, "mnemon_aml_shed_total", "route", "add")
	m.is(t, 2, "mnemon_aml_requests_total", "route", "search", "code", "503")
	m.is(t, 1, "mnemon_aml_in_flight", "route", "search")
	left()
	<-first
	h.scrape(t).is(t, 0, "mnemon_aml_in_flight", "route", "search")
}

// A response written without a status is a 200, and counted so.
func TestMetricsCountAnImplicit200(t *testing.T) {
	h := newHarness(t, nil)
	implicit := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	h.server.metrics.instrument("add", implicit).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/add", nil))
	h.scrape(t).is(t, 1, "mnemon_aml_requests_total", "route", "add", "code", "200")
}

// An Add whose messages have no text stores nothing, opens no store, and is
// answered 200 and counted as served.
func TestAddWithoutTextIsCounted(t *testing.T) {
	h := newHarness(t, nil)
	h.mustAdd(t, AddRequest{RequestID: "r1", UserID: "u1", SessionID: "s1", Messages: []Message{{Role: "user", Content: "  ", Timestamp: at(0)}}})
	if status := h.adminStatus(t); status.Adds != 1 || status.OpenStores != 0 {
		t.Fatalf("%+v", status)
	}
	h.scrape(t).is(t, 1, "mnemon_aml_requests_total", "route", "add", "code", "200")
}

// A handler that panics is answered 500 (recovered) or, if the panic gets
// past it, counted as a 500 all the same; either way it is no longer in
// flight.
func TestMetricsCountPanickedRequestsAs500(t *testing.T) {
	h := newHarness(t, nil)
	boom := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })
	recorder := httptest.NewRecorder()
	h.server.metrics.instrument("add", h.server.recovered("add", boom)).ServeHTTP(recorder, httptest.NewRequest("POST", "/add", nil))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatal(recorder.Code)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("the panic was swallowed")
			}
		}()
		h.server.metrics.instrument("search", boom).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/search", nil))
	}()
	m := h.scrape(t)
	m.is(t, 1, "mnemon_aml_requests_total", "route", "add", "code", "500")
	m.is(t, 1, "mnemon_aml_requests_total", "route", "search", "code", "500")
	m.is(t, 0, "mnemon_aml_in_flight", "route", "add")
	m.is(t, 0, "mnemon_aml_in_flight", "route", "search")
}

func TestModelAttemptsAreMetered(t *testing.T) {
	h := newHarness(t, nil)
	observer := memoryagent.ModelObserver(h.server.metrics)
	for _, event := range []memoryagent.AttemptEvent{
		{Kind: "deepseek", Class: "interactive", Outcome: "retryable", Attempt: 1, Duration: 100 * time.Millisecond, Status: 503},
		{Kind: "deepseek", Class: "interactive", Outcome: "ok", Attempt: 2, Duration: 1500 * time.Millisecond, Status: 200, Retried: true},
		{Kind: "deepseek", Class: "background", Outcome: "failed", Attempt: 1, Duration: 2 * time.Second, Status: 400},
		{Kind: "jev", Class: "interactive", Outcome: "breaker-open", Attempt: 1},
		{Kind: "jev", Class: "flush", Outcome: "bulkhead-full", Attempt: 1, Duration: 20 * time.Second},
		// The provider's retry budget stopped a retry that was never sent.
		{Kind: "deepseek", Class: "interactive", Outcome: "budget", Attempt: 3, Retried: true},
		// A JEV call refused with 429, then answered invalid, then valid.
		{Kind: "jev", Class: "background", Outcome: "throttled", Attempt: 1, Duration: 50 * time.Millisecond, Status: 429},
		{Kind: "jev", Class: "background", Outcome: "invalid", Attempt: 2, Duration: 250 * time.Millisecond, Status: 200, Retried: true},
		{Kind: "jev", Class: "background", Outcome: "ok", Attempt: 3, Duration: 200 * time.Millisecond, Status: 200, Retried: true},
	} {
		observer.ModelAttempt(event)
	}
	m := h.scrape(t)
	m.is(t, 1, "mnemon_aml_model_attempts_total", "kind", "deepseek", "class", "interactive", "outcome", "retryable")
	m.is(t, 1, "mnemon_aml_model_attempts_total", "kind", "deepseek", "class", "interactive", "outcome", "ok")
	m.is(t, 1, "mnemon_aml_model_attempts_total", "kind", "deepseek", "class", "interactive", "outcome", "budget")
	m.is(t, 1, "mnemon_aml_model_attempts_total", "kind", "deepseek", "class", "background", "outcome", "failed")
	m.is(t, 1, "mnemon_aml_model_attempts_total", "kind", "jev", "class", "interactive", "outcome", "breaker-open")
	m.is(t, 1, "mnemon_aml_model_attempts_total", "kind", "jev", "class", "flush", "outcome", "bulkhead-full")
	m.is(t, 1, "mnemon_aml_model_attempts_total", "kind", "jev", "class", "background", "outcome", "throttled")
	m.is(t, 1, "mnemon_aml_model_attempts_total", "kind", "jev", "class", "background", "outcome", "invalid")
	m.is(t, 1, "mnemon_aml_model_attempts_total", "kind", "jev", "class", "background", "outcome", "ok")
	// Only attempts that went to the provider are timed: not those a policy
	// refused (breaker, bulkhead, budget). A throttled or invalid attempt
	// went to it.
	m.is(t, 2, "mnemon_aml_model_attempt_seconds_count", "kind", "deepseek", "class", "interactive")
	m.is(t, 1.6, "mnemon_aml_model_attempt_seconds_sum", "kind", "deepseek", "class", "interactive")
	m.is(t, 1, "mnemon_aml_model_attempt_seconds_count", "kind", "deepseek", "class", "background")
	m.is(t, 3, "mnemon_aml_model_attempt_seconds_count", "kind", "jev", "class", "background")
	m.is(t, 0.5, "mnemon_aml_model_attempt_seconds_sum", "kind", "jev", "class", "background")
	m.absent(t, "mnemon_aml_model_attempt_seconds_count", "kind", "jev", "class", "interactive")
	m.absent(t, "mnemon_aml_model_attempt_seconds_count", "kind", "jev", "class", "flush")
	// Retries are the attempts after a call's first that were sent.
	m.is(t, 1, "mnemon_aml_model_retries_total", "kind", "deepseek")
	m.is(t, 2, "mnemon_aml_model_retries_total", "kind", "jev")
}

// The providers behind the server report through the same registry: a
// Search's planner calls show as interactive attempts of their kind.
func TestProvidersReportToTheMetrics(t *testing.T) {
	planner := newModelEndpoint(t, func(n int, w http.ResponseWriter) {
		if n == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": "Caroline support group"}, "finish_reason": "stop"}}})
	})
	provider, err := memoryagent.NewProvider(memoryagent.ModelConfig{Kind: "local", Model: "m", BaseURL: planner.URL + "/v1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, func(config *ServerConfig) {
		config.Models = func(memoryagent.Config) (memoryagent.Models, error) {
			return memoryagent.Models{Planner: provider, Judge: &fakeJudge{}, Consolidator: &fakeConsolidator{}}, nil
		}
	})
	t.Cleanup(func() { provider.Close() }) // before the server closes
	h.mustAdd(t, addRequest("r1", "u1"))
	h.call("POST", "/search", SearchRequest{Query: question, UserID: "u1", TopK: 10})

	m := h.scrape(t)
	m.is(t, 1, "mnemon_aml_model_attempts_total", "kind", "local", "class", "interactive", "outcome", "retryable")
	if m[seriesKey("mnemon_aml_model_attempts_total", "kind", "local", "class", "interactive", "outcome", "ok")] < 1 {
		t.Error(m)
	}
	m.is(t, 1, "mnemon_aml_model_retries_total", "kind", "local")
}

// newModelEndpoint is a model endpoint whose n-th request (from 1) answer
// writes the reply to.
func newModelEndpoint(t *testing.T, answer func(n int, w http.ResponseWriter)) *httptest.Server {
	t.Helper()
	var n atomic.Int64
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		answer(int(n.Add(1)), w)
	}))
	t.Cleanup(endpoint.Close)
	return endpoint
}

// Batches folded, deferred and dropped, jobs by outcome and the scheduler's
// queue are measured as background consolidation works.
func TestBackgroundConsolidationIsMetered(t *testing.T) {
	settings := memoryagent.ConsolidationConfig{Mode: "timelines", BatchCharacters: 300}
	h := newHarness(t, func(config *ServerConfig) {
		config.MemoryConfig.Consolidation = &settings
		config.Consolidation = ConsolidationBackground
	})
	h.consolidator.answer(parityReply)
	clock := newFakeClock()
	h.server.sched.now = clock.now
	// With no worker running, the four Adds (two full batches) queue their
	// user, whose work has waited since the first.
	for i := 1; i <= 4; i++ {
		h.mustAdd(t, addRequest(fmt.Sprintf("r%d", i), "u1"))
	}
	clock.advance(90 * time.Second)
	status := h.adminStatus(t)
	if status.Queue.Queued != 1 || status.Queue.OldestQueued != 90*time.Second || status.Queue.MaxBacklog <= 0 {
		t.Errorf("%+v", status.Queue)
	}
	m := h.scrape(t)
	m.is(t, 1, "mnemon_aml_consolidation_queued")
	m.is(t, 0, "mnemon_aml_consolidation_running")
	m.is(t, 0, "mnemon_aml_consolidation_backing_off")
	m.is(t, 90, "mnemon_aml_consolidation_oldest_queued_seconds")
	m.is(t, float64(status.Queue.MaxBacklog), "mnemon_aml_consolidation_max_backlog_characters")

	h.run(t)
	h.settle(t)
	m = h.scrape(t)
	m.is(t, 2, "mnemon_aml_consolidation_batches_total", "outcome", "folded")
	m.is(t, 0, "mnemon_aml_consolidation_batches_total", "outcome", "deferred")
	m.is(t, 0, "mnemon_aml_consolidation_batches_total", "outcome", "dropped")
	m.is(t, 0, "mnemon_aml_consolidation_queued")
	m.is(t, 0, "mnemon_aml_consolidation_running")
	m.is(t, 0, "mnemon_aml_consolidation_oldest_queued_seconds")
	if m[seriesKey("mnemon_aml_consolidation_jobs_total", "outcome", "ok")] < 1 {
		t.Error(m)
	}
	for _, outcome := range []string{"failed", "paused", "canceled"} {
		m.is(t, 0, "mnemon_aml_consolidation_jobs_total", "outcome", outcome)
	}
}

// While JEV is down a job defers the batch it cannot link: the batch is
// counted deferred, the job failed, the user backs off and keeps its age.
func TestDeferredBatchesAndFailedJobsAreMetered(t *testing.T) {
	settings := memoryagent.ConsolidationConfig{Mode: "layered", BatchCharacters: 300}
	h := newHarness(t, func(config *ServerConfig) {
		config.MemoryConfig.Consolidation = &settings
		config.Consolidation = ConsolidationBackground
	})
	h.consolidator.answer(parityReply)
	h.judge.outage.Store(true)
	h.run(t)
	for i := 1; i <= 6; i++ {
		h.mustAdd(t, addRequest(fmt.Sprintf("r%d", i), "u1"))
	}
	eventually(t, func() bool { return h.server.sched.Status().BackingOff == 1 })

	m := h.scrape(t)
	m.is(t, 1, "mnemon_aml_consolidation_backing_off")
	m.is(t, 0, "mnemon_aml_consolidation_batches_total", "outcome", "dropped")
	if m[seriesKey("mnemon_aml_consolidation_batches_total", "outcome", "deferred")] < 1 ||
		m[seriesKey("mnemon_aml_consolidation_jobs_total", "outcome", "failed")] < 1 ||
		m["mnemon_aml_consolidation_oldest_queued_seconds"] <= 0 {
		t.Error(m)
	}
	m.is(t, 0, "mnemon_aml_consolidation_link_fallbacks_total")
	if status := h.adminStatus(t); status.Queue.BackingOff != 1 || status.Queue.OldestQueued <= 0 || status.LinkFallbacks != 0 || status.Dropped != 0 {
		t.Errorf("%+v", status)
	}
}

// A batch whose reply never parses is split, and each half that still does
// not is dropped from the layer. Dropped counts the drops (the "consolidation
// dropped" lines), and the batch was also taken off the pending records, which
// folded counts.
func TestDroppedBatchesAreCounted(t *testing.T) {
	h := timelinesHarness(t, 300)
	h.consolidator.answer(func(context.Context, string) (string, error) { return "no JSON here", nil })
	h.mustAdd(t, addRequest("r1", "u1"))
	h.scrape(t).is(t, 0, "mnemon_aml_consolidation_batches_total", "outcome", "dropped")
	h.mustAdd(t, addRequest("r2", "u1")) // fills a batch of two records, which splits into two drops
	if drops := strings.Count(h.log.String(), "consolidation dropped 1 records"); drops != 2 {
		t.Fatal(drops, h.log.String())
	}
	m := h.scrape(t)
	m.is(t, 2, "mnemon_aml_consolidation_batches_total", "outcome", "dropped")
	m.is(t, 1, "mnemon_aml_consolidation_batches_total", "outcome", "folded")
	m.is(t, 0, "mnemon_aml_consolidation_batches_total", "outcome", "deferred")
	if status := h.adminStatus(t); status.Dropped != 2 || status.LinkFallbacks != 0 {
		t.Fatalf("%+v", status)
	}
}

// What a job reports is counted by the batches it took off the pending
// records, the batch it deferred, its drops (not their records) and the batches
// it linked without JEV.
func TestConsolidatedCountsWhatAJobDid(t *testing.T) {
	h := newHarness(t, nil)
	h.server.metrics.consolidated(memoryagent.ConsolidateResult{
		Folded: 3, Deferred: 1, LinkFallbacks: 2, Dropped: 7,
		Drops: []memoryagent.ConsolidateDrop{{Records: 5, Reason: "a"}, {Records: 2, Reason: "b"}},
	})
	h.server.metrics.consolidated(memoryagent.ConsolidateResult{Folded: 1})
	m := h.scrape(t)
	m.is(t, 4, "mnemon_aml_consolidation_batches_total", "outcome", "folded")
	m.is(t, 1, "mnemon_aml_consolidation_batches_total", "outcome", "deferred")
	m.is(t, 2, "mnemon_aml_consolidation_batches_total", "outcome", "dropped")
	m.is(t, 2, "mnemon_aml_consolidation_link_fallbacks_total")
	if status := h.adminStatus(t); status.Dropped != 2 || status.LinkFallbacks != 2 {
		t.Fatalf("%+v", status)
	}
}

// A batch linked without JEV (on-add mode links at once when JEV fails) is a
// link fallback, in the metrics and in /admin/status.
func TestLinkFallbacksAreCounted(t *testing.T) {
	h := newHarness(t, func(config *ServerConfig) {
		config.MemoryConfig.Consolidation = &memoryagent.ConsolidationConfig{Mode: "layered", BatchCharacters: 300}
		config.ConsolidateOnAdd = true
	})
	for _, id := range []string{"r1", "r2", "r3"} {
		h.mustAdd(t, addRequest(id, "u1"))
	}
	h.scrape(t).is(t, 0, "mnemon_aml_consolidation_link_fallbacks_total")
	h.judge.fail.Store(true)
	for _, id := range []string{"r4", "r5"} {
		h.mustAdd(t, addRequest(id, "u1"))
	}
	h.scrape(t).is(t, 1, "mnemon_aml_consolidation_link_fallbacks_total")
	if status := h.adminStatus(t); status.LinkFallbacks != 1 || status.Dropped != 0 {
		t.Fatalf("%+v", status)
	}
}

// An Add that waited out its window for consolidation and answered anyway is
// counted.
func TestAddOutranIsCounted(t *testing.T) {
	settings := memoryagent.ConsolidationConfig{Mode: "timelines", BatchCharacters: 300, MaxBatches: 1}
	h := newHarness(t, func(config *ServerConfig) {
		config.MemoryConfig.Consolidation = &settings
		config.Consolidation = ConsolidationBackground
	})
	h.server.addWindow = 20 * time.Millisecond
	h.mustAdd(t, addRequest("r1", "u1"))
	h.scrape(t).is(t, 0, "mnemon_aml_add_outran_total")
	h.mustAdd(t, addRequest("r2", "u1")) // no worker runs: past the bound it waits its window out
	h.scrape(t).is(t, 1, "mnemon_aml_add_outran_total")
	if status := h.adminStatus(t); status.AddOutran != 1 || status.Adds != 2 {
		t.Fatalf("%+v", status)
	}
}

// The window keeps the current minute and the nine before it.
func TestSearchWindowCoversTheLastTenMinutes(t *testing.T) {
	clock := newFakeClock()
	w := &searchWindow{now: clock.now}
	totals := func(wantServed, wantPartial int64) {
		t.Helper()
		if served, partial := w.totals(); served != wantServed || partial != wantPartial {
			t.Fatalf("at +%s: %d answered, %d partial; want %d, %d", clock.now().Sub(newFakeClock().now()), served, partial, wantServed, wantPartial)
		}
	}
	totals(0, 0)
	for range 3 {
		w.answered()
	}
	w.partlyDegraded()
	totals(3, 1)
	// Five minutes on: two more, one partial.
	clock.advance(5 * time.Minute)
	w.answered()
	w.answered()
	w.partlyDegraded()
	totals(5, 2)
	// Until the last second of the tenth minute the first minute counts…
	clock.advance(4*time.Minute + 59*time.Second)
	totals(5, 2)
	// …and then it is gone. Its bucket is reused by the new minute without
	// its old counts.
	clock.advance(time.Second)
	totals(2, 1)
	w.answered()
	totals(3, 1)
	// Ten minutes on from the second batch, only the newest remains.
	clock.advance(5 * time.Minute)
	totals(1, 0)
	// A long idle time empties it, and it counts again after.
	clock.advance(3 * time.Hour)
	totals(0, 0)
	w.answered()
	totals(1, 0)
}

// The window is safe for the requests that fill it concurrently.
func TestSearchWindowIsConcurrencySafe(t *testing.T) {
	w := &searchWindow{now: time.Now}
	done := make(chan struct{})
	for range 8 {
		go func() {
			defer func() { done <- struct{}{} }()
			for range 500 {
				w.answered()
				w.partlyDegraded()
				w.totals()
			}
		}()
	}
	for range 8 {
		<-done
	}
	if served, partial := w.totals(); served != 4000 || partial != 4000 {
		t.Fatal(served, partial)
	}
}
