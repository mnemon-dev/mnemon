package memoryagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// completionServer is an httptest model endpoint whose replies a test
// scripts; it keeps the request bodies it received.
type completionServer struct {
	*httptest.Server
	mu     sync.Mutex
	bodies []string
	answer func(body string) (int, string)
}

func newCompletionServer(t *testing.T, reply string) *completionServer {
	t.Helper()
	s := &completionServer{}
	s.set(http.StatusOK, reply)
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.bodies = append(s.bodies, string(raw))
		answer := s.answer
		s.mu.Unlock()
		status, reply := answer(string(raw))
		w.WriteHeader(status)
		io.WriteString(w, reply)
	}))
	t.Cleanup(s.Close)
	return s
}

// set makes the server answer every request with status and reply.
func (s *completionServer) set(status int, reply string) {
	s.respond(func(string) (int, string) { return status, reply })
}

// respond makes the server answer each request body with answer, which may
// run concurrently.
func (s *completionServer) respond(answer func(body string) (int, string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.answer = answer
}

func (s *completionServer) received() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.bodies...)
}

// meteredProvider is an openai-kind provider on server, metered against the
// budget file at budgetPath, replaying from cache (nil sends every request).
func meteredProvider(t *testing.T, server *completionServer, budgetPath string, cache *ModelCache) (*Provider, *Budget) {
	t.Helper()
	budget, err := OpenBudget(budgetPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { budget.Close() })
	t.Setenv("UNIT_OPENAI_CREDENTIAL", "test-only-placeholder")
	p, err := NewProvider(ModelConfig{Kind: "openai", Model: "test-model", BaseURL: server.URL + "/v1", KeyEnv: "UNIT_OPENAI_CREDENTIAL", PriceIn: 1, PriceOut: 2}, budget)
	if err != nil {
		t.Fatal(err)
	}
	p.cache = cache
	return p, budget
}

// completionBody is a chat completion reply with text and metering.
func completionBody(text string) string {
	return fmt.Sprintf(`{"choices":[{"message":{"content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":1000,"completion_tokens":10}}`, text)
}

// openTestCache opens a model cache at path, closed when the test ends.
func openTestCache(t *testing.T, path string) *ModelCache {
	t.Helper()
	cache, err := OpenModelCache(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cache.Close() })
	return cache
}

func TestModelCacheFileIsAbsolutePrivateAndWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "eval", "model-cache.db")
	cache := openTestCache(t, path)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatal("cache file is not a private regular file", info, err)
	}
	// Several serve processes share one file: readers never wait for the
	// writer, and writers wait for each other.
	var mode string
	if err = cache.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Fatal("journal mode", mode, err)
	}
	var timeout int
	if err = cache.db.QueryRow(`PRAGMA busy_timeout`).Scan(&timeout); err != nil || timeout != 10000 {
		t.Fatal("busy timeout", timeout, err)
	}
	if _, err = OpenModelCache("relative/model-cache.db"); err == nil {
		t.Fatal("relative cache path accepted")
	}
	shared := filepath.Join(t.TempDir(), "shared.db")
	if err = os.WriteFile(shared, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = OpenModelCache(shared); err == nil {
		t.Fatal("cache file readable by others accepted")
	}
	if _, err = OpenModelCache(t.TempDir()); err == nil {
		t.Fatal("directory accepted as cache file")
	}
}

func TestModelCacheLookupAndLastWriteWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model-cache.db")
	a, b := openTestCache(t, path), openTestCache(t, path)
	ctx := context.Background()
	if body, ok := a.lookup(ctx, "k"); ok || body != nil {
		t.Fatal("empty cache found a reply", body)
	}
	if err := a.store(ctx, "k", []byte(`{"first":1}`)); err != nil {
		t.Fatal(err)
	}
	if body, ok := b.lookup(ctx, "k"); !ok || string(body) != `{"first":1}` {
		t.Fatal("another handle on the file does not see the reply", string(body), ok)
	}
	// Two processes that missed the same request at once both store: the
	// last write wins.
	if err := b.store(ctx, "k", []byte(`{"second":2}`)); err != nil {
		t.Fatal(err)
	}
	if body, ok := a.lookup(ctx, "k"); !ok || string(body) != `{"second":2}` {
		t.Fatal("last write did not win", string(body), ok)
	}
	// A store outlives its caller's cancellation: the reply was paid for.
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := a.store(canceled, "late", []byte(`{}`)); err != nil {
		t.Fatal("store gave up on a canceled caller", err)
	}
	if _, ok := b.lookup(ctx, "late"); !ok {
		t.Fatal("reply stored after cancellation is missing")
	}
}

func TestModelCacheOpensConcurrently(t *testing.T) {
	// Serve processes started together open a new file at once: they race
	// to switch it to WAL, and every one of them must open it.
	for round := range 100 {
		path := filepath.Join(t.TempDir(), fmt.Sprint("model-cache-", round, ".db"))
		caches := make(chan *ModelCache, 8)
		failures := make(chan error, 8)
		var work sync.WaitGroup
		for range 8 {
			work.Add(1)
			go func() {
				defer work.Done()
				cache, err := OpenModelCache(path)
				if err != nil {
					failures <- err
					return
				}
				caches <- cache
			}()
		}
		work.Wait()
		close(caches)
		close(failures)
		for cache := range caches {
			cache.Close()
		}
		for err := range failures {
			t.Fatal("concurrent open failed", round, err)
		}
	}
}

func TestReplayKeyCoversKindEndpointModelAndBody(t *testing.T) {
	base := replayKey("deepseek", "https://api.example/chat/completions", "m", []byte(`{"a":1}`))
	if len(base) != 64 || base != replayKey("deepseek", "https://api.example/chat/completions", "m", []byte(`{"a":1}`)) {
		t.Fatal("key is not a stable sha256", base)
	}
	for name, other := range map[string]string{
		"kind":     replayKey("openai", "https://api.example/chat/completions", "m", []byte(`{"a":1}`)),
		"endpoint": replayKey("deepseek", "https://api.example/v2/chat/completions", "m", []byte(`{"a":1}`)),
		"model":    replayKey("deepseek", "https://api.example/chat/completions", "n", []byte(`{"a":1}`)),
		"body":     replayKey("deepseek", "https://api.example/chat/completions", "m", []byte(`{"a": 1}`)),
		// Parts are length-prefixed: moving a byte between parts changes the key.
		"boundary": replayKey("deepseekh", "ttps://api.example/chat/completions", "m", []byte(`{"a":1}`)),
	} {
		if other == base {
			t.Error("key ignores the", name)
		}
	}
}

func TestProviderReplaysRecordedCompletion(t *testing.T) {
	server := newCompletionServer(t, completionBody("hello"))
	cache := openTestCache(t, filepath.Join(t.TempDir(), "model-cache.db"))
	p, budget := meteredProvider(t, server, filepath.Join(t.TempDir(), "budget.db"), cache)
	ctx := context.Background()
	text, live, err := p.Complete(ctx, "system", "prompt")
	if err != nil || text != "hello" || live.Cached != 0 || live.InputTokens != 1000 || live.OutputTokens != 10 || !near(live.USD, 0.00102) {
		t.Fatal(text, live, err)
	}
	// The identical request is answered from the cache: nothing is sent,
	// reserved or settled, and the usage reported is the recorded reply's,
	// flagged as replayed.
	text, replayed, err := p.Complete(ctx, "system", "prompt")
	want := live
	want.Cached = 1
	if err != nil || text != "hello" || replayed != want {
		t.Fatal(text, replayed, err)
	}
	if len(server.received()) != 1 || len(budgetAttempts(t, budget)) != 1 {
		t.Fatal("a replayed call reached the network or the budget", len(server.received()), budgetAttempts(t, budget))
	}
	if held, spent := accountHeld(t, budget, "openai"); !near(held, 0) || !near(spent, live.USD) {
		t.Fatal("a replayed call was metered", held, spent)
	}
	// Any change of the body sent is another request.
	calls := []func() (string, Usage, error){
		func() (string, Usage, error) { return p.CompleteJSON(ctx, "system", "prompt") },
		func() (string, Usage, error) { return p.CompleteWith(ctx, "system", "prompt", 400) },
		func() (string, Usage, error) { return p.Complete(ctx, "system", "prompt!") },
		func() (string, Usage, error) { return p.Complete(ctx, "system!", "prompt") },
	}
	for i, call := range calls {
		if _, usage, err := call(); err != nil || usage.Cached != 0 || len(server.received()) != i+2 || len(budgetAttempts(t, budget)) != i+2 {
			t.Fatal("a different body was replayed", i, usage, err)
		}
	}
	for i, call := range calls {
		if text, usage, err := call(); err != nil || text != "hello" || usage.Cached != 1 || len(server.received()) != len(calls)+1 {
			t.Fatal("a recorded body was not replayed", i, usage, err)
		}
	}
}

func TestProviderRecordsOnlyRepliesItReturns(t *testing.T) {
	good := completionBody("fine")
	server := newCompletionServer(t, good)
	cache := openTestCache(t, filepath.Join(t.TempDir(), "model-cache.db"))
	p, _ := meteredProvider(t, server, filepath.Join(t.TempDir(), "budget.db"), cache)
	ctx := context.Background()
	// Each failed call is followed by the same request with the provider
	// answering well: it must reach the network, then be recorded.
	for i, bad := range []struct {
		name   string
		status int
		reply  string
	}{
		{"server error", http.StatusInternalServerError, good},
		{"rate limit", http.StatusTooManyRequests, good},
		{"refusal", http.StatusBadRequest, good},
		{"unreadable reply", http.StatusOK, `{"choices":[]}`},
		{"empty reply", http.StatusOK, `{"choices":[{"message":{"content":""}}],"usage":{"prompt_tokens":10,"completion_tokens":0}}`},
		{"cut-off reply", http.StatusOK, `{"choices":[{"message":{"content":"partial"},"finish_reason":"length"}],"usage":{"prompt_tokens":10,"completion_tokens":1024}}`},
		{"unmetered reply", http.StatusOK, `{"choices":[{"message":{"content":"fine"}}]}`},
	} {
		prompt := fmt.Sprint("prompt ", i)
		server.set(bad.status, bad.reply)
		if _, _, err := p.Complete(ctx, "system", prompt); err == nil {
			t.Fatal(bad.name, "succeeded")
		}
		server.set(http.StatusOK, good)
		before := len(server.received())
		if text, usage, err := p.Complete(ctx, "system", prompt); err != nil || text != "fine" || usage.Cached != 0 || len(server.received()) != before+1 {
			t.Fatal(bad.name, "was recorded", text, usage, err)
		}
		if _, usage, err := p.Complete(ctx, "system", prompt); err != nil || usage.Cached != 1 || len(server.received()) != before+1 {
			t.Fatal(bad.name, "then a good reply was not recorded", usage, err)
		}
	}
	// A request that failed in transport is not recorded either.
	transport := p.client.Transport
	p.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		return nil, &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")}
	})
	if _, _, err := p.Complete(ctx, "system", "reset"); err == nil {
		t.Fatal("a reset succeeded")
	}
	p.client.Transport = transport
	if _, usage, err := p.Complete(ctx, "system", "reset"); err != nil || usage.Cached != 0 {
		t.Fatal("a reset was recorded", usage, err)
	}

	// A reply cut at the limit is returned by CompleteWith, so it is recorded;
	// replayed to Complete with the identical body, it fails as the live reply
	// would.
	server.set(http.StatusOK, `{"choices":[{"message":{"content":"partial"},"finish_reason":"length"}],"usage":{"prompt_tokens":10,"completion_tokens":1024}}`)
	if text, usage, err := p.CompleteWith(ctx, "system", "cut", 1024); err != nil || text != "partial" || usage.Cached != 0 {
		t.Fatal(text, usage, err)
	}
	before := len(server.received())
	if _, usage, err := p.Complete(ctx, "system", "cut"); err == nil || !strings.Contains(err.Error(), "output token budget") || usage.Cached != 1 || len(server.received()) != before {
		t.Fatal("a replayed cut-off reply was not judged as the live one", usage, err)
	}

	// A decision whose answers do not validate is not recorded: the
	// completion succeeded, the decision did not.
	questions := map[string]Question{"a": binary("Relevant?")}
	for _, content := range []string{`{"a":null}`, `{"a":2}`, `{"b":0.5}`, `not json`} {
		server.set(http.StatusOK, completionBody(content))
		if _, _, err := p.Decide(ctx, "evidence", questions); err == nil {
			t.Fatal("invalid decision accepted", content)
		}
	}
	server.set(http.StatusOK, completionBody(`{"a":0.7}`))
	before = len(server.received())
	if answers, usage, err := p.Decide(ctx, "evidence", questions); err != nil || answers["a"] != 0.7 || usage.Cached != 0 || len(server.received()) != before+1 {
		t.Fatal("an invalid decision was recorded", answers, usage, err)
	}
	if answers, usage, err := p.Decide(ctx, "evidence", questions); err != nil || answers["a"] != 0.7 || usage.Cached != 1 || len(server.received()) != before+1 {
		t.Fatal("a valid decision was not replayed", answers, usage, err)
	}
}

func TestProviderReplaysJEVDecision(t *testing.T) {
	p, server := jevTestProvider(t)
	p.cache = openTestCache(t, filepath.Join(t.TempDir(), "model-cache.db"))
	ctx := context.Background()
	questions := binaryQuestions(3)
	answers, live, err := p.Decide(ctx, "evidence", questions)
	if err != nil || len(answers) != 3 || live.Cached != 0 || live.InputTokens != 1000 {
		t.Fatal(answers, live, err)
	}
	again, replayed, err := p.Decide(ctx, "evidence", questions)
	want := live
	want.Cached = 1
	if err != nil || fmt.Sprint(again) != fmt.Sprint(answers) || replayed != want {
		t.Fatal(again, replayed, err)
	}
	if len(server.received()) != 1 || len(budgetAttempts(t, p.budget)) != 1 {
		t.Fatal("a replayed decision reached the network or the budget", len(server.received()), budgetAttempts(t, p.budget))
	}
	// Other state or other questions are other requests.
	if _, usage, err := p.Decide(ctx, "other evidence", questions); err != nil || usage.Cached != 0 || len(server.received()) != 2 {
		t.Fatal(usage, err)
	}
	if _, usage, err := p.Decide(ctx, "evidence", binaryQuestions(4)); err != nil || usage.Cached != 0 || len(server.received()) != 3 {
		t.Fatal(usage, err)
	}

	// Replies that fail the decision are not recorded.
	model, _ := json.Marshal(p.config.Model)
	replies := []string{
		`{"model":` + string(model) + `,"answers":{"a":{"type":"noul","noul":null}},"usage":{"input_tokens":1000,"output_tokens":1}}`,
		`{"model":` + string(model) + `,"answers":{"a":{"type":"noul","noul":0.4}}}`,
		`{"model":` + string(model) + `,"answers":{"b":{"type":"noul","noul":0.4}},"usage":{"input_tokens":1000,"output_tokens":1}}`,
		`{"model":` + string(model) + `,"answers":{"a":{"type":"noul","noul":0.4}},"usage":{"input_tokens":1000,"output_tokens":1}}`,
	}
	var sent atomic.Int32
	scripted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, replies[min(int(sent.Add(1))-1, len(replies)-1)])
	}))
	defer scripted.Close()
	p.endpoint = scripted.URL
	one := map[string]Question{"a": binary("Relevant?")}
	for i := range 3 {
		if _, _, err := p.Decide(ctx, "evidence", one); err == nil {
			t.Fatal("invalid JEV reply accepted", i)
		}
	}
	if answers, usage, err := p.Decide(ctx, "evidence", one); err != nil || answers["a"] != 0.4 || usage.Cached != 0 || sent.Load() != 4 {
		t.Fatal("an invalid JEV reply was recorded", answers, usage, err)
	}
	if answers, usage, err := p.Decide(ctx, "evidence", one); err != nil || answers["a"] != 0.4 || usage.Cached != 1 || sent.Load() != 4 {
		t.Fatal("a valid JEV reply was not replayed", answers, usage, err)
	}
}

func TestProviderCacheSharedByConcurrentProcesses(t *testing.T) {
	var replies atomic.Int32
	server := newCompletionServer(t, "")
	server.respond(func(string) (int, string) {
		return http.StatusOK, completionBody(fmt.Sprint("reply ", replies.Add(1)))
	})
	cachePath, budgetPath := filepath.Join(t.TempDir(), "model-cache.db"), filepath.Join(t.TempDir(), "budget.db")
	// Two serve processes: each its own handles on the shared files.
	a, _ := meteredProvider(t, server, budgetPath, openTestCache(t, cachePath))
	b, _ := meteredProvider(t, server, budgetPath, openTestCache(t, cachePath))
	ctx := context.Background()
	const prompts, workers, calls = 4, 16, 8
	failures := make(chan error, workers*calls)
	var work sync.WaitGroup
	for w := range workers {
		work.Add(1)
		go func() {
			defer work.Done()
			p := []*Provider{a, b}[w%2]
			for c := range calls {
				text, _, err := p.Complete(ctx, "system", fmt.Sprint("prompt ", (w+c)%prompts))
				if err == nil && !strings.HasPrefix(text, "reply ") {
					err = fmt.Errorf("unexpected reply %q", text)
				}
				if err != nil {
					failures <- err
				}
			}
		}()
	}
	work.Wait()
	close(failures)
	for err := range failures {
		t.Fatal("concurrent call failed", err)
	}
	// Concurrent misses of one request may each call the model; later calls
	// replay.
	sent := len(server.received())
	if sent < prompts || sent > workers*calls {
		t.Fatal("requests sent", sent)
	}
	// Afterwards both processes replay the same reply for every request.
	for i := range prompts {
		prompt := fmt.Sprint("prompt ", i)
		fromA, usageA, errA := a.Complete(ctx, "system", prompt)
		fromB, usageB, errB := b.Complete(ctx, "system", prompt)
		if errA != nil || errB != nil || fromA != fromB || usageA.Cached != 1 || usageB.Cached != 1 {
			t.Fatal("processes disagree or missed", fromA, fromB, usageA, usageB, errA, errB)
		}
	}
	if len(server.received()) != sent {
		t.Fatal("a recorded request reached the network")
	}
}

func TestProviderWithoutCacheIsUnchanged(t *testing.T) {
	uncachedServer, cachedServer := newCompletionServer(t, completionBody("same")), newCompletionServer(t, completionBody("same"))
	uncached, uncachedBudget := meteredProvider(t, uncachedServer, filepath.Join(t.TempDir(), "budget.db"), nil)
	cached, cachedBudget := meteredProvider(t, cachedServer, filepath.Join(t.TempDir(), "budget.db"), openTestCache(t, filepath.Join(t.TempDir(), "model-cache.db")))
	ctx := context.Background()
	big := strings.Repeat("<x> & ", 20000) // above the flat reservation
	for _, p := range []*Provider{uncached, cached} {
		for _, call := range []func() (string, Usage, error){
			func() (string, Usage, error) { return p.Complete(ctx, "system", "prompt") },
			func() (string, Usage, error) { return p.CompleteJSON(ctx, "system", big) },
			func() (string, Usage, error) { return p.CompleteWith(ctx, "system", "prompt", 8192) },
		} {
			if text, usage, err := call(); err != nil || text != "same" || usage.Cached != 0 {
				t.Fatal(text, usage, err)
			}
		}
	}
	// A miss sends the bytes a provider without a cache sends, and reserves
	// and settles the same amounts.
	if fmt.Sprint(uncachedServer.received()) != fmt.Sprint(cachedServer.received()) {
		t.Fatal("request bytes changed")
	}
	settled := func(b *Budget) string {
		out := []string{}
		for _, a := range budgetAttempts(t, b) {
			if a.actual == nil {
				t.Fatal("an attempt is still held", a.reserved)
			}
			out = append(out, fmt.Sprint(a.reserved, *a.actual))
		}
		return strings.Join(out, ";")
	}
	if settled(uncachedBudget) != settled(cachedBudget) || len(budgetAttempts(t, cachedBudget)) != 3 {
		t.Fatal("budget behavior changed", settled(uncachedBudget), settled(cachedBudget))
	}
	// Without a cache an identical request is sent and metered again.
	if _, usage, err := uncached.Complete(ctx, "system", "prompt"); err != nil || usage.Cached != 0 || len(uncachedServer.received()) != 4 || len(budgetAttempts(t, uncachedBudget)) != 4 {
		t.Fatal("a provider without a cache replayed", usage, err)
	}
}

func TestBrokenModelCacheNeverFailsACall(t *testing.T) {
	server := newCompletionServer(t, completionBody("live"))
	cache, err := OpenModelCache(filepath.Join(t.TempDir(), "model-cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	p, _ := meteredProvider(t, server, filepath.Join(t.TempDir(), "budget.db"), cache)
	ctx := context.Background()
	if _, _, err = p.Complete(ctx, "system", "prompt"); err != nil {
		t.Fatal(err)
	}
	// A recorded reply that no longer parses is a miss, and the new reply
	// replaces it.
	if _, err = cache.db.Exec(`UPDATE replies SET body='not a reply'`); err != nil {
		t.Fatal(err)
	}
	if text, usage, err := p.Complete(ctx, "system", "prompt"); err != nil || text != "live" || usage.Cached != 0 || len(server.received()) != 2 {
		t.Fatal("an unreadable entry was replayed", text, usage, err)
	}
	if _, usage, err := p.Complete(ctx, "system", "prompt"); err != nil || usage.Cached != 1 || len(server.received()) != 2 {
		t.Fatal("the new reply did not replace the unreadable entry", usage, err)
	}
	// A cache that can no longer be read or written turns every call into a
	// live one; none fails.
	cache.Close()
	for i := range 2 {
		if text, usage, err := p.Complete(ctx, "system", "prompt"); err != nil || text != "live" || usage.Cached != 0 || len(server.received()) != 3+i {
			t.Fatal("a closed cache failed or replayed a call", text, usage, err)
		}
	}
}

// walOpen reports whether a handle on the SQLite file at path is open: the
// write-ahead log exists from the first open to the last close.
func walOpen(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path + "-wal")
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return err == nil
}

func TestOpenModelsSharesOneModelCache(t *testing.T) {
	local := &ModelConfig{Kind: "local", Model: "m", BaseURL: "http://127.0.0.1:1/v1"}
	path := filepath.Join(t.TempDir(), "model-cache.db")
	models, budget, cache, err := OpenModels(Config{Version: 1, Planner: local, Judge: local, Consolidator: local, ModelCache: path})
	if err != nil || budget != nil || cache == nil || !walOpen(t, path) {
		t.Fatal(budget, cache, err)
	}
	for _, model := range []any{models.Planner, models.Judge, models.Consolidator} {
		if p, ok := model.(*Provider); !ok || p.cache != cache {
			t.Fatal("a provider does not share the config's cache", model)
		}
	}
	// The caller owns the cache.
	cache.Close()
	if walOpen(t, path) {
		t.Fatal("cache still open")
	}
	// Without modelCache nothing is opened and every request is sent.
	models, _, cache, err = OpenModels(Config{Version: 1, Planner: local})
	if err != nil || cache != nil || models.Planner.(*Provider).cache != nil {
		t.Fatal(cache, err)
	}
	for _, bad := range []string{"relative/model-cache.db", t.TempDir()} {
		if _, _, _, err = OpenModels(Config{Version: 1, Planner: local, ModelCache: bad, BudgetFile: filepath.Join(t.TempDir(), "budget.db")}); err == nil {
			t.Fatal("unusable model cache accepted", bad)
		}
	}
	// A model that cannot be built leaves no cache open.
	path = filepath.Join(t.TempDir(), "model-cache.db")
	if _, _, _, err = OpenModels(Config{Version: 1, Judge: &ModelConfig{Kind: "nope", Model: "m"}, ModelCache: path}); err == nil || walOpen(t, path) {
		t.Fatal("a failed OpenModels left its cache open", err)
	}
}

func TestModelCacheStaysOutOfPolicy(t *testing.T) {
	// View.policy names the read policy; a cache only replays its answers.
	for name, config := range representativeV1Configs() {
		with := config
		with.ModelCache = "/eval/model-cache.db"
		if v1Policy(t, with) != v1Policy(t, config) {
			t.Error(name, "policy changed with a model cache")
		}
	}
	s, _ := agentStore(t)
	models := Models{Planner: replies("", "", ""), Judge: decisionFunc(fixtureJudge)}
	config := Config{Version: 1, Strategy: "simple", ModelCache: "/eval/model-cache.db"}
	cached, err := New(s, config, models, nil)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := New(s, Config{Version: 1, Strategy: "simple"}, models, nil)
	if err != nil {
		t.Fatal(err)
	}
	if (&simpleRun{agent: cached}).policy() != (&simpleRun{agent: plain}).policy() || cached.config.ModelCache != "" || config.ModelCache == "" {
		t.Fatal("the model cache entered the simple policy, or New changed the caller's config")
	}
	// Service configs decode strictly (cmd/memory readMemoryConfig).
	decoder := json.NewDecoder(strings.NewReader(`{"version":1,"modelCache":"/eval/model-cache.db"}`))
	decoder.DisallowUnknownFields()
	var decoded Config
	if err = decoder.Decode(&decoded); err != nil || decoded.ModelCache != "/eval/model-cache.db" {
		t.Fatal(decoded, err)
	}
}

// readEndpoint is an OpenAI-compatible httptest endpoint for whole simple
// reads: planner prompts get the codename plan and need, decision prompts
// fixtureJudge's answers.
func readEndpoint(t *testing.T) *completionServer {
	t.Helper()
	server := newCompletionServer(t, "")
	server.respond(func(body string) (int, string) {
		var request struct {
			Messages       []struct{ Content string }
			ResponseFormat any `json:"response_format"`
		}
		if json.Unmarshal([]byte(body), &request) != nil || len(request.Messages) != 2 {
			return http.StatusBadRequest, "{}"
		}
		system, prompt := request.Messages[0].Content, request.Messages[1].Content
		reply := "NEED: the codename"
		switch {
		case request.ResponseFormat != nil:
			var decision struct {
				State     any
				Questions map[string]Question
			}
			json.Unmarshal([]byte(prompt), &decision)
			answers, _, _ := fixtureJudge(context.Background(), decision.State, decision.Questions)
			raw, _ := json.Marshal(answers)
			reply = string(raw)
		case strings.HasPrefix(system, "You plan searches in the notes"):
			reply = "codename\nNOTE: the codename is ORBIT"
		}
		return http.StatusOK, completionBody(reply)
	})
	return server
}

func TestReadReplaysFromModelCache(t *testing.T) {
	s, dir := agentStore(t)
	seedNote(t, s, "answer", "codename ORBIT")
	seedNote(t, s, "other", "the weather was fine")
	server := readEndpoint(t)
	t.Setenv("UNIT_OPENAI_CREDENTIAL", "test-only-placeholder")
	model := &ModelConfig{Kind: "openai", Model: "test-model", BaseURL: server.URL + "/v1", KeyEnv: "UNIT_OPENAI_CREDENTIAL", PriceIn: 1, PriceOut: 2}
	budgetFile, cacheFile := filepath.Join(t.TempDir(), "budget.db"), filepath.Join(t.TempDir(), "model-cache.db")
	config := Config{Version: 1, Strategy: "simple", Planner: model, Judge: model, BudgetFile: budgetFile, ModelCache: cacheFile}
	budget, err := OpenBudget(budgetFile)
	if err != nil {
		t.Fatal(err)
	}
	defer budget.Close()
	// Each read is another serve process: its own agent, models and handles
	// on the shared files, closed after the read.
	read := func() View {
		t.Helper()
		a, err := Open(s, dir, config, false)
		if err != nil {
			t.Fatal(err)
		}
		defer a.Close()
		view, err := a.Read(context.Background(), ReadRequest{Question: "What is the codename?"})
		if err != nil || len(view.Records) == 0 {
			t.Fatal(view, err)
		}
		return view
	}
	base := read()
	sent := len(server.received())
	calls := base.Stats.PlannerCalls + base.Stats.DecisionCalls
	if sent == 0 || sent != calls || len(budgetAttempts(t, budget)) != sent || base.Stats.Usage.Cached != 0 {
		t.Fatal("base read", sent, calls, base.Stats)
	}
	if walOpen(t, cacheFile) {
		t.Fatal("the agent's Close left the model cache open")
	}
	// The same read replays every model answer: no request, no reservation,
	// the same View.
	replay := read()
	if len(server.received()) != sent || len(budgetAttempts(t, budget)) != sent || replay.Stats.Usage.Cached != calls {
		t.Fatal("replayed read reached the model or the budget", len(server.received()), replay.Stats)
	}
	same := func(v View) string {
		v.Stats.QueryMS, v.Stats.ModelMS, v.Stats.Usage.Cached = 0, 0, 0
		raw, _ := json.Marshal(v)
		return string(raw)
	}
	if same(replay) != same(base) {
		t.Fatalf("replayed read differs:\n%s\n%s", same(base), same(replay))
	}
}
