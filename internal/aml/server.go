package aml

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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
)

// ServerConfig configures a Server.
type ServerConfig struct {
	// DataRoot is the absolute directory holding the per-user stores.
	DataRoot string
	// MemoryConfig is the memory strategy every user's agent runs.
	MemoryConfig memoryagent.Config
	// AuthKey is the API key every Add and Search must carry.
	AuthKey string
	// ConsolidateOnAdd consolidates during requests: in timelines mode an
	// Add folds the full batches before it returns and a Search first folds
	// the rest (the conversation's tail); otherwise an Add folds until
	// caught up. Failures are logged, not returned.
	ConsolidateOnAdd bool
	// SearchTimeout bounds a Search's memory read (default 10 minutes);
	// when it expires with the client still connected, the raw ranking
	// answers.
	SearchTimeout time.Duration
	// MaxOpenStores bounds the cached open user stores (default 64).
	MaxOpenStores int
	// Models builds the models from MemoryConfig; nil builds its configured
	// providers. Tests inject scripted models here.
	Models func(memoryagent.Config) (memoryagent.Models, error)
	// Logger receives operational lines, never request bodies; nil logs to
	// standard error.
	Logger *log.Logger
}

// Server serves the AML contract. Every user's agent shares the models.
type Server struct {
	config   ServerConfig
	key      [sha256.Size]byte // digest of the API key
	models   memoryagent.Models
	budget   *memoryagent.Budget
	maxItems int // the View's record bound; 0 for memory-agent/v1
	// timelines reports consolidation mode "timelines" or "layered": Adds
	// fold full batches and Searches flush the tail.
	timelines bool
	users     *registry
	log       *log.Logger
	// Served requests, and Searches answered by the raw ranking (degraded)
	// or by a read with a stage that fell back (partial).
	adds, searches, degraded, partial atomic.Int64
	// The windows of an Add's consolidation, a Search's flush and its read.
	addWindow, flushWindow, searchTimeout time.Duration
	handler                               http.Handler
	once                                  sync.Once
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
	if config.SearchTimeout == 0 {
		config.SearchTimeout = defaultSearchTimeout
	}
	if config.SearchTimeout < 0 {
		return nil, errors.New("the search timeout must be positive")
	}
	if err := os.MkdirAll(config.DataRoot, 0o700); err != nil {
		return nil, err
	}
	consolidation := config.MemoryConfig.Consolidation
	s := &Server{config: config, key: sha256.Sum256([]byte(config.AuthKey)), log: config.Logger,
		timelines: consolidation.Timelines(),
		addWindow: addWindow, flushWindow: flushWindow, searchTimeout: config.SearchTimeout}
	if s.log == nil {
		s.log = log.New(os.Stderr, "aml: ", log.LstdFlags|log.LUTC)
	}
	var err error
	if config.Models != nil {
		s.models, err = config.Models(config.MemoryConfig)
	} else {
		s.models, s.budget, err = memoryagent.OpenModels(config.MemoryConfig)
	}
	if err != nil {
		return nil, err
	}
	if s.maxItems, err = s.probe(); err != nil {
		if s.budget != nil {
			s.budget.Close()
		}
		return nil, err
	}
	s.users = newRegistry(config.DataRoot, config.MaxOpenStores, s.openUser)
	s.handler = s.routes()
	return s, nil
}

// probe makes an agent over an empty scratch store, so a configuration the
// agents refuse fails at startup rather than on every request, and reads
// the View's record bound.
func (s *Server) probe() (int, error) {
	dir, err := os.MkdirTemp("", "aml-probe-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(dir)
	store, err := records.Open(dir, false)
	if err != nil {
		return 0, err
	}
	defer store.Close()
	agent, err := memoryagent.New(store, s.config.MemoryConfig, s.models, nil)
	if err != nil {
		return 0, err
	}
	defer agent.Close()
	return agent.MaxItems(), nil
}

// openUser opens a user's store with the query runtime records serve runs
// (query cache, reader pool, background lexical index), and its agent.
func (s *Server) openUser(dir string) (*records.Store, *memoryagent.Agent, func(), error) {
	store, err := records.Open(dir, false)
	if err != nil {
		return nil, nil, nil, err
	}
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

// Close closes the user stores (held ones when their request ends) and the
// models' budget. Stop the HTTP server first.
func (s *Server) Close() error {
	s.once.Do(func() {
		s.users.close()
		if s.budget != nil {
			s.budget.Close()
		}
	})
	return nil
}

// DegradedSearches counts the Searches answered by raw search because the
// memory read failed.
func (s *Server) DegradedSearches() int64 { return s.degraded.Load() }

// PartialDegradedSearches counts the Searches answered by a memory read in
// which a stage fell back (memoryagent.ReadStats.Degraded).
func (s *Server) PartialDegradedSearches() int64 { return s.partial.Load() }

// statusLine is the served Adds and Searches, the degraded and partially
// degraded Searches and, with a budget file, each account's spent, held and
// cap.
func (s *Server) statusLine(ctx context.Context) string {
	line := fmt.Sprintf("status: adds %d, searches %d, degraded %d, partially degraded %d", s.adds.Load(), s.searches.Load(), s.degraded.Load(), s.partial.Load())
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

// LogStatus logs the status line every interval until ctx ends.
func (s *Server) LogStatus(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.log.Print(s.statusLine(ctx))
		}
	}
}

// Handler routes POST /add and POST /search (authenticated) and GET /health
// and GET / (not); anything else is 404.
func (s *Server) Handler() http.Handler { return s.handler }

// HTTPServer is the HTTP server for addr with the service's timeouts: an Add
// may consolidate for up to the platform's 30-minute I/O timeout.
func (s *Server) HTTPServer(addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           s.handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute,
		WriteTimeout:      31 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          s.log,
	}
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	health := func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
	mux.HandleFunc("GET /{$}", health)
	mux.HandleFunc("GET /health", health)
	mux.Handle("POST /add", s.authorized(s.recovered("add", s.add)))
	mux.Handle("POST /search", s.authorized(s.recovered("search", s.search)))
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})
	return s.logged(mux)
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
			s.log.Printf("%s panicked: %v\n%s", operation, value, debug.Stack())
			if !writer.wrote {
				writeError(writer, http.StatusInternalServerError, operation+" failed")
			}
		}()
		next(writer, r)
	}
}

// add stores one chunk of a conversation and returns once it is persisted and
// searchable: written (in parts when it is longer than a document holds),
// its new chunks embedded when the configuration embeds, and, with
// ConsolidateOnAdd, consolidated (consolidateAdd). A retry of a stored
// request succeeds and finishes what the earlier attempt may not have. Only
// a missing request_id, user_id or session_id, or a request_id reused for
// other text, is a 400.
func (s *Server) add(w http.ResponseWriter, r *http.Request) {
	deadline := time.Now().Add(s.addWindow)
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
		s.fail(w, "pack", "", err)
		return
	}
	done := AddResponse{Success: true, RequestID: req.RequestID, UserID: req.UserID, SessionID: req.SessionID}
	if skipped > 0 {
		s.log.Printf("add skipped %d of %d messages without text", skipped, len(req.Messages))
	}
	if len(parts) == 0 {
		s.adds.Add(1)
		writeJSON(w, http.StatusOK, done)
		return
	}
	ctx := r.Context()
	user, err := s.users.acquire(ctx, req.UserID, true)
	if err != nil {
		s.fail(w, "open store", "", err)
		return
	}
	defer s.users.release(user)
	user.waiting.Add(1)
	user.writes.Lock()
	user.waiting.Add(-1)
	defer user.writes.Unlock()
	chunks := []string{}
	for _, part := range parts {
		err := s.write(ctx, user, part)
		if errors.Is(err, errRequestReused) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err != nil {
			s.fail(w, "write", user.key, err)
			return
		}
		chunks = append(chunks, part.chunks...)
	}
	if embedding := s.config.MemoryConfig.Embedding; embedding != nil {
		if err := embedChunks(ctx, user.store, *embedding, chunks); err != nil {
			s.log.Printf("embedding failed (user %s): %v", user.key[:8], err)
			writeError(w, http.StatusServiceUnavailable, "embedding is unavailable; retry the same request")
			return
		}
	}
	if s.config.ConsolidateOnAdd {
		s.consolidateAdd(ctx, user, deadline)
	}
	s.adds.Add(1)
	writeJSON(w, http.StatusOK, done)
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
		s.log.Printf("consolidation left to a queued Add (user %s)", user.key[:8])
		return
	}
	if !time.Now().Before(deadline) {
		s.log.Printf("consolidation skipped: the Add's window has passed (user %s)", user.key[:8])
		return
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	s.consolidate(ctx, user, s.timelines, "consolidation")
	if err := user.agent.WarmLayer(ctx, records.Scope{}); err != nil {
		s.log.Printf("layer embedding failed (user %s): %v", user.key[:8], err)
	}
}

// flush folds what the user's Adds left pending, the tail of the
// conversation, before a Search reads, for at most flushWindow; then it
// stores the layer's embeddings when it folded anything. A failed or
// unfinished flush is logged, and the Search reads what is consolidated.
// With nothing pending it only loads the state.
func (s *Server) flush(ctx context.Context, user *userEntry) {
	ctx, cancel := context.WithTimeout(ctx, s.flushWindow)
	defer cancel()
	if s.consolidate(ctx, user, false, "flush") == 0 {
		return
	}
	if err := user.agent.WarmLayer(ctx, records.Scope{}); err != nil {
		s.log.Printf("layer embedding failed (user %s): %v", user.key[:8], err)
	}
}

// consolidate runs the user's consolidation jobs: with hold, until a job
// folds no full batch; without, until nothing remains (a job reads full
// batches; one that finds nothing new folds the rest). It stops at the first
// error, logged under label, or after maxConsolidateCalls jobs; it logs each
// drop of records, and returns the batches folded. The jobs defer the
// records' native edges (nativeEdges); when one leaves them pending, they
// are rebuilt once after the jobs.
func (s *Server) consolidate(ctx context.Context, user *userEntry, hold bool, label string) int {
	folded, pending := 0, false
	defer func() {
		if pending {
			s.rebuildEdges(ctx, user, label)
		}
	}()
	for call := 1; call <= maxConsolidateCalls; call++ {
		result, err := s.consolidateOnce(ctx, user, memoryagent.ConsolidateRequest{Hold: hold, DeferEdges: true})
		folded += result.Folded
		pending = pending || result.EdgesPending
		for _, drop := range result.Drops {
			s.log.Printf("consolidation dropped %d records (user %s): %s", drop.Records, user.key[:8], drop.Reason)
		}
		if err != nil {
			s.log.Printf("%s failed (user %s): %v", label, user.key[:8], err)
			break
		}
		if hold && result.Folded == 0 || !hold && result.Remaining == 0 {
			break
		}
		if call == maxConsolidateCalls {
			s.log.Printf("%s stopped with %d records pending (user %s)", label, result.Remaining, user.key[:8])
		}
	}
	return folded
}

// rebuildEdges rebuilds the records' native edges that consolidate's jobs
// left pending, unless its window has passed; a failure (a rebuild returns
// its error; it reports no edgeError), the event edge pairs JEV left
// undecided for the next rebuild, and those it gave up (edgeKinds), are
// logged under label, and the next rebuild catches up.
func (s *Server) rebuildEdges(ctx context.Context, user *userEntry, label string) {
	if ctx.Err() != nil {
		s.log.Printf("%s left native edges pending (user %s): %v", label, user.key[:8], ctx.Err())
		return
	}
	result, err := s.consolidateOnce(ctx, user, memoryagent.ConsolidateRequest{RebuildEdges: true})
	if err != nil {
		s.log.Printf("%s native edges failed (user %s): %v", label, user.key[:8], err)
	}
	if result.EdgeFallbacks > 0 {
		s.log.Printf("%s native edges left %d pairs undecided (user %s)", label, result.EdgeFallbacks, user.key[:8])
	}
	if result.EdgeGivenUp > 0 {
		s.log.Printf("%s native edges gave up %d pairs whose calls failed 3 times (user %s)", label, result.EdgeGivenUp, user.key[:8])
	}
}

// consolidateOnce runs one job; a job that panics fails, its panic and stack
// logged.
func (s *Server) consolidateOnce(ctx context.Context, user *userEntry, req memoryagent.ConsolidateRequest) (result memoryagent.ConsolidateResult, err error) {
	defer func() {
		if value := recover(); value != nil {
			s.log.Printf("consolidation panicked (user %s): %v\n%s", user.key[:8], value, debug.Stack())
			result, err = memoryagent.ConsolidateResult{}, fmt.Errorf("panic: %v", value)
		}
	}()
	return user.agent.Consolidate(ctx, req)
}

// search reads the user's memory for the question, within SearchTimeout,
// after flushing the user's consolidation (timelines with
// ConsolidateOnAdd). When the read fails for any reason but the client
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
		s.fail(w, "open store", "", err)
		return
	}
	if user == nil {
		s.answer(w, []SearchItem{})
		return
	}
	defer s.users.release(user)
	if s.config.ConsolidateOnAdd && s.timelines {
		s.flush(ctx, user)
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
			n := s.partial.Add(1)
			s.log.Printf("partially degraded search %d (user %s): %s", n, user.key[:8], strings.Join(stages, ", "))
		}
		s.answer(w, searchItems(view, topK))
		return
	}
	if ctx.Err() != nil {
		writeError(w, http.StatusServiceUnavailable, "request canceled")
		return
	}
	n := s.degraded.Add(1)
	s.log.Printf("degraded search %d (user %s): %v", n, user.key[:8], err)
	items, err := s.rawSearch(ctx, user, req.Query, topK)
	if err != nil {
		s.fail(w, "degraded search", user.key, err)
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
			s.log.Printf("memory read panicked (user %s): %v\n%s", user.key[:8], value, debug.Stack())
			view, err = memoryagent.View{}, fmt.Errorf("panic: %v", value)
		}
	}()
	return user.agent.Read(ctx, req)
}

// answer writes a Search's items, scored by rank (1/(rank+1), best first),
// and counts the Search.
func (s *Server) answer(w http.ResponseWriter, items []SearchItem) {
	for i := range items {
		items[i].Score = 1 / float64(i+1)
	}
	s.searches.Add(1)
	writeJSON(w, http.StatusOK, SearchResponse{Data: items})
}

// rawSearch is the store's own ranking of the query, hybrid when the
// configuration embeds (words when the embedder fails), hydrated as reads
// hydrate evidence. It uses no model.
func (s *Server) rawSearch(ctx context.Context, user *userEntry, query string, topK int) ([]SearchItem, error) {
	snapshot, err := user.store.Get(ctx, records.GetRequest{IDs: []string{}})
	if err != nil {
		return nil, err
	}
	config := s.config.MemoryConfig
	ids, err := rawQuery(ctx, user.store, snapshot.Revision, query, config.Embedding, config.Index)
	if err != nil && config.Embedding != nil {
		s.log.Printf("hybrid search failed, searching words (user %s): %v", user.key[:8], err)
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
	for _, item := range evidence {
		items = append(items, SearchItem{ID: item.ID, Content: item.Text, CreatedAt: item.Date})
	}
	return items, nil
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
// or changed store, shutdown), else 500.
func (s *Server) fail(w http.ResponseWriter, operation, user string, err error) {
	if len(user) > 8 {
		user = user[:8]
	}
	s.log.Printf("%s failed (user %s): %v", operation, user, err)
	if retryable(err) {
		writeError(w, http.StatusServiceUnavailable, operation+" is temporarily unavailable; retry")
		return
	}
	writeError(w, http.StatusInternalServerError, operation+" failed")
}

func retryable(err error) bool {
	message := err.Error()
	return errors.Is(err, records.ErrConflict) || errors.Is(err, errClosing) || errors.Is(err, context.DeadlineExceeded) ||
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
		s.log.Printf("%s %s %d %dms", r.Method, r.URL.Path, recorder.status, time.Since(start).Milliseconds())
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
