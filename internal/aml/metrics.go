package aml

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
)

// The server's metrics, for Prometheus (/metrics on the admin listener) and
// for /admin/status. They live in a registry of their own, with the Go and
// process collectors, and are named mnemon_aml_*. A label takes its values
// from a closed set (routes, status codes, the models' kinds, classes and
// outcomes, account kinds, the stages of a read); no user, request or path
// ever labels a series.
//
// The totals that the status line and /admin/status report are atomics here,
// and the counter metrics that restate them read those atomics when scraped
// (totalsCollector), so each count has one source. What the server keeps
// elsewhere (the scheduler's queue, the open stores, the providers' breakers,
// the paid accounts) is read when scraped too (stateCollector).

// The routes requests are counted under.
const (
	routeAdd    = "add"
	routeSearch = "search"
	routeHealth = "health"
	routeOther  = "other" // every other path: 404s and the like
)

var routeNames = []string{routeAdd, routeSearch, routeHealth, routeOther}

// latencyBuckets are the upper bounds of the latency histograms, in seconds:
// 5 ms up to the 10 minutes a Search's read may take.
var latencyBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300, 600}

// The outcomes batches of records are counted under (consolidation_batches_total).
const (
	batchFolded = iota
	batchDeferred
	batchDropped
	batchOutcomes
)

var batchOutcomeNames = [batchOutcomes]string{"folded", "deferred", "dropped"}

type metrics struct {
	registry *prometheus.Registry

	// Served Adds and Searches; Searches answered by the raw ranking
	// (degraded) or by a read with a stage that fell back (partial); batches
	// by outcome; batches linked without JEV; Adds that answered after
	// waiting out their window for consolidation.
	adds, searches, degraded, partial atomic.Int64
	batches                           [batchOutcomes]atomic.Int64
	linkFallbacks, addOutran          atomic.Int64
	// recent counts the Searches of the last ten minutes.
	recent searchWindow

	requests       *prometheus.CounterVec   // route, code
	duration       *prometheus.HistogramVec // route
	inFlight       *prometheus.GaugeVec     // route
	shed           *prometheus.CounterVec   // route
	partialStages  *prometheus.CounterVec   // stage
	attempts       *prometheus.CounterVec   // kind, class, outcome
	attemptSeconds *prometheus.HistogramVec // kind, class
	retries        *prometheus.CounterVec   // kind
	jobs           *prometheus.CounterVec   // outcome
	// adminAuthFailures counts the admin requests refused for lack of the
	// admin token; it has no labels, as nothing a refused request sends may
	// become one.
	adminAuthFailures prometheus.Counter
}

const (
	metricsNamespace = "mnemon"
	metricsSubsystem = "aml"
)

func counterOpts(name, help string) prometheus.CounterOpts {
	return prometheus.CounterOpts{Namespace: metricsNamespace, Subsystem: metricsSubsystem, Name: name, Help: help}
}

func gaugeOpts(name, help string) prometheus.GaugeOpts {
	return prometheus.GaugeOpts{Namespace: metricsNamespace, Subsystem: metricsSubsystem, Name: name, Help: help}
}

func histogramOpts(name, help string) prometheus.HistogramOpts {
	return prometheus.HistogramOpts{Namespace: metricsNamespace, Subsystem: metricsSubsystem, Name: name, Help: help, Buckets: latencyBuckets}
}

// newMetrics makes the registry and the metrics that count events as they
// happen. now is the clock of the ten-minute Search window.
func newMetrics(now func() time.Time) *metrics {
	m := &metrics{registry: prometheus.NewRegistry(), recent: searchWindow{now: now}}
	m.registry.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}), totalsCollector{m})
	factory := promauto.With(m.registry)
	m.requests = factory.NewCounterVec(counterOpts("requests_total", "Requests answered, by route and HTTP status code."), []string{"route", "code"})
	m.duration = factory.NewHistogramVec(histogramOpts("request_duration_seconds", "Time to answer a request, queueing for a slot included."), []string{"route"})
	m.inFlight = factory.NewGaugeVec(gaugeOpts("in_flight", "Requests being served, those queued for a slot included."), []string{"route"})
	m.shed = factory.NewCounterVec(counterOpts("shed_total", "Requests refused with 503 because no slot freed within the wait."), []string{"route"})
	m.partialStages = factory.NewCounterVec(counterOpts("search_partial_total", "Stages of a Search's read that fell back, counting each stage once per Search."), []string{"stage"})
	m.attempts = factory.NewCounterVec(counterOpts("model_attempts_total", "Attempts of model calls, by provider kind, call class and outcome."), []string{"kind", "class", "outcome"})
	m.attemptSeconds = factory.NewHistogramVec(histogramOpts("model_attempt_seconds", "Time the caller waited for an attempt that went to the provider."), []string{"kind", "class"})
	m.retries = factory.NewCounterVec(counterOpts("model_retries_total", "Attempts after a call's first that went to the provider."), []string{"kind"})
	m.jobs = factory.NewCounterVec(counterOpts("consolidation_jobs_total", "Consolidation jobs of the scheduler, by how they ended."), []string{"outcome"})
	m.adminAuthFailures = factory.NewCounter(counterOpts("admin_auth_failures_total", "Admin requests refused with 401 because they lacked the admin token or carried a wrong one."))
	// Series known in advance exist from the start, at 0.
	for _, route := range routeNames {
		m.inFlight.WithLabelValues(route)
	}
	for _, route := range []string{routeAdd, routeSearch} {
		m.shed.WithLabelValues(route)
	}
	for _, outcome := range jobOutcomes {
		m.jobs.WithLabelValues(outcome)
	}
	return m
}

// handler serves the registry in the Prometheus text format. A collector that
// fails does not blank the scrape; the failure is logged.
func (m *metrics) handler(logger *slog.Logger) http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{
		ErrorLog:      slog.NewLogLogger(logger.Handler(), slog.LevelError),
		ErrorHandling: promhttp.ContinueOnError,
	})
}

// instrument counts and times the requests of route that next serves: the
// answer by status code, its duration, and the requests in flight meanwhile.
// A request whose handler panics past every guard is counted as a 500.
func (m *metrics) instrument(route string, next http.Handler) http.Handler {
	inFlight, duration := m.inFlight.WithLabelValues(route), m.duration.WithLabelValues(route)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		inFlight.Inc()
		recorder := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		returned := false
		defer func() {
			inFlight.Dec()
			code := recorder.status
			if !returned {
				code = http.StatusInternalServerError
			}
			m.requests.WithLabelValues(route, strconv.Itoa(code)).Inc()
			duration.Observe(time.Since(start).Seconds())
		}()
		next.ServeHTTP(recorder, r)
		returned = true
	})
}

// ModelAttempt counts an attempt of a model call (memoryagent.ModelObserver).
// Every attempt is counted by its outcome; only those that went to the
// provider (ok, throttled, invalid, rejected, retryable, failed: not the
// ones a breaker, a bulkhead or a budget refused) are timed and, when they
// follow an earlier attempt of their call, counted as retries.
func (m *metrics) ModelAttempt(e memoryagent.AttemptEvent) {
	m.attempts.WithLabelValues(e.Kind, e.Class, e.Outcome).Inc()
	switch e.Outcome {
	case memoryagent.OutcomeBreakerOpen, memoryagent.OutcomeBulkheadFull, memoryagent.OutcomeBudget:
		return
	}
	m.attemptSeconds.WithLabelValues(e.Kind, e.Class).Observe(e.Duration.Seconds())
	if e.Retried {
		m.retries.WithLabelValues(e.Kind).Inc()
	}
}

// searchAnswered counts a Search answered 200.
func (m *metrics) searchAnswered() {
	m.searches.Add(1)
	m.recent.answered()
}

// searchDegraded counts a Search answered by the raw ranking and returns the
// count so far.
func (m *metrics) searchDegraded() int64 { return m.degraded.Add(1) }

// searchPartlyDegraded counts a Search whose read answered with the given
// stages fallen back, and returns the count of such Searches so far.
func (m *metrics) searchPartlyDegraded(stages []string) int64 {
	for _, stage := range stages {
		m.partialStages.WithLabelValues(stage).Inc()
	}
	m.recent.partlyDegraded()
	return m.partial.Add(1)
}

// consolidated counts what a consolidation job did: the batches it took off
// the pending records (those it gave up on among them), the batch it deferred,
// the drops it made (each a batch, or a half of one, whose records the layer
// does not cover) and the batches it linked without JEV.
func (m *metrics) consolidated(result memoryagent.ConsolidateResult) {
	m.batches[batchFolded].Add(int64(result.Folded))
	m.batches[batchDeferred].Add(int64(result.Deferred))
	m.batches[batchDropped].Add(int64(len(result.Drops)))
	m.linkFallbacks.Add(int64(result.LinkFallbacks))
}

// job counts a scheduler job that ended with outcome (jobOutcomes).
func (m *metrics) job(outcome string) { m.jobs.WithLabelValues(outcome).Inc() }

// windowMinutes is how many one-minute buckets searchWindow keeps.
const windowMinutes = 10

// searchWindow counts the Searches answered and the partially degraded ones
// over the last ten minutes, in ten one-minute buckets: the current minute
// and the nine before it, so what it holds spans between nine and ten
// minutes. Its memory is fixed, and a bucket is emptied when its minute comes
// round again.
type searchWindow struct {
	now func() time.Time

	mu sync.Mutex
	// minute is the Unix minute bucket i last counted for.
	minute            [windowMinutes]int64
	served, partially [windowMinutes]int64
}

// bucketLocked is the bucket of the current minute, emptied if it still
// counts an older one; mu is held. It returns the minute too.
func (w *searchWindow) bucketLocked() (int, int64) {
	minute := w.now().Unix() / 60
	i := int(((minute % windowMinutes) + windowMinutes) % windowMinutes)
	if w.minute[i] != minute {
		w.minute[i], w.served[i], w.partially[i] = minute, 0, 0
	}
	return i, minute
}

// answered counts a Search answered.
func (w *searchWindow) answered() {
	w.mu.Lock()
	defer w.mu.Unlock()
	i, _ := w.bucketLocked()
	w.served[i]++
}

// partlyDegraded counts a Search answered by a read in which a stage fell back.
func (w *searchWindow) partlyDegraded() {
	w.mu.Lock()
	defer w.mu.Unlock()
	i, _ := w.bucketLocked()
	w.partially[i]++
}

// totals are the Searches answered and the partially degraded ones in the
// window.
func (w *searchWindow) totals() (answered, partially int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, now := w.bucketLocked()
	for i := range windowMinutes {
		if now-w.minute[i] < windowMinutes {
			answered, partially = answered+w.served[i], partially+w.partially[i]
		}
	}
	return answered, partially
}

func newDesc(name, help string, labels ...string) *prometheus.Desc {
	return prometheus.NewDesc(prometheus.BuildFQName(metricsNamespace, metricsSubsystem, name), help, labels, nil)
}

var (
	degradedDesc      = newDesc("search_degraded_total", "Searches answered by the raw ranking because the memory read failed.")
	linkFallbacksDesc = newDesc("consolidation_link_fallbacks_total", "Batches linked without JEV because JEV failed.")
	addOutranDesc     = newDesc("add_outran_total", "Adds that waited out their window for consolidation and answered anyway.")
	batchesDesc       = newDesc("consolidation_batches_total", "Batches of records by outcome: folded (taken off the pending records, those given up on included), deferred (left pending because linking failed) and dropped (drops: a batch, or half of a split one, the layer gave up on).", "outcome")
)

// constMetric sends a series of desc to ch, unless it cannot be made: a
// label value that is not UTF-8 (a budget account a damaged or foreign
// budget file names) leaves the series out of the scrape, which goes on. A
// collector must not panic: the registry collects on goroutines of its own,
// and a panic there ends the process (the final review's parked T8-b).
func constMetric(ch chan<- prometheus.Metric, desc *prometheus.Desc, kind prometheus.ValueType, value float64, labels ...string) {
	if metric, err := prometheus.NewConstMetric(desc, kind, value, labels...); err == nil {
		ch <- metric
	}
}

// totalsCollector exposes the counters that are atomics of metrics.
type totalsCollector struct{ m *metrics }

func (totalsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- degradedDesc
	ch <- linkFallbacksDesc
	ch <- addOutranDesc
	ch <- batchesDesc
}

func (c totalsCollector) Collect(ch chan<- prometheus.Metric) {
	counter := func(desc *prometheus.Desc, count int64, labels ...string) {
		constMetric(ch, desc, prometheus.CounterValue, float64(count), labels...)
	}
	counter(degradedDesc, c.m.degraded.Load())
	counter(linkFallbacksDesc, c.m.linkFallbacks.Load())
	counter(addOutranDesc, c.m.addOutran.Load())
	for outcome, name := range batchOutcomeNames {
		counter(batchesDesc, c.m.batches[outcome].Load(), name)
	}
}

var (
	queuedDesc     = newDesc("consolidation_queued", "Users queued for a consolidation job (always 0 unless consolidation is background).")
	runningDesc    = newDesc("consolidation_running", "Users whose consolidation job is running.")
	backingOffDesc = newDesc("consolidation_backing_off", "Users waiting to retry after a failed consolidation job.")
	oldestDesc     = newDesc("consolidation_oldest_queued_seconds", "Age of the oldest work not yet consolidated: queued, running or backing off since a notification no successful job has covered.")
	backlogDesc    = newDesc("consolidation_max_backlog_characters", "Largest backlog of any user, in the characters a job reads.")
	openStoresDesc = newDesc("open_stores", "User stores open now.")
	// The next two come from the scan of the data root that GET /admin/v1/stores
	// shares: at most 60 seconds old.
	storeCountDesc = newDesc("stores", "User stores in the data root, from the latest scan of it (at most 60 seconds old).")
	storeBytesDesc = newDesc("store_bytes", "Bytes of the regular files of the user stores in the data root, from the latest scan of it (at most 60 seconds old).")
	lagBoundDesc   = newDesc("consolidation_lag_bound_characters", "Backlog, in characters, above which an Add waits for consolidation (always 0 unless consolidation is background).")
	breakerDesc    = newDesc("breaker_open", "1 while a provider kind's circuit breaker refuses calls, else 0 (1 if any breaker of the kind does).", "kind")
	spentDesc      = newDesc("budget_spent", "Spent from a paid account, in its currency.", "account")
	heldDesc       = newDesc("budget_held", "Held by unresolved reservations of a paid account, in its currency.", "account")
	capDesc        = newDesc("budget_cap", "Cap of a paid account, in its currency.", "account")
)

// scrapeWait bounds what a scrape waits for: its reads of the budget file,
// and the scan of the data root, its own or one under way that it joins
// (storeScanner.scan).
const scrapeWait = 5 * time.Second

// stateCollector reads the state the server keeps elsewhere when a scrape
// asks for it.
type stateCollector struct{ s *Server }

func (stateCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range []*prometheus.Desc{queuedDesc, runningDesc, backingOffDesc, oldestDesc, backlogDesc, lagBoundDesc, openStoresDesc, storeCountDesc, storeBytesDesc, breakerDesc, spentDesc, heldDesc, capDesc} {
		ch <- desc
	}
}

func (c stateCollector) Collect(ch chan<- prometheus.Metric) {
	gauge := func(desc *prometheus.Desc, value float64, labels ...string) {
		constMetric(ch, desc, prometheus.GaugeValue, value, labels...)
	}
	queue := c.s.queueStatus()
	gauge(queuedDesc, float64(queue.Queued))
	gauge(runningDesc, float64(queue.Running))
	gauge(backingOffDesc, float64(queue.BackingOff))
	gauge(oldestDesc, queue.OldestQueued.Seconds())
	gauge(backlogDesc, float64(queue.MaxBacklog))
	gauge(lagBoundDesc, float64(c.s.consolidationLagBound()))
	gauge(openStoresDesc, float64(c.s.users.open()))
	for kind, state := range c.s.breakerStates() {
		open := 0.0
		if state == memoryagent.BreakerOpen {
			open = 1
		}
		gauge(breakerDesc, open, kind)
	}
	ctx, cancel := context.WithTimeout(context.Background(), scrapeWait)
	defer cancel()
	// An unreadable budget file shows as missing accounts here; /ready says why.
	accounts, _ := c.s.Accounts(ctx)
	for _, account := range accounts {
		gauge(spentDesc, account.Spent, account.Kind)
		gauge(heldDesc, account.Held, account.Kind)
		gauge(capDesc, account.Cap, account.Kind)
	}
	// The stores' number and bytes are read from the cached scan of the data
	// root (a scan older than 60 seconds is made again, within what is left of
	// scrapeWait). A scan that fails leaves them out of this scrape, 0 would
	// be a claim; the scanner has told of it. No user or store ever labels
	// them.
	if scan, err := c.s.stores.scan(ctx); err == nil {
		gauge(storeCountDesc, float64(len(scan.stores)))
		gauge(storeBytesDesc, float64(scan.bytes))
	}
}
