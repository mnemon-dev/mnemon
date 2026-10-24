package aml

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
)

// addSessions stores n Adds about the support group, each its own session.
func addSessions(t *testing.T, h *harness, user string, n int) {
	t.Helper()
	for i := range n {
		req := addRequest(fmt.Sprintf("r%d", i), user)
		req.SessionID = fmt.Sprintf("s%d", i)
		h.mustAdd(t, req)
	}
}

func checkRankScores(t *testing.T, items []SearchItem) {
	t.Helper()
	for i, item := range items {
		if item.Score != 1/float64(i+1) {
			t.Fatal(i, item.Score)
		}
	}
}

func TestSearchTimeoutAnswersRawRanking(t *testing.T) {
	h := newHarness(t, func(config *ServerConfig) { config.SearchTimeout = 100 * time.Millisecond })
	addSessions(t, h, "u1", 3)
	h.judge.block.Store(true)
	start := time.Now()
	items := h.search(t, SearchRequest{Query: "Caroline support group", UserID: "u1", TopK: 2})
	if len(items) != 2 || strings.HasPrefix(items[0].ID, "index:") || time.Since(start) > 5*time.Second || h.server.DegradedSearches() != 1 {
		t.Fatal(items, time.Since(start), h.log.String())
	}
	checkRankScores(t, items)
	if !strings.Contains(h.log.String(), "degraded search 1 (user "+userKey("u1")[:8]+"): context deadline exceeded") {
		t.Fatal(h.log.String())
	}
	// A client that leaves during the read gets 503, not counted as
	// degraded.
	h.server.searchTimeout = time.Minute
	ctx, cancel := context.WithCancel(context.Background())
	raw, _ := json.Marshal(SearchRequest{Query: "Caroline support group", UserID: "u1", TopK: 2})
	request := httptest.NewRequest("POST", "/search", strings.NewReader(string(raw))).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer "+testKey)
	recorder := httptest.NewRecorder()
	waits := h.judge.blocked.Load()
	done := make(chan struct{})
	go func() { h.server.Handler().ServeHTTP(recorder, request); close(done) }()
	eventually(t, func() bool { return h.judge.blocked.Load() > waits })
	cancel()
	<-done
	if recorder.Code != http.StatusServiceUnavailable || h.server.DegradedSearches() != 1 {
		t.Fatal(recorder.Code, recorder.Body.String())
	}
}

func TestSearchPartialDegradationIsLoggedAndCounted(t *testing.T) {
	h := newHarness(t, nil)
	addSessions(t, h, "u1", 2)
	h.judge.failJudgement.Store(true)
	items := h.search(t, SearchRequest{Query: "Caroline support group", UserID: "u1", TopK: 10})
	if len(items) == 0 || h.server.DegradedSearches() != 0 || h.server.PartialDegradedSearches() != 1 {
		t.Fatal(items, h.log.String())
	}
	if !strings.Contains(h.log.String(), "partially degraded search 1 (user "+userKey("u1")[:8]+"): judge") {
		t.Fatal(h.log.String())
	}
	h.judge.failJudgement.Store(false)
	h.search(t, SearchRequest{Query: "Caroline support group", UserID: "u1", TopK: 10})
	if h.server.PartialDegradedSearches() != 1 {
		t.Fatal(h.log.String())
	}
}

func TestStatusLine(t *testing.T) {
	h := newHarness(t, nil)
	h.mustAdd(t, addRequest("r1", "u1"))
	h.search(t, SearchRequest{Query: "Caroline support group", UserID: "u1", TopK: 10})
	h.search(t, SearchRequest{Query: " ", UserID: "u1", TopK: 10})
	ctx := context.Background()
	if line := h.server.statusLine(ctx); line != "status: adds 1, searches 2, degraded 0, partially degraded 0" {
		t.Fatal(line)
	}
	budget, err := memoryagent.OpenBudgetWithCaps(filepath.Join(t.TempDir(), "budget.db"), map[string]float64{"openai": 7})
	if err != nil {
		t.Fatal(err)
	}
	h.server.budget = budget // closed by the server
	// A call that cannot connect reserves, then releases: the account
	// exists with nothing spent or held.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := listener.Addr().String()
	listener.Close()
	t.Setenv("STATUS_TEST_OPENAI", "test-only-placeholder")
	provider, err := memoryagent.NewProvider(memoryagent.ModelConfig{Kind: "openai", Model: "gpt-4.1-mini", BaseURL: "http://" + closed + "/v1", KeyEnv: "STATUS_TEST_OPENAI"}, budget)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = provider.Complete(ctx, "s", "p"); err == nil {
		t.Fatal("a closed port answered")
	}
	if line := h.server.statusLine(ctx); line != "status: adds 1, searches 2, degraded 0, partially degraded 0; budget openai spent 0.0000 held 0.0000 cap 7" {
		t.Fatal(line)
	}
	// The line is logged on a schedule until the context ends.
	logCtx, cancel := context.WithCancel(ctx)
	stopped := make(chan struct{})
	go func() { h.server.logStatus(logCtx, 10*time.Millisecond); close(stopped) }()
	eventually(t, func() bool { return strings.Contains(h.log.String(), "status: adds 1") })
	cancel()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("logStatus did not stop")
	}
}

func TestSearchTopKDefaultsToViewAndBlankQueryIsEmpty(t *testing.T) {
	h := newHarness(t, func(config *ServerConfig) {
		config.MemoryConfig.View = &memoryagent.ViewConfig{MaxItems: 2, Fusion: "rrf"}
	})
	addSessions(t, h, "u1", 3)
	for _, body := range []string{
		`{"query":"Caroline support group","user_id":"u1"}`,
		`{"query":"Caroline support group","user_id":"u1","top_k":null}`,
		`{"query":"Caroline support group","user_id":"u1","top_k":0}`,
		`{"query":"Caroline support group","user_id":"u1","top_k":-3}`,
		`{"query":"Caroline support group","user_id":"u1","top_k":0.5}`,
		`{"query":"Caroline support group","user_id":"u1","top_k":"many"}`,
	} {
		response := h.call("POST", "/search", body)
		var out SearchResponse
		if json.Unmarshal(response.Body.Bytes(), &out); response.Code != http.StatusOK || len(out.Data) != 2 {
			t.Fatal(body, response.Code, response.Body.String())
		}
	}
	for _, body := range []string{`{"query":" \n ","user_id":"u1","top_k":5}`, `{"user_id":"u1","top_k":5}`, `{"query":"","user_id":"nobody"}`} {
		if response := h.call("POST", "/search", body); response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != `{"data":[]}` {
			t.Fatal(body, response.Code, response.Body.String())
		}
	}
	for _, body := range []string{`{"query":"q","top_k":5}`, `{"query":"q","user_id":" ","top_k":5}`, `{"query":" ","user_id":""}`} {
		if response := h.call("POST", "/search", body); response.Code != http.StatusBadRequest {
			t.Fatal(body, response.Code)
		}
	}
	// Without a View (memory-agent/v1 has none) a missing top_k is 100.
	if topK := (&Server{}).topK(0); topK != 100 {
		t.Fatal(topK)
	}
}

func TestPanicsAreContained(t *testing.T) {
	h := newHarness(t, func(config *ServerConfig) {
		config.MemoryConfig.Consolidation = &memoryagent.ConsolidationConfig{Mode: "timelines"}
		config.ConsolidateOnAdd = true
	})
	addSessions(t, h, "u1", 2)
	prefix := userKey("u1")[:8]
	// A read that panics answers the raw ranking: in a model call's
	// goroutine (the read fails with the panic) ...
	h.planner.panics.Store(true)
	items := h.search(t, SearchRequest{Query: "Caroline support group", UserID: "u1", TopK: 2})
	logged := h.log.String()
	if len(items) != 2 || h.server.DegradedSearches() != 1 || !strings.Contains(logged, "degraded search 1 (user "+prefix+"): panic: planner bug") || !strings.Contains(logged, "goroutine ") {
		t.Fatal(items, logged)
	}
	checkRankScores(t, items)
	// ... or in the read's own (the layer's judgement).
	h.planner.panics.Store(false)
	h.judge.panicOnLayer.Store(true)
	items = h.search(t, SearchRequest{Query: "Caroline support group", UserID: "u1", TopK: 2})
	if logged = h.log.String(); len(items) != 2 || h.server.DegradedSearches() != 2 || !strings.Contains(logged, "memory read panicked (user "+prefix+"): layer judge bug") {
		t.Fatal(items, logged)
	}
	// Any other panic is a 500 with a JSON error, logged with its stack and
	// never the request; a handler that already answered keeps its answer.
	recorder := httptest.NewRecorder()
	h.server.recovered("add", func(http.ResponseWriter, *http.Request) { panic("boom") })(recorder, httptest.NewRequest("POST", "/add", strings.NewReader(`{"secret":"x"}`)))
	if recorder.Code != http.StatusInternalServerError || errorBody(t, recorder) != "add failed" {
		t.Fatal(recorder.Code, recorder.Body.String())
	}
	if logged = h.log.String(); !strings.Contains(logged, "add panicked: boom") || strings.Contains(logged, "secret") {
		t.Fatal(logged)
	}
	recorder = httptest.NewRecorder()
	h.server.recovered("search", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, SearchResponse{Data: []SearchItem{}})
		panic("late")
	})(recorder, httptest.NewRequest("POST", "/search", nil))
	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != `{"data":[]}` {
		t.Fatal(recorder.Code, recorder.Body.String())
	}
}

func TestAddTimestampForms(t *testing.T) {
	h := newHarness(t, nil)
	// 2023-05-08 13:56 UTC, as RFC 3339, seconds, milliseconds and strings.
	forms := map[string]string{
		"rfc3339":        `"2023-05-08T13:56:00Z"`,
		"rfc3339 offset": `"2023-05-08T15:56:00.250+02:00"`,
		"seconds":        `1683554160`,
		"seconds frac":   `1683554160.5`,
		"seconds text":   `"1683554160"`,
		"milliseconds":   `1683554160000`,
		"ms text":        `"1683554160000"`,
	}
	for name, timestamp := range forms {
		body := `{"request_id":"` + name + `","user_id":"u1","session_id":"s1","messages":[{"role":"user","content":"hello","timestamp":` + timestamp + `}]}`
		if response := h.call("POST", "/add", body); response.Code != http.StatusOK {
			t.Fatal(name, response.Code, response.Body.String())
		}
	}
	h.server.Close()
	parents := storedParents(t, h.root, "u1")
	for name := range forms {
		if got := parents[documentID(name)]; got != "Conversation s1 on 2023/05/08 (Mon) 13:56:\nuser: hello" {
			t.Fatalf("%s: %q", name, got)
		}
	}
}

func TestBlockIDsFollowBlockText(t *testing.T) {
	a := memoryagent.IndexItem{ID: "2-topics", Kind: "2-topics", Text: "Topic: adoption"}
	b := memoryagent.IndexItem{ID: "2-topics", Kind: "2-topics", Text: "Topic: hiking"}
	if blockID(a) != blockID(a) || blockID(a) == blockID(b) || !strings.HasPrefix(blockID(a), "index:2-topics:") {
		t.Fatal(blockID(a), blockID(b))
	}
}
