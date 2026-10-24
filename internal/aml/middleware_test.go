package aml

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
)

// logLines are the log lines that contain every needle.
func (h *harness) logLines(needles ...string) []string {
	var lines []string
next:
	for _, line := range strings.Split(h.log.String(), "\n") {
		for _, needle := range needles {
			if !strings.Contains(line, needle) {
				continue next
			}
		}
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// goroutinesIn counts the goroutines whose stack shows frame.
func goroutinesIn(frame string) int {
	stacks := make([]byte, 1<<20)
	for {
		n := runtime.Stack(stacks, true)
		if n < len(stacks) {
			stacks = stacks[:n]
			break
		}
		stacks = make([]byte, 2*len(stacks))
	}
	count := 0
	for _, stack := range strings.Split(string(stacks), "\n\n") {
		if strings.Contains(stack, frame) {
			count++
		}
	}
	return count
}

const semaphoreWait = "semaphore.(*Weighted).Acquire"

var generatedRequestID = regexp.MustCompile(`^[0-9a-f]{32}$`)

func searchBody(user string) SearchRequest {
	return SearchRequest{Query: "Caroline support group", UserID: user, TopK: 2}
}

func withAuth(headers ...string) []string {
	return append([]string{"Authorization", "Bearer " + testKey}, headers...)
}

// A request's id comes from X-Request-Id, or is made, is echoed in the
// response header and is on every line the request logs, those its handling
// logs deep inside as well as its own.
func TestRequestIDEchoedAndLogged(t *testing.T) {
	h := newHarness(t, nil)
	addSessions(t, h, "u1", 3)
	// A failing judge makes the Search log a line of its own (degraded).
	h.judge.fail.Store(true)

	given := h.call("POST", "/search", searchBody("u1"), withAuth("X-Request-Id", "trace-42")...)
	if given.Code != http.StatusOK || given.Header().Get("X-Request-Id") != "trace-42" {
		t.Fatal(given.Code, given.Header())
	}
	lines := h.logLines(" request_id=trace-42")
	if len(lines) != 2 || !strings.Contains(lines[0], `msg="degraded search 1 (user `+userKey("u1")[:8]+`)`) || !strings.Contains(lines[1], `msg="POST /search 200 `) {
		t.Fatal(lines, h.log.String())
	}

	// Without one, each request gets its own: 16 random bytes in hex.
	made := h.call("POST", "/search", searchBody("u1"))
	other := h.call("POST", "/search", searchBody("u1"))
	id, otherID := made.Header().Get("X-Request-Id"), other.Header().Get("X-Request-Id")
	if !generatedRequestID.MatchString(id) || !generatedRequestID.MatchString(otherID) || id == otherID {
		t.Fatal(id, otherID)
	}
	for _, id := range []string{id, otherID} {
		if lines := h.logLines(" request_id=" + id); len(lines) != 2 {
			t.Fatal(id, lines, h.log.String())
		}
	}
	// Every line so far is a request's: each carries an id.
	for _, line := range h.logLines() {
		if !strings.Contains(line, " request_id=") {
			t.Fatal(line)
		}
	}

	// Every answer carries one: errors, health checks and unknown paths too.
	for name, response := range map[string]*httptest.ResponseRecorder{
		"health":       h.call("GET", "/health", nil, "X-None", ""),
		"root":         h.call("GET", "/", nil, "X-None", ""),
		"not found":    h.call("GET", "/nope", nil),
		"unauthorized": h.call("POST", "/search", searchBody("u1"), "X-None", ""),
		"bad body":     h.call("POST", "/add", "{", withAuth()...),
	} {
		if got := response.Header().Get("X-Request-Id"); !generatedRequestID.MatchString(got) {
			t.Fatal(name, response.Code, got)
		}
	}
	// A health check is not logged, even with an id of its own.
	if lines := h.logLines("GET /health"); len(lines) != 0 {
		t.Fatal(lines)
	}
}

// An id that is not 1 to 128 printable ASCII characters is not echoed (it
// could split a header or a log line): the server makes one.
func TestRequestIDRejectsUnusableIDs(t *testing.T) {
	h := newHarness(t, nil)
	accepted := map[string]string{
		"one character":        "x",
		"the longest":          strings.Repeat("a", 128),
		"punctuation":          `a"b\c=d`,
		"the printable bounds": "!~",
	}
	for name, id := range accepted {
		response := h.call("GET", "/nope", nil, withAuth("X-Request-Id", id)...)
		if got := response.Header().Get("X-Request-Id"); got != id {
			t.Fatal(name, "not accepted:", got)
		}
		// The id is quoted where the line needs it: the field stays one.
		if lines := h.logLines("request_id="+quotedIfNeeded(id), "GET /nope 404"); len(lines) != 1 {
			t.Fatal(name, h.log.String())
		}
	}
	rejected := map[string]string{
		"too long":    strings.Repeat("a", 129),
		"a space":     "has space",
		"a tab":       "tab\there",
		"a newline":   "line\nbreak",
		"a carriage":  "ret\rurn",
		"a null":      "nul\x00",
		"a delete":    "del\x7f",
		"non-ASCII":   "café",
		"only spaces": "   ",
	}
	for name, id := range rejected {
		response := h.call("GET", "/nope", nil, withAuth("X-Request-Id", id)...)
		if got := response.Header().Get("X-Request-Id"); !generatedRequestID.MatchString(got) {
			t.Fatal(name, "echoed:", got)
		}
	}
	// (Not "caf": made of hex digits, a made id may contain it.)
	for _, text := range []string{"has space", "break", "\u00e9", "ret"} {
		if strings.Contains(h.log.String(), text) {
			t.Fatal("a rejected id reached the log:", text, h.log.String())
		}
	}
}

// quotedIfNeeded is how the text handler writes id as an attribute value.
func quotedIfNeeded(id string) string {
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
		if len(groups) == 0 && (attr.Key == slog.TimeKey || attr.Key == slog.LevelKey || attr.Key == slog.MessageKey) {
			return slog.Attr{}
		}
		return attr
	}})).Info("", "request_id", id)
	return strings.TrimPrefix(strings.TrimSpace(buf.String()), "request_id=")
}

// A logger the server derives (With, WithGroup) keeps adding the request id.
func TestContextHandlerAddsTheRequestIDToDerivedLoggers(t *testing.T) {
	var out bytes.Buffer
	logger := slog.New(contextHandler{slog.NewTextHandler(&out, nil)}).With("component", "aml")
	logger.InfoContext(withRequestID(context.Background(), "abc"), "with an id")
	logger.Info("without one")
	logger.WithGroup("g").InfoContext(withRequestID(context.Background(), "def"), "in a group")
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 ||
		!strings.Contains(lines[0], "component=aml") || !strings.HasSuffix(lines[0], " request_id=abc") ||
		strings.Contains(lines[1], "request_id") || !strings.Contains(lines[1], "component=aml") ||
		!strings.Contains(lines[2], "request_id=def") {
		t.Fatal(out.String())
	}
	if requestID(context.Background()) != "" || requestID(withRequestID(context.Background(), "x")) != "x" {
		t.Fatal("requestID")
	}
}

// A Search past the limit waits for a slot, then is shed with 503 and a
// Retry-After, which the platform retries; the limit is the Searches' alone.
func TestSearchShedsWith503(t *testing.T) {
	h := newHarness(t, func(config *ServerConfig) { config.MaxConcurrentSearches = 1 })
	h.server.admitWait = 100 * time.Millisecond
	addSessions(t, h, "u1", 2)
	h.judge.block.Store(true)
	// The first Search takes the only slot, and blocks in the judge until
	// its client leaves.
	leave, left := context.WithCancel(context.Background())
	defer left()
	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- h.callContext(leave, "POST", "/search", searchBody("u1"), withAuth()...) }()
	eventually(t, func() bool { return h.judge.blocked.Load() > 0 })

	start := time.Now()
	shed := h.call("POST", "/search", searchBody("u1"), withAuth("X-Request-Id", "shed-1")...)
	if waited := time.Since(start); shed.Code != http.StatusServiceUnavailable || shed.Header().Get("Retry-After") != "5" ||
		errorBody(t, shed) != "busy; retry" || waited < 100*time.Millisecond || waited > 5*time.Second {
		t.Fatal(shed.Code, shed.Header(), shed.Body.String(), waited)
	}
	if shed.Header().Get("X-Request-Id") != "shed-1" || len(h.logLines(" request_id=shed-1", "search shed: no free slot within 100ms (limit 1)")) != 1 ||
		len(h.logLines(" request_id=shed-1", "POST /search 503 ")) != 1 {
		t.Fatal(h.log.String())
	}
	// The shed Search was not served, and nothing else was shed: Adds have
	// their own gate (none here), health checks none.
	h.mustAdd(t, addRequest("r9", "u2"))
	if response := h.call("GET", "/health", nil, "X-None", ""); response.Code != http.StatusOK {
		t.Fatal(response.Code)
	}
	if h.server.metrics.searches.Load() != 0 || h.server.DegradedSearches() != 0 {
		t.Fatal(h.server.metrics.searches.Load(), h.server.DegradedSearches())
	}

	// The client of the first leaves: its slot is free again.
	left()
	if response := <-first; response.Code != http.StatusServiceUnavailable || errorBody(t, response) != "request canceled" {
		t.Fatal(response.Code, response.Body.String())
	}
	h.judge.block.Store(false)
	if items := h.search(t, searchBody("u1")); len(items) == 0 {
		t.Fatal("no items", h.log.String())
	}
	// Nothing is left waiting for a slot, or in a handler.
	eventually(t, func() bool { return goroutinesIn(semaphoreWait) == 0 })
}

// The wait is queueing, not shedding: a Search that finds the slot taken is
// admitted once it frees, in time.
func TestQueuedSearchIsAdmittedWhenASlotFrees(t *testing.T) {
	h := newHarness(t, func(config *ServerConfig) { config.MaxConcurrentSearches = 1 })
	addSessions(t, h, "u1", 2)
	h.judge.block.Store(true)
	leave, left := context.WithCancel(context.Background())
	defer left()
	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- h.callContext(leave, "POST", "/search", searchBody("u1"), withAuth()...) }()
	eventually(t, func() bool { return h.judge.blocked.Load() > 0 })
	h.judge.block.Store(false)

	second := make(chan *httptest.ResponseRecorder, 1)
	go func() { second <- h.call("POST", "/search", searchBody("u1")) }()
	eventually(t, func() bool { return goroutinesIn(semaphoreWait) == 1 })
	select {
	case response := <-second:
		t.Fatal("admitted past the limit", response.Code)
	case <-time.After(50 * time.Millisecond):
	}
	left()
	<-first
	select {
	case response := <-second:
		var body SearchResponse
		if json.Unmarshal(response.Body.Bytes(), &body); response.Code != http.StatusOK || len(body.Data) == 0 {
			t.Fatal(response.Code, response.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the queued Search was not admitted")
	}
	eventually(t, func() bool { return goroutinesIn(semaphoreWait) == 0 })
}

// A request that stops waiting because its client left (or CancelRequests
// ended its context) is not shed: no Retry-After, no shed line, no slot kept.
func TestRequestThatLeavesWhileWaitingIsNotShed(t *testing.T) {
	h := newHarness(t, func(config *ServerConfig) { config.MaxConcurrentSearches = 1 })
	addSessions(t, h, "u1", 2)
	h.judge.block.Store(true)
	leave, left := context.WithCancel(context.Background())
	defer left()
	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- h.callContext(leave, "POST", "/search", searchBody("u1"), withAuth()...) }()
	eventually(t, func() bool { return h.judge.blocked.Load() > 0 })

	gone, goes := context.WithCancel(context.Background())
	second := make(chan *httptest.ResponseRecorder, 1)
	go func() { second <- h.callContext(gone, "POST", "/search", searchBody("u1"), withAuth()...) }()
	eventually(t, func() bool { return goroutinesIn(semaphoreWait) == 1 })
	goes()
	select {
	case response := <-second:
		if response.Code != http.StatusServiceUnavailable || errorBody(t, response) != "request canceled" || response.Header().Get("Retry-After") != "" {
			t.Fatal(response.Code, response.Header(), response.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a request that left kept waiting")
	}
	if shed := h.logLines("shed"); len(shed) != 0 {
		t.Fatal(shed)
	}
	eventually(t, func() bool { return goroutinesIn(semaphoreWait) == 0 })
	left()
	<-first
}

// An Add past its limit is shed the same way, before it touches a store; the
// Searches' gate is not the Adds'.
func TestAddShedsWith503(t *testing.T) {
	h := newHarness(t, func(config *ServerConfig) {
		config.MemoryConfig.Consolidation = &memoryagent.ConsolidationConfig{Mode: "timelines", BatchCharacters: 100}
		config.ConsolidateOnAdd = true
		config.MaxConcurrentAdds = 1
	})
	h.server.admitWait = 100 * time.Millisecond
	h.consolidator.answer(func(ctx context.Context, _ string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	// The first Add is stored, then consolidates until its client leaves.
	leave, left := context.WithCancel(context.Background())
	defer left()
	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- h.callContext(leave, "POST", "/add", addRequest("r1", "u1"), withAuth()...) }()
	eventually(t, func() bool { return h.consolidator.calls.Load() > 0 })

	shed := h.call("POST", "/add", addRequest("r2", "u2"))
	if shed.Code != http.StatusServiceUnavailable || shed.Header().Get("Retry-After") != "5" || errorBody(t, shed) != "busy; retry" {
		t.Fatal(shed.Code, shed.Header(), shed.Body.String())
	}
	if len(h.logLines("add shed: no free slot within 100ms (limit 1)")) != 1 {
		t.Fatal(h.log.String())
	}
	if _, err := os.Stat(filepath.Join(h.root, "users", userKey("u2"))); !os.IsNotExist(err) {
		t.Fatal("a shed Add touched a store", err)
	}
	// Searches are not held up by a full Add gate.
	if response := h.call("POST", "/search", SearchRequest{Query: "anything", UserID: "nobody"}); response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}

	left()
	if response := <-first; response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}
	h.consolidator.answer(func(context.Context, string) (string, error) {
		return `{"events":[{"thread":"new: Group","date":"2023/05/08","text":"Caroline went.","records":["r0"]}]}`, nil
	})
	h.mustAdd(t, addRequest("r2", "u2"))
	eventually(t, func() bool { return goroutinesIn(semaphoreWait) == 0 })
}

// No limit is the default: nothing is admitted or shed, and a negative one
// is refused (TestNewServerValidates).
func TestNoLimitByDefault(t *testing.T) {
	h := newHarness(t, nil)
	if h.server.addGate != nil || h.server.searchGate != nil {
		t.Fatal("a gate without a limit")
	}
	limited := newHarness(t, func(config *ServerConfig) { config.MaxConcurrentAdds, config.MaxConcurrentSearches = 3, 5 })
	if limited.server.addGate.limit != 3 || limited.server.searchGate.limit != 5 {
		t.Fatal(limited.server.addGate, limited.server.searchGate)
	}
}

// serveOver serves h's HTTPServer on a loopback port and returns its URL.
func serveOver(t *testing.T, h *harness) (*http.Server, string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := h.server.HTTPServer(listener.Addr().String())
	served := make(chan struct{})
	go func() { server.Serve(listener); close(served) }()
	t.Cleanup(func() { server.Close(); <-served })
	return server, "http://" + listener.Addr().String()
}

// CancelRequests ends the context of every request the HTTP server runs: a
// Search blocked in a model returns, with 503, instead of running to its
// timeout.
func TestCancelRequestsStopsHandlers(t *testing.T) {
	h := newHarness(t, nil)
	addSessions(t, h, "u1", 2)
	server, url := serveOver(t, h)
	base := server.BaseContext(nil)
	if base.Err() != nil {
		t.Fatal("the base context is canceled at start")
	}
	h.judge.block.Store(true)
	type result struct {
		code int
		body string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		raw, _ := json.Marshal(searchBody("u1"))
		request, _ := http.NewRequest("POST", url+"/search", bytes.NewReader(raw))
		request.Header.Set("Authorization", "Bearer "+testKey)
		response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
		if err != nil {
			done <- result{err: err}
			return
		}
		defer response.Body.Close()
		var buf bytes.Buffer
		buf.ReadFrom(response.Body)
		done <- result{code: response.StatusCode, body: buf.String()}
	}()
	eventually(t, func() bool { return h.judge.blocked.Load() > 0 })
	select {
	case got := <-done:
		t.Fatal("answered before CancelRequests", got)
	case <-time.After(50 * time.Millisecond):
	}
	h.server.CancelRequests()
	select {
	case got := <-done:
		if got.err != nil || got.code != http.StatusServiceUnavailable || !strings.Contains(got.body, "request canceled") {
			t.Fatal(got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the Search outlived CancelRequests")
	}
	if base.Err() == nil || h.server.DegradedSearches() != 0 {
		t.Fatal("base context", base.Err(), h.server.DegradedSearches())
	}
	// The handler has returned: nothing is left running in it.
	eventually(t, func() bool { return goroutinesIn("(*Server).search(") == 0 })
	h.server.CancelRequests() // idempotent
}

// A request that arrives after CancelRequests (a connection accepted before
// the listener closed) is canceled at once.
func TestCancelRequestsCancelsLaterRequests(t *testing.T) {
	h := newHarness(t, nil)
	addSessions(t, h, "u1", 1)
	_, url := serveOver(t, h)
	h.server.CancelRequests()
	raw, _ := json.Marshal(searchBody("u1"))
	request, _ := http.NewRequest("POST", url+"/search", bytes.NewReader(raw))
	request.Header.Set("Authorization", "Bearer "+testKey)
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable || response.Header.Get("X-Request-Id") == "" {
		t.Fatal(response.StatusCode, response.Header)
	}
}

// A handler its context stopped (the server's forced shutdown, a client that
// left) answers a retryable 503, not a 500: the work can be tried again.
func TestCanceledRequestsAnswerRetryable(t *testing.T) {
	h := newHarness(t, nil)
	for _, err := range []error{context.Canceled, fmt.Errorf("put: %w", context.Canceled), context.DeadlineExceeded} {
		recorder := httptest.NewRecorder()
		h.server.fail(context.Background(), recorder, "write", userKey("u1"), err)
		if recorder.Code != http.StatusServiceUnavailable {
			t.Fatal(err, recorder.Code)
		}
	}
	recorder := httptest.NewRecorder()
	h.server.fail(context.Background(), recorder, "write", userKey("u1"), errors.New("disk full"))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatal(recorder.Code)
	}
}

// A user's store reports what its runtime cannot return (a lexical build that
// fails) to the server's logger, naming the user, not to standard error.
func TestUserStoreReportsThroughTheServerLogger(t *testing.T) {
	h := newHarness(t, nil)
	h.mustAdd(t, addRequest("r1", "u1"))
	// Break the index from outside: the next write wakes the worker, which
	// can no longer build.
	db, err := sql.Open("sqlite", "file:"+filepath.Join(h.root, "users", userKey("u1"), "records.db")+"?_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DROP TABLE lexical_documents"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	h.mustAdd(t, addRequest("r2", "u1"))
	eventually(t, func() bool {
		return len(h.logLines("level=WARN", `msg="records query preparation: `, "(user "+userKey("u1")[:8]+`)"`)) > 0
	})
}
