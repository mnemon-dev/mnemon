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
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf16"

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
	// ConsolidationBackground (timelines or layered) folds the full
	// batches in supervised workers that Run starts (scheduler.go): an Add
	// only writes and notifies them, then waits while its user's backlog is
	// above one job's window, and a Search first folds the rest, as in
	// on-add mode. A link JEV cannot make for now is deferred
	// (ConsolidateRequest.DeferLinks), never made without JEV.
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
	// ProviderKeysFile is the account's provider key file (byok.go): its
	// keys are sent in place of the host's, kind by kind, and the admin
	// listener's PUT and DELETE /admin/v1/provider-keys replace and revert
	// them. Absolute and outside the data root; "" for the host's keys alone
	// and no such routes. A missing file holds no key.
	ProviderKeysFile string
	// SettingsFile is the instance's settings file (budgets.go): its budget
	// caps replace the configuration's, kind by kind (null for unlimited),
	// and the admin listener's PUT and DELETE /admin/v1/budgets set and
	// revert them. Absolute and outside the data root; "" for the
	// configuration's caps alone and no such routes. A missing file holds no
	// cap.
	SettingsFile string
	// LogDir is the directory of the log files (logfiles.go): every line the
	// server logs, a file per hour, kept 30 days and 1 GiB at most, which GET
	// /admin/v1/logs/export streams. Absolute and outside the data root; ""
	// for none.
	LogDir string
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
	// keys are the providers' key source: the account's keys (the key file)
	// over the host's. keysWriter admits one change of the key file at a
	// time (writeKeys), and keyCheckTimeout bounds the check of one key
	// (memoryagent.KeyCheckTimeout; tests shorten it).
	keys            *memoryagent.ProviderKeys
	keysWriter      chan struct{}
	keyCheckTimeout time.Duration
	// configCaps are the configuration's budget caps (the budget's as it
	// opened) and settingsCaps the settings file's, which the budget
	// enforces over them; capsWriter admits one change of the file at a
	// time (writeCaps).
	configCaps   map[string]float64
	settingsCaps atomic.Pointer[map[string]float64]
	capsWriter   chan struct{}
	// configRead is the configuration's role reading, normalized, and
	// settingsRead the settings file's, nil when it sets none: the one the
	// agents' consolidation reads by (MemoryConfig.ReadOverride) while set.
	configRead   memoryagent.RoleReading
	settingsRead atomic.Pointer[memoryagent.RoleReading]
	// lines keeps the latest lines in memory (GET /admin/v1/logs and
	// /admin/v1/events), and logFiles every line on disk, a file per hour
	// (GET /admin/v1/logs/export); nil without a log directory.
	lines    *lineMemory
	logFiles *logFiles
	// stores scans the data root for the store overview and the store gauges.
	stores *storeScanner
	// freeDisk is the free bytes of the disk holding a directory, which
	// readiness measures.
	freeDisk func(dir string) (uint64, error)
	// The windows of an Add's consolidation, a Search's flush and its read.
	addWindow, flushWindow, searchTimeout time.Duration
	// heartbeatEvery spaces the ticks of Run's heartbeat, which
	// heartbeatTicker makes (its ticks and its stop; tests tick it
	// themselves); beat holds the last one's time (nil before the first).
	heartbeatEvery  time.Duration
	heartbeatTicker func(time.Duration) (<-chan time.Time, func())
	beat            atomic.Pointer[time.Time]
	handler, admin  http.Handler
	once            sync.Once
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
		return nil, fmt.Errorf("the admin token must hold at least %d bytes", MinAdminTokenLength)
	}
	mode, err := consolidationMode(config)
	if err != nil {
		return nil, err
	}
	config.Consolidation = mode
	if config.ProviderKeysFile != "" {
		if err := checkStateFile(keyFileWhat, config.ProviderKeysFile, config.DataRoot, "a snapshot of the data root must never hold a key"); err != nil {
			return nil, err
		}
		config.ProviderKeysFile = filepath.Clean(config.ProviderKeysFile)
	}
	if config.LogDir != "" {
		if err := checkStateFile("the log directory", config.LogDir, config.DataRoot, "the data root holds the users' stores"); err != nil {
			return nil, err
		}
		config.LogDir = filepath.Clean(config.LogDir)
	}
	if config.SettingsFile != "" {
		if err := checkStateFile(settingsWhat, config.SettingsFile, config.DataRoot, "it is the instance's, beside the key file, and the data root holds the users' stores"); err != nil {
			return nil, err
		}
		config.SettingsFile = filepath.Clean(config.SettingsFile)
	}
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
		addWindow: addWindow, flushWindow: flushWindow, searchTimeout: config.SearchTimeout, heartbeatEvery: heartbeatEvery, heartbeatTicker: newTicker,
		metrics: newMetrics(time.Now), started: time.Now().UTC(), freeDisk: freeDiskBytes,
		keys: memoryagent.NewProviderKeys(config.MemoryConfig), keysWriter: make(chan struct{}, 1), keyCheckTimeout: memoryagent.KeyCheckTimeout,
		capsWriter: make(chan struct{}, 1)}
	if s.log == nil {
		s.log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	s.lines = newLineMemory()
	handler := s.log.Handler()
	if config.LogDir != "" {
		if s.logFiles, err = openLogFiles(config.LogDir, time.Now); err != nil {
			return nil, fmt.Errorf("the log directory: %w", err)
		}
		handler = teeHandler{handler, slog.NewJSONHandler(s.logFiles, &slog.HandlerOptions{ReplaceAttr: utcTime})}
	}
	s.log = slog.New(contextHandler{linesHandler{handler, s.lines}})
	if config.AdminToken != "" {
		digest := sha256.Sum256([]byte(config.AdminToken))
		s.adminKey = &digest
	}
	s.stores = newStoreScanner(config.DataRoot, time.Now, s.scanLog)
	// The account's keys, before the models that send them: a start refuses
	// a key file that breaks a rule. aml-serve is the file's one writer, so
	// what a write a crash interrupted left beside it is removed first.
	if config.ProviderKeysFile != "" {
		if removed := removeInterrupted(config.ProviderKeysFile); len(removed) > 0 {
			s.logf(context.Background(), slog.LevelWarn, "removed what an interrupted write of %s left: %s", keyFileWhat, strings.Join(removed, ", "))
		}
	}
	if err := loadKeyFile(config.ProviderKeysFile, s.keys); err != nil {
		return nil, err
	}
	// The settings file's caps and role reading, refused at the start as the
	// key file is; the server is its one writer too.
	settings := map[string]float64{}
	var read *memoryagent.RoleReading
	if config.SettingsFile != "" {
		if removed := removeInterrupted(config.SettingsFile); len(removed) > 0 {
			s.logf(context.Background(), slog.LevelWarn, "removed what an interrupted write of %s left: %s", settingsWhat, strings.Join(removed, ", "))
		}
		if settings, read, err = readSettingsFile(config.SettingsFile, s.keys.Uses); err != nil {
			return nil, err
		}
	}
	s.settingsCaps.Store(&settings)
	s.settingsRead.Store(read)
	s.configRead = consolidation.RoleReading()
	if s.configRead, err = s.configRead.Normalized(); err != nil {
		return nil, fmt.Errorf("memory config: consolidation.read: %w", err)
	}
	// Every user's agent reads by the settings file's role reading while it
	// sets one.
	s.config.MemoryConfig.ReadOverride = s.settingsRead.Load
	if config.Models != nil {
		s.models, err = config.Models(config.MemoryConfig)
	} else {
		s.models, s.budget, s.cache, err = memoryagent.OpenModelsWith(config.MemoryConfig, s.keys)
	}
	if err != nil {
		return nil, err
	}
	// The budget enforces the settings file's caps over the configuration's.
	if s.budget != nil {
		s.configCaps = s.budget.Caps()
		if err := s.budget.SetCaps(settings); err != nil {
			s.closeModels(context.Background())
			return nil, err
		}
	}
	if s.maxItems, s.lagBound, err = s.probe(); err != nil {
		s.closeModels(context.Background())
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
	s.logFiles.start()
	return s, nil
}

// logf logs one line at level. The lines keep the wording they had before the
// server logged through slog, which the runbook's table of lines quotes; ctx
// is the context of the work the line is about. A line's source, with a
// logger that adds it, is logf's caller (the final review's parked T7-f).
func (s *Server) logf(ctx context.Context, level slog.Level, format string, args ...any) {
	s.logAt(ctx, 1, level, format, args...)
}

// logAt logs a line whose source is the caller of skip frames between it
// and logAt: 1 for the caller of logf, or of a logger of its own that calls
// logAt (schedulerLog).
func (s *Server) logAt(ctx context.Context, skip int, level slog.Level, format string, args ...any) {
	if !s.log.Enabled(ctx, level) {
		return
	}
	var pcs [1]uintptr
	runtime.Callers(2+skip, pcs[:]) // past runtime.Callers, logAt and those frames
	record := slog.NewRecord(time.Now(), level, fmt.Sprintf(format, args...), pcs[0])
	_ = s.log.Handler().Handle(ctx, record)
}

// schedulerLog is the scheduler's logf, on the context of the work a line is
// about (a request's carries its id). Its lines report faults (a full queue,
// a failed or panicked job, a pause), so they are warnings.
func (s *Server) schedulerLog(ctx context.Context, format string, args ...any) {
	s.logAt(ctx, 1, slog.LevelWarn, format, args...)
}

// scanLog is the store scanner's logf. A scan that fails leaves the overview
// and the store gauges out, which is a fault: a warning.
func (s *Server) scanLog(format string, args ...any) {
	s.logAt(context.Background(), 1, slog.LevelWarn, format, args...)
}

// storeLog is the logger of the store of the user with key: what the store's
// query runtime reports is a warning that names the user.
func (s *Server) storeLog(key string) func(format string, args ...any) {
	return func(format string, args ...any) {
		s.logAt(context.Background(), 1, slog.LevelWarn, format+" (user %s)", append(args[:len(args):len(args)], short(key))...)
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
		if !config.MemoryConfig.Consolidation.Timelines() {
			return "", errors.New(`background consolidation requires consolidation mode "timelines" or "layered"`)
		}
	default:
		return "", fmt.Errorf("unknown consolidation %q: off (\"\"), on-add or background", mode)
	}
	if config.ConsolidationWorkers < 0 {
		// 0 is the default (defaultConsolidationWorkers).
		return "", errors.New("consolidation workers must not be negative")
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

// Close is CloseWithin with no deadline: closing the models waits for their
// requests in flight, abandoned ones included, each until it ends or its hard
// timeout, so Close can take that long.
func (s *Server) Close() error { return s.CloseWithin(context.Background()) }

// CloseWithin stops a running Run and waits for it, then closes the user
// stores (held ones when their request ends) and the models, by the deadline
// of ctx, and the budget and model cache they share. Stop the HTTP server
// first. The models wait for their requests in flight, abandoned ones
// included, until the deadline, then cut those still running, which settle
// or release their reservations before the budget closes (ruling R90,
// memoryagent.Models.CloseWithin). It returns what the models' close cut,
// nil when it cut nothing; a later call does nothing and returns nil.
func (s *Server) CloseWithin(ctx context.Context) error {
	var err error
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
		err = s.closeModels(ctx)
		s.logFiles.close()
	})
	return err
}

// SettleHolds settles the reservations that a previous process of this
// instance left held in its budget file, the requests a crash or a stop past
// its windows cut, at the input estimates they recorded (ruling R90,
// memoryagent.Budget.SettleHolds), and logs what it settled by kind, once;
// with nothing held it says nothing. Call it once, holding the data root's
// lock, before Run and before serving: the instance's budget file then has
// no other writer, and none of its holds is of a request still in flight.
// Without a budget file it does nothing.
func (s *Server) SettleHolds(ctx context.Context) error {
	if s.budget == nil {
		return nil
	}
	settled, err := s.budget.SettleHolds(ctx)
	if err != nil {
		return fmt.Errorf("settling what the budget file holds: %w", err)
	}
	if line := settledLine(settled); line != "" {
		s.logf(ctx, slog.LevelWarn, "%s", line)
	}
	return nil
}

// settledLine is the line that says what SettleHolds settled, by kind, ""
// when nothing: how many reservations, what they held and what they settled
// at, in each account's currency.
func settledLine(settled []memoryagent.HeldSettled) string {
	holds, kinds := 0, []string{}
	for _, account := range settled {
		holds += account.Holds
		kinds = append(kinds, fmt.Sprintf("%s %d (held %.4f, settled %.4f)", account.Kind, account.Holds, account.Held, account.Settled))
	}
	if holds == 0 {
		return ""
	}
	// One hold reads in the singular: "1 budget reservation ... at its input
	// estimate".
	noun, pronoun, estimate := "reservations", "their", "estimates"
	if holds == 1 {
		noun, pronoun, estimate = "reservation", "its", "estimate"
	}
	return fmt.Sprintf("settled %d budget %s a previous process left held, at %s input %s: %s", holds, noun, pronoun, estimate, strings.Join(kinds, ", "))
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
	ticks, stop := s.heartbeatTicker(s.heartbeatEvery)
	defer stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			s.tick()
		}
	}
}

// newTicker is a ticker of period d: its ticks and its stop.
func newTicker(d time.Duration) (<-chan time.Time, func()) {
	ticker := time.NewTicker(d)
	return ticker.C, ticker.Stop
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
			s.sched.Notify(context.Background(), entry.Name(), 0)
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
		s.sched.Notify(ctx, key, 0)
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

// closeModels closes the models by the deadline of ctx, then the files they
// share, after their last call; it returns what the models' close cut.
func (s *Server) closeModels(ctx context.Context) error {
	err := s.models.CloseWithin(ctx)
	if s.budget != nil {
		s.budget.Close()
	}
	if s.cache != nil {
		s.cache.Close()
	}
	return err
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
		cap := fmt.Sprintf("%g", account.Cap)
		if account.Unlimited {
			cap = "unlimited"
		}
		parts = append(parts, fmt.Sprintf("%s spent %.4f held %.4f cap %s", account.Kind, account.Spent, account.Held, cap))
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

// RequestsInFlight counts the requests of the public handler being served
// now, those waiting for a slot included: what a shutdown still waits for.
func (s *Server) RequestsInFlight() int64 { return s.metrics.active.Load() }

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
// mode, consolidated (consolidateAdd). In background mode it hands what it
// wrote to the scheduler (tellScheduler, as part of the write) and then waits
// while the user's backlog is above the lag bound (awaitConsolidation); it
// calls no model. A retry of a stored request succeeds and finishes what the
// earlier attempt may not have. Only a missing request_id, user_id or
// session_id, or a request_id reused for other text, is a 400.
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
	nameUser(ctx, userKey(req.UserID))
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
	byRole := s.messageRoles(parts)
	nameRoles(ctx, byRole)
	if !s.writeParts(ctx, w, user, parts, deadline) {
		return
	}
	s.metrics.added(byRole)
	if s.sched != nil {
		// Stored and told to the scheduler: the wait needs the scheduler, not
		// the store.
		release()
		s.awaitConsolidation(ctx, user.key, deadline)
	}
	s.metrics.adds.Add(1)
	writeJSON(w, http.StatusOK, done)
}

// messageRoles counts the messages of an Add's parts by role, under the
// bucket names of the role reading consolidation reads by.
func (s *Server) messageRoles(parts []addPart) map[string]int {
	reading := s.configRead
	if read := s.settingsRead.Load(); read != nil {
		reading = *read
	}
	byRole := map[string]int{}
	for _, part := range parts {
		for _, message := range part.conversation.Messages {
			byRole[reading.Bucket(message.Role)]++
		}
	}
	return byRole
}

// writeParts writes an Add's parts under the user's write lock and embeds
// their chunks; in on-add mode it then consolidates under the lock, and in
// background mode it tells the scheduler what it wrote (tellScheduler), also
// under the lock. It returns false when it answered with an error.
func (s *Server) writeParts(ctx context.Context, w http.ResponseWriter, user *userEntry, parts []addPart, deadline time.Time) bool {
	user.waiting.Add(1)
	user.writes.Lock()
	user.waiting.Add(-1)
	defer user.writes.Unlock()
	chunks := []string{}
	for _, part := range parts {
		err := s.write(ctx, user, part)
		if errors.Is(err, errRequestReused) {
			writeError(w, http.StatusBadRequest, err.Error())
			return false
		}
		if err != nil {
			s.fail(ctx, w, "write", user.key, err)
			return false
		}
		chunks = append(chunks, part.chunks...)
	}
	if embedding := s.config.MemoryConfig.Embedding; embedding != nil {
		if err := embedChunks(ctx, user.store, *embedding, chunks); err != nil {
			s.logf(ctx, slog.LevelError, "embedding failed (user %s): %v", user.key[:8], err)
			writeError(w, http.StatusServiceUnavailable, "embedding is unavailable; retry the same request")
			return false
		}
	}
	if s.mode == ConsolidationOnAdd {
		s.consolidateAdd(ctx, user, deadline)
	}
	if s.sched != nil {
		// The characters of the chunks written, in UTF-16 units as a job's
		// window and the backlog's measure count them, about what
		// consolidation will read of them.
		written := int64(0)
		for _, chunk := range chunks {
			written += utf16Units(chunk)
		}
		s.tellScheduler(ctx, user, written)
	}
	return true
}

// tellScheduler hands the scheduler what an Add wrote for its user
// (background mode): the user's backlog measured, when the scheduler has not
// measured it yet (measureUnknown); the notification of the characters
// written; and the measure, recorded (scheduler.Measured), which replaces the
// estimate the notification has just grown. The caller holds the user's write
// lock throughout, so that no other Add of the user writes or notifies between
// the three, and the estimate is the measure plus what the Adds after it
// wrote. Between them, another Add's records could be in the measure with its
// notification after the record (counted twice), or its notification before
// the record with its records out of the measure (not counted at all): the
// estimate off by an Add (the re-review of the final fix's N6).
func (s *Server) tellScheduler(ctx context.Context, user *userEntry, written int64) {
	measure := s.measureUnknown(ctx, user)
	s.sched.Notify(ctx, user.key, written)
	if measure >= 0 {
		s.sched.Measured(user.key, measure)
	}
}

// utf16Units is the length of s in UTF-16 code units, as JavaScript counts
// it: a character outside the Basic Multilingual Plane is two.
func utf16Units(s string) int64 {
	n := int64(0)
	for _, r := range s {
		n += int64(max(utf16.RuneLen(r), 1))
	}
	return n
}

// measureUnknown is the user's backlog, measured now that its Add has
// written, when the scheduler has not measured the user since it started
// (nor has a job), so that the lag bound holds from this Add on rather than
// from the user's first job (the final review's parked T6-a): one measure per
// user and start (scheduler.HasMeasured), whichever Add of the user comes
// first, since the Adds of a user tell the scheduler one at a time
// (tellScheduler). It is -1 when the scheduler has measured the user, or when
// the measure fails, which is logged.
func (s *Server) measureUnknown(ctx context.Context, user *userEntry) int64 {
	if s.sched.HasMeasured(user.key) {
		return -1
	}
	backlog, err := user.agent.Backlog(ctx, records.Scope{})
	if err != nil {
		s.logf(ctx, slog.LevelWarn, "consolidation backlog unreadable (user %s): %v", short(user.key), err)
		return -1
	}
	return backlog.Characters
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
	started := time.Now()
	outcome := s.consolidate(ctx, user, false, "flush", nil)
	if outcome.folded > 0 {
		s.metrics.flushSeconds.Observe(time.Since(started).Seconds())
	}
	if outcome.unfinished && s.sched != nil {
		s.sched.Notify(ctx, user.key, 0)
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
// (DeferLinks).
func (s *Server) consolidate(ctx context.Context, user *userEntry, hold bool, label string, stop func() bool) (outcome foldOutcome) {
	for call := 1; call <= maxConsolidateCalls; call++ {
		if stop != nil && stop() {
			outcome.unfinished = true
			break
		}
		result, err := s.consolidateOnce(ctx, user, memoryagent.ConsolidateRequest{Hold: hold, DeferLinks: s.mode == ConsolidationBackground})
		outcome.folded += result.Folded
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
	ctx := r.Context()
	nameUser(ctx, userKey(req.UserID))
	if strings.TrimSpace(req.Query) == "" {
		s.answer(w, []SearchItem{})
		return
	}
	topK := s.topK(req.TopK)
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
		if view.NoEvidence {
			s.metrics.noEvidence.Inc()
			nameNoEvidence(ctx)
		}
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

// answer writes a Search's items, scored by position (scoreByPosition), and
// counts the Search.
func (s *Server) answer(w http.ResponseWriter, items []SearchItem) {
	scoreByPosition(items)
	s.metrics.searchAnswered()
	s.metrics.delivered(items)
	writeJSON(w, http.StatusOK, SearchResponse{Data: items})
}

// scoreByPosition scores a Search's items by position, 1/(position+1), in
// the order delivered.
func scoreByPosition(items []SearchItem) {
	for i := range items {
		items[i].Score = 1 / float64(i+1)
	}
}

// rawSearch is the raw ranking (rawRanking) of the user's store, what a
// degraded Search answers; what it falls back from is logged.
func (s *Server) rawSearch(ctx context.Context, user *userEntry, query string, topK int) ([]SearchItem, error) {
	ranking, err := rawRanking(ctx, user.store, user.agent, s.config.MemoryConfig, query, topK, s.rawWarn(ctx, user))
	return ranking.items, err
}

// rawWarn logs, as a warning that names the user, what the raw ranking of
// the user's store fell back from.
func (s *Server) rawWarn(ctx context.Context, user *userEntry) func(event string, err error) {
	return func(event string, err error) {
		s.logf(ctx, slog.LevelWarn, "%s (user %s): %v", event, user.key[:8], err)
	}
}

// ordered lists the raw ranking's evidence as the View's order setting lists
// a read's records (orderedEvidence), logging a failure to order.
func (s *Server) ordered(ctx context.Context, user *userEntry, revision string, evidence []memoryagent.Evidence) []memoryagent.Evidence {
	return orderedEvidence(ctx, user.agent, revision, evidence, s.rawWarn(ctx, user))
}

// rawRanked is a raw ranking: its items (unscored), the ids it hydrated in
// rank order, how many records the store ranked (at most 200), and the
// revision it read.
type rawRanked struct {
	items    []SearchItem
	hydrated []string
	ranked   int
	revision string
}

// rawRanking is the store's own ranking of the query, hybrid when the
// configuration embeds (words when the embedder fails), hydrated as reads
// hydrate evidence, cut at top_k by rank and listed as the View's order
// setting lists a read's records (time order with view.order "time"; on a
// failure to order, rank order). It uses no model. warn is told what it
// falls back from: the event and its error. aml-serve's degraded Search and
// the inspect service's lexical mode both answer it.
func rawRanking(ctx context.Context, store *records.Store, agent *memoryagent.Agent, config memoryagent.Config, query string, topK int, warn func(event string, err error)) (rawRanked, error) {
	snapshot, err := store.Get(ctx, records.GetRequest{IDs: []string{}})
	if err != nil {
		return rawRanked{}, err
	}
	ids, err := rawQuery(ctx, store, snapshot.Revision, query, config.Embedding, config.Index)
	if err != nil && config.Embedding != nil {
		warn("hybrid search failed, searching words", err)
		ids, err = rawQuery(ctx, store, snapshot.Revision, query, nil, config.Index)
	}
	if err != nil {
		return rawRanked{}, err
	}
	evidence, err := agent.Hydrate(ctx, snapshot.Revision, records.Scope{}, ids[:min(len(ids), topK, 200)])
	if err != nil {
		return rawRanked{}, err
	}
	ranking := rawRanked{items: []SearchItem{}, hydrated: []string{}, ranked: min(len(ids), 200), revision: snapshot.Revision}
	for _, item := range evidence {
		ranking.hydrated = append(ranking.hydrated, item.ID)
	}
	for _, item := range orderedEvidence(ctx, agent, snapshot.Revision, evidence, warn) {
		ranking.items = append(ranking.items, SearchItem{ID: item.ID, Content: item.Text, CreatedAt: item.Date})
	}
	return ranking, nil
}

// orderedEvidence lists the raw ranking's evidence as the View's order
// setting lists a read's records. When their write positions cannot be read
// it keeps the rank order and tells warn: a degraded Search still answers.
func orderedEvidence(ctx context.Context, agent *memoryagent.Agent, revision string, evidence []memoryagent.Evidence, warn func(event string, err error)) []memoryagent.Evidence {
	ordered, err := agent.OrderRecords(ctx, revision, evidence)
	if err != nil {
		warn("degraded search kept rank order", err)
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
// or changed store, shutdown, a canceled request), else 500. One a retry can
// pass is expected now and then, a warning; any other an error (the final
// review's parked T7-e).
func (s *Server) fail(ctx context.Context, w http.ResponseWriter, operation, user string, err error) {
	if len(user) > 8 {
		user = user[:8]
	}
	level := slog.LevelError
	if retryable(err) {
		level = slog.LevelWarn
	}
	s.logf(ctx, level, "%s failed (user %s): %v", operation, user, err)
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
// and duration, never bodies; with its id, and the user's prefix once the
// request named a user (contextHandler).
func (s *Server) logged(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" || r.URL.Path == "/" {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		recorder := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		s.logRequest(r.Context(), r.Method, r.URL.Path, recorder.status, time.Since(start))
	})
}

// logRequest logs a request's line as logf would, its status and
// milliseconds as attributes too: GET /admin/v1/logs filters by them, and the
// log files keep them.
func (s *Server) logRequest(ctx context.Context, method, path string, status int, took time.Duration) {
	if !s.log.Enabled(ctx, slog.LevelInfo) {
		return
	}
	var pcs [1]uintptr
	runtime.Callers(2, pcs[:]) // past runtime.Callers and logRequest
	ms := took.Milliseconds()
	record := slog.NewRecord(time.Now(), slog.LevelInfo, fmt.Sprintf("%s %s %d %dms", method, path, status, ms), pcs[0])
	record.AddAttrs(slog.Int("status", status), slog.Int64("ms", ms))
	_ = s.log.Handler().Handle(ctx, record)
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
