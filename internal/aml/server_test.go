package aml

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
	"github.com/mnemon-dev/mnemon/internal/records"
)

const testKey = "k"

// fakePlanner answers the simple strategy's cue plan, need plan and rewrite,
// and records every prompt it reads.
type fakePlanner struct {
	mu      sync.Mutex
	prompts []string
}

func (p *fakePlanner) Complete(_ context.Context, system, prompt string) (string, memoryagent.Usage, error) {
	p.mu.Lock()
	p.prompts = append(p.prompts, prompt)
	p.mu.Unlock()
	switch {
	case strings.HasPrefix(system, "You plan searches in the notes"):
		return "Caroline support group\nNOTE: Caroline went to the LGBTQ support group", memoryagent.Usage{}, nil
	case strings.Contains(prompt, "NOT FOUND YET"):
		return "Caroline group", memoryagent.Usage{}, nil
	default:
		return "NEED: when Caroline went to the support group", memoryagent.Usage{}, nil
	}
}

func (p *fakePlanner) seen() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.prompts)
}

// fakeJudge calls everything needed and nothing superseded, or fails.
type fakeJudge struct{ fail atomic.Bool }

func (j *fakeJudge) Decide(_ context.Context, _ any, questions map[string]memoryagent.Question) (map[string]float64, memoryagent.Usage, error) {
	if j.fail.Load() {
		return nil, memoryagent.Usage{}, errors.New("judge unavailable")
	}
	out := map[string]float64{}
	for key := range questions {
		out[key] = 0.9
		if strings.HasPrefix(key, "s") {
			out[key] = 0
		}
	}
	return out, memoryagent.Usage{}, nil
}

// fakeConsolidator folds every batch into one event on one thread, linked
// to the batch's first record.
type fakeConsolidator struct{ calls atomic.Int64 }

func (c *fakeConsolidator) Complete(context.Context, string, string) (string, memoryagent.Usage, error) {
	c.calls.Add(1)
	return `{"events":[{"thread":"new: Support group","date":"2023/05/08","text":"Caroline went to the LGBTQ support group.","records":["r0"]}]}`, memoryagent.Usage{}, nil
}

// syncBuffer is a log destination the server and the test share.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type harness struct {
	server       *Server
	root         string
	planner      *fakePlanner
	judge        *fakeJudge
	consolidator *fakeConsolidator
	log          *syncBuffer
}

func simpleConfig() memoryagent.Config {
	return memoryagent.Config{Version: 1, Strategy: "simple", View: &memoryagent.ViewConfig{MaxItems: 100, MaxCharacters: 240000, Fusion: "rrf"}}
}

func newHarness(t *testing.T, edit func(*ServerConfig)) *harness {
	t.Helper()
	h := &harness{root: t.TempDir(), planner: &fakePlanner{}, judge: &fakeJudge{}, consolidator: &fakeConsolidator{}, log: &syncBuffer{}}
	config := ServerConfig{
		DataRoot:     h.root,
		MemoryConfig: simpleConfig(),
		AuthKey:      testKey,
		Models: func(memoryagent.Config) (memoryagent.Models, error) {
			return memoryagent.Models{Planner: h.planner, Judge: h.judge, Consolidator: h.consolidator}, nil
		},
		Logger: log.New(h.log, "", 0),
	}
	if edit != nil {
		edit(&config)
	}
	server, err := NewServer(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	h.server = server
	return h
}

// call sends one request through the handler with the test key (unless
// headers say otherwise) and returns the recorded response.
func (h *harness) call(method, path string, body any, headers ...string) *httptest.ResponseRecorder {
	var reader *bytes.Reader
	switch value := body.(type) {
	case nil:
		reader = bytes.NewReader(nil)
	case string:
		reader = bytes.NewReader([]byte(value))
	case []byte:
		reader = bytes.NewReader(value)
	default:
		raw, _ := json.Marshal(value)
		reader = bytes.NewReader(raw)
	}
	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Content-Type", "application/json")
	if len(headers) == 0 {
		headers = []string{"Authorization", "Bearer " + testKey}
	}
	for i := 0; i+1 < len(headers); i += 2 {
		if headers[i+1] != "" {
			request.Header.Set(headers[i], headers[i+1])
		}
	}
	recorder := httptest.NewRecorder()
	h.server.Handler().ServeHTTP(recorder, request)
	return recorder
}

func at(minute int) *int64 {
	ms := time.Date(2023, 5, 8, 13, 56+minute, 0, 0, time.UTC).UnixMilli()
	return &ms
}

func addRequest(requestID, userID string) AddRequest {
	return AddRequest{RequestID: requestID, UserID: userID, SessionID: "s1", Messages: []Message{
		{Role: "user", Content: "I went to the LGBTQ support group yesterday, Caroline here.", Timestamp: at(0)},
		{Role: "assistant", Content: "That sounds meaningful. How did it feel?", Timestamp: at(1)},
		{Role: "user", Content: "It was powerful; everyone shared their stories.", Timestamp: at(2)},
	}}
}

func (h *harness) mustAdd(t *testing.T, req AddRequest) {
	t.Helper()
	if response := h.call("POST", "/add", req); response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}
}

func (h *harness) search(t *testing.T, req SearchRequest) []SearchItem {
	t.Helper()
	response := h.call("POST", "/search", req)
	if response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}
	var body struct {
		Data []SearchItem `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Data == nil {
		t.Fatal(response.Body.String(), err)
	}
	return body.Data
}

func errorBody(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Error == "" {
		t.Fatalf("not a JSON error: %d %q", response.Code, response.Body.String())
	}
	return body.Error
}

// documents counts the conversation documents stored for userID.
func documents(t *testing.T, root, userID string) int {
	t.Helper()
	store, err := records.Open(filepath.Join(root, "users", userKey(userID)), true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot, err := store.Get(context.Background(), records.GetRequest{})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, raw := range snapshot.Records {
		if strings.Contains(raw, `"kind":"memory-document"`) {
			n++
		}
	}
	return n
}

func TestHealthUnauthenticated(t *testing.T) {
	h := newHarness(t, nil)
	for _, path := range []string{"/health", "/"} {
		if response := h.call("GET", path, nil, "X-None", ""); response.Code != http.StatusOK {
			t.Fatal(path, response.Code)
		}
	}
	for _, auth := range []string{"", "Bearer " + testKey} {
		response := h.call("GET", "/nope", nil, "Authorization", auth)
		if response.Code != http.StatusNotFound {
			t.Fatal(response.Code)
		}
		errorBody(t, response)
	}
	if response := h.call("GET", "/add", nil); response.Code != http.StatusNotFound {
		t.Fatal("GET /add", response.Code)
	}
}

func TestAuthRequired(t *testing.T) {
	h := newHarness(t, nil)
	search := SearchRequest{Query: "anything?", UserID: "nobody", TopK: 5}
	for _, headers := range [][]string{{"X-None", ""}, {"Authorization", "Bearer nope"}, {"Authorization", "Basic " + testKey}, {"X-Api-Key", "kk"}, {"Authorization", testKey}} {
		response := h.call("POST", "/search", search, headers...)
		if response.Code != http.StatusUnauthorized {
			t.Fatal(headers, response.Code)
		}
		errorBody(t, response)
	}
	if response := h.call("POST", "/add", addRequest("r1", "u1"), "X-None", ""); response.Code != http.StatusUnauthorized {
		t.Fatal(response.Code)
	}
	for _, headers := range [][]string{{"Authorization", "Bearer " + testKey}, {"Authorization", "Token " + testKey}, {"X-Api-Key", testKey}, {"Authorization", "bearer " + testKey}} {
		response := h.call("POST", "/search", search, headers...)
		if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != `{"data":[]}` {
			t.Fatal(headers, response.Code, response.Body.String())
		}
	}
}

func TestAddValidation(t *testing.T) {
	h := newHarness(t, nil)
	valid := func() map[string]any {
		return map[string]any{"request_id": "r1", "user_id": "u1", "session_id": "s1", "messages": []any{map[string]any{"role": "user", "content": "hello", "timestamp": 1683554160000}}}
	}
	// Only what identifies and isolates the Add is required.
	cases := map[string]func(map[string]any){
		"missing request_id": func(m map[string]any) { delete(m, "request_id") },
		"blank request_id":   func(m map[string]any) { m["request_id"] = "  " },
		"object request_id":  func(m map[string]any) { m["request_id"] = map[string]any{"id": 1} },
		"empty user_id":      func(m map[string]any) { m["user_id"] = "" },
		"missing user_id":    func(m map[string]any) { delete(m, "user_id") },
		"empty session_id":   func(m map[string]any) { m["session_id"] = "" },
	}
	for name, edit := range cases {
		body := valid()
		edit(body)
		response := h.call("POST", "/add", body)
		if response.Code != http.StatusBadRequest {
			t.Fatal(name, response.Code, response.Body.String())
		}
		errorBody(t, response)
	}
	for _, malformed := range []string{`{"request_id":`, `[]`, `"text"`} {
		if response := h.call("POST", "/add", malformed); response.Code != http.StatusBadRequest {
			t.Fatal("malformed JSON", malformed, response.Code)
		}
	}
	if response := h.call("POST", "/add", `{"request_id":"r","user_id":"u","session_id":"s","messages":[{"role":"user","content":"`+strings.Repeat("x", 30<<20)+`"}]}`); response.Code != http.StatusRequestEntityTooLarge {
		t.Fatal("over 30 MiB", response.Code)
	}
	if entries, _ := os.ReadDir(h.root); len(entries) != 0 {
		t.Fatal("a rejected Add created", entries)
	}
	// A fractional or null timestamp is accepted.
	body := valid()
	body["messages"] = []any{map[string]any{"role": "user", "content": "x", "timestamp": 1683554160000.5}, map[string]any{"role": "assistant", "content": "y", "timestamp": nil}}
	if response := h.call("POST", "/add", body); response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}

	for name, search := range map[string]string{
		"top_k 0":       `{"query":"q","user_id":"u1","top_k":0}`,
		"missing top_k": `{"query":"q","user_id":"u1"}`,
		"negative":      `{"query":"q","user_id":"u1","top_k":-1}`,
		"empty query":   `{"query":" ","user_id":"u1","top_k":5}`,
		"empty user":    `{"query":"q","user_id":"","top_k":5}`,
		"word top_k":    `{"query":"q","user_id":"u1","top_k":"many"}`,
		"fraction":      `{"query":"q","user_id":"u1","top_k":0.5}`,
	} {
		response := h.call("POST", "/search", search)
		if response.Code != http.StatusBadRequest {
			t.Fatal(name, response.Code)
		}
		errorBody(t, response)
	}
}

// storedParents lists the conversation documents of userID, id to content.
func storedParents(t *testing.T, root, userID string) map[string]string {
	t.Helper()
	store, err := records.Open(filepath.Join(root, "users", userKey(userID)), true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot, err := store.Get(context.Background(), records.GetRequest{})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, raw := range snapshot.Records {
		var value struct{ ID, Kind, Content string }
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			t.Fatal(err)
		}
		if value.Kind == "memory-document" {
			out[value.ID] = value.Content
		}
	}
	return out
}

func TestAddContentShapes(t *testing.T) {
	h := newHarness(t, nil)
	// Empty and whitespace-only messages are skipped, multimodal parts give
	// their text, timestamps that are not numbers are ignored, a missing role
	// is "unknown"; nothing about the content is a 400.
	body := `{"request_id":"r1","user_id":"u1","session_id":"s1","messages":[
		{"role":"user","content":"   "},
		{"role":"assistant","content":""},
		{"role":"user"},
		"not a message",
		{"role":"user","content":[{"type":"text","text":"Caroline painted"},{"type":"image_url","image_url":{"url":"http://x"}},{"type":"text","text":"a sunset."}]},
		{"role":"assistant","content":[{"type":"image_url","image_url":{"url":"http://x"}}]},
		{"role":"user","content":"Real text","timestamp":"1683554160000"},
		{"role":"user","content":"Later","timestamp":"soon"},
		{"role":"user","content":"Again","timestamp":true},
		{"role":"","content":"no role"},
		{"role":"assistant","content":42}]}`
	if response := h.call("POST", "/add", body); response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}
	// Nothing left to store: success without a store, also on retry.
	for _, empty := range []string{
		`{"request_id":"r2","user_id":"u2","session_id":"s1","messages":[{"role":"user","content":" \n "},{"role":"user","content":[{"type":"image_url","image_url":{"url":"x"}}]}]}`,
		`{"request_id":"r3","user_id":"u2","session_id":"s1","messages":[]}`,
		`{"request_id":"r4","user_id":"u2","session_id":"s1"}`,
		`{"request_id":"r2","user_id":"u2","session_id":"s1","messages":[{"role":"user","content":""}]}`,
	} {
		response := h.call("POST", "/add", empty)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"success":true`) {
			t.Fatal(empty, response.Code, response.Body.String())
		}
	}
	if _, err := os.Stat(filepath.Join(h.root, "users", userKey("u2"))); !os.IsNotExist(err) {
		t.Fatal("an Add without text created a store", err)
	}
	h.server.Close()
	want := "Conversation s1 on 2023/05/08 (Mon) 13:56:\nuser: Caroline painted a sunset.\nuser: Real text\nuser: Later\nuser: Again\nunknown: no role\nassistant: 42"
	if parents := storedParents(t, h.root, "u1"); len(parents) != 1 || parents[documentID("r1")] != want {
		t.Fatalf("%q", parents)
	}
}

func TestAddCoercesNumericIDs(t *testing.T) {
	h := newHarness(t, nil)
	response := h.call("POST", "/add", `{"request_id":12345,"user_id":42,"session_id":7,"messages":[{"role":"user","content":"Caroline went to the LGBTQ support group."}]}`)
	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != `{"success":true,"request_id":"12345","user_id":"42","session_id":"7"}` {
		t.Fatal(response.Code, response.Body.String())
	}
	for _, search := range []string{`{"query":"Caroline support group","user_id":42,"top_k":5}`, `{"query":"Caroline support group","user_id":"42","top_k":5}`} {
		response := h.call("POST", "/search", search)
		var body SearchResponse
		json.Unmarshal(response.Body.Bytes(), &body)
		if response.Code != http.StatusOK || len(body.Data) != 1 || !strings.HasPrefix(body.Data[0].Content, "Conversation 7:\nuser: Caroline") || !strings.HasPrefix(body.Data[0].ID, documentID("12345")) {
			t.Fatal(search, response.Code, response.Body.String())
		}
	}
}

func TestAddSanitizesSessionID(t *testing.T) {
	h := newHarness(t, nil)
	session := "a b\nc\td" + strings.Repeat("é", 150) // 307 bytes
	req := AddRequest{RequestID: "r1", UserID: "u1", SessionID: session, Messages: []Message{{Role: "user", Content: "hello"}}}
	response := h.call("POST", "/add", req)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"session_id":"a b\nc\td`) {
		t.Fatal(response.Code, response.Body.String())
	}
	h.mustAdd(t, req) // the same sanitized document: a retry
	h.server.Close()
	// Whitespace and controls become "_", then 200 bytes at a rune boundary.
	want := "Conversation a_b_c_d" + strings.Repeat("é", 96) + ":\nuser: hello"
	if parents := storedParents(t, h.root, "u1"); len(parents) != 1 || parents[documentID("r1")] != want {
		t.Fatalf("%q", parents)
	}
}

func hexSHA256(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// words is n distinct words "<prefix><5 digits>" joined by spaces.
func words(prefix string, n int) string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s%05d", prefix, i)
	}
	return strings.Join(out, " ")
}

// storedWords counts the words of the stored documents that start with
// prefix, and fails on any that was cut.
func storedWords(t *testing.T, parents map[string]string, prefix string) int {
	t.Helper()
	seen := map[string]bool{}
	for _, content := range parents {
		for _, word := range strings.Fields(content) {
			if !strings.HasPrefix(word, prefix) {
				continue
			}
			if len(word) != len(prefix)+5 || seen[word] {
				t.Fatalf("word %q cut or repeated", word)
			}
			seen[word] = true
		}
	}
	return len(seen)
}

func TestAddSplitsOverLimit(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	// 3 × 40 kB of text: more than one document holds (100 kB), so parts of
	// at most 90 kB in message order: [m1 m2] and [m3].
	req := AddRequest{RequestID: "big", UserID: "u1", SessionID: "s1", Messages: []Message{
		{Role: "user", Content: words("ka", 5000), Timestamp: at(0)},
		{Role: "assistant", Content: words("kb", 5000), Timestamp: at(1)},
		{Role: "user", Content: words("kc", 5000), Timestamp: at(2)},
	}}
	h.mustAdd(t, req)
	h.mustAdd(t, req) // retry: every part exists and is equal
	second := partID("big", 1)
	if second == documentID("big") || second != "aml-"+hexSHA256("big\x001")[:40] {
		t.Fatal(second)
	}
	// A retry writes the parts an earlier attempt did not.
	user, err := h.server.users.acquire(ctx, "u1", false)
	if err != nil || user == nil {
		t.Fatal(err)
	}
	snapshot, err := user.store.Get(ctx, records.GetRequest{})
	if err != nil {
		t.Fatal(err)
	}
	gone := []string{}
	for _, raw := range snapshot.Records {
		var value struct{ ID string }
		json.Unmarshal([]byte(raw), &value)
		if strings.HasPrefix(value.ID, second) {
			gone = append(gone, value.ID)
		}
	}
	if _, err := user.store.Put(ctx, records.PutRequest{Delete: gone}); err != nil || len(gone) < 2 {
		t.Fatal(gone, err)
	}
	h.server.users.release(user)
	h.mustAdd(t, req)
	// Another payload in any part is a reused request_id.
	changed := req
	changed.Messages = slices.Clone(req.Messages)
	changed.Messages[2].Content = words("kd", 5000)
	if response := h.call("POST", "/add", changed); response.Code != http.StatusBadRequest || !strings.Contains(errorBody(t, response), "request_id") {
		t.Fatal(response.Code, response.Body.String())
	}

	// One message longer than a part: cut at whitespace into same-role,
	// same-time messages, 90 kB at most each. ("z " moves the words off the
	// 90 kB boundary, so a cut there would split one.)
	huge := AddRequest{RequestID: "huge", UserID: "u2", SessionID: "s1", Messages: []Message{{Role: "user", Content: "z " + words("kw", 30000), Timestamp: at(0)}}}
	h.mustAdd(t, huge)
	// Text without whitespace is cut at a UTF-8 boundary.
	cjk := AddRequest{RequestID: "cjk", UserID: "u3", SessionID: "s1", Messages: []Message{{Role: "user", Content: strings.Repeat("汉", 40000)}}}
	h.mustAdd(t, cjk)
	h.server.Close()

	parents := storedParents(t, h.root, "u1")
	if len(parents) != 2 || !strings.HasPrefix(parents[documentID("big")], "Conversation s1 on 2023/05/08 (Mon) 13:56:\nuser: ka00000 ") ||
		!strings.HasPrefix(parents[second], "Conversation s1 on 2023/05/08 (Mon) 13:58:\nuser: kc00000 ") || strings.Contains(parents[second], "kb04999") {
		t.Fatal(len(parents))
	}
	if storedWords(t, parents, "ka") != 5000 || storedWords(t, parents, "kb") != 5000 || storedWords(t, parents, "kc") != 5000 {
		t.Fatal("words lost")
	}
	parents = storedParents(t, h.root, "u2")
	if len(parents) != 3 || storedWords(t, parents, "kw") != 30000 {
		t.Fatal(len(parents))
	}
	for id, content := range parents {
		if len(content) > 110000 || !strings.HasPrefix(content, "Conversation s1 on 2023/05/08 (Mon) 13:56:\nuser: ") {
			t.Fatal(id, len(content))
		}
	}
	parents = storedParents(t, h.root, "u3")
	total := 0
	for _, content := range parents {
		if !utf8.ValidString(content) {
			t.Fatal("invalid UTF-8")
		}
		total += strings.Count(content, "汉")
	}
	if len(parents) != 2 || total != 40000 {
		t.Fatal(len(parents), total)
	}
}

func TestAddMissingParentIsInternal(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	h.mustAdd(t, addRequest("seed", "u1"))
	user, err := h.server.users.acquire(ctx, "u1", false)
	if err != nil || user == nil {
		t.Fatal(err)
	}
	orphan := documentID("orphan") + "-00000"
	raw := `{"id":"` + orphan + `","kind":"note","title":"x","content":"x","scope":"global","state":"active","createdAt":"2026-10-20T00:00:00Z"}`
	if _, err := user.store.Put(ctx, records.PutRequest{Upsert: []records.Upsert{{RecordJSON: raw, SearchText: "x", VectorText: "x"}}}); err != nil {
		t.Fatal(err)
	}
	h.server.users.release(user)
	response := h.call("POST", "/add", addRequest("orphan", "u1"))
	if response.Code != http.StatusInternalServerError {
		t.Fatal(response.Code, response.Body.String())
	}
	errorBody(t, response)
}

func TestAddThenSearch(t *testing.T) {
	h := newHarness(t, nil)
	response := h.call("POST", "/add", addRequest("r1", "u1"))
	if response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}
	var echo map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &echo); err != nil || fmt.Sprint(echo) != fmt.Sprint(map[string]any{"success": true, "request_id": "r1", "user_id": "u1", "session_id": "s1"}) {
		t.Fatal(response.Body.String())
	}
	items := h.search(t, SearchRequest{Query: "When did Caroline go to the LGBTQ support group?", UserID: "u1", TopK: 100})
	if len(items) == 0 {
		t.Fatal("no items")
	}
	for _, item := range items {
		if !strings.HasPrefix(item.Content, "Conversation s1 on 2023/05/08 (Mon) 13:56:\nuser: I went to the LGBTQ support group") ||
			!strings.HasPrefix(item.ID, "aml-") || item.CreatedAt != "2023-05-08T13:56:00Z" || item.Score <= 0 {
			t.Fatal(item)
		}
	}
	if h.server.DegradedSearches() != 0 {
		t.Fatal(h.log.String())
	}
}

func TestAddIdempotentConcurrent(t *testing.T) {
	h := newHarness(t, nil)
	var wg sync.WaitGroup
	codes := make([]int, 4)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response := h.call("POST", "/add", addRequest("same", "u1"))
			codes[i] = response.Code
		}()
	}
	wg.Wait()
	if slices.ContainsFunc(codes, func(code int) bool { return code != http.StatusOK }) {
		t.Fatal(codes)
	}
	// A retry is a success; another payload under the same request_id is not.
	h.mustAdd(t, addRequest("same", "u1"))
	changed := addRequest("same", "u1")
	changed.Messages[0].Content = "Something else entirely."
	response := h.call("POST", "/add", changed)
	if response.Code != http.StatusBadRequest || !strings.Contains(errorBody(t, response), "request_id") {
		t.Fatal(response.Code, response.Body.String())
	}
	// The same request_id under another user is that user's own document.
	h.mustAdd(t, addRequest("same", "u2"))
	h.server.Close()
	if n := documents(t, h.root, "u1"); n != 1 {
		t.Fatal("documents", n)
	}
	if n := documents(t, h.root, "u2"); n != 1 {
		t.Fatal("documents", n)
	}
}

func TestUserIsolation(t *testing.T) {
	h := newHarness(t, nil)
	h.mustAdd(t, addRequest("r1", "alice"))
	if items := h.search(t, SearchRequest{Query: "Caroline support group", UserID: "bob", TopK: 100}); len(items) != 0 {
		t.Fatal(items)
	}
	bob := AddRequest{RequestID: "r1", UserID: "bob", SessionID: "s9", Messages: []Message{{Role: "user", Content: "Melanie painted a sunrise over the lake."}}}
	h.mustAdd(t, bob)
	for _, item := range h.search(t, SearchRequest{Query: "Caroline support group", UserID: "bob", TopK: 100}) {
		if strings.Contains(item.Content, "Caroline") {
			t.Fatal("bob reads alice", item)
		}
	}
	if items := h.search(t, SearchRequest{Query: "Caroline support group", UserID: "alice", TopK: 100}); len(items) == 0 {
		t.Fatal("alice lost her record")
	}
	entries, err := os.ReadDir(filepath.Join(h.root, "users"))
	if err != nil || len(entries) != 2 || entries[0].Name() == entries[1].Name() || len(entries[0].Name()) != 32 {
		t.Fatal(entries, err)
	}
}

func TestSearchUnknownUser(t *testing.T) {
	h := newHarness(t, nil)
	response := h.call("POST", "/search", SearchRequest{Query: "anything?", UserID: "ghost", TopK: 100})
	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != `{"data":[]}` {
		t.Fatal(response.Code, response.Body.String())
	}
	// top_k may arrive as an integral float or a numeric string.
	for _, body := range []string{`{"query":"q","user_id":"ghost","top_k":100.0}`, `{"query":"q","user_id":"ghost","top_k":"100"}`} {
		if response := h.call("POST", "/search", body); response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != `{"data":[]}` {
			t.Fatal(body, response.Code, response.Body.String())
		}
	}
	if entries, err := os.ReadDir(h.root); err != nil || len(entries) != 0 {
		t.Fatal("Search created", entries, err)
	}
}

func TestSearchTopKIncludesBlocks(t *testing.T) {
	h := newHarness(t, func(config *ServerConfig) {
		config.MemoryConfig.Consolidation = &memoryagent.ConsolidationConfig{Mode: "timelines"}
		config.ConsolidateOnAdd = true
	})
	h.mustAdd(t, addRequest("r1", "u1"))
	h.mustAdd(t, addRequest("r2", "u1"))
	// Each Add folds until caught up: the short rest needs a second call.
	if h.consolidator.calls.Load() == 0 {
		t.Fatal("no consolidation")
	}
	items := h.search(t, SearchRequest{Query: "When did Caroline go to the LGBTQ support group?", UserID: "u1", TopK: 3})
	if len(items) != 3 || items[0].ID != "index:2-topics" || items[1].ID != "index:3-timelines" || strings.HasPrefix(items[2].ID, "index:") {
		t.Fatal(items)
	}
	if !strings.Contains(items[1].Content, "Caroline went to the LGBTQ support group.") || items[0].Score != 0 || items[0].CreatedAt != "" || items[2].CreatedAt == "" {
		t.Fatal(items)
	}
	// Blocks count toward top_k.
	if items = h.search(t, SearchRequest{Query: "When did Caroline go to the LGBTQ support group?", UserID: "u1", TopK: 1}); len(items) != 1 || items[0].ID != "index:2-topics" {
		t.Fatal(items)
	}
}

func TestSearchDegradesOnModelFailure(t *testing.T) {
	h := newHarness(t, nil)
	for i := range 3 {
		req := addRequest(fmt.Sprintf("r%d", i), "u1")
		req.SessionID = fmt.Sprintf("s%d", i)
		h.mustAdd(t, req)
	}
	h.mustAdd(t, AddRequest{RequestID: "other", UserID: "u1", SessionID: "x", Messages: []Message{{Role: "user", Content: "Melanie painted a sunrise."}}})
	h.judge.fail.Store(true)
	items := h.search(t, SearchRequest{Query: "Caroline support group", UserID: "u1", TopK: 2})
	if len(items) != 2 {
		t.Fatal(items)
	}
	for _, item := range items {
		if !strings.Contains(item.Content, "Caroline") || strings.HasPrefix(item.ID, "index:") || item.CreatedAt != "2023-05-08T13:56:00Z" {
			t.Fatal(item)
		}
	}
	logged := h.log.String()
	if h.server.DegradedSearches() != 1 || !strings.Contains(logged, "degraded search 1") || strings.Contains(logged, "Caroline") {
		t.Fatal(h.server.DegradedSearches(), logged)
	}
}

func TestSearchOptionsJoinQuestion(t *testing.T) {
	h := newHarness(t, nil)
	h.mustAdd(t, addRequest("r1", "u1"))
	h.search(t, SearchRequest{Query: "Where did Caroline go?", Options: []string{"a", "b"}, UserID: "u1", TopK: 10})
	if !slices.ContainsFunc(h.planner.seen(), func(prompt string) bool {
		return strings.Contains(prompt, "user: Where did Caroline go?\nOptions: a | b")
	}) {
		t.Fatal(h.planner.seen())
	}
}

func TestSearchClampsTopKToView(t *testing.T) {
	h := newHarness(t, func(config *ServerConfig) {
		config.MemoryConfig.View = &memoryagent.ViewConfig{MaxItems: 1, Fusion: "rrf"}
	})
	for i := range 3 {
		h.mustAdd(t, addRequest(fmt.Sprintf("r%d", i), "u1"))
	}
	if items := h.search(t, SearchRequest{Query: "Caroline support group", UserID: "u1", TopK: 100}); len(items) != 1 || h.server.DegradedSearches() != 0 {
		t.Fatal(items, h.log.String())
	}
}

// embedder is a fake Ollama /api/embed endpoint that can fail; it records
// every input it embeds.
type embedder struct {
	fail   atomic.Bool
	mu     sync.Mutex
	inputs []string
}

func (e *embedder) seen() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.inputs)
}

func (e *embedder) start(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if e.fail.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var body struct{ Input []string }
		json.NewDecoder(r.Body).Decode(&body)
		e.mu.Lock()
		e.inputs = append(e.inputs, body.Input...)
		e.mu.Unlock()
		out := [][]float64{}
		for _, text := range body.Input {
			out = append(out, []float64{1, float64(len(text) % 7)})
		}
		json.NewEncoder(w).Encode(map[string]any{"embeddings": out})
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func TestAddEmbedsAndRetriesAfterEmbeddingFailure(t *testing.T) {
	embed := &embedder{}
	url := embed.start(t)
	h := newHarness(t, func(config *ServerConfig) {
		config.MemoryConfig.Embedding = &records.EmbeddingConfig{URL: url, Model: "test", QueryPrefix: "search_query: ", DocumentPrefix: "search_document: "}
	})
	embed.fail.Store(true)
	response := h.call("POST", "/add", addRequest("r1", "u1"))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatal(response.Code, response.Body.String())
	}
	errorBody(t, response)
	// The platform retries the same request: the stored write is reused and
	// embedded now.
	embed.fail.Store(false)
	h.mustAdd(t, addRequest("r1", "u1"))
	// Only the new chunks are embedded, with the document prefix the
	// query-time fill uses; the archived parent is not.
	chunks := 0
	for _, input := range embed.seen() {
		if input == "search_document: " || strings.HasPrefix(input, "search_query: ") {
			t.Fatalf("embedded %q", input)
		}
		if strings.HasPrefix(input, "search_document: Conversation s1 on 2023/05/08 (Mon) 13:56:\nuser: I went") {
			chunks++
		}
	}
	if chunks != 1 {
		t.Fatal(embed.seen())
	}
	if items := h.search(t, SearchRequest{Query: "Caroline support group", UserID: "u1", TopK: 10}); len(items) == 0 || h.server.DegradedSearches() != 0 {
		t.Fatal(items, h.log.String())
	}
	h.server.Close()
	if n := documents(t, h.root, "u1"); n != 1 {
		t.Fatal(n)
	}
}

func TestOpenStoresAreBoundedAndSafe(t *testing.T) {
	h := newHarness(t, func(config *ServerConfig) { config.MaxOpenStores = 1 })
	users := []string{"a", "b", "c"}
	var wg sync.WaitGroup
	failures := make(chan string, 64)
	for round := range 3 {
		for _, user := range users {
			wg.Add(2)
			go func() {
				defer wg.Done()
				if response := h.call("POST", "/add", addRequest(fmt.Sprintf("r%d", round), user)); response.Code != http.StatusOK {
					failures <- response.Body.String()
				}
			}()
			go func() {
				defer wg.Done()
				if response := h.call("POST", "/search", SearchRequest{Query: "Caroline support group", UserID: user, TopK: 5}); response.Code != http.StatusOK {
					failures <- response.Body.String()
				}
			}()
		}
	}
	wg.Wait()
	close(failures)
	for failure := range failures {
		t.Fatal(failure)
	}
	if open := h.server.users.open(); open > 1 {
		t.Fatal("open stores", open)
	}
	for _, user := range users {
		if items := h.search(t, SearchRequest{Query: "Caroline support group", UserID: user, TopK: 5}); len(items) == 0 {
			t.Fatal(user)
		}
	}

	// A held store is never closed under its user: an eviction waits for
	// the release.
	g := h.server.users
	held, err := g.acquire(context.Background(), "a", false)
	if err != nil || held == nil {
		t.Fatal(err)
	}
	if held.stop == nil {
		t.Fatal("the store's query runtime is not running")
	}
	other, err := g.acquire(context.Background(), "b", false)
	if err != nil || other == nil {
		t.Fatal(err)
	}
	if _, err := held.store.Get(context.Background(), records.GetRequest{IDs: []string{}}); err != nil {
		t.Fatal("held store closed", err)
	}
	g.release(other)
	g.release(held)
	if g.open() > 1 {
		t.Fatal(g.open())
	}
}

func TestNewServerValidates(t *testing.T) {
	models := func(memoryagent.Config) (memoryagent.Models, error) {
		return memoryagent.Models{Planner: &fakePlanner{}, Judge: &fakeJudge{}}, nil
	}
	root := t.TempDir()
	for name, config := range map[string]ServerConfig{
		"no key":        {DataRoot: root, MemoryConfig: simpleConfig(), Models: models},
		"relative root": {DataRoot: "data", MemoryConfig: simpleConfig(), AuthKey: testKey, Models: models},
		"bad config":    {DataRoot: root, MemoryConfig: memoryagent.Config{Version: 2}, AuthKey: testKey, Models: models},
		"no models":     {DataRoot: root, MemoryConfig: simpleConfig(), AuthKey: testKey},
		"model error": {DataRoot: root, MemoryConfig: simpleConfig(), AuthKey: testKey, Models: func(memoryagent.Config) (memoryagent.Models, error) {
			return memoryagent.Models{}, errors.New("no credentials")
		}},
	} {
		if server, err := NewServer(config); err == nil {
			server.Close()
			t.Fatal(name)
		}
	}
	server, err := NewServer(ServerConfig{DataRoot: root, MemoryConfig: simpleConfig(), AuthKey: testKey, Models: models})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Fatal("startup wrote into the data root", entries)
	}
	hs := server.HTTPServer("127.0.0.1:0")
	if hs.ReadHeaderTimeout != 10*time.Second || hs.ReadTimeout != 5*time.Minute || hs.WriteTimeout != 31*time.Minute || hs.IdleTimeout != 2*time.Minute || hs.Handler == nil {
		t.Fatal(hs)
	}
}
