package aml

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
	"github.com/mnemon-dev/mnemon/internal/records"
)

// inspectToken is the inspect service's bearer token in the tests that set
// one.
const inspectToken = "inspect-token-0123456789abcdefghijkl"

// inspectHarness is an inspect service over a harness's data root, with the
// harness's scripted models for full searches, and its log.
type inspectHarness struct {
	inspector *Inspector
	log       *syncBuffer
	token     string
}

// shippedView is the shipped configuration's View: 100 records, in time
// order.
func shippedView(config *ServerConfig) {
	config.MemoryConfig.View = &memoryagent.ViewConfig{MaxItems: 100, MaxCharacters: 240000, Order: "time"}
}

// layeredHarness consolidates on Add in timelines mode, batches of 300 units
// (two Adds), with the shipped View.
func layeredHarness(t *testing.T) *harness {
	t.Helper()
	return newHarness(t, func(config *ServerConfig) {
		shippedView(config)
		config.MemoryConfig.Consolidation = &memoryagent.ConsolidationConfig{Mode: "timelines", BatchCharacters: 300}
		config.ConsolidateOnAdd = true
	})
}

// newInspector makes an inspect service over h's data root and memory
// configuration, its full searches answered by h's scripted planner and
// judge and billed to a debug budget file of its own; edit changes the
// configuration first.
func newInspector(t *testing.T, h *harness, edit func(*InspectConfig)) *inspectHarness {
	t.Helper()
	ih := &inspectHarness{log: &syncBuffer{}}
	config := InspectConfig{
		DataRoot:        h.root,
		MemoryConfig:    h.server.config.MemoryConfig,
		DebugBudgetFile: filepath.Join(t.TempDir(), "debug-budget.db"),
		DebugCaps:       map[string]float64{"deepseek": 5, "jev": 1},
		Logger:          slog.New(slog.NewTextHandler(ih.log, nil)),
		Version:         "test-version",
		Models: func(memoryagent.Config) (memoryagent.Models, error) {
			return memoryagent.Models{Planner: h.planner, Judge: h.judge}, nil
		},
	}
	if edit != nil {
		edit(&config)
	}
	inspector, err := NewInspector(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { inspector.Close() })
	ih.inspector, ih.token = inspector, config.Token
	return ih
}

// call sends one request to the inspect service, with its token when it has
// one, and returns the recorded answer.
func (ih *inspectHarness) call(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return ih.callContext(t, context.Background(), method, path, body)
}

// callContext is call for a client that can leave: ctx is the request's.
func (ih *inspectHarness) callContext(t *testing.T, ctx context.Context, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader = http.NoBody
	switch value := body.(type) {
	case nil:
	case string:
		reader = strings.NewReader(value)
	default:
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	request := httptest.NewRequest(method, path, reader).WithContext(ctx)
	if ih.token != "" {
		request.Header.Set("Authorization", "Bearer "+ih.token)
	}
	recorder := httptest.NewRecorder()
	ih.inspector.Handler().ServeHTTP(recorder, request)
	return recorder
}

// ok sends a request, fails the test unless it answers 200 with
// Cache-Control: no-store, and decodes the answer into out (unless nil).
func (ih *inspectHarness) ok(t *testing.T, method, path string, body, out any) []byte {
	t.Helper()
	response := ih.call(t, method, path, body)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("%s %s: %d %v %s", method, path, response.Code, response.Header(), response.Body.String())
	}
	if out != nil {
		if err := json.Unmarshal(response.Body.Bytes(), out); err != nil {
			t.Fatal(err, response.Body.String())
		}
	}
	return response.Body.Bytes()
}

// refused sends a request and fails the test unless it answers status with
// error code (and field, when not empty).
func (ih *inspectHarness) refused(t *testing.T, method, path string, body any, status int, code, field string) *httptest.ResponseRecorder {
	t.Helper()
	response := ih.call(t, method, path, body)
	var answer struct {
		Error string `json:"error"`
		Field string `json:"field"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &answer); err != nil || response.Code != status || answer.Error != code || answer.Field != field {
		t.Fatalf("%s %s %v: %d %s, want %d %s %s", method, path, body, response.Code, response.Body.String(), status, code, field)
	}
	return response
}

// startFakeProvider builds scripts/aml/fakeprovider and runs it on a free
// loopback port, with args, until the test ends; it returns its base URL.
func startFakeProvider(t *testing.T, args ...string) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is missing; the fake provider is built with it")
	}
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "fakeprovider")
	build := exec.Command("go", "build", "-o", binary, "./scripts/aml/fakeprovider")
	build.Dir = repo
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build the fake provider: %v\n%s", err, out)
	}
	command := exec.Command(binary, append([]string{"-listen", "127.0.0.1:0"}, args...)...)
	stderr, err := command.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		command.Process.Kill()
		command.Wait()
	})
	lines := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			if addr, ok := strings.CutPrefix(scanner.Text(), "fakeprovider listening on "); ok {
				lines <- addr
				break
			}
		}
		io.Copy(io.Discard, stderr)
	}()
	select {
	case addr := <-lines:
		return "http://" + addr
	case <-time.After(30 * time.Second):
		t.Fatal("the fake provider did not say where it listens")
	}
	return ""
}

// fakeProviderModels points a configuration's planner (DeepSeek) and judge
// (JEV) at the fake provider, as a fault drill does, with test keys.
func fakeProviderModels(t *testing.T, config *memoryagent.Config, base string) {
	t.Helper()
	t.Setenv("INSPECT_TEST_DEEPSEEK", "test-only-placeholder")
	t.Setenv("INSPECT_TEST_JEV", "test-only-placeholder")
	config.Planner = &memoryagent.ModelConfig{Kind: "deepseek", Model: "deepseek-flash", KeyEnv: "INSPECT_TEST_DEEPSEEK", TestEndpoint: base + "/chat/completions"}
	config.Judge = &memoryagent.ModelConfig{Kind: "jev", Model: "jev-1.13.0", KeyEnv: "INSPECT_TEST_JEV", TestEndpoint: base + "/v1/systemone"}
}

// realModels makes an inspect service build its own providers.
func realModels(config *InspectConfig) { config.Models = nil }

type infoAnswer struct {
	Version string          `json:"version"`
	Modes   map[string]bool `json:"modes"`
	Debug   struct {
		Currency  map[string]string  `json:"currency"`
		Caps      map[string]float64 `json:"caps"`
		Spent     map[string]float64 `json:"spent"`
		Held      map[string]float64 `json:"held"`
		Remaining map[string]float64 `json:"remaining"`
	} `json:"debugBudget"`
	Recent struct {
		FullSearches int                `json:"fullSearches"`
		AvgCost      map[string]float64 `json:"avgCost"`
	} `json:"recent"`
	Limits map[string]int `json:"limits"`
}

func TestInspectInfo(t *testing.T) {
	h := layeredHarness(t)
	h.mustAdd(t, addRequest("r1", "u1"))
	ih := newInspector(t, h, nil)
	var info infoAnswer
	ih.ok(t, "GET", "/v1/info", nil, &info)
	if info.Version != "test-version" || !info.Modes["lexical"] || !info.Modes["full"] {
		t.Fatalf("%+v", info)
	}
	if !sameMap(info.Debug.Currency, map[string]string{"deepseek": "CNY", "jev": "USD"}) || info.Debug.Caps["deepseek"] != 5 || info.Debug.Caps["jev"] != 1 ||
		info.Debug.Spent["jev"] != 0 || info.Debug.Remaining["deepseek"] != 5 || info.Debug.Held["jev"] != 0 {
		t.Fatalf("%+v", info.Debug)
	}
	if info.Recent.FullSearches != 0 || info.Recent.AvgCost["deepseek"] != 0 {
		t.Fatalf("%+v", info.Recent)
	}
	for limit, want := range map[string]int{"maxConcurrent": 2, "topKMax": 100, "topKDefault": 100, "queryMaxBytes": 12000, "bodyMaxBytes": 65536,
		"storesPageMax": 500, "storesPageDefault": 100, "recordsPageMax": 200, "recordsPageDefault": 50, "memoryPageMax": 200,
		"memoryPageDefault": 50, "pendingIdsMax": 100, "excerptMax": 200, "fullSearchSeconds": 600} {
		if info.Limits[limit] != want {
			t.Errorf("limits.%s is %d, want %d", limit, info.Limits[limit], want)
		}
	}
	var health map[string]bool
	ih.ok(t, "GET", "/healthz", nil, &health)
	if !health["ok"] || len(health) != 1 {
		t.Fatal(health)
	}

	// Without caps there is no full mode.
	ih = newInspector(t, h, func(config *InspectConfig) { config.DebugCaps = nil })
	ih.ok(t, "GET", "/v1/info", nil, &info)
	if info.Modes["full"] || info.Debug.Caps["deepseek"] != 0 || info.Debug.Remaining["jev"] != 0 {
		t.Fatalf("%+v", info)
	}
}

func sameMap[V comparable](a, b map[string]V) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// The configuration the service refuses at its start.
func TestNewInspectorRefuses(t *testing.T) {
	h := layeredHarness(t)
	h.mustAdd(t, addRequest("r1", "u1"))
	base := func() InspectConfig {
		return InspectConfig{DataRoot: h.root, MemoryConfig: h.server.config.MemoryConfig, Models: func(memoryagent.Config) (memoryagent.Models, error) {
			return memoryagent.Models{Planner: h.planner, Judge: h.judge}, nil
		}}
	}
	instance := filepath.Join(t.TempDir(), "model-budget.db")
	sep := string(filepath.Separator)
	// A directory that is a symbolic link to the instance's budget file's,
	// and one to the data root; and a hard link to an instance's budget file
	// that exists.
	links := t.TempDir()
	toInstance, toRoot := filepath.Join(links, "instance"), filepath.Join(links, "root")
	existing := filepath.Join(t.TempDir(), "model-budget.db")
	budget, err := memoryagent.OpenBudgetWithCaps(existing, nil)
	if err != nil {
		t.Fatal(err)
	}
	budget.Close()
	hardLink := filepath.Join(filepath.Dir(existing), "debug-budget.db")
	linked := errors.Join(os.Symlink(filepath.Dir(instance), toInstance), os.Symlink(h.root, toRoot), os.Link(existing, hardLink))
	existingBefore := fileStates(t, filepath.Dir(existing))
	cases := map[string]func(*InspectConfig){
		"relative data root": func(c *InspectConfig) { c.DataRoot = "data" },
		"not a data root":    func(c *InspectConfig) { c.DataRoot = t.TempDir() },
		"short token":        func(c *InspectConfig) { c.Token = "short" },
		"the instance's budget": func(c *InspectConfig) {
			c.MemoryConfig.BudgetFile = instance
			c.DebugBudgetFile = instance
			c.DebugCaps = map[string]float64{"deepseek": 1}
		},
		"the instance's budget, spelled with . and ..": func(c *InspectConfig) {
			c.MemoryConfig.BudgetFile = instance
			c.DebugBudgetFile = filepath.Dir(instance) + sep + "." + sep + "sub" + sep + ".." + sep + "model-budget.db"
		},
		"a budget in the data root": func(c *InspectConfig) { c.DebugBudgetFile = filepath.Join(h.root, "debug.db") },
		"a budget in the data root, spelled with ..": func(c *InspectConfig) {
			c.DebugBudgetFile = h.root + sep + "users" + sep + ".." + sep + "debug.db"
		},
		"a relative budget":     func(c *InspectConfig) { c.DebugBudgetFile = "debug.db" },
		"caps without a budget": func(c *InspectConfig) { c.DebugCaps = map[string]float64{"jev": 1} },
		"an unknown kind": func(c *InspectConfig) {
			c.DebugBudgetFile = filepath.Join(t.TempDir(), "debug.db")
			c.DebugCaps = map[string]float64{"claude": 1}
		},
		"a negative cap": func(c *InspectConfig) {
			c.DebugBudgetFile = filepath.Join(t.TempDir(), "debug.db")
			c.DebugCaps = map[string]float64{"jev": -1}
		},
		"no concurrency": func(c *InspectConfig) { c.MaxConcurrent = -1 },
		"an embedder": func(c *InspectConfig) {
			c.MemoryConfig.Embedding = &records.EmbeddingConfig{URL: "http://127.0.0.1:1/api/embed", Model: "nomic-embed-text"}
		},
		"memory-agent/v1": func(c *InspectConfig) { c.MemoryConfig.Strategy, c.MemoryConfig.View = "", nil },
	}
	if linked != nil {
		t.Log("no links here, so no case of them:", linked)
	} else {
		cases["the instance's budget, not made yet, through a linked directory"] = func(c *InspectConfig) {
			c.MemoryConfig.BudgetFile = instance
			c.DebugBudgetFile = filepath.Join(toInstance, "model-budget.db")
		}
		cases["the instance's budget, through a hard link"] = func(c *InspectConfig) {
			c.MemoryConfig.BudgetFile = existing
			c.DebugBudgetFile = hardLink
			c.DebugCaps = map[string]float64{"deepseek": 1}
		}
		cases["a budget in the data root, through a linked directory"] = func(c *InspectConfig) {
			c.DebugBudgetFile = filepath.Join(toRoot, "debug.db")
		}
	}
	for name, edit := range cases {
		config := base()
		edit(&config)
		if inspector, err := NewInspector(config); err == nil {
			inspector.Close()
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := os.Stat(instance); !os.IsNotExist(err) {
		t.Fatal("a refused service touched the instance's budget file", err)
	}
	if !sameMap(existingBefore, fileStates(t, filepath.Dir(existing))) {
		t.Fatal("a refused service changed the instance's budget file")
	}
	if _, err := os.Stat(filepath.Join(h.root, "debug.db")); !os.IsNotExist(err) {
		t.Fatal("a refused service made a file in the data root", err)
	}

	// An index policy fuses other routes into the ranking, which then does
	// not sort by the words scores the trace shows: refused, saying so.
	config := base()
	config.MemoryConfig.Index = &records.IndexPolicy{Version: 1, Lexical: true, Entities: true}
	if inspector, err := NewInspector(config); err == nil || !strings.Contains(err.Error(), "index policy") || !strings.Contains(err.Error(), "BM25") {
		if inspector != nil {
			inspector.Close()
		}
		t.Fatal("an index policy:", err)
	}
}

// Every request must carry the token when the service has one: /healthz and
// unknown paths too, before anything else is done; the token is never
// logged.
func TestInspectToken(t *testing.T) {
	h := layeredHarness(t)
	h.mustAdd(t, addRequest("r1", "u1"))
	ih := newInspector(t, h, func(config *InspectConfig) { config.Token = inspectToken })
	for _, path := range []string{"/healthz", "/v1/info", "/v1/stores", "/v1/stores/" + userKey("u1") + "/records", "/nope"} {
		for _, header := range []string{"", "Bearer wrong-token-0123456789abcdefghijkl", "Token " + inspectToken, inspectToken, "Basic " + inspectToken} {
			request := httptest.NewRequest("GET", path, nil)
			if header != "" {
				request.Header.Set("Authorization", header)
			}
			recorder := httptest.NewRecorder()
			ih.inspector.Handler().ServeHTTP(recorder, request)
			if recorder.Code != http.StatusUnauthorized || strings.TrimSpace(recorder.Body.String()) != `{"error":"unauthorized"}` ||
				recorder.Header().Get("WWW-Authenticate") != `Bearer realm="mnemon-inspect"` {
				t.Fatalf("%s with %q: %d %s %v", path, header, recorder.Code, recorder.Body.String(), recorder.Header())
			}
		}
	}
	request := httptest.NewRequest("GET", "/healthz", nil)
	request.Header.Set("Authorization", "bearer "+inspectToken)
	recorder := httptest.NewRecorder()
	ih.inspector.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatal(recorder.Code)
	}
	ih.refused(t, "GET", "/nope", nil, http.StatusNotFound, "not_found", "")
	if strings.Contains(ih.log.String(), inspectToken) || strings.Contains(ih.log.String(), "wrong-token") {
		t.Fatal("the log holds a token:", ih.log.String())
	}
}

func TestInspectRoutesAndMethods(t *testing.T) {
	h := layeredHarness(t)
	h.mustAdd(t, addRequest("r1", "u1"))
	ih := newInspector(t, h, nil)
	key := userKey("u1")
	for _, c := range []struct{ method, path, allow string }{
		{"POST", "/healthz", "GET"}, {"POST", "/v1/info", "GET"}, {"DELETE", "/v1/stores", "GET"},
		{"GET", "/v1/resolve", "POST"}, {"POST", "/v1/stores/" + key + "/records", "GET"},
		{"PUT", "/v1/stores/" + key + "/memory", "GET"}, {"GET", "/v1/search", "POST"},
		{"POST", "/v1/stores/" + key + "/records/r1", "GET"}, {"DELETE", "/v1/stores/" + key + "/memory/threads/T1", "GET"},
	} {
		response := ih.refused(t, c.method, c.path, nil, http.StatusMethodNotAllowed, "method_not_allowed", "")
		if response.Header().Get("Allow") != c.allow {
			t.Fatal(c, response.Header())
		}
	}
	for _, path := range []string{"/", "/v1", "/v1/stores/", "/v1/stores/" + key, "/v1/stores/" + key + "/other", "/admin/v1/stores", "/search",
		"/v1/stores/" + key + "/records/a/b", "/v1/stores/" + key + "/memory/threads", "/v1/stores/" + key + "/memory/threads/T1/x"} {
		ih.refused(t, "GET", path, nil, http.StatusNotFound, "not_found", "")
	}
	ih.refused(t, "POST", "/v1/resolve", strings.Repeat(" ", 70000), http.StatusRequestEntityTooLarge, "too_large", "")
	ih.refused(t, "POST", "/v1/resolve", "[1]", http.StatusBadRequest, "invalid", "body")
	ih.refused(t, "POST", "/v1/search", "{", http.StatusBadRequest, "invalid", "body")
	// The body is read to its end: white space may follow the object, and
	// nothing else; the bound counts what follows it.
	ih.ok(t, "POST", "/v1/resolve", "{\"user_id\":\"u1\"}\r\n\t "+strings.Repeat(" ", 2000), nil)
	for _, body := range []string{`{"user_id":"u1"} x`, `{"user_id":"u1"}{"user_id":"u2"}`, `{"user_id":"u1"}]`, `{"user_id":"u1"} 7`} {
		ih.refused(t, "POST", "/v1/resolve", body, http.StatusBadRequest, "invalid", "body")
	}
	ih.refused(t, "POST", "/v1/resolve", `{"user_id":"u1"}`+strings.Repeat(" ", 70000), http.StatusRequestEntityTooLarge, "too_large", "")
}

type storesAnswer struct {
	Total  int `json:"total"`
	Stores []struct {
		Store         string `json:"store"`
		Key           string `json:"key"`
		Records       *int   `json:"records"`
		Bytes         int64  `json:"bytes"`
		Modified      string `json:"modified"`
		Consolidation *struct {
			Watermark int64 `json:"watermark"`
			Pending   int   `json:"pending"`
		} `json:"consolidation"`
		Error string `json:"error"`
	} `json:"stores"`
	Next *string `json:"next"`
}

func TestInspectStores(t *testing.T) {
	h := layeredHarness(t)
	users := []string{"u1", "u2", "u3"}
	for _, user := range users {
		h.mustAdd(t, addRequest("r1", user))
	}
	h.mustAdd(t, addRequest("r2", "u1")) // u1 folds a batch
	ih := newInspector(t, h, nil)
	keys := []string{}
	for _, user := range users {
		keys = append(keys, userKey(user))
	}
	slices.Sort(keys)

	var first storesAnswer
	ih.ok(t, "GET", "/v1/stores?limit=2", nil, &first)
	if first.Total != 3 || len(first.Stores) != 2 || first.Next == nil || *first.Next != keys[1] {
		t.Fatalf("%+v", first)
	}
	for i, row := range first.Stores {
		if row.Key != keys[i] || row.Store != keys[i][:8] || row.Records == nil || *row.Records != map[bool]int{true: 2, false: 1}[row.Key == userKey("u1")] ||
			row.Bytes <= 0 || row.Error != "" || row.Consolidation == nil {
			t.Fatalf("%+v", row)
		}
		if _, err := time.Parse(time.RFC3339, row.Modified); err != nil {
			t.Fatal(row.Modified)
		}
		pending := 1
		if row.Key == userKey("u1") {
			pending = 0
			if row.Consolidation.Watermark == 0 {
				t.Fatalf("%+v", row.Consolidation)
			}
		}
		if row.Consolidation.Pending != pending {
			t.Fatalf("%s: %+v", row.Store, row.Consolidation)
		}
	}
	var second storesAnswer
	ih.ok(t, "GET", "/v1/stores?limit=2&after="+*first.Next, nil, &second)
	if second.Total != 3 || len(second.Stores) != 1 || second.Stores[0].Key != keys[2] || second.Next != nil {
		t.Fatalf("%+v", second)
	}
	var all storesAnswer
	ih.ok(t, "GET", "/v1/stores", nil, &all)
	if len(all.Stores) != 3 || all.Next != nil {
		t.Fatalf("%+v", all)
	}
	for _, query := range []string{"limit=0", "limit=501", "limit=x", "after=nothex", "after=" + strings.ToUpper(keys[0])} {
		field := strings.SplitN(query, "=", 2)[0]
		ih.refused(t, "GET", "/v1/stores?"+query, nil, http.StatusBadRequest, "invalid", field)
	}
	// The service logs the route, never a key.
	for _, key := range keys {
		if strings.Contains(ih.log.String(), key) {
			t.Fatal("the log holds a store key:", ih.log.String())
		}
	}
	if !strings.Contains(ih.log.String(), "GET /v1/stores 200") {
		t.Fatal(ih.log.String())
	}
}

// spoilMemory rewrites every stored timelines state of the derived index at
// path with a memory that does not decode, and the digest of the payload
// that results (the token is the payload's SHA-256, timelines_store.go).
func spoilMemory(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var scope, raw string
	if err := db.QueryRow(`SELECT scope,payload FROM timelines`).Scan(&scope, &raw); err != nil {
		t.Fatal(err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}
	payload["memory"] = json.RawMessage(`{"threads":7}`)
	spoiled := string(must(json.Marshal(payload)))
	sum := sha256.Sum256([]byte(spoiled))
	if _, err := db.Exec(`UPDATE timelines SET payload=?,token=? WHERE scope=?`, spoiled, hex.EncodeToString(sum[:]), scope); err != nil {
		t.Fatal(err)
	}
}

// copyStore copies the files of the store directory from into to.
func copyStore(t *testing.T, from, to string) {
	t.Helper()
	entries, err := os.ReadDir(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(to, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(from, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(to, entry.Name()), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// A page of 100 store rows decodes no consolidated memory: every row of a
// store whose stored memory does not decode is whole (records, watermark,
// pending), while /memory, which decodes it, cannot read the store.
func TestInspectStoresPageDecodesNoMemory(t *testing.T) {
	h := layeredHarness(t)
	for _, id := range []string{"r1", "r2", "r3"} {
		h.mustAdd(t, addRequest(id, "u1")) // r1 and r2 fold; r3 is pending
	}
	ih := newInspector(t, h, nil)
	var before storesAnswer
	ih.ok(t, "GET", "/v1/stores", nil, &before)
	want := before.Stores[0]
	if want.Records == nil || want.Consolidation == nil || want.Consolidation.Watermark == 0 || want.Consolidation.Pending != 1 {
		t.Fatalf("%+v", want)
	}
	h.server.Close()
	source := filepath.Join(h.root, "users", userKey("u1"))
	spoilMemory(t, filepath.Join(source, "memory-index.db"))
	for n := 1; n < 100; n++ {
		copyStore(t, source, filepath.Join(h.root, "users", fmt.Sprintf("%032x", n)))
	}
	var page storesAnswer
	ih.ok(t, "GET", "/v1/stores?limit=100", nil, &page)
	if page.Total != 100 || len(page.Stores) != 100 || page.Next != nil {
		t.Fatalf("%d stores, %d rows", page.Total, len(page.Stores))
	}
	for _, row := range page.Stores {
		if row.Error != "" || row.Records == nil || *row.Records != *want.Records || row.Consolidation == nil || *row.Consolidation != *want.Consolidation {
			t.Fatalf("%+v, want %+v", row, want)
		}
	}
	ih.refused(t, "GET", "/v1/stores/"+userKey("u1")+"/memory", nil, http.StatusInternalServerError, "internal", "")
}

func TestInspectResolve(t *testing.T) {
	h := layeredHarness(t)
	h.mustAdd(t, addRequest("r1", "alice"))
	ih := newInspector(t, h, nil)
	var answer struct {
		Key    string `json:"key"`
		Store  string `json:"store"`
		Exists bool   `json:"exists"`
	}
	ih.ok(t, "POST", "/v1/resolve", map[string]any{"user_id": "alice"}, &answer)
	if answer.Key != userKey("alice") || answer.Store != userKey("alice")[:8] || !answer.Exists {
		t.Fatalf("%+v", answer)
	}
	ih.ok(t, "POST", "/v1/resolve", `{"user_id":12345}`, &answer)
	if answer.Key != userKey("12345") || answer.Exists {
		t.Fatalf("%+v", answer)
	}
	for _, body := range []string{`{}`, `{"user_id":" "}`, `{"user_id":null}`, `{"user_id":["a"]}`} {
		ih.refused(t, "POST", "/v1/resolve", body, http.StatusBadRequest, "invalid", "user_id")
	}
}

// Resolving a user logs the route alone: neither the id, nor its key, nor
// the key's prefix.
func TestInspectResolveLogsNothing(t *testing.T) {
	h := layeredHarness(t)
	const user = "carol-the-private-user@example.com"
	h.mustAdd(t, addRequest("r1", user))
	ih := newInspector(t, h, nil)
	ih.ok(t, "POST", "/v1/resolve", map[string]any{"user_id": user}, nil)
	ih.ok(t, "POST", "/v1/resolve", map[string]any{"user_id": "nobody-at-all"}, nil)
	log := ih.log.String()
	for _, secret := range []string{user, userKey(user), userKey(user)[:8], "nobody-at-all", userKey("nobody-at-all")[:8]} {
		if strings.Contains(log, secret) {
			t.Fatalf("the log holds %q:\n%s", secret, log)
		}
	}
	if strings.Count(log, "POST /v1/resolve 200") != 2 {
		t.Fatal(log)
	}
}

type recordsAnswer struct {
	Records []struct {
		ID        string `json:"id"`
		Seq       int64  `json:"seq"`
		CreatedAt string `json:"created_at"`
		Session   string `json:"session"`
		Content   string `json:"content"`
	} `json:"records"`
	Next *int64 `json:"next"`
}

func TestInspectRecords(t *testing.T) {
	h := layeredHarness(t)
	talk(t, h, "u1", 3)
	ih := newInspector(t, h, nil)
	key := userKey("u1")
	var all recordsAnswer
	ih.ok(t, "GET", "/v1/stores/"+key+"/records", nil, &all)
	if len(all.Records) != 3 || all.Next != nil {
		t.Fatalf("%+v", all)
	}
	// The records are what /search returns: the same ids, texts and times.
	items := h.search(t, SearchRequest{Query: question, UserID: "u1", TopK: 100})
	byID := map[string]SearchItem{}
	for _, item := range items {
		byID[item.ID] = item
	}
	for i, record := range all.Records {
		item, ok := byID[record.ID]
		if !ok || item.Content != record.Content || item.CreatedAt != record.CreatedAt || record.Session != fmt.Sprintf("s%d", i) || i > 0 && record.Seq <= all.Records[i-1].Seq {
			t.Fatalf("%+v %+v", record, item)
		}
	}
	var page recordsAnswer
	ih.ok(t, "GET", "/v1/stores/"+key+"/records?limit=2", nil, &page)
	if len(page.Records) != 2 || page.Next == nil || *page.Next != all.Records[1].Seq {
		t.Fatalf("%+v", page)
	}
	ih.ok(t, "GET", "/v1/stores/"+key+"/records?limit=2&after="+itoa(*page.Next), nil, &page)
	if len(page.Records) != 1 || page.Records[0].ID != all.Records[2].ID || page.Next != nil {
		t.Fatalf("%+v", page)
	}
	for _, query := range []string{"limit=0", "limit=201", "after=-1", "after=x"} {
		ih.refused(t, "GET", "/v1/stores/"+key+"/records?"+query, nil, http.StatusBadRequest, "invalid", strings.SplitN(query, "=", 2)[0])
	}
	ih.refused(t, "GET", "/v1/stores/"+userKey("nobody")+"/records", nil, http.StatusNotFound, "store_not_found", "")
	ih.refused(t, "GET", "/v1/stores/not-a-key/records", nil, http.StatusBadRequest, "invalid", "key")
	if log := ih.log.String(); !strings.Contains(log, "GET /v1/stores/{key}/records 200") || !strings.Contains(log, "store "+key[:8]) || strings.Contains(log, key) {
		t.Fatal(log)
	}
}

// rawItems is a page's items (key "records" or "items"), each as the page's
// bytes.
func rawItems(t *testing.T, page []byte, key string) []json.RawMessage {
	t.Helper()
	var body map[string]json.RawMessage
	var items []json.RawMessage
	if err := json.Unmarshal(page, &body); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body[key], &items); err != nil {
		t.Fatal(err)
	}
	return items
}

// badIDs are path segments that are no lookup's id: empty, too long, or
// with a control character.
var badIDs = []string{"", strings.Repeat("x", lookupIDMax+1), "a%00b", "a%0Ab", "%7F", "a%C2%85b"}

// A record by its id: the very object its page lists, byte for byte; a
// record the page does not list, or none, is record_not_found; the id is
// never logged.
func TestInspectRecord(t *testing.T) {
	h := layeredHarness(t)
	talk(t, h, "u1", 3)
	ih := newInspector(t, h, nil)
	key := userKey("u1")
	base := "/v1/stores/" + key + "/records/"
	page := rawItems(t, ih.ok(t, "GET", "/v1/stores/"+key+"/records", nil, nil), "records")
	if len(page) != 3 {
		t.Fatal(len(page))
	}
	ids := []string{}
	for _, item := range page {
		var record struct{ ID string }
		json.Unmarshal(item, &record)
		ids = append(ids, record.ID)
		var answer map[string]json.RawMessage
		ih.ok(t, "GET", base+url.PathEscape(record.ID), nil, &answer)
		if len(answer) != 1 || !bytes.Equal(answer["record"], item) {
			t.Fatalf("%s:\n%s\n%s", record.ID, answer["record"], item)
		}
	}
	// The documents the records are cut from are not listed: not found.
	store, err := records.OpenReader(filepath.Join(h.root, "users", key))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Get(context.Background(), records.GetRequest{})
	store.Close()
	if err != nil {
		t.Fatal(err)
	}
	missing := []string{"no-such-record-4711", strings.Repeat("y", 256)}
	for _, raw := range snapshot.Records {
		var record struct{ ID, Kind string }
		json.Unmarshal([]byte(raw), &record)
		if record.Kind == "memory-document" {
			missing = append(missing, record.ID)
		}
	}
	if len(missing) == 2 {
		t.Fatal("no document")
	}
	for _, id := range missing {
		ih.refused(t, "GET", base+url.PathEscape(id), nil, http.StatusNotFound, "record_not_found", "")
	}
	ih.refused(t, "GET", "/v1/stores/"+userKey("nobody")+"/records/"+url.PathEscape(ids[0]), nil, http.StatusNotFound, "store_not_found", "")
	ih.refused(t, "GET", "/v1/stores/NOT-A-KEY/records/x", nil, http.StatusBadRequest, "invalid", "key")
	for _, id := range badIDs {
		ih.refused(t, "GET", base+id, nil, http.StatusBadRequest, "invalid", "id")
	}
	log := ih.log.String()
	for _, id := range append(ids, missing...) {
		if strings.Contains(log, id) {
			t.Fatalf("the log holds the id %q:\n%s", id, log)
		}
	}
	if !strings.Contains(log, "GET /v1/stores/{key}/records/{id} 200") || !strings.Contains(log, "(store "+key[:8]+")") || strings.Contains(log, key) {
		t.Fatal(log)
	}
}

// A memory item by its section and id: the very object its section's page
// lists, byte for byte, a fact's key with its spaces and "·" included; an id
// the section does not have is item_not_found; the id is never logged (a
// fact's key is content).
func TestInspectMemoryItem(t *testing.T) {
	h := layeredHarness(t)
	h.consolidator.answer(func(context.Context, string) (string, error) {
		return `{"events":[{"thread":"new: Support group","date":"2023/05/08","text":"Caroline went to the support group.","records":["r0"]},{"thread":"new: Stories","date":"2023/05/08","text":"Everyone shared stories.","records":["r1"]}],` +
			`"facts":[{"key":"group · day","value":"Monday","date":"2023/05/08","records":["r0"],"conflict":false},{"key":"group · day","value":"Tuesday","date":"2023/05/09","records":["r1"],"conflict":true},{"key":"secret fact key","value":"x","date":"2023/05/09","records":["r1"]}],` +
			`"directives":[{"text":"Answer briefly.","date":"2023/05/08","records":["r1"]}]}`, nil
	})
	for _, id := range []string{"r1", "r2", "r3"} {
		h.mustAdd(t, addRequest(id, "u1"))
	}
	ih := newInspector(t, h, nil)
	key := userKey("u1")
	base := "/v1/stores/" + key + "/memory/"
	ids := []string{}
	for _, section := range []string{"threads", "events", "facts", "directives"} {
		items := rawItems(t, ih.ok(t, "GET", base[:len(base)-1]+"?section="+section, nil, nil), "items")
		if len(items) == 0 {
			t.Fatal("no", section)
		}
		for _, item := range items {
			var head struct{ ID string }
			json.Unmarshal(item, &head)
			ids = append(ids, head.ID)
			var answer map[string]json.RawMessage
			ih.ok(t, "GET", base+section+"/"+url.PathEscape(head.ID), nil, &answer)
			if len(answer) != 2 || string(answer["section"]) != `"`+section+`"` || !bytes.Equal(answer["item"], item) {
				t.Fatalf("%s %s:\n%s\n%s", section, head.ID, answer["item"], item)
			}
		}
	}
	if !slices.Contains(ids, "group · day") {
		t.Fatal(ids)
	}
	var fact struct {
		Item struct {
			Key    string            `json:"key"`
			Values []json.RawMessage `json:"values"`
		} `json:"item"`
	}
	ih.ok(t, "GET", base+"facts/group%20%C2%B7%20day", nil, &fact)
	if fact.Item.Key != "group · day" || len(fact.Item.Values) != 2 {
		t.Fatalf("%+v", fact)
	}
	for _, path := range []string{"threads/T99", "threads/e0", "events/e99", "events/T1", "facts/group", "facts/no-such-item-4711", "directives/d9"} {
		ih.refused(t, "GET", base+path, nil, http.StatusNotFound, "item_not_found", "")
	}
	ih.refused(t, "GET", "/v1/stores/"+userKey("nobody")+"/memory/threads/T1", nil, http.StatusNotFound, "store_not_found", "")
	ih.refused(t, "GET", "/v1/stores/NOT-A-KEY/memory/threads/T1", nil, http.StatusBadRequest, "invalid", "key")
	for _, section := range []string{"nope", "thread", "Threads"} {
		ih.refused(t, "GET", base+section+"/T1", nil, http.StatusBadRequest, "invalid", "section")
	}
	for _, id := range badIDs {
		ih.refused(t, "GET", base+"facts/"+id, nil, http.StatusBadRequest, "invalid", "id")
	}
	log := ih.log.String()
	for _, secret := range []string{"group · day", "group%20", "secret fact key", "no-such-item-4711"} {
		if strings.Contains(log, secret) {
			t.Fatalf("the log holds %q:\n%s", secret, log)
		}
	}
	if !strings.Contains(log, "GET /v1/stores/{key}/memory/{section}/{id} 200") {
		t.Fatal(log)
	}
}

func itoa(n int64) string { return strings.TrimSpace(string(must(json.Marshal(n)))) }

func must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

type memoryAnswer struct {
	Counts    map[string]int `json:"counts"`
	Watermark *int64         `json:"watermark"`
	Pending   *struct {
		Count int      `json:"count"`
		IDs   []string `json:"ids"`
	} `json:"pending"`
	Section string            `json:"section"`
	Items   []json.RawMessage `json:"items"`
	Next    *int              `json:"next"`
}

func TestInspectMemory(t *testing.T) {
	h := layeredHarness(t)
	h.consolidator.answer(func(context.Context, string) (string, error) {
		return `{"events":[{"thread":"new: Support group","date":"2023/05/08","text":"Caroline went to the support group.","records":["r0"]},{"thread":"new: Stories","date":"2023/05/08","text":"Everyone shared stories.","records":["r1"]}],` +
			`"facts":[{"key":"group · day","value":"Monday","date":"2023/05/08","records":["r0"],"conflict":false},{"key":"group · day","value":"Tuesday","date":"2023/05/09","records":["r1"],"conflict":true}],` +
			`"directives":[{"text":"Answer briefly.","date":"2023/05/08","records":["r1"]}]}`, nil
	})
	for _, id := range []string{"r1", "r2", "r3"} {
		h.mustAdd(t, addRequest(id, "u1"))
	}
	ih := newInspector(t, h, nil)
	key := userKey("u1")
	var threads memoryAnswer
	ih.ok(t, "GET", "/v1/stores/"+key+"/memory", nil, &threads)
	if threads.Section != "threads" || !sameMap(threads.Counts, map[string]int{"threads": 2, "events": 2, "facts": 1, "directives": 1}) || threads.Watermark == nil || *threads.Watermark == 0 ||
		threads.Pending == nil || threads.Pending.Count != 1 || len(threads.Pending.IDs) != 1 || len(threads.Items) != 2 || threads.Next != nil {
		t.Fatalf("%+v", threads)
	}
	if got := string(threads.Items[0]); got != `{"position":0,"id":"T1","title":"Support group","events":1,"first":"2023/05/08","last":"2023/05/08"}` {
		t.Fatal(got)
	}
	var events memoryAnswer
	ih.ok(t, "GET", "/v1/stores/"+key+"/memory?section=events&limit=1", nil, &events)
	if len(events.Items) != 1 || events.Next == nil || *events.Next != 0 {
		t.Fatalf("%+v", events)
	}
	var event struct {
		Position int      `json:"position"`
		ID       string   `json:"id"`
		Thread   string   `json:"thread"`
		Records  []string `json:"records"`
	}
	json.Unmarshal(events.Items[0], &event)
	if event.ID != "e0" || event.Thread != "T1" || len(event.Records) != 1 || strings.Contains(string(events.Items[0]), "entities") {
		t.Fatal(string(events.Items[0]))
	}
	ih.ok(t, "GET", "/v1/stores/"+key+"/memory?section=events&limit=1&after=0", nil, &events)
	if len(events.Items) != 1 || events.Next != nil || !strings.Contains(string(events.Items[0]), `"id":"e1"`) {
		t.Fatalf("%+v", events)
	}
	var facts memoryAnswer
	ih.ok(t, "GET", "/v1/stores/"+key+"/memory?section=facts", nil, &facts)
	var fact struct {
		ID     string `json:"id"`
		Key    string `json:"key"`
		Values []struct {
			Value      string   `json:"value"`
			Records    []string `json:"records"`
			Conflict   bool     `json:"conflict"`
			Supersedes *int     `json:"supersedes"`
		} `json:"values"`
	}
	json.Unmarshal(facts.Items[0], &fact)
	if fact.ID != "group · day" || fact.Key != fact.ID || len(fact.Values) != 2 || fact.Values[0].Supersedes != nil || fact.Values[1].Supersedes == nil ||
		*fact.Values[1].Supersedes != 0 || !fact.Values[1].Conflict || fact.Values[0].Conflict {
		t.Fatal(string(facts.Items[0]))
	}
	var directives memoryAnswer
	ih.ok(t, "GET", "/v1/stores/"+key+"/memory?section=directives", nil, &directives)
	if len(directives.Items) != 1 || !strings.Contains(string(directives.Items[0]), `"id":"d0"`) || !strings.Contains(string(directives.Items[0]), `"text":"Answer briefly."`) {
		t.Fatalf("%+v", directives)
	}
	for _, query := range []string{"section=nope", "limit=0", "limit=201", "after=-2", "after=x"} {
		ih.refused(t, "GET", "/v1/stores/"+key+"/memory?"+query, nil, http.StatusBadRequest, "invalid", strings.SplitN(query, "=", 2)[0])
	}
	ih.refused(t, "GET", "/v1/stores/"+userKey("nobody")+"/memory", nil, http.StatusNotFound, "store_not_found", "")
}

// Past --max-concurrent requests reading stores, the next is answered 503
// busy at once, with Retry-After; /healthz and /v1/info still answer.
func TestInspectBusy(t *testing.T) {
	h := layeredHarness(t)
	h.mustAdd(t, addRequest("r1", "u1"))
	ih := newInspector(t, h, func(config *InspectConfig) { config.MaxConcurrent = 1 })
	h.judge.block.Store(true)
	ctx, leave := context.WithCancel(context.Background())
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- ih.callContext(t, ctx, "POST", "/v1/search", map[string]any{"key": userKey("u1"), "query": question, "mode": "full"})
	}()
	eventually(t, func() bool { return h.judge.blocked.Load() > 0 })
	for _, path := range []string{"/v1/stores", "/v1/stores/" + userKey("u1") + "/records", "/v1/stores/" + userKey("u1") + "/records/r1",
		"/v1/stores/" + userKey("u1") + "/memory/threads/T1"} {
		response := ih.refused(t, "GET", path, nil, http.StatusServiceUnavailable, "busy", "")
		if response.Header().Get("Retry-After") != "2" {
			t.Fatal(response.Header())
		}
	}
	ih.ok(t, "GET", "/healthz", nil, nil)
	ih.ok(t, "GET", "/v1/info", nil, nil)
	leave() // the client leaves: its search ends, and its slot is free
	<-done
	h.judge.block.Store(false)
	ih.ok(t, "GET", "/v1/stores", nil, nil)
}

// Parked T18-d (final review): a fact's key is what the fold's model wrote,
// longer than 256 bytes when it likes: a lookup takes the longest a fold can
// write (lookupIDMax), so a long key's fact is found like any other.
func TestInspectMemoryItemWithALongKey(t *testing.T) {
	long := "long key " + strings.Repeat("k", 300)
	h := layeredHarness(t)
	h.consolidator.answer(func(context.Context, string) (string, error) {
		return `{"events":[{"thread":"new: Support group","date":"2023/05/08","text":"Caroline went to the support group.","records":["r0"]}],` +
			`"facts":[{"key":"` + long + `","value":"long","date":"2023/05/08","records":["r0"],"conflict":false}],"directives":[]}`, nil
	})
	for _, id := range []string{"r1", "r2", "r3"} {
		h.mustAdd(t, addRequest(id, "u1"))
	}
	ih := newInspector(t, h, nil)
	var fact struct {
		Item struct {
			Key string `json:"key"`
		} `json:"item"`
	}
	ih.ok(t, "GET", "/v1/stores/"+userKey("u1")+"/memory/facts/"+url.PathEscape(long), nil, &fact)
	if fact.Item.Key != long {
		t.Fatalf("%+v", fact)
	}
}

// Parked T18-e (final review): a full search that fails after its read ran,
// at a failure that is neither the store being busy nor the client leaving,
// answers 500 with what the read cost and its trace: the spend is the
// console's to show, as a 503 store_busy and a 502 provider_failed show it.
func TestFullSearchFailureAfterTheReadCarriesItsUsage(t *testing.T) {
	h := layeredHarness(t)
	h.mustAdd(t, addRequest("r1", "u1"))
	ih := newInspector(t, h, nil)
	usage := searchUsage{Calls: 3, Tokens: tokenCount{Input: 1200, Output: 40}, Cost: usageByKind{DeepSeek: 0.0026}}
	answer := searchOutcome{read: true, usage: usage, trace: []any{map[string]any{"name": "cues"}}}
	recorder := httptest.NewRecorder()
	ih.inspector.searchFailed(recorder, httptest.NewRequest("POST", "/v1/search", nil), inspectSearch{key: userKey("u1"), full: true, tracing: true}, answer, errors.New("the trace's store read failed"))
	var body struct {
		Error string          `json:"error"`
		Trace []any           `json:"trace"`
		Usage json.RawMessage `json:"usage"`
	}
	if recorder.Code != http.StatusInternalServerError || json.Unmarshal(recorder.Body.Bytes(), &body) != nil || body.Error != "internal" || len(body.Trace) != 1 ||
		string(body.Usage) != string(must(json.Marshal(usage))) {
		t.Fatalf("%d %s", recorder.Code, recorder.Body.String())
	}
	// Before the read, nothing was spent: the plain 500.
	recorder = httptest.NewRecorder()
	ih.inspector.searchFailed(recorder, httptest.NewRequest("POST", "/v1/search", nil), inspectSearch{key: userKey("u1")}, searchOutcome{}, errors.New("broken"))
	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), "usage") {
		t.Fatalf("%d %s", recorder.Code, recorder.Body.String())
	}
}

// Parked T18-f (final review): a store's row of GET /v1/stores (its records
// and consolidation's progress, a scan of its records) is counted once and
// kept for a minute while the store's files are as the scan found them: a
// page asked again soon opens no store whose files have not changed. Here a
// store made unreadable in place (its files' sizes and times as they were)
// still shows its counted row, and one whose files changed is counted again.
func TestInspectStoreRowsAreCountedOnce(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a file mode that keeps the owner out")
	}
	h := layeredHarness(t)
	for _, user := range []string{"u1", "u2"} {
		h.mustAdd(t, addRequest("r1", user))
	}
	ih := newInspector(t, h, nil)
	var first storesAnswer
	ih.ok(t, "GET", "/v1/stores", nil, &first)
	if len(first.Stores) != 2 || first.Stores[0].Records == nil || first.Stores[1].Records == nil {
		t.Fatalf("%+v", first)
	}
	// u1's records file kept out, its size and time as they were.
	records := filepath.Join(h.root, "users", userKey("u1"), "records.db")
	if err := os.Chmod(records, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(records, 0o600) })
	var second storesAnswer
	ih.ok(t, "GET", "/v1/stores", nil, &second)
	for _, row := range second.Stores {
		if row.Error != "" || row.Records == nil {
			t.Fatalf("a row counted a minute ago was counted again: %+v", row)
		}
	}
	// u2 adds: its files change, and its row is counted again.
	h.mustAdd(t, addRequest("r2", "u2"))
	var third storesAnswer
	ih.ok(t, "GET", "/v1/stores", nil, &third)
	for _, row := range third.Stores {
		if row.Key == userKey("u2") && (row.Records == nil || *row.Records != 2) {
			t.Fatalf("a changed store was not counted again: %+v", row)
		}
	}
}
