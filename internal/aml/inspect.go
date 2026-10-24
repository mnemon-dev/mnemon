package aml

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
	"github.com/mnemon-dev/mnemon/internal/records"
)

// The inspect service (aml-inspect) is the read-only backend of the memory
// cloud console's retrieval tab: a process of its own beside aml-serve, that
// reads aml-serve's data root and runs retrieval on it without side effects
// (docs/inspect-api.md). It opens every store as a records reader
// (records.OpenReader), so it writes, creates and checkpoints nothing; it
// never folds; its full searches bill a debug budget of their own; it has no
// metrics; and its logs name a store by its 8-digit prefix only.

const (
	// inspectBody bounds a request body.
	inspectBody = 64 << 10
	// The pages and the limits of docs/inspect-api.md.
	recordsPageDefault, recordsPageMax = 50, 200
	memoryPageDefault, memoryPageMax   = 50, 200
	inspectTopKMax                     = 100
	inspectQueryMax                    = 12000
	pendingIDsMax                      = 100
	excerptMax                         = 200
	defaultInspectConcurrent           = 2
	// fullSearchTimeout bounds a full search's read, as aml-serve's default
	// --search-timeout bounds a Search's.
	fullSearchTimeout = 10 * time.Minute
	// busyRetry and storeBusyRetry are the Retry-After of the two 503s.
	busyRetry, storeBusyRetry = "2", "1"
)

// paidKinds are the provider kinds a budget accounts, with their currencies
// (memoryagent.PaidKinds).
var paidKinds = memoryagent.PaidKinds()

// PaidKindNames names the kinds a debug cap is for, in order: "deepseek,
// jev and openai".
func PaidKindNames() string {
	kinds := slices.Sorted(maps.Keys(paidKinds))
	if len(kinds) < 2 {
		return strings.Join(kinds, "")
	}
	return strings.Join(kinds[:len(kinds)-1], ", ") + " and " + kinds[len(kinds)-1]
}

// InspectConfig configures an Inspector.
type InspectConfig struct {
	// DataRoot is aml-serve's data root, which holds users/: read only.
	DataRoot string
	// MemoryConfig is the instance's memory configuration: the read's
	// settings and models. Its budget file is never opened, nor its model
	// cache.
	MemoryConfig memoryagent.Config
	// Token, when not empty, is the bearer token every request must carry
	// (MinAdminTokenLength bytes at least). It is never logged.
	Token string
	// DebugBudgetFile is the budget file full searches reserve and settle in,
	// "" for none; it must be absolute, outside the data root, and another
	// file than MemoryConfig.BudgetFile.
	DebugBudgetFile string
	// DebugCaps are the debug budget's caps by provider kind; a kind it does
	// not list has a cap of 0. Full searches run while every paid kind of
	// the planner and the judge has a positive cap.
	DebugCaps map[string]float64
	// MaxConcurrent bounds the requests that read stores at once (default
	// 2); past it a request answers 503 busy at once.
	MaxConcurrent int
	// Logger receives one line per request (method, route, status, time and
	// the store's prefix) and the failures; nil logs text to standard error.
	Logger *slog.Logger
	// Version is the product version /v1/info reports.
	Version string
	// Models builds the full searches' models from the configuration it is
	// given (the instance's, with the debug budget); nil builds its
	// providers. Tests inject scripted models.
	Models func(memoryagent.Config) (memoryagent.Models, error)
}

// Inspector is the inspect service. Its methods are safe for concurrent use.
type Inspector struct {
	config   InspectConfig
	tokenKey *[sha256.Size]byte
	models   memoryagent.Models
	// budget is the debug budget; nil without a debug budget file.
	budget *memoryagent.Budget
	// caps are the debug budget's caps of every paid kind (0 for unset).
	caps map[string]float64
	// full says full searches may run.
	full bool
	// maxItems is the View's record bound, the default top_k.
	maxItems int
	// slots admits the requests that read stores (MaxConcurrent).
	slots chan struct{}
	// open counts the stores open now.
	open    atomic.Int64
	log     *slog.Logger
	handler http.Handler
	// baseCtx is the context of the requests HTTPServer's servers run;
	// CancelRequests ends it.
	baseCtx        context.Context
	cancelRequests context.CancelFunc

	mu           sync.Mutex
	fullSearches int                // guarded by mu
	fullCost     map[string]float64 // guarded by mu
	closeOnce    sync.Once

	// rows keeps the counted rows of GET /v1/stores, by store key (keptRow);
	// guarded by rowsMu.
	rowsMu sync.Mutex
	rows   map[string]keptStoreRow
}

// NewInspector checks the configuration, opens the debug budget, builds the
// full searches' models and checks that an agent can be made from them.
func NewInspector(config InspectConfig) (*Inspector, error) {
	if err := checkInspectConfig(&config); err != nil {
		return nil, err
	}
	i := &Inspector{config: config, log: config.Logger, slots: make(chan struct{}, config.MaxConcurrent), fullCost: map[string]float64{}}
	if i.log == nil {
		i.log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	if config.Token != "" {
		digest := sha256.Sum256([]byte(config.Token))
		i.tokenKey = &digest
	}
	i.caps = map[string]float64{}
	for kind := range paidKinds {
		i.caps[kind] = config.DebugCaps[kind]
	}
	// Full searches need the debug budget, a cap, and a cap for every paid
	// kind the read calls.
	i.full = config.DebugBudgetFile != "" && slices.ContainsFunc(slices.Collect(maps.Values(i.caps)), func(cap float64) bool { return cap > 0 })
	for _, kind := range i.readKinds() {
		i.full = i.full && i.caps[kind] > 0
	}
	if err := i.openModels(); err != nil {
		return nil, err
	}
	var err error
	if i.maxItems, err = i.probe(); err != nil {
		i.closeModels(context.Background())
		return nil, err
	}
	i.baseCtx, i.cancelRequests = context.WithCancel(context.Background())
	i.handler = i.routes()
	return i, nil
}

// checkInspectConfig checks the configuration and fills its defaults.
func checkInspectConfig(config *InspectConfig) error {
	switch {
	case !filepath.IsAbs(config.DataRoot):
		return errors.New("the inspect service requires an absolute data root")
	case config.Token != "" && len(config.Token) < MinAdminTokenLength:
		return fmt.Errorf("the inspect token must hold at least %d bytes", MinAdminTokenLength)
	case config.MaxConcurrent < 0:
		return errors.New("max concurrent must not be negative")
	case config.MemoryConfig.Strategy != "simple":
		return fmt.Errorf("the inspect service traces the simple strategy's read path, not strategy %q", config.MemoryConfig.Strategy)
	case config.MemoryConfig.Embedding != nil:
		// Lexical mode calls no model, and the memory cloud ships no embedder.
		return errors.New("the inspect service reads configurations without an embedder (BM25 only)")
	case config.MemoryConfig.Index.Enabled():
		// A trace's scores are the words (BM25) scores of a search's records.
		return errors.New("the inspect service reads configurations without an index policy: a trace's scores are BM25 scores, and an index policy's ranking fuses other routes, so they would not be the scores it sorted by")
	}
	if !dataRoot(config.DataRoot) {
		return fmt.Errorf("%s is not an aml-serve data root: it has neither a users/ directory nor aml-serve's %s", config.DataRoot, dataRootLock)
	}
	if config.MaxConcurrent == 0 {
		config.MaxConcurrent = defaultInspectConcurrent
	}
	for kind, cap := range config.DebugCaps {
		if _, ok := paidKinds[kind]; !ok {
			return fmt.Errorf("debug cap kind %q: the kinds are %s", kind, PaidKindNames())
		}
		if math.IsNaN(cap) || math.IsInf(cap, 0) || cap < 0 {
			return fmt.Errorf("the debug cap of %s must be a finite amount of at least 0", kind)
		}
	}
	if config.DebugBudgetFile == "" {
		if len(config.DebugCaps) > 0 {
			return errors.New("debug caps need a debug budget file")
		}
		return nil
	}
	// The service opens the file by the name it checks.
	config.DebugBudgetFile = filepath.Clean(config.DebugBudgetFile)
	return checkDebugBudget(config.DebugBudgetFile, config.MemoryConfig.BudgetFile, config.DataRoot)
}

// dataRootLock is the file aml-serve locks its data root with
// (cmd/memory's lockDataRoot), which it makes before any user's first Add
// makes users/.
const dataRootLock = ".aml-serve.lock"

// dataRoot reports whether dir looks like an aml-serve data root: it holds
// users/, or aml-serve's lock when no user has added anything yet.
func dataRoot(dir string) bool {
	if info, err := os.Stat(filepath.Join(dir, "users")); err == nil && info.IsDir() {
		return true
	}
	info, err := os.Stat(filepath.Join(dir, dataRootLock))
	return err == nil && info.Mode().IsRegular()
}

// checkDebugBudget refuses a debug budget file that is relative, inside the
// data root, or the instance's budget file, however a path spells them. The
// paths are compared by the names the file system resolves them to
// (canonicalPath), so that neither "." and ".." nor a symbolic link passes
// for another file, even one not made yet; and two files that exist by their
// identity, so that a hard link does not either. debug is clean.
func checkDebugBudget(debug, instance, dataRoot string) error {
	if !filepath.IsAbs(debug) {
		return errors.New("the debug budget file must be an absolute path")
	}
	debugName, err := canonicalPath(debug)
	if err != nil {
		return fmt.Errorf("the debug budget file: %w", err)
	}
	root, err := canonicalPath(filepath.Clean(dataRoot))
	if err != nil {
		return fmt.Errorf("the data root: %w", err)
	}
	if rel, err := filepath.Rel(root, debugName); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("the debug budget file must be outside the data root, which the service only reads")
	}
	if instance == "" {
		return nil
	}
	instanceName, err := canonicalPath(filepath.Clean(instance))
	if err != nil {
		return fmt.Errorf("the configuration's budgetFile: %w", err)
	}
	if debugName == instanceName {
		return errors.New("the debug budget file must differ from the configuration's budgetFile: full searches never spend the instance's budget")
	}
	a, errA := os.Stat(debug)
	b, errB := os.Stat(instance)
	if errA == nil && errB == nil && os.SameFile(a, b) {
		return errors.New("the debug budget file is the configuration's budgetFile under another name: full searches never spend the instance's budget")
	}
	return nil
}

// canonicalPath is the name the file system resolves the clean path to: its
// nearest ancestor that exists (path itself, when it does) with every
// symbolic link resolved (filepath.EvalSymlinks), joined with the names
// below it, which do not exist yet. Two paths of the same canonical name
// name one file, or will once it is made. A symbolic link that leads
// nowhere is left as a name: the budget refuses to open one anyway.
func canonicalPath(path string) (string, error) {
	rest := ""
	for dir := path; ; {
		resolved, err := filepath.EvalSymlinks(dir)
		if err == nil {
			return filepath.Join(resolved, rest), nil
		}
		parent := filepath.Dir(dir)
		if !errors.Is(err, fs.ErrNotExist) || parent == dir {
			return "", err
		}
		rest, dir = filepath.Join(filepath.Base(dir), rest), parent
	}
}

// readKinds are the paid kinds of the read's models: the planner's and the
// judge's.
func (i *Inspector) readKinds() []string {
	kinds := []string{}
	for _, model := range []*memoryagent.ModelConfig{i.config.MemoryConfig.Planner, i.config.MemoryConfig.Judge} {
		if model != nil && paidKinds[model.Kind] != "" && !slices.Contains(kinds, model.Kind) {
			kinds = append(kinds, model.Kind)
		}
	}
	return kinds
}

// openModels opens the debug budget and builds the models full searches
// call, billed to it; without full mode the models refuse every call
// (lexical searches make none).
func (i *Inspector) openModels() error {
	debug := i.config.MemoryConfig
	debug.BudgetFile, debug.Budgets, debug.ModelCache, debug.Consolidator = i.config.DebugBudgetFile, i.caps, "", nil
	if i.full && i.config.Models == nil {
		models, budget, _, err := memoryagent.OpenModels(debug)
		if err != nil {
			return err
		}
		i.models, i.budget = models, budget
		return nil
	}
	if i.config.DebugBudgetFile != "" {
		budget, err := memoryagent.OpenBudgetWithCaps(i.config.DebugBudgetFile, i.caps)
		if err != nil {
			return err
		}
		i.budget = budget
	}
	i.models = memoryagent.Models{Planner: refusingModel{}, Judge: refusingModel{}}
	if i.full {
		models, err := i.config.Models(debug)
		if err != nil {
			i.closeModels(context.Background())
			return err
		}
		i.models = models
	}
	return nil
}

// errFullDisabled is what the models of a service without full mode answer;
// no search reaches them.
var errFullDisabled = errors.New("full searches are disabled: no debug caps")

// refusingModel is the planner and the judge of a service without full mode.
type refusingModel struct{}

func (refusingModel) Complete(context.Context, string, string) (string, memoryagent.Usage, error) {
	return "", memoryagent.Usage{}, errFullDisabled
}

func (refusingModel) Decide(context.Context, any, map[string]memoryagent.Question) (map[string]float64, memoryagent.Usage, error) {
	return nil, memoryagent.Usage{}, errFullDisabled
}

// probe makes an agent over an empty scratch store, as aml-serve does, so a
// configuration the agents refuse fails at the start, and reads the View's
// record bound.
func (i *Inspector) probe() (int, error) {
	dir, err := os.MkdirTemp("", "aml-inspect-probe-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(dir)
	store, err := records.Open(dir, false)
	if err != nil {
		return 0, err
	}
	defer store.Close()
	agent, err := memoryagent.New(store, i.config.MemoryConfig, i.models, nil)
	if err != nil {
		return 0, err
	}
	defer agent.Close()
	return agent.MaxItems(), nil
}

// closeModels closes the models by the deadline of ctx, after their last
// call, then the budget; it returns what the models' close cut.
func (i *Inspector) closeModels(ctx context.Context) error {
	err := i.models.CloseWithin(ctx)
	if i.budget != nil {
		i.budget.Close()
	}
	return err
}

// Close is CloseWithin with no deadline: closing the models waits for their
// requests in flight, abandoned ones included, each until it ends or its
// hard timeout.
func (i *Inspector) Close() error { return i.CloseWithin(context.Background()) }

// CloseWithin cancels the requests still served, then closes the models by
// the deadline of ctx and the debug budget. Stop the HTTP server first. The
// models wait for their requests in flight, abandoned ones included, until
// the deadline, then cut those still running, which settle or release their
// reservations in the debug budget before it closes (ruling R90). It returns
// what the models' close cut, nil when it cut nothing; a later call does
// nothing and returns nil.
func (i *Inspector) CloseWithin(ctx context.Context) error {
	var err error
	i.closeOnce.Do(func() {
		i.cancelRequests()
		err = i.closeModels(ctx)
	})
	return err
}

// SettleHolds settles the reservations that a previous run of the service
// left held in its debug budget, at the input estimates they recorded
// (ruling R90), and logs what it settled by kind, once; with nothing held it
// says nothing. Call it once, holding the debug budget's lock (aml-inspect
// takes it), before serving. Without a debug budget it does nothing.
func (i *Inspector) SettleHolds(ctx context.Context) error {
	if i.budget == nil {
		return nil
	}
	settled, err := i.budget.SettleHolds(ctx)
	if err != nil {
		return fmt.Errorf("settling what the debug budget holds: %w", err)
	}
	if line := settledLine(settled); line != "" {
		i.log.Warn(line)
	}
	return nil
}

// Handler serves the routes of docs/inspect-api.md.
func (i *Inspector) Handler() http.Handler { return i.handler }

// HTTPServer is the HTTP server for the service: its write timeout outlasts
// a full search, and its requests run on a context CancelRequests ends.
func (i *Inspector) HTTPServer() *http.Server {
	return &http.Server{
		Handler:           i.handler,
		BaseContext:       func(net.Listener) context.Context { return i.baseCtx },
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       time.Minute,
		WriteTimeout:      fullSearchTimeout + time.Minute,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(i.log.Handler(), slog.LevelError),
	}
}

// CancelRequests ends the context of every request being served through
// HTTPServer's servers, as a forced shutdown does: their model calls stop,
// and their answers are 503 busy.
func (i *Inspector) CancelRequests() { i.cancelRequests() }

func (i *Inspector) routes() http.Handler {
	mux := http.NewServeMux()
	route := func(pattern, method string, gated bool, handle http.HandlerFunc) {
		if gated {
			handle = i.gated(handle)
		}
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != method {
				w.Header().Set("Allow", method)
				inspectError(w, http.StatusMethodNotAllowed, "method_not_allowed")
				return
			}
			handle(w, r)
		})
	}
	route("/healthz", http.MethodGet, false, func(w http.ResponseWriter, _ *http.Request) {
		inspectJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	route("/v1/info", http.MethodGet, false, i.serveInfo)
	route("/v1/stores", http.MethodGet, true, i.serveStores)
	route("/v1/resolve", http.MethodPost, true, i.serveResolve)
	route("/v1/stores/{key}/records", http.MethodGet, true, i.serveRecords)
	route("/v1/stores/{key}/memory", http.MethodGet, true, i.serveMemory)
	// The lookups by id; {$} takes an empty id, which is refused.
	route("/v1/stores/{key}/records/{id}", http.MethodGet, true, i.serveRecord)
	route("/v1/stores/{key}/records/{$}", http.MethodGet, true, i.serveRecord)
	route("/v1/stores/{key}/memory/{section}/{id}", http.MethodGet, true, i.serveMemoryItem)
	route("/v1/stores/{key}/memory/{section}/{$}", http.MethodGet, true, i.serveMemoryItem)
	route("/v1/search", http.MethodPost, true, i.serveSearch)
	notFound := func(w http.ResponseWriter, _ *http.Request) { inspectError(w, http.StatusNotFound, "not_found") }
	// Not a route: without it, ServeMux would redirect it to its {$}.
	mux.HandleFunc("/v1/stores/{key}/memory/{section}", notFound)
	mux.HandleFunc("/", notFound)
	refuse := func(w http.ResponseWriter) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="mnemon-inspect"`)
		inspectError(w, http.StatusUnauthorized, "unauthorized")
	}
	return i.logged(i.recovered(requireBearer(i.tokenKey, refuse, mux)))
}

// gated admits a request that reads stores into one of the MaxConcurrent
// slots, or answers 503 busy at once.
func (i *Inspector) gated(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		select {
		case i.slots <- struct{}{}:
			defer func() { <-i.slots }()
			next(w, r)
		default:
			w.Header().Set("Retry-After", busyRetry)
			inspectError(w, http.StatusServiceUnavailable, "busy")
		}
	}
}

// inspectLine is what a request's log line names besides its route: the
// store's prefix, set by the handler that reads a store.
type inspectLine struct{ store string }

type inspectLineKey struct{}

// noteStore names the store a request reads in its log line, by its prefix.
func noteStore(r *http.Request, key string) {
	if line, ok := r.Context().Value(inspectLineKey{}).(*inspectLine); ok {
		line.store = short(key)
	}
}

// logged writes one line per request but /healthz: method, route (the
// pattern, never the path, which may hold a key), status, time and the
// store's prefix; never a body, a header or a query.
func (i *Inspector) logged(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		line := &inspectLine{}
		r = r.WithContext(context.WithValue(r.Context(), inspectLineKey{}, line))
		start := time.Now()
		recorder := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		if r.URL.Path == "/healthz" && recorder.status == http.StatusOK {
			return
		}
		route := r.Pattern
		if route == "" || route == "/" {
			route = "-"
		}
		message := fmt.Sprintf("%s %s %d %dms", r.Method, route, recorder.status, time.Since(start).Milliseconds())
		if line.store != "" {
			message += " (store " + line.store + ")"
		}
		i.log.Info(message)
	})
}

// recovered answers 500 internal when a handler panics before it answered,
// and logs the panic with its stack, never the request.
func (i *Inspector) recovered(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writer := &statusWriter{ResponseWriter: w}
		defer func() {
			value := recover()
			if value == nil {
				return
			}
			if value == http.ErrAbortHandler {
				panic(value)
			}
			i.log.Error(fmt.Sprintf("inspect request panicked: %v\n%s", value, debug.Stack()))
			if !writer.wrote {
				inspectError(writer, http.StatusInternalServerError, "internal")
			}
		}()
		next.ServeHTTP(writer, r)
	})
}

// inspectJSON writes an answer with the headers of every inspect answer.
func inspectJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, value)
}

// inspectError writes {"error":code}.
func inspectError(w http.ResponseWriter, status int, code string) {
	inspectJSON(w, status, map[string]string{"error": code})
}

// invalid writes 400 {"error":"invalid","field":field}.
func invalid(w http.ResponseWriter, field string) {
	inspectJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid", "field": field})
}

// decodeBody reads a JSON object of at most inspectBody bytes into fields,
// then the body to its end: nothing may follow the object but white space.
// net/http watches a request's connection only once its body has reached
// its end; from then on a client that leaves cancels the request's context,
// and a full search's model calls with it. On failure it has answered 413
// too_large or 400 invalid (body).
func decodeBody(w http.ResponseWriter, r *http.Request, fields *map[string]json.RawMessage) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, inspectBody))
	err := decoder.Decode(fields)
	if err == nil {
		err = readEnd(decoder)
	}
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		inspectError(w, http.StatusRequestEntityTooLarge, "too_large")
	case err != nil || *fields == nil:
		invalid(w, "body")
	default:
		return true
	}
	return false
}

// readEnd reads what follows a decoded JSON value to the end of its input:
// nil when that is white space alone.
func readEnd(decoder *json.Decoder) error {
	switch _, err := decoder.Token(); {
	case errors.Is(err, io.EOF):
		return nil
	case err == nil:
		return errors.New("data follows the JSON object")
	default:
		return err
	}
}

// usageByKind is an amount of each paid kind the console shows: deepseek and
// jev.
type usageByKind struct {
	DeepSeek float64 `json:"deepseek"`
	JEV      float64 `json:"jev"`
}

// inspectInfo is GET /v1/info.
type inspectInfo struct {
	Version     string          `json:"version"`
	Modes       map[string]bool `json:"modes"`
	DebugBudget struct {
		Currency  map[string]string `json:"currency"`
		Caps      usageByKind       `json:"caps"`
		Spent     usageByKind       `json:"spent"`
		Held      usageByKind       `json:"held"`
		Remaining usageByKind       `json:"remaining"`
	} `json:"debugBudget"`
	Recent struct {
		FullSearches int         `json:"fullSearches"`
		AvgCost      usageByKind `json:"avgCost"`
	} `json:"recent"`
	Limits map[string]int `json:"limits"`
}

func (i *Inspector) serveInfo(w http.ResponseWriter, r *http.Request) {
	var info inspectInfo
	info.Version, info.Modes = i.config.Version, map[string]bool{"lexical": true, "full": i.full}
	info.DebugBudget.Currency = map[string]string{"deepseek": paidKinds["deepseek"], "jev": paidKinds["jev"]}
	info.DebugBudget.Caps = usageByKind{i.caps["deepseek"], i.caps["jev"]}
	spent, held, err := i.debugAccounts(r.Context())
	if err != nil {
		i.log.Warn(fmt.Sprintf("the debug budget does not answer: %v", err))
		inspectError(w, http.StatusInternalServerError, "internal")
		return
	}
	info.DebugBudget.Spent = usageByKind{spent["deepseek"], spent["jev"]}
	info.DebugBudget.Held = usageByKind{held["deepseek"], held["jev"]}
	info.DebugBudget.Remaining = usageByKind{i.remaining("deepseek", spent, held), i.remaining("jev", spent, held)}
	i.mu.Lock()
	info.Recent.FullSearches = i.fullSearches
	if i.fullSearches > 0 {
		n := float64(i.fullSearches)
		info.Recent.AvgCost = usageByKind{i.fullCost["deepseek"] / n, i.fullCost["jev"] / n}
	}
	i.mu.Unlock()
	info.Limits = map[string]int{
		"maxConcurrent": i.config.MaxConcurrent, "topKMax": inspectTopKMax, "topKDefault": i.topKDefault(), "queryMaxBytes": inspectQueryMax,
		"bodyMaxBytes": inspectBody, "storesPageMax": maxStoresPage, "storesPageDefault": defaultStoresPage, "recordsPageMax": recordsPageMax,
		"recordsPageDefault": recordsPageDefault, "memoryPageMax": memoryPageMax, "memoryPageDefault": memoryPageDefault,
		"pendingIdsMax": pendingIDsMax, "excerptMax": excerptMax, "fullSearchSeconds": int(fullSearchTimeout / time.Second),
	}
	inspectJSON(w, http.StatusOK, info)
}

// debugAccounts are the debug budget's spent and held amounts by kind; none
// without a debug budget file.
func (i *Inspector) debugAccounts(ctx context.Context) (spent, held map[string]float64, err error) {
	spent, held = map[string]float64{}, map[string]float64{}
	if i.budget == nil {
		return spent, held, nil
	}
	ctx, cancel := context.WithTimeout(ctx, adminReadWait)
	defer cancel()
	accounts, err := i.budget.Accounts(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, account := range accounts {
		spent[account.Kind], held[account.Kind] = account.Spent, account.Held
	}
	return spent, held, nil
}

// remaining is a kind's cap less what is spent and held, at least 0.
func (i *Inspector) remaining(kind string, spent, held map[string]float64) float64 {
	return max(0, i.caps[kind]-spent[kind]-held[kind])
}

// topKDefault is a search's top_k when it gives none: /search's, the View's
// record bound, within the service's.
func (i *Inspector) topKDefault() int {
	if i.maxItems > 0 {
		return min(i.maxItems, inspectTopKMax)
	}
	return inspectTopKMax
}

// serveResolve answers POST /v1/resolve {user_id}: the user's store key and
// whether the data root holds it. It logs no store.
func (i *Inspector) serveResolve(w http.ResponseWriter, r *http.Request) {
	var fields map[string]json.RawMessage
	if !decodeBody(w, r, &fields) {
		return
	}
	user := scalarText(fields["user_id"])
	if strings.TrimSpace(user) == "" {
		invalid(w, "user_id")
		return
	}
	key := userKey(user)
	_, err := os.Stat(filepath.Join(i.storeDir(key), "records.db"))
	inspectJSON(w, http.StatusOK, map[string]any{"key": key, "store": short(key), "exists": err == nil})
}

// storeDir is the directory of the store with key.
func (i *Inspector) storeDir(key string) string {
	return filepath.Join(i.config.DataRoot, "users", key)
}
