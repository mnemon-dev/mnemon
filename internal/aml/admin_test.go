package aml

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
)

// readyBody is what /ready answers.
type readyBody struct {
	Ready   bool     `json:"ready"`
	Reasons []string `json:"reasons"`
}

// ready asks /ready for its status and answer.
func (h *harness) ready(t *testing.T) (int, readyBody) {
	t.Helper()
	response := h.adminCall("GET", "/ready")
	var body readyBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Header().Get("Content-Type") != "application/json" {
		t.Fatal(response.Code, response.Header(), response.Body.String(), err)
	}
	return response.Code, body
}

// roomy gives the server all the free disk it asks for, so that readiness does
// not depend on the machine the test runs on.
func (h *harness) roomy() {
	h.server.freeDisk = func(string) (uint64, error) { return 100 << 30, nil }
}

// holdBudget gives the server a budget file in which the openai account holds a
// reservation of 0.01 (the flat amount) against a cap of cap: a request the
// endpoint hung up on may have been processed, so its reservation stays held.
func holdBudget(t *testing.T, h *harness, cap float64) {
	t.Helper()
	budget, err := memoryagent.OpenBudgetWithCaps(filepath.Join(t.TempDir(), "budget.db"), map[string]float64{"openai": cap})
	if err != nil {
		t.Fatal(err)
	}
	h.server.budget = budget // closed by the server
	hangup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
			conn.Close()
		}
	}))
	t.Cleanup(hangup.Close)
	t.Setenv("ADMIN_TEST_OPENAI", "test-only-placeholder")
	provider, err := memoryagent.NewProvider(memoryagent.ModelConfig{Kind: "openai", Model: "gpt-4.1-mini", BaseURL: hangup.URL + "/v1", KeyEnv: "ADMIN_TEST_OPENAI", MaxAttempts: 1}, budget)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { provider.Close() }) // before the server closes the budget
	if _, _, err := provider.Complete(context.Background(), "s", "p"); err == nil {
		t.Fatal("a request the endpoint hung up on succeeded")
	}
}

// openBreaker is a provider of kind local whose breaker is open: its endpoint
// answers 503 to everything and a run of such failures opens it.
func openBreaker(t *testing.T) *memoryagent.Provider {
	t.Helper()
	failing := newModelEndpoint(t, func(_ int, w http.ResponseWriter) { w.WriteHeader(http.StatusServiceUnavailable) })
	provider, err := memoryagent.NewProvider(memoryagent.ModelConfig{Kind: "local", Model: "m", BaseURL: failing.URL + "/v1", MaxAttempts: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { provider.Close() })
	models := memoryagent.Models{Planner: provider}
	for range 20 {
		if models.Breakers()[0].State == memoryagent.BreakerOpen {
			return provider
		}
		provider.Complete(context.Background(), "s", "p")
	}
	t.Fatal("the breaker stayed", models.Breakers())
	return nil
}

// serverWith is a harness whose models are the given roles' provider.
func serverWith(t *testing.T, planner memoryagent.Completer) *harness {
	t.Helper()
	return newHarness(t, func(config *ServerConfig) {
		config.Models = func(memoryagent.Config) (memoryagent.Models, error) {
			return memoryagent.Models{Planner: planner, Judge: &fakeJudge{}, Consolidator: &fakeConsolidator{}}, nil
		}
	})
}

func TestReadyWhenNothingIsWrong(t *testing.T) {
	h := newHarness(t, nil)
	h.roomy()
	response := h.adminCall("GET", "/ready")
	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != `{"ready":true}` {
		t.Fatal(response.Code, response.Body.String())
	}
	// A probe of the data root leaves nothing in it.
	if entries, err := os.ReadDir(h.root); err != nil || len(entries) != 0 {
		t.Fatal("readiness left", entries, err)
	}
}

func TestReadyReportsReasons(t *testing.T) {
	h := newHarness(t, nil)
	h.roomy()
	// A cap of 0.01/0.96 leaves the account at 96% with the one reservation.
	holdBudget(t, h, 0.01/0.96)
	status, body := h.ready(t)
	if status != http.StatusServiceUnavailable || body.Ready || !slices.Equal(body.Reasons, []string{"budget openai is at 96.0% of its cap"}) {
		t.Fatal(status, body)
	}
	// /admin/status and the gauges show the account the reason is about.
	accounts := h.adminStatus(t).Budgets
	if len(accounts) != 1 || accounts[0].Kind != "openai" || accounts[0].Held != 0.01 || accounts[0].Spent != 0 || accounts[0].Cap != 0.01/0.96 {
		t.Fatalf("%+v", accounts)
	}
	m := h.scrape(t)
	m.is(t, 0.01, "mnemon_aml_budget_held", "account", "openai")
	m.is(t, 0, "mnemon_aml_budget_spent", "account", "openai")
	m.is(t, 0.01/0.96, "mnemon_aml_budget_cap", "account", "openai")
}

func TestReadyWithBudgetHeadroom(t *testing.T) {
	h := newHarness(t, nil)
	h.roomy()
	holdBudget(t, h, 0.04) // 25% of the cap
	if status, body := h.ready(t); status != http.StatusOK || !body.Ready || len(body.Reasons) != 0 {
		t.Fatal(status, body)
	}
}

// The reasons are those of accounts strictly above 95% of their cap, held
// reservations counted as spent; a cap of zero has no percentage.
func TestOverBudgetReasons(t *testing.T) {
	got := overBudget([]memoryagent.BudgetAccount{
		{Kind: "a", Cap: 100, Spent: 94, Held: 0.9},
		{Kind: "b", Cap: 100, Spent: 95},
		{Kind: "c", Cap: 100, Spent: 90, Held: 5.1},
		{Kind: "d", Cap: 100, Spent: 120},
		{Kind: "e", Cap: 0, Held: 0.01},
	})
	if want := []string{"budget c is at 95.1% of its cap", "budget d is at 120.0% of its cap"}; !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestReadyReportsAnOpenBreaker(t *testing.T) {
	broken := openBreaker(t)
	h := serverWith(t, broken)
	h.roomy()
	status, body := h.ready(t)
	if status != http.StatusServiceUnavailable || body.Ready || !slices.Equal(body.Reasons, []string{"breaker open: local"}) {
		t.Fatal(status, body)
	}
	if breakers := h.adminStatus(t).Breakers; len(breakers) != 1 || breakers["local"] != "open" {
		t.Fatal(breakers)
	}
	h.scrape(t).is(t, 1, "mnemon_aml_breaker_open", "kind", "local")
}

func TestBreakerGaugeIsZeroWhileClosed(t *testing.T) {
	endpoint := newModelEndpoint(t, func(_ int, w http.ResponseWriter) {
		json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": "ok"}, "finish_reason": "stop"}}})
	})
	provider, err := memoryagent.NewProvider(memoryagent.ModelConfig{Kind: "local", Model: "m", BaseURL: endpoint.URL + "/v1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := serverWith(t, provider)
	t.Cleanup(func() { provider.Close() })
	h.roomy()
	if status, body := h.ready(t); status != http.StatusOK {
		t.Fatal(status, body)
	}
	if breakers := h.adminStatus(t).Breakers; len(breakers) != 1 || breakers["local"] != "closed" {
		t.Fatal(breakers)
	}
	h.scrape(t).is(t, 0, "mnemon_aml_breaker_open", "kind", "local")
}

// Only a breaker that refuses calls makes the server not ready: a half-open
// one is probing the provider, a closed one is serving it.
func TestBreakerReasons(t *testing.T) {
	got := breakerReasons(map[string]string{"jev": "half-open", "local": "open", "openai": "closed", "deepseek": "open"})
	if want := []string{"breaker open: deepseek", "breaker open: local"}; !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := breakerReasons(map[string]string{}); got == nil || len(got) != 0 {
		t.Fatalf("got %#v", got)
	}
}

// Of the providers that share a kind (the planner and the consolidator can
// both be DeepSeek), the kind is as bad as its worst breaker.
func TestKindsAreAsBadAsTheirWorstBreaker(t *testing.T) {
	for _, tc := range []struct {
		states []string
		want   string
	}{
		{nil, ""},
		{[]string{"closed"}, "closed"},
		{[]string{"closed", "half-open"}, "half-open"},
		{[]string{"half-open", "closed", "open"}, "open"},
		{[]string{"open", "closed"}, "open"},
	} {
		worst := ""
		for _, state := range tc.states {
			worst = worse(worst, state)
		}
		if worst != tc.want {
			t.Errorf("%v: %q, want %q", tc.states, worst, tc.want)
		}
	}
}

func TestReadyReportsDiskAndDataRoot(t *testing.T) {
	h := newHarness(t, nil)
	var asked string
	free := uint64(1<<30 - 1)
	h.server.freeDisk = func(dir string) (uint64, error) { asked = dir; return free, nil }
	// One byte short of 1 GiB is too little…
	status, body := h.ready(t)
	if status != http.StatusServiceUnavailable || !slices.Equal(body.Reasons, []string{"free disk 1023 MiB is below 1 GiB"}) || asked != h.root {
		t.Fatal(status, body, asked)
	}
	// …1 GiB is enough.
	free = 1 << 30
	if status, body = h.ready(t); status != http.StatusOK {
		t.Fatal(status, body)
	}
	// A disk that cannot be measured is not ready either.
	h.server.freeDisk = func(string) (uint64, error) { return 0, errors.New("statfs: boom") }
	if status, body = h.ready(t); status != http.StatusServiceUnavailable || !slices.Equal(body.Reasons, []string{"free disk unreadable: statfs: boom"}) {
		t.Fatal(status, body)
	}
	// So is a data root that is gone.
	h.roomy()
	if err := os.RemoveAll(h.root); err != nil {
		t.Fatal(err)
	}
	if status, body = h.ready(t); status != http.StatusServiceUnavailable || len(body.Reasons) != 1 || !strings.HasPrefix(body.Reasons[0], "data root is not writable: ") {
		t.Fatal(status, body)
	}
}

func TestReadyReportsAReadOnlyDataRoot(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not stop this user")
	}
	h := newHarness(t, nil)
	h.roomy()
	if err := os.Chmod(h.root, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(h.root, 0o700) })
	status, body := h.ready(t)
	if status != http.StatusServiceUnavailable || len(body.Reasons) != 1 || !strings.HasPrefix(body.Reasons[0], "data root is not writable: ") {
		t.Fatal(status, body)
	}
}

// Every reason a server has is given, not only the first.
func TestReadyGivesEveryReason(t *testing.T) {
	broken := openBreaker(t)
	h := serverWith(t, broken)
	h.server.freeDisk = func(string) (uint64, error) { return 0, nil }
	holdBudget(t, h, 0.01)
	status, body := h.ready(t)
	want := []string{"free disk 0 MiB is below 1 GiB", "breaker open: local", "budget openai is at 100.0% of its cap"}
	if status != http.StatusServiceUnavailable || !slices.Equal(body.Reasons, want) {
		t.Fatalf("%d %q, want %q", status, body.Reasons, want)
	}
}

func TestFreeDiskBytes(t *testing.T) {
	free, err := freeDiskBytes(t.TempDir())
	if err != nil || free == 0 {
		t.Fatal(free, err)
	}
	if _, err := freeDiskBytes(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("the free disk of a missing directory was measured")
	}
}

// The profiling endpoints exist on the admin listener alone: the public
// handler, which the platform reaches, serves none of the admin routes, and
// the admin handler serves none of the public ones.
func TestAdminServesPprofOnlyOnAdmin(t *testing.T) {
	h := newHarness(t, nil)
	for _, path := range []string{"/debug/pprof/", "/debug/pprof/heap?debug=1", "/debug/pprof/cmdline", "/metrics", "/ready", "/admin/status"} {
		for _, headers := range [][]string{nil, {"X-None", ""}} {
			if response := h.call("GET", path, nil, headers...); response.Code != http.StatusNotFound {
				t.Errorf("public %s (%v): %d", path, headers, response.Code)
			}
		}
	}
	for path, mustHave := range map[string]string{
		"/debug/pprof/":             "goroutine",
		"/debug/pprof/heap?debug=1": "heap profile",
		"/debug/pprof/goroutine":    "",
		"/debug/pprof/cmdline":      "",
		"/debug/pprof/symbol":       "num_symbols",
	} {
		response := h.adminCall("GET", path)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), mustHave) {
			t.Errorf("admin %s: %d %.80q", path, response.Code, response.Body.String())
		}
	}
	if response := h.adminCall("GET", "/debug/pprof/no-such-profile"); response.Code != http.StatusNotFound {
		t.Errorf("an unknown profile: %d", response.Code)
	}
	for _, path := range []string{"/add", "/search", "/health", "/"} {
		if response := h.adminCall("GET", path); response.Code != http.StatusNotFound {
			t.Errorf("admin %s: %d", path, response.Code)
		}
		if response := h.adminCall("POST", path); response.Code != http.StatusNotFound {
			t.Errorf("admin POST %s: %d", path, response.Code)
		}
	}
	// The operator's endpoints are for reading.
	for _, path := range []string{"/metrics", "/ready", "/admin/status"} {
		if response := h.adminCall("POST", path); response.Code == http.StatusOK {
			t.Errorf("POST %s was answered", path)
		}
	}
	if response := h.adminCall("GET", "/metrics"); !strings.HasPrefix(response.Header().Get("Content-Type"), "text/plain") {
		t.Error(response.Header())
	}
}

// /admin/status is read by the alert watcher: its field names are a contract.
func TestAdminStatusFieldsAreStable(t *testing.T) {
	before := time.Now().Add(-time.Second)
	h := newHarness(t, nil)
	response := h.adminCall("GET", "/admin/status")
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/json" {
		t.Fatal(response.Code, response.Header())
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &fields); err != nil {
		t.Fatal(err, response.Body.String())
	}
	keys := func(raw map[string]json.RawMessage) []string {
		out := []string{}
		for key := range raw {
			out = append(out, key)
		}
		slices.Sort(out)
		return out
	}
	want := []string{"addOutran", "adds", "breakers", "budgets", "degraded", "dropped", "linkFallbacks", "openStores", "partial", "partialLast10m", "queue", "searches", "searchesLast10m", "startedAt"}
	if got := keys(fields); !slices.Equal(got, want) {
		t.Fatalf("fields %v, want %v", got, want)
	}
	var queue map[string]json.RawMessage
	if err := json.Unmarshal(fields["queue"], &queue); err != nil {
		t.Fatal(err)
	}
	if got, want := keys(queue), []string{"backingOff", "held", "maxBacklog", "oldestQueued", "queued", "running"}; !slices.Equal(got, want) {
		t.Fatalf("queue fields %v, want %v", got, want)
	}
	// No provider or paid account: empty, not null.
	if string(fields["breakers"]) != "{}" || string(fields["budgets"]) != "[]" {
		t.Fatal(string(fields["breakers"]), string(fields["budgets"]))
	}
	var started time.Time
	if err := json.Unmarshal(fields["startedAt"], &started); err != nil || started.Before(before) || started.After(time.Now()) {
		t.Fatal(string(fields["startedAt"]), err)
	}
	// It names the moment the server was made, whenever it is asked.
	if again := h.adminStatus(t); !again.StartedAt.Equal(started) {
		t.Fatal(again.StartedAt, started)
	}
}

func TestAdminStatusAccountFieldsAreStable(t *testing.T) {
	h := newHarness(t, nil)
	holdBudget(t, h, 1)
	var status struct{ Budgets []map[string]json.RawMessage }
	if err := json.Unmarshal(h.adminCall("GET", "/admin/status").Body.Bytes(), &status); err != nil || len(status.Budgets) != 1 {
		t.Fatal(err, status)
	}
	got := []string{}
	for key := range status.Budgets[0] {
		got = append(got, key)
	}
	slices.Sort(got)
	if want := []string{"cap", "held", "kind", "spent"}; !slices.Equal(got, want) {
		t.Fatalf("account fields %v, want %v", got, want)
	}
}

func TestAdminStatusCountsWhatTheStatusLinePrints(t *testing.T) {
	h := newHarness(t, nil)
	h.mustAdd(t, addRequest("r1", "u1"))
	h.mustAdd(t, addRequest("r2", "u2"))
	h.search(t, SearchRequest{Query: question, UserID: "u1", TopK: 10})
	h.planner.panics.Store(true)
	h.search(t, SearchRequest{Query: question, UserID: "u1", TopK: 10})
	status := h.adminStatus(t)
	if status.Adds != 2 || status.Searches != 2 || status.Degraded != 1 || status.Partial != 0 || status.OpenStores != 2 ||
		status.SearchesLast10m != 2 || status.PartialLast10m != 0 || status.Dropped != 0 || status.LinkFallbacks != 0 || status.AddOutran != 0 {
		t.Fatalf("%+v", status)
	}
	if line := h.server.statusLine(context.Background()); line != "status: adds 2, searches 2, degraded 1, partially degraded 0" {
		t.Fatal(line)
	}
}
