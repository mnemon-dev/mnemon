package aml

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
	"github.com/mnemon-dev/mnemon/internal/records"
)

const (
	// maxBody bounds a request body.
	maxBody = 30 << 20
	// defaultOpenStores is how many user stores stay open by default.
	defaultOpenStores = 64
	// maxConsolidateCalls bounds the consolidation jobs one request runs.
	maxConsolidateCalls = 50
	// An Add consolidates until addWindow after it arrived, so it returns
	// well inside the platform's 1,800 s I/O timeout.
	addWindow = 25 * time.Minute
	// A Search first folds what the user's Adds left pending, for at most
	// flushWindow; its memory read then has SearchTimeout.
	flushWindow          = 10 * time.Minute
	defaultSearchTimeout = 10 * time.Minute
	// defaultTopK is a Search's top_k when it gives none and the strategy
	// has no View bound.
	defaultTopK = 100
	// embedBatch keeps a DerivedVectors call within its 512 texts and 2 MiB
	// (a chunk is at most ~602 UTF-16 units, under 2.5 kB).
	embedBatch = 256
	// statusEvery is how often Run logs the status line.
	statusEvery = 10 * time.Minute
	// heartbeatEvery is how often Run's heartbeat ticks (Heartbeat): well
	// within any watchdog interval systemd is given.
	heartbeatEvery = time.Second
)

// ConsolidationMode says when the server consolidates its users' records.
type ConsolidationMode string

const (
	// ConsolidationOff consolidates nothing.
	ConsolidationOff ConsolidationMode = ""
	// ConsolidationOnAdd consolidates during requests: in timelines mode
	// (and layered) an Add folds the full batches before it returns and a
	// Search first folds the rest (the conversation's tail); otherwise an
	// Add folds until caught up. Failures are logged, not returned.
	ConsolidationOnAdd ConsolidationMode = "on-add"
	// ConsolidationBackground (timelines or layered, without insights)
	// folds the full batches in supervised workers that Run starts
	// (scheduler.go): an Add only writes and notifies them, then waits while
	// its user's backlog is above one job's window, and a Search first folds
	// the rest, as in on-add mode. A link JEV cannot make for now is
	// deferred (ConsolidateRequest.DeferLinks), never made without JEV.
	ConsolidationBackground ConsolidationMode = "background"
)

// ServerConfig configures a Server.
type ServerConfig struct {
	// DataRoot is the absolute directory holding the per-user stores.
	DataRoot string
	// MemoryConfig is the memory strategy every user's agent runs.
	MemoryConfig memoryagent.Config
	// AuthKey is the API key every Add and Search must carry.
	AuthKey string
	// Consolidation is when the server consolidates (default off).
	Consolidation ConsolidationMode
	// ConsolidateOnAdd is the older name of Consolidation on-add; with
	// Consolidation unset it selects on-add.
	ConsolidateOnAdd bool
	// ConsolidationWorkers is how many users background consolidation folds
	// at once (default 4).
	ConsolidationWorkers int
	// SearchTimeout bounds a Search's memory read (default 10 minutes);
	// when it expires with the client still connected, the raw ranking
	// answers.
	SearchTimeout time.Duration
	// MaxOpenStores bounds the cached open user stores (default 64).
	MaxOpenStores int
	// MaxConcurrentAdds and MaxConcurrentSearches bound the Adds and the
	// Searches served at once (0 for no limit). A request that finds no free
	// slot waits for one; past 30 seconds it is shed with 503 and a
	// Retry-After, which the platform retries.
	MaxConcurrentAdds, MaxConcurrentSearches int
	// Models builds the models from MemoryConfig; nil builds its configured
	// providers. Tests inject scripted models here.
	Models func(memoryagent.Config) (memoryagent.Models, error)
	// Logger receives operational lines, never request bodies; nil logs text
	// to standard error. The server adds the request_id attribute to the
	// lines of a request.
	Logger *slog.Logger
	// AdminToken, when not empty, is the token every request to the admin
	// handler must carry as `Authorization: Bearer <token>` (AdminHandler); it
	// holds MinAdminTokenLength bytes at least. It never appears in a log, an
	// error or a metric.
	AdminToken string
	// Version is the product version /admin/status reports.
	Version string
}

// MinAdminTokenLength is the fewest bytes an admin token (ServerConfig) holds.
const MinAdminTokenLength = 32

// Server serves the AML contract. Every user's agent shares the models.
type Server struct {
	config   ServerConfig
	key      [sha256.Size]byte // digest of the API key
	models   memoryagent.Models
	budget   *memoryagent.Budget
	cache    *memoryagent.ModelCache // the models' replay cache (eval only); nil without one
	maxItems int                     // the View's record bound; 0 for memory-agent/v1
	// timelines reports consolidation mode "timelines" or "layered": Adds
	// fold full batches and Searches flush the tail.
	timelines bool
	// mode is when the server consolidates; in background mode sched runs
	// the jobs, and an Add waits while its user's backlog is above lagBound,
	// one job's window (memoryagent.Agent.JobWindow).
	mode     ConsolidationMode
	sched    *scheduler
	lagBound int64
	users    *registry
	log      *slog.Logger
	// addGate and searchGate admit the Adds and the Searches (nil: without
	// limit); a request waits admitWait for a slot before it is shed.
	addGate, searchGate *gate
	admitWait           time.Duration
	// baseCtx is the context of every request HTTPServer's servers run;
	// CancelRequests ends it.
	baseCtx        context.Context
	cancelRequests context.CancelFunc
	// metrics counts what the server serves and does (metrics.go): the
	// requests, the Searches degraded or partly degraded, the consolidation
	// batches, the models' attempts.
	metrics *metrics
	started time.Time // when NewServer made the server
	// adminKey is the digest of the admin token, nil without one: the admin
	// handler then asks nothing of a request.
	adminKey *[sha256.Size]byte
	// stores scans the data root for the store overview and the store gauges.
	stores *storeScanner
	// freeDisk is the free bytes of the disk holding a directory, which
	// readiness measures.
	freeDisk func(dir string) (uint64, error)
	// The windows of an Add's consolidation, a Search's flush and its read.
	addWindow, flushWindow, searchTimeout time.Duration
	// heartbeatEvery spaces the ticks of Run's heartbeat; beat holds the
	// last one's time (nil before the first).
	heartbeatEvery time.Duration
	beat           atomic.Pointer[time.Time]
	handler, admin http.Handler
	once           sync.Once
	// runMu guards Run's lifecycle: stopRun ends the running Run, which
	// closes runDone once its goroutines have exited; closed refuses Run
	// after Close.
	runMu   sync.Mutex
	stopRun context.CancelFunc
	runDone chan struct{}
	closed  bool
}

// NewServer checks the configuration, builds the shared models and checks
// that an agent can be made from them before it serves anything.
func NewServer(config ServerConfig) (*Server, error) {
	if config.AuthKey == "" {
		return nil, errors.New("the AML server requires a nonempty API key")
	}
	if config.DataRoot == "" || !filepath.IsAbs(config.DataRoot) {
		return nil, errors.New("the AML server requires an absolute data root")
	}
	if config.MaxOpenStores == 0 {
		config.MaxOpenStores = defaultOpenStores
	}
	if config.MaxOpenStores < 1 {
		return nil, errors.New("max open stores must be at least 1")
	}
	if config.MaxConcurrentAdds < 0 {
		return nil, errors.New("max concurrent adds must not be negative")
	}
	if config.MaxConcurrentSearches < 0 {
		return nil, errors.New("max concurrent searches must not be negative")
	}
	if config.SearchTimeout == 0 {
		config.SearchTimeout = defaultSearchTimeout
	}
	if config.SearchTimeout < 0 {
		return nil, errors.New("the search timeout must be positive")
	}
	if config.AdminToken != "" && len(config.AdminToken) < MinAdminTokenLength {
		// The error does not repeat the token.
		return nil, fmt.Errorf("the admin token must hold at least %d characters", MinAdminTokenLength)
	}
	mode, err := consolidationMode(config)
	if err != nil {
		return nil, err
	}
	config.Consolidation = mode
	if config.ConsolidationWorkers == 0 {
		config.ConsolidationWorkers = defaultConsolidationWorkers
	}
	if err := os.MkdirAll(config.DataRoot, 0o700); err != nil {
		return nil, err
	}
	consolidation := config.MemoryConfig.Consolidation
	s := &Server{config: config, key: sha256.Sum256([]byte(config.AuthKey)), log: config.Logger,
		timelines: consolidation.Timelines(), mode: mode,
		addGate: newGate(config.MaxConcurrentAdds), searchGate: newGate(config.MaxConcurrentSearches), admitWait: admissionWait,
		addWindow: addWindow, flushWindow: flushWindow, searchTimeout: config.SearchTimeout, heartbeatEvery: heartbeatEvery,
		metrics: newMetrics(time.Now), started: time.Now().UTC(), freeDisk: freeDiskBytes}
	if s.log == nil {
		s.log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	s.log = slog.New(contextHandler{s.log.Handler()})
	if config.AdminToken != "" {
		digest := sha256.Sum256([]byte(config.AdminToken))
		s.adminKey = &digest
	}
	s.stores = newStoreScanner(config.DataRoot, time.Now, s.scanLog)
	if config.Models != nil {
		s.models, err = config.Models(config.MemoryConfig)
	} else {
		s.models, s.budget, s.cache, err = memoryagent.OpenModels(config.MemoryConfig)
	}
	if err != nil {
		return nil, err
	}
	if s.maxItems, s.lagBound, err = s.probe(); err != nil {
		s.closeModels()
		return nil, err
	}
	s.baseCtx, s.cancelRequests = context.WithCancel(context.Background())
	s.users = newRegistry(config.DataRoot, config.MaxOpenStores, s.openUser)
	if mode == ConsolidationBackground {
		s.sched = newScheduler(config.ConsolidationWorkers, maxConsolidationUsers, s.consolidateUser, s.schedulerLog, time.Now)
		s.sched.jobDone = s.metrics.job
	}
	// The providers' attempts, the queue, the stores, the breakers and the
	// accounts reach the metrics from here on.
	s.metrics.registry.MustRegister(stateCollector{s})
	s.models.Observe(s.metrics)
	s.handler = s.routes()
	s.admin = s.adminRoutes()
	return s, nil
}

// logf logs one line at level. The lines keep the wording they had before the
// server logged through slog, which the runbook's table of lines quotes; ctx
// is the context of the work the line is about.
func (s *Server) logf(ctx context.Context, level slog.Level, format string, args ...any) {
	if s.log.Enabled(ctx, level) {
		s.log.Log(ctx, level, fmt.Sprintf(format, args...))
	}
}

// schedulerLog is the scheduler's logf. Its lines report faults (a full
// queue, a failed or panicked job, a pause), so they are warnings.
func (s *Server) schedulerLog(format string, args ...any) {
	s.logf(context.Background(), slog.LevelWarn, format, args...)
}

// scanLog is the store scanner's logf. A scan that fails leaves the overview
// and the store gauges out, which is a fault: a warning.
func (s *Server) scanLog(format string, args ...any) {
	s.logf(context.Background(), slog.LevelWarn, format, args...)
}

// storeLog is the logger of the store of the user with key: what the store's
// query runtime reports is a warning that names the user.
func (s *Server) storeLog(key string) func(format string, args ...any) {
	return func(format string, args ...any) {
		s.logf(context.Background(), slog.LevelWarn, format+" (user %s)", append(args[:len(args):len(args)], short(key))...)
	}
}

// consolidationMode is the configuration's consolidation mode, checked with
// its workers; ConsolidateOnAdd alone is on-add.
func consolidationMode(config ServerConfig) (ConsolidationMode, error) {
	mode := config.Consolidation
	if config.ConsolidateOnAdd {
		if mode != ConsolidationOff && mode != ConsolidationOnAdd {
			return "", fmt.Errorf("ConsolidateOnAdd conflicts with consolidation %q", mode)
		}
		mode = ConsolidationOnAdd
	}
	switch mode {
	case ConsolidationOff, ConsolidationOnAdd:
	case ConsolidationBackground:
		consolidation := config.MemoryConfig.Consolidation
		if !consolidation.Timelines() {
			return "", errors.New(`background consolidation requires consolidation mode "timelines" or "layered"`)
		}
		// L2 reads every unread item in date order, so its batches depend
		// on how many batches each job folded: a worker's jobs would read
		// other L2 batches than the Adds' jobs of on-add mode.
		if consolidation.Insights {
			return "", errors.New("background consolidation does not support consolidation.insights: its L2 batches would differ from on-add mode's; use on-add")
		}
	default:
		return "", fmt.Errorf("unknown consolidation %q: off (\"\"), on-add or background", mode)
	}
	if config.ConsolidationWorkers < 0 {
		return "", errors.New("consolidation workers must be at least 1")
	}
	return mode, nil
}

// probe makes an agent over an empty scratch store, so a configuration the
// agents refuse fails at startup rather than on every request, and reads
// the View's record bound and a consolidation job's window.
func (s *Server) probe() (int, int64, error) {
	dir, err := os.MkdirTemp("", "aml-probe-")
	if err != nil {
		return 0, 0, err
	}
	defer os.RemoveAll(dir)
	store, err := records.Open(dir, false)
	if err != nil {
		return 0, 0, err
	}
	defer store.Close()
	agent, err := memoryagent.New(store, s.config.MemoryConfig, s.models, nil)
	if err != nil {
		return 0, 0, err
	}
	defer agent.Close()
	return agent.MaxItems(), agent.JobWindow(), nil
}

// openUser opens a user's store with the query runtime records serve runs
// (query cache, reader pool, background lexical index), and its agent.
func (s *Server) openUser(dir string) (*records.Store, *memoryagent.Agent, func(), error) {
	store, err := records.Open(dir, false)
	if err != nil {
		return nil, nil, nil, err
	}
	store.SetLogger(s.storeLog(filepath.Base(dir)))
	stop, err := store.StartRuntime(context.Background())
	if err != nil {
		store.Close()
		return nil, nil, nil, err
	}
	agent, err := memoryagent.OpenWithModels(store, dir, s.config.MemoryConfig, false, s.models)
	if err != nil {
		stop()
		store.Close()
		return nil, nil, nil, err
	}
	return store, agent, stop, nil
}

// Close stops a running Run and waits for it, then closes the user stores
// (held ones when their request ends) and the models' budget and model
// cache. Stop the HTTP server first. Closing the models waits for their
// requests in flight, abandoned ones included, each until it ends or its hard
// timeout (memoryagent.Provider.Close), so Close can take that long.
func (s *Server) Close() error {
	s.once.Do(func() {
		s.runMu.Lock()
		s.closed = true
		stop, done := s.stopRun, s.runDone
		s.runMu.Unlock()
		if stop != nil {
			stop()
			<-done
		}
		s.users.close()
		s.closeModels()
	})
	return nil
}

// Run runs the server's background work until ctx ends or Close is called:
// in background mode the consolidation scheduler, which first queues every
// user directory under <data-root>/users/ (a user with nothing pending costs
// one state read); the status line every 10 minutes; and the heartbeat
// (Heartbeat). Once all of it runs, the users queued and the heartbeat's
// first tick taken, it calls ready unless ready is nil: requests may come
// from then on. It returns once all of it has exited, nil unless Run is
// already running or the server is closed (ready is not called then). No
// background work runs without Run. A graceful stop first drains the
// scheduler (Drain, WaitDrained), then ends ctx, which cuts the jobs still
// running.
func (s *Server) Run(ctx context.Context, ready func()) error {
	s.runMu.Lock()
	switch {
	case s.closed:
		s.runMu.Unlock()
		return errClosing
	case s.runDone != nil:
		s.runMu.Unlock()
		return errors.New("the AML server is already running")
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.stopRun, s.runDone = cancel, done
	s.runMu.Unlock()
	defer func() {
		cancel()
		s.runMu.Lock()
		s.stopRun, s.runDone = nil, nil
		s.runMu.Unlock()
		close(done)
	}()
	var work sync.WaitGroup
	if s.sched != nil {
		s.queueUsers()
		work.Add(1)
		go func() {
			defer work.Done()
			s.sched.Run(ctx)
		}()
	}
	work.Add(1)
	go func() {
		defer work.Done()
		s.logStatus(ctx, statusEvery)
	}()
	s.tick()
	if ready != nil {
		ready()
	}
	s.heartbeat(ctx)
	work.Wait()
	return nil
}

// heartbeat ticks every heartbeatEvery until ctx ends.
func (s *Server) heartbeat(ctx context.Context) {
	ticker := time.NewTicker(s.heartbeatEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick()
		}
	}
}

// tick is one beat of the heartbeat: once the scheduler and the store
// registry have answered (their locks were free), it records the time. A
// tick that cannot take a lock waits for it, and the heartbeat stops.
func (s *Server) tick() {
	if s.sched != nil {
		s.sched.runningJobs()
	}
	s.users.open()
	now := time.Now()
	s.beat.Store(&now)
}

// Heartbeat is when Run's heartbeat last ticked, zero before its first tick.
// It ticks every second while Run runs, each tick once the consolidation
// scheduler and the store registry have answered, so a heartbeat older than a
// few seconds means that Run has stopped or that the server's locks are
// stuck. systemd's watchdog (aml-serve) reads it.
func (s *Server) Heartbeat() time.Time {
	if beat := s.beat.Load(); beat != nil {
		return *beat
	}
	return time.Time{}
}

// Drain is the first step of a graceful stop of the background
// consolidation: from now on no job starts, a user's running job ends after
// its current consolidation job (its batches saved), and no Add waits for
// consolidation (its records are stored and searchable). It does not stop
// Run; Run's context ending cuts the jobs still running, which resume after
// a restart. Without background consolidation it does nothing. It may be
// called again.
func (s *Server) Drain() {
	if s.sched != nil {
		s.sched.Drain()
	}
}

// WaitDrained drains the background consolidation (Drain) and returns nil
// once no job runs; if ctx ends first, an error wrapping ctx's that counts
// the jobs still running.
func (s *Server) WaitDrained(ctx context.Context) error {
	if s.sched == nil {
		return nil
	}
	return s.sched.WaitDrained(ctx)
}

// queueUsers queues every user store under the data root for a job.
func (s *Server) queueUsers() {
	entries, err := os.ReadDir(filepath.Join(s.config.DataRoot, "users"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		s.logf(context.Background(), slog.LevelError, "consolidation could not list the users: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() && isUserKey(entry.Name()) {
			s.sched.Notify(entry.Name(), 0)
		}
	}
}

// consolidateUser is a background job of the user with key: through the
// registry (so eviction cannot close the store under it), consolidate's held
// jobs with DeferLinks until a job folds no full batch, or until the user's
// Search holds it or the scheduler drains, then the layer warmed when
// anything folded. It returns the user's backlog measured after them
// (negative when unknown) and the failure of a job. A job that stopped with
// full batches left notifies the scheduler, which runs the user again.
func (s *Server) consolidateUser(ctx context.Context, key string) (int64, error) {
	user, err := s.users.acquireKey(ctx, key, false)
	if err != nil {
		s.logf(ctx, slog.LevelWarn, "consolidation failed (user %s): %v", short(key), err)
		return -1, err
	}
	if user == nil {
		return 0, nil
	}
	defer s.users.release(user)
	outcome := s.consolidate(ctx, user, true, "consolidation", func() bool { return s.sched.yielding(key) })
	if outcome.folded > 0 {
		if err := user.agent.WarmLayer(ctx, records.Scope{}); err != nil {
			s.logf(ctx, slog.LevelWarn, "layer embedding failed (user %s): %v", short(key), err)
		}
	}
	if outcome.unfinished && outcome.err == nil {
		s.sched.Notify(key, 0)
	}
	backlog, err := user.agent.Backlog(ctx, records.Scope{})
	if err != nil {
		if outcome.err == nil {
			s.logf(ctx, slog.LevelWarn, "consolidation backlog unreadable (user %s): %v", short(key), err)
			return -1, err
		}
		return -1, outcome.err
	}
	return backlog.Characters, outcome.err
}

// closeModels closes the files the models share, after their last call.
func (s *Server) closeModels() {
	s.models.Close()
	if s.budget != nil {
		s.budget.Close()
	}
	if s.cache != nil {
		s.cache.Close()
	}
}

// DegradedSearches counts the Searches answered by raw search because the
// memory read failed.
func (s *Server) DegradedSearches() int64 { return s.metrics.degraded.Load() }

// PartialDegradedSearches counts the Searches answered by a memory read in
// which a stage fell back (memoryagent.ReadStats.Degraded).
func (s *Server) PartialDegradedSearches() int64 { return s.metrics.partial.Load() }

// statusLine is the served Adds and Searches, the degraded and partially
// degraded Searches and, with a budget file, each account's spent, held and
// cap.
func (s *Server) statusLine(ctx context.Context) string {
	m := s.metrics
	line := fmt.Sprintf("status: adds %d, searches %d, degraded %d, partially degraded %d", m.adds.Load(), m.searches.Load(), m.degraded.Load(), m.partial.Load())
	if s.budget == nil {
		return line
	}
	accounts, err := s.budget.Accounts(ctx)
	if err != nil {
		return line + "; budget unreadable: " + err.Error()
	}
	parts := []string{}
	for _, account := range accounts {
		parts = append(parts, fmt.Sprintf("%s spent %.4f held %.4f cap %g", account.Kind, account.Spent, account.Held, account.Cap))
	}
	if len(parts) > 0 {
		line += "; budget " + strings.Join(parts, ", ")
	}
	return line
}

// logStatus logs the status line every interval until ctx ends; Run runs it,
// so the line is logged once per interval.
func (s *Server) logStatus(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.logf(ctx, slog.LevelInfo, "%s", s.statusLine(ctx))
		}
	}
}

// Handler routes POST /add and POST /search (authenticated) and GET /health
// and GET / (not); anything else is 404. Every response carries the request's
// id in X-Request-Id.
func (s *Server) Handler() http.Handler { return s.handler }

// HTTPServer is the HTTP server for addr with the service's timeouts: an Add
// may consolidate for up to the platform's 30-minute I/O timeout. Its requests
// run on a context CancelRequests ends.
func (s *Server) HTTPServer(addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           s.handler,
		BaseContext:       func(net.Listener) context.Context { return s.baseCtx },
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute,
		WriteTimeout:      31 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelError),
	}
}

// CancelRequests ends the context of every request being served through the
// servers HTTPServer made, and of those that arrive later, as a forced
// shutdown does once its grace has ended: their model calls and waits stop and
// the handlers answer 503, which the platform retries. It does not touch the
// background scheduler (Run's context ends that), and may be called again.
func (s *Server) CancelRequests() { s.cancelRequests() }

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	health := s.metrics.instrument(routeHealth, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))
	mux.Handle("GET /{$}", health)
	mux.Handle("GET /health", health)
	mux.Handle("POST /add", s.guarded(routeAdd, s.addGate, s.add))
	mux.Handle("POST /search", s.guarded(routeSearch, s.searchGate, s.search))
	mux.Handle("/", s.metrics.instrument(routeOther, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})))
	return identified(s.logged(mux))
}

// authorized accepts `Authorization: Bearer <key>`, `Authorization: Token
// <key>` or `X-Api-Key: <key>`. The SHA-256 digests of the presented and the
// configured key are compared in constant time, so neither the key nor its
// length leaks through timing.
func (s *Server) authorized(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.validKey(r) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="aml"`)
			writeError(w, http.StatusUnauthorized, "missing or invalid API key")
			return
		}
		next(w, r)
	})
}

func (s *Server) validKey(r *http.Request) bool {
	keys := []string{}
	if scheme, key, ok := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " "); ok && (strings.EqualFold(scheme, "Bearer") || strings.EqualFold(scheme, "Token")) {
		keys = append(keys, strings.TrimSpace(key))
	}
	if key := r.Header.Get("X-Api-Key"); key != "" {
		keys = append(keys, strings.TrimSpace(key))
	}
	valid := false
	for _, key := range keys {
		digest := sha256.Sum256([]byte(key))
		if subtle.ConstantTimeCompare(digest[:], s.key[:]) == 1 {
			valid = true
		}
	}
	return valid
}

// recovered answers 500 when the handler panics before it answered, and
// logs the panic with its stack, never the request.
func (s *Server) recovered(operation string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writer := &statusWriter{ResponseWriter: w}
		defer func() {
			value := recover()
			if value == nil {
				return
			}
			if value == http.ErrAbortHandler {
				panic(value)
			}
			s.logf(r.Context(), slog.LevelError, "%s panicked: %v\n%s", operation, value, debug.Stack())
			if !writer.wrote {
				writeError(writer, http.StatusInternalServerError, operation+" failed")
			}
		}()
		next(writer, r)
	}
}

// add stores one chunk of a conversation and returns once it is persisted and
// searchable: written (in parts when it is longer than a document holds),
// its new chunks embedded when the configuration embeds, and, in on-add
// mode, consolidated (consolidateAdd). In background mode it then hands the
// user to the scheduler and waits while the user's backlog is above the lag
// bound (awaitConsolidation); it calls no model. A retry of a stored request
// succeeds and finishes what the earlier attempt may not have. Only a
// missing request_id, user_id or session_id, or a request_id reused for
// other text, is a 400.
func (s *Server) add(w http.ResponseWriter, r *http.Request) {
	deadline := time.Now().Add(s.addWindow)
	ctx := r.Context()
	var req AddRequest
	if !s.decode(w, r, &req) {
		return
	}
	if err := req.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	parts, skipped, err := req.parts()
	if err != nil {
		s.fail(ctx, w, "pack", "", err)
		return
	}
	done := AddResponse{Success: true, RequestID: req.RequestID, UserID: req.UserID, SessionID: req.SessionID}
	if skipped > 0 {
		s.logf(ctx, slog.LevelInfo, "add skipped %d of %d messages without text", skipped, len(req.Messages))
	}
	if len(parts) == 0 {
		s.metrics.adds.Add(1)
		writeJSON(w, http.StatusOK, done)
		return
	}
	user, err := s.users.acquire(ctx, req.UserID, true)
	if err != nil {
		s.fail(ctx, w, "open store", "", err)
		return
	}
	release := sync.OnceFunc(func() { s.users.release(user) })
	defer release()
	written, ok := s.writeParts(ctx, w, user, parts, deadline)
	if !ok {
		return
	}
	if s.sched != nil {
		// Stored: the wait needs the scheduler, not the store.
		release()
		s.sched.Notify(user.key, written)
		s.awaitConsolidation(ctx, user.key, deadline)
	}
	s.metrics.adds.Add(1)
	writeJSON(w, http.StatusOK, done)
}

// writeParts writes an Add's parts under the user's write lock and embeds
// their chunks; in on-add mode it then consolidates under the lock. It
// returns the characters of the chunks written, about what consolidation
// will read of them, and false when it answered with an error.
func (s *Server) writeParts(ctx context.Context, w http.ResponseWriter, user *userEntry, parts []addPart, deadline time.Time) (int64, bool) {
	user.waiting.Add(1)
	user.writes.Lock()
	user.waiting.Add(-1)
	defer user.writes.Unlock()
	chunks := []string{}
	for _, part := range parts {
		err := s.write(ctx, user, part)
		if errors.Is(err, errRequestReused) {
			writeError(w, http.StatusBadRequest, err.Error())
			return 0, false
		}
		if err != nil {
			s.fail(ctx, w, "write", user.key, err)
			return 0, false
		}
		chunks = append(chunks, part.chunks...)
	}
	if embedding := s.config.MemoryConfig.Embedding; embedding != nil {
		if err := embedChunks(ctx, user.store, *embedding, chunks); err != nil {
			s.logf(ctx, slog.LevelError, "embedding failed (user %s): %v", user.key[:8], err)
			writeError(w, http.StatusServiceUnavailable, "embedding is unavailable; retry the same request")
			return 0, false
		}
	}
	if s.mode == ConsolidationOnAdd {
		s.consolidateAdd(ctx, user, deadline)
	}
	written := int64(0)
	for _, chunk := range chunks {
		written += int64(utf8.RuneCountInString(chunk))
	}
	return written, true
}

// awaitConsolidation waits until the user's backlog is within the lag bound,
// one job's window, or the Add's window ends at deadline. An Add that
// outran consolidation still answers: its records are stored and
// searchable.
func (s *Server) awaitConsolidation(ctx context.Context, key string, deadline time.Time) {
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	if err := s.sched.WaitBacklog(ctx, key, s.lagBound); errors.Is(err, context.DeadlineExceeded) {
		s.metrics.addOutran.Add(1)
		s.logf(ctx, slog.LevelWarn, "add outran consolidation (user %s)", short(key))
	}
}

var errRequestReused = errors.New("request_id was already used for a different payload")

// write stores one part. A document stored under the part's id with the same
// content is an earlier attempt of this request: success. A changed revision
// is retried once.
func (s *Server) write(ctx context.Context, user *userEntry, part addPart) error {
	for attempt := 0; ; attempt++ {
		_, err := memoryagent.WriteConversation(ctx, user.store, part.conversation)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, memoryagent.ErrDocumentExists):
			same, err := sameDocument(ctx, user.store, part.conversation.ID, part.parent)
			if err != nil {
				return err
			}
			if !same {
				return errRequestReused
			}
			return nil
		case attempt == 0 && errors.Is(err, records.ErrConflict):
			continue
		default:
			return err
		}
	}
}

// sameDocument reports whether the stored document id holds content. Records
// of the document without the document itself are a damaged store, an error.
func sameDocument(ctx context.Context, store *records.Store, id, content string) (bool, error) {
	snapshot, err := store.Get(ctx, records.GetRequest{IDs: []string{id}})
	if err != nil {
		return false, err
	}
	if len(snapshot.Records) != 1 {
		return false, fmt.Errorf("records of document %s exist without the document", id)
	}
	var stored struct {
		Kind    string `json:"kind"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(snapshot.Records[0]), &stored); err != nil {
		return false, err
	}
	if stored.Kind != "memory-document" {
		return false, fmt.Errorf("record %s is not a conversation document", id)
	}
	return stored.Content == content, nil
}

// embedChunks stores the document vectors of the new chunks: the texts their
// records are embedded by, under the document prefix, so a query finds them
// cached. Stored vectors are reused.
func embedChunks(ctx context.Context, store *records.Store, config records.EmbeddingConfig, chunks []string) error {
	for start := 0; start < len(chunks); start += embedBatch {
		if _, err := store.DerivedVectors(ctx, chunks[start:min(start+embedBatch, len(chunks))], config, true); err != nil {
			return err
		}
	}
	return nil
}

// consolidateAdd consolidates what an Add stored, until deadline (the Add's
// arrival plus addWindow), then stores the layer's embeddings for the next
// read. In timelines mode it folds only full batches until a job folds none;
// the short rest waits for the user's next Search (flush), as the reference
// system folds it once the conversation pauses. Otherwise it folds until
// caught up. It gives way to an Add queued behind it for the same user,
// which catches up, and is skipped when the deadline has passed. Failures
// are logged: the records are searchable already.
func (s *Server) consolidateAdd(ctx context.Context, user *userEntry, deadline time.Time) {
	if user.waiting.Load() > 0 {
		s.logf(ctx, slog.LevelInfo, "consolidation left to a queued Add (user %s)", user.key[:8])
		return
	}
	if !time.Now().Before(deadline) {
		s.logf(ctx, slog.LevelWarn, "consolidation skipped: the Add's window has passed (user %s)", user.key[:8])
		return
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	s.consolidate(ctx, user, s.timelines, "consolidation", nil)
	if err := user.agent.WarmLayer(ctx, records.Scope{}); err != nil {
		s.logf(ctx, slog.LevelWarn, "layer embedding failed (user %s): %v", user.key[:8], err)
	}
}

// flushHeld is flush while the Search holds its user in the scheduler
// (background mode): no job of the user starts meanwhile, and one already
// running ends first (the jobs' timelines lock).
func (s *Server) flushHeld(ctx context.Context, user *userEntry) {
	if s.sched != nil {
		defer s.sched.Hold(user.key)()
	}
	s.flush(ctx, user)
}

// flush folds what the user's Adds left pending, the tail of the
// conversation, before a Search reads, for at most flushWindow, its model
// calls of the Flush class; then it stores the layer's embeddings when it
// folded anything. A failed or unfinished flush is logged, and the Search
// reads what is consolidated; in background mode a job takes up the rest.
// With nothing pending it only loads the state.
func (s *Server) flush(ctx context.Context, user *userEntry) {
	ctx, cancel := context.WithTimeout(memoryagent.WithCallClass(ctx, memoryagent.Flush), s.flushWindow)
	defer cancel()
	outcome := s.consolidate(ctx, user, false, "flush", nil)
	if outcome.unfinished && s.sched != nil {
		s.sched.Notify(user.key, 0)
	}
	if outcome.folded == 0 {
		return
	}
	if err := user.agent.WarmLayer(ctx, records.Scope{}); err != nil {
		s.logf(ctx, slog.LevelWarn, "layer embedding failed (user %s): %v", user.key[:8], err)
	}
}

// foldOutcome is what consolidate's jobs did: the batches they folded, the
// error the last one failed with, and whether they stopped with work left
// (after an error, maxConsolidateCalls jobs, or stop).
type foldOutcome struct {
	folded     int
	err        error
	unfinished bool
}

// consolidate runs the user's consolidation jobs: with hold, until a job
// folds no full batch; without, until nothing remains (a job reads full
// batches; one that finds nothing new folds the rest). It stops at the first
// error, logged under label, after maxConsolidateCalls jobs, or when stop
// (nil for never) says so before a job; it logs each drop of records. In
// background mode the jobs defer the links JEV cannot make for now
// (DeferLinks). The jobs defer the records' native edges (nativeEdges);
// when one leaves them pending, they are rebuilt once after the jobs.
func (s *Server) consolidate(ctx context.Context, user *userEntry, hold bool, label string, stop func() bool) (outcome foldOutcome) {
	pending := false
	defer func() {
		if pending {
			s.rebuildEdges(ctx, user, label)
		}
	}()
	for call := 1; call <= maxConsolidateCalls; call++ {
		if stop != nil && stop() {
			outcome.unfinished = true
			break
		}
		result, err := s.consolidateOnce(ctx, user, memoryagent.ConsolidateRequest{Hold: hold, DeferEdges: true, DeferLinks: s.mode == ConsolidationBackground})
		outcome.folded += result.Folded
		pending = pending || result.EdgesPending
		s.metrics.consolidated(result)
		for _, drop := range result.Drops {
			s.logf(ctx, slog.LevelWarn, "consolidation dropped %d records (user %s): %s", drop.Records, user.key[:8], drop.Reason)
		}
		if result.LinkFallbacks > 0 {
			s.logf(ctx, slog.LevelWarn, "consolidation linked %d batches without JEV (user %s): JEV failed or its budget is spent, so only keys of the same name were joined", result.LinkFallbacks, user.key[:8])
		}
		if err != nil {
			s.logf(ctx, slog.LevelWarn, "%s failed (user %s): %v", label, user.key[:8], err)
			outcome.err, outcome.unfinished = err, true
			break
		}
		if hold && result.Folded == 0 || !hold && result.Remaining == 0 {
			break
		}
		if call == maxConsolidateCalls {
			s.logf(ctx, slog.LevelWarn, "%s stopped with %d records pending (user %s)", label, result.Remaining, user.key[:8])
			outcome.unfinished = true
		}
	}
	return outcome
}

// rebuildEdges rebuilds the records' native edges that consolidate's jobs
// left pending, unless its window has passed; a failure (a rebuild returns
// its error; it reports no edgeError), the event edge pairs JEV left
// undecided for the next rebuild, and those it gave up (edgeKinds), are
// logged under label, and the next rebuild catches up.
func (s *Server) rebuildEdges(ctx context.Context, user *userEntry, label string) {
	if ctx.Err() != nil {
		s.logf(ctx, slog.LevelWarn, "%s left native edges pending (user %s): %v", label, user.key[:8], ctx.Err())
		return
	}
	result, err := s.consolidateOnce(ctx, user, memoryagent.ConsolidateRequest{RebuildEdges: true})
	if err != nil {
		s.logf(ctx, slog.LevelWarn, "%s native edges failed (user %s): %v", label, user.key[:8], err)
	}
	if result.EdgeFallbacks > 0 {
		s.logf(ctx, slog.LevelWarn, "%s native edges left %d pairs undecided (user %s)", label, result.EdgeFallbacks, user.key[:8])
	}
	if result.EdgeGivenUp > 0 {
		s.logf(ctx, slog.LevelWarn, "%s native edges gave up %d pairs whose calls failed 3 times (user %s)", label, result.EdgeGivenUp, user.key[:8])
	}
}

// consolidateOnce runs one job; a job that panics fails, its panic and stack
// logged.
func (s *Server) consolidateOnce(ctx context.Context, user *userEntry, req memoryagent.ConsolidateRequest) (result memoryagent.ConsolidateResult, err error) {
	defer func() {
		if value := recover(); value != nil {
			s.logf(ctx, slog.LevelError, "consolidation panicked (user %s): %v\n%s", user.key[:8], value, debug.Stack())
			result, err = memoryagent.ConsolidateResult{}, fmt.Errorf("panic: %v", value)
		}
	}()
	return user.agent.Consolidate(ctx, req)
}

// search reads the user's memory for the question, within SearchTimeout,
// after flushing the user's consolidation (timelines or layered, on-add or
// in the background). When the read fails for any reason but the client
// leaving (a model, a budget, the embedder, the timeout, a panic), the raw
// search ranking answers instead, and the degradation is counted; a read in
// which a stage fell back is logged and counted as partially degraded. A
// blank query finds nothing; a missing or nonpositive top_k is the View's
// maximum. Only a missing user_id is a 400.
func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	var req SearchRequest
	if !s.decode(w, r, &req) {
		return
	}
	if err := req.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Query) == "" {
		s.answer(w, []SearchItem{})
		return
	}
	topK := s.topK(req.TopK)
	ctx := r.Context()
	user, err := s.users.acquire(ctx, req.UserID, false)
	if err != nil {
		s.fail(ctx, w, "open store", "", err)
		return
	}
	if user == nil {
		s.answer(w, []SearchItem{})
		return
	}
	defer s.users.release(user)
	if s.mode != ConsolidationOff && s.timelines {
		s.flushHeld(ctx, user)
	}
	// The platform asks a fixed top_k; a View bound below it gives fewer
	// records rather than an error.
	maxItems := 0
	if s.maxItems > 0 {
		maxItems = min(topK, s.maxItems)
	}
	view, err := s.read(ctx, user, memoryagent.ReadRequest{Question: req.question(), MaxItems: maxItems})
	if err == nil {
		if stages := view.Stats.Degraded; len(stages) > 0 {
			n := s.metrics.searchPartlyDegraded(stages)
			s.logf(ctx, slog.LevelWarn, "partially degraded search %d (user %s): %s", n, user.key[:8], strings.Join(stages, ", "))
		}
		s.answer(w, searchItems(view, topK))
		return
	}
	if ctx.Err() != nil {
		writeError(w, http.StatusServiceUnavailable, "request canceled")
		return
	}
	n := s.metrics.searchDegraded()
	s.logf(ctx, slog.LevelWarn, "degraded search %d (user %s): %v", n, user.key[:8], err)
	items, err := s.rawSearch(ctx, user, req.Query, topK)
	if err != nil {
		s.fail(ctx, w, "degraded search", user.key, err)
		return
	}
	s.answer(w, items)
}

// topK is a Search's top_k: a missing or nonpositive one is the View's
// record bound, or defaultTopK without one.
func (s *Server) topK(requested int) int {
	switch {
	case requested > 0:
		return requested
	case s.maxItems > 0:
		return s.maxItems
	}
	return defaultTopK
}

// read is the user's memory read within searchTimeout; a read that panics
// fails, its panic and stack logged.
func (s *Server) read(ctx context.Context, user *userEntry, req memoryagent.ReadRequest) (view memoryagent.View, err error) {
	ctx, cancel := context.WithTimeout(ctx, s.searchTimeout)
	defer cancel()
	defer func() {
		if value := recover(); value != nil {
			s.logf(ctx, slog.LevelError, "memory read panicked (user %s): %v\n%s", user.key[:8], value, debug.Stack())
			view, err = memoryagent.View{}, fmt.Errorf("panic: %v", value)
		}
	}()
	return user.agent.Read(ctx, req)
}

// answer writes a Search's items, scored by position (1/(position+1), in
// the order delivered), and counts the Search.
func (s *Server) answer(w http.ResponseWriter, items []SearchItem) {
	for i := range items {
		items[i].Score = 1 / float64(i+1)
	}
	s.metrics.searchAnswered()
	writeJSON(w, http.StatusOK, SearchResponse{Data: items})
}

// rawSearch is the store's own ranking of the query, hybrid when the
// configuration embeds (words when the embedder fails), hydrated as reads
// hydrate evidence, cut at top_k by rank and listed as the View's order
// setting lists a read's records (time order with view.order "time"; on a
// failure to order, rank order, logged). It uses no model.
func (s *Server) rawSearch(ctx context.Context, user *userEntry, query string, topK int) ([]SearchItem, error) {
	snapshot, err := user.store.Get(ctx, records.GetRequest{IDs: []string{}})
	if err != nil {
		return nil, err
	}
	config := s.config.MemoryConfig
	ids, err := rawQuery(ctx, user.store, snapshot.Revision, query, config.Embedding, config.Index)
	if err != nil && config.Embedding != nil {
		s.logf(ctx, slog.LevelWarn, "hybrid search failed, searching words (user %s): %v", user.key[:8], err)
		ids, err = rawQuery(ctx, user.store, snapshot.Revision, query, nil, config.Index)
	}
	if err != nil {
		return nil, err
	}
	evidence, err := user.agent.Hydrate(ctx, snapshot.Revision, records.Scope{}, ids[:min(len(ids), topK, 200)])
	if err != nil {
		return nil, err
	}
	items := []SearchItem{}
	for _, item := range s.ordered(ctx, user, snapshot.Revision, evidence) {
		items = append(items, SearchItem{ID: item.ID, Content: item.Text, CreatedAt: item.Date})
	}
	return items, nil
}

// ordered lists the raw ranking's evidence as the View's order setting lists
// a read's records. When their write positions cannot be read it keeps the
// rank order and logs it: a degraded Search still answers.
func (s *Server) ordered(ctx context.Context, user *userEntry, revision string, evidence []memoryagent.Evidence) []memoryagent.Evidence {
	ordered, err := user.agent.OrderRecords(ctx, revision, evidence)
	if err != nil {
		s.logf(ctx, slog.LevelWarn, "degraded search kept rank order (user %s): %v", user.key[:8], err)
		return evidence
	}
	return ordered
}

func rawQuery(ctx context.Context, store *records.Store, revision, query string, embedding *records.EmbeddingConfig, index *records.IndexPolicy) ([]string, error) {
	text, _ := json.Marshal(query)
	input := map[string]json.RawMessage{"query": text, "match": json.RawMessage(`"words"`)}
	if embedding != nil {
		input["match"] = json.RawMessage(`"hybrid"`)
	}
	result, err := store.Query(ctx, records.QueryRequest{Revision: revision, Input: input, Embedding: embedding, Index: index, CandidateLimit: 200})
	return result.IDs, err
}

// decode reads a JSON body of at most maxBody bytes; on failure it has
// answered 413 or 400.
func (s *Server) decode(w http.ResponseWriter, r *http.Request, into any) bool {
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(into)
	if err == nil {
		return true
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("request body exceeds %d bytes", maxBody))
		return false
	}
	writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
	return false
}

// fail logs an internal error and answers 503 when retrying can help (a busy
// or changed store, shutdown, a canceled request), else 500.
func (s *Server) fail(ctx context.Context, w http.ResponseWriter, operation, user string, err error) {
	if len(user) > 8 {
		user = user[:8]
	}
	s.logf(ctx, slog.LevelError, "%s failed (user %s): %v", operation, user, err)
	if retryable(err) {
		writeError(w, http.StatusServiceUnavailable, operation+" is temporarily unavailable; retry")
		return
	}
	writeError(w, http.StatusInternalServerError, operation+" failed")
}

func retryable(err error) bool {
	message := err.Error()
	return errors.Is(err, records.ErrConflict) || errors.Is(err, errClosing) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
		strings.Contains(message, "database is locked") || strings.Contains(message, "SQLITE_BUSY") || strings.Contains(message, "too many open files")
}

// logged writes one line per request but health checks: method, path, status
// and duration, never bodies.
func (s *Server) logged(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" || r.URL.Path == "/" {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		recorder := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		s.logf(r.Context(), slog.LevelInfo, "%s %s %d %dms", r.Method, r.URL.Path, recorder.status, time.Since(start).Milliseconds())
	})
}

// statusWriter records a response's status and whether anything was
// written.
type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(status int) {
	w.status, w.wrote = status, true
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(data []byte) (int, error) {
	w.wrote = true
	return w.ResponseWriter.Write(data)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message})
}
