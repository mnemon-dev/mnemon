package aml

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
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
// reservation of 0.01 (the flat amount) against a cap of cap: a request in
// flight holds its reservation, and this one's endpoint keeps it waiting until
// the test ends. (A request that ends settles or releases its reservation.)
func holdBudget(t *testing.T, h *harness, cap float64) {
	t.Helper()
	budget, err := memoryagent.OpenBudgetWithCaps(filepath.Join(t.TempDir(), "budget.db"), map[string]float64{"openai": cap})
	if err != nil {
		t.Fatal(err)
	}
	h.server.budget = budget // closed by the server
	arrived, release := make(chan struct{}, 1), make(chan struct{})
	waiting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		arrived <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(waiting.Close)
	t.Setenv("ADMIN_TEST_OPENAI", "test-only-placeholder")
	provider, err := memoryagent.NewProvider(memoryagent.ModelConfig{Kind: "openai", Model: "gpt-4.1-mini", BaseURL: waiting.URL + "/v1", KeyEnv: "ADMIN_TEST_OPENAI", MaxAttempts: 1}, budget)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { provider.Close() }) // before the server closes the budget
	called := make(chan error, 1)
	go func() {
		_, _, err := provider.Complete(context.Background(), "s", "p")
		called <- err
	}()
	// First of the cleanups: the endpoint answers, and the call ends.
	t.Cleanup(func() {
		close(release)
		<-called
	})
	select {
	case <-arrived:
	case err := <-called:
		called <- err // for the cleanup
		t.Fatal("the request ended before the test:", err)
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
	want := []string{"addOutran", "adds", "breakerTrips", "breakers", "budgets", "degraded", "dropped", "lagBound", "linkFallbacks", "openStores", "partial", "partialLast10m", "providerKeys", "queue", "rejectedByKind", "searches", "searchesLast10m", "startedAt", "version"}
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
	// No provider or paid account: empty, not null. No consolidation lag
	// bound without background consolidation. No model of a key kind
	// (DeepSeek or JEV) in the configuration: no providerKeys entry.
	if string(fields["breakers"]) != "{}" || string(fields["breakerTrips"]) != "{}" || string(fields["rejectedByKind"]) != "{}" || string(fields["budgets"]) != "[]" || string(fields["lagBound"]) != "0" || string(fields["providerKeys"]) != "{}" {
		t.Fatal(string(fields["breakers"]), string(fields["breakerTrips"]), string(fields["rejectedByKind"]), string(fields["budgets"]), string(fields["lagBound"]), string(fields["providerKeys"]))
	}
	var started time.Time
	if err := json.Unmarshal(fields["startedAt"], &started); err != nil || started.Before(before) || started.After(time.Now()) {
		t.Fatal(string(fields["startedAt"]), err)
	}
	// It names the moment the server was made, whenever it is asked.
	if again := h.adminStatus(t); !again.StartedAt.Equal(started) {
		t.Fatal(again.StartedAt, started)
	}
	// Each entry of providerKeys is a source and a fingerprint, and a kind
	// without any key says so.
	h = newHarness(t, func(config *ServerConfig) {
		config.MemoryConfig.Planner = &memoryagent.ModelConfig{Kind: "deepseek", Model: "deepseek-flash", KeyEnv: "STATUS_TEST_DEEPSEEK"}
		config.MemoryConfig.Judge = &memoryagent.ModelConfig{Kind: "jev", Model: "jev-1.13.0", KeyEnv: "STATUS_TEST_JEV"}
	})
	t.Setenv("STATUS_TEST_DEEPSEEK", "host-deepseek-key-0001")
	t.Setenv("STATUS_TEST_JEV", "")
	if err := json.Unmarshal(h.adminCall("GET", "/admin/status").Body.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	if got, want := string(fields["providerKeys"]), `{"deepseek":{"source":"host","fingerprint":"`+memoryagent.Fingerprint("host-deepseek-key-0001")+`"},"jev":{"source":"none","fingerprint":""}}`; got != want {
		t.Fatalf("providerKeys %s, want %s", got, want)
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
	if want := []string{"cap", "held", "kind", "source", "spent", "unlimited"}; !slices.Equal(got, want) {
		t.Fatalf("account fields %v, want %v", got, want)
	}
	// Without a settings file every cap is the configuration's, and none is
	// unlimited.
	if string(status.Budgets[0]["source"]) != `"config"` || string(status.Budgets[0]["unlimited"]) != "false" || string(status.Budgets[0]["cap"]) != "1" {
		t.Fatal(status.Budgets[0])
	}
}

// Accounts reads the paid accounts, as the command's watchdog does: none
// without a budget file, an error once the file is closed.
func TestAccountsReadTheBudget(t *testing.T) {
	h := newHarness(t, nil)
	if accounts, err := h.server.Accounts(context.Background()); err != nil || len(accounts) != 0 {
		t.Fatal(accounts, err)
	}
	holdBudget(t, h, 1)
	accounts, err := h.server.Accounts(context.Background())
	if err != nil || len(accounts) != 1 || accounts[0].Kind != "openai" || accounts[0].Held <= 0 || accounts[0].Cap != 1 {
		t.Fatal(accounts, err)
	}
	h.server.Close()
	if _, err := h.server.Accounts(context.Background()); err == nil {
		t.Fatal("the budget answered after Close")
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

// lagBound is the bound a background Add waits at, in the unit of
// queue.maxBacklog, so that the alert watcher compares the two without a
// number of its own. Without background consolidation no Add waits, and the
// status says so with 0.
func TestAdminStatusReportsTheLagBound(t *testing.T) {
	settings := memoryagent.ConsolidationConfig{Mode: "timelines", BatchCharacters: 300, MaxBatches: 2}
	bound := func(mode ConsolidationMode, settings memoryagent.ConsolidationConfig) int64 {
		t.Helper()
		h := newHarness(t, func(config *ServerConfig) {
			config.MemoryConfig.Consolidation = &settings
			config.Consolidation = mode
		})
		return h.adminStatus(t).LagBound
	}
	if got := bound(ConsolidationBackground, settings); got != 600 {
		t.Fatal("two batches of 300 characters:", got)
	}
	if got := bound(ConsolidationBackground, memoryagent.ConsolidationConfig{Mode: "layered"}); got != 160000 {
		t.Fatal("the default job window:", got)
	}
	for _, mode := range []ConsolidationMode{ConsolidationOff, ConsolidationOnAdd} {
		if got := bound(mode, settings); got != 0 {
			t.Fatalf("mode %q: %d", mode, got)
		}
	}
}

// A breaker's openings only grow, so a breaker that opened and closed between
// two reads still shows: the states of breakers do not.
func TestAdminStatusCountsBreakerTrips(t *testing.T) {
	broken := openBreaker(t)
	h := serverWith(t, broken)
	status := h.adminStatus(t)
	if status.Breakers["local"] != "open" || len(status.BreakerTrips) != 1 || status.BreakerTrips["local"] != 1 {
		t.Fatalf("breakers %v, trips %v", status.Breakers, status.BreakerTrips)
	}
	// Reading it changes nothing.
	if again := h.adminStatus(t); again.BreakerTrips["local"] != 1 {
		t.Fatal(again.BreakerTrips)
	}
	// A kind with a closed breaker is in the map with 0 trips.
	endpoint := newModelEndpoint(t, func(_ int, w http.ResponseWriter) {
		json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": "ok"}, "finish_reason": "stop"}}})
	})
	healthy, err := memoryagent.NewProvider(memoryagent.ModelConfig{Kind: "local", Model: "m", BaseURL: endpoint.URL + "/v1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { healthy.Close() })
	closed := serverWith(t, healthy).adminStatus(t)
	if closed.Breakers["local"] != "closed" || len(closed.BreakerTrips) != 1 || closed.BreakerTrips["local"] != 0 {
		t.Fatalf("breakers %v, trips %v", closed.Breakers, closed.BreakerTrips)
	}
}

// rejectedByKind counts, by provider kind, the requests the provider
// rejected with 401, 402 or 403 (ruling R65), from 0 for every kind the
// models have: the attempts whose outcome is rejected, which the metrics
// count too. Reading it changes nothing.
func TestAdminStatusCountsRejections(t *testing.T) {
	// An empty balance, as DeepSeek answers it.
	endpoint := newModelEndpoint(t, func(_ int, w http.ResponseWriter) {
		w.WriteHeader(http.StatusPaymentRequired)
		io.WriteString(w, `{"error":{"message":"Insufficient Balance"}}`)
	})
	provider, err := memoryagent.NewProvider(memoryagent.ModelConfig{Kind: "local", Model: "m", BaseURL: endpoint.URL + "/v1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { provider.Close() })
	h := serverWith(t, provider)
	if status := h.adminStatus(t); len(status.RejectedByKind) != 1 || status.RejectedByKind["local"] != 0 {
		t.Fatal(status.RejectedByKind)
	}
	for range 2 {
		if _, _, err := provider.Complete(context.Background(), "s", "p"); err == nil || !strings.Contains(err.Error(), "model HTTP 402") {
			t.Fatal(err)
		}
	}
	for range 2 {
		if status := h.adminStatus(t); len(status.RejectedByKind) != 1 || status.RejectedByKind["local"] != 2 {
			t.Fatal(status.RejectedByKind)
		}
	}
	m := h.scrape(t)
	m.is(t, 2, "mnemon_aml_model_attempts_total", "kind", "local", "class", "interactive", "outcome", "rejected")
	m.is(t, 2, "mnemon_aml_model_attempt_seconds_count", "kind", "local", "class", "interactive")
}

// budgets is always an array: [] when no account has been used and when the
// budget file cannot be read (/ready reports why), never null.
func TestAdminStatusBudgetsAreAnArrayWhenTheBudgetCannotBeRead(t *testing.T) {
	h := newHarness(t, nil)
	holdBudget(t, h, 1)
	body := func() map[string]json.RawMessage {
		t.Helper()
		response := h.adminCall("GET", "/admin/status")
		var fields map[string]json.RawMessage
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &fields) != nil {
			t.Fatal(response.Code, response.Body.String())
		}
		return fields
	}
	if got := string(body()["budgets"]); !strings.HasPrefix(got, `[{"kind":"openai"`) {
		t.Fatal(got)
	}
	if err := h.server.budget.Close(); err != nil {
		t.Fatal(err)
	}
	if got := string(body()["budgets"]); got != "[]" {
		t.Fatal("an unreadable budget file shows as", got)
	}
}

// ---- the admin token ----

// testAdminToken is the admin token of the servers that have one: longer than
// the 32 characters a server asks for.
const testAdminToken = "adm1n-t0ken-0123456789abcdef-ghijklmnop"

// adminBearer is the Authorization header of a request that holds the token.
var adminBearer = []string{"Authorization", "Bearer " + testAdminToken}

// adminCallWith sends a request to the admin handler with headers, given as
// name, value pairs.
func (h *harness) adminCallWith(method, path string, headers ...string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		request.Header.Set(headers[i], headers[i+1])
	}
	recorder := httptest.NewRecorder()
	h.server.AdminHandler().ServeHTTP(recorder, request)
	return recorder
}

// tokenHarness is a harness whose admin routes demand testAdminToken, with
// room on its disk so that /ready can say yes.
func tokenHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t, func(config *ServerConfig) { config.AdminToken = testAdminToken })
	h.roomy()
	return h
}

// authFailures is mnemon_aml_admin_auth_failures_total, scraped with the token.
func (h *harness) authFailures(t *testing.T) float64 {
	t.Helper()
	response := h.adminCallWith("GET", "/metrics", adminBearer...)
	if response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}
	for _, line := range strings.Split(response.Body.String(), "\n") {
		if value, found := strings.CutPrefix(line, "mnemon_aml_admin_auth_failures_total "); found {
			count, err := strconv.ParseFloat(value, 64)
			if err != nil {
				t.Fatal(line, err)
			}
			return count
		}
	}
	t.Fatal("no mnemon_aml_admin_auth_failures_total series")
	return 0
}

// With an admin token every admin route answers only the request that holds it
// as a Bearer token (the scheme in any case). Any other gets 401 with the
// fixed body, counted, and nothing the token or its digest is made of reaches
// a log, a metric or an answer.
func TestAdminRoutesRequireToken(t *testing.T) {
	h := tokenHarness(t)
	routes := []string{"/metrics", "/ready", "/admin/status", "/admin/v1/stores", "/debug/pprof/"}
	almost := testAdminToken[:len(testAdminToken)-1] + "!" // the token but for its last character
	refused := []struct {
		name    string
		headers []string
	}{
		{"no token", nil},
		{"a wrong token", []string{"Authorization", "Bearer " + almost}},
		{"the token cut short", []string{"Authorization", "Bearer " + testAdminToken[:len(testAdminToken)-1]}},
		{"the token and more", []string{"Authorization", "Bearer " + testAdminToken + "x"}},
		{"nothing after the scheme", []string{"Authorization", "Bearer "}},
		{"the token without a scheme", []string{"Authorization", testAdminToken}},
		{"the Token scheme", []string{"Authorization", "Token " + testAdminToken}},
		{"X-Api-Key", []string{"X-Api-Key", testAdminToken}},
		{"X-Api-Key beside a wrong token", []string{"Authorization", "Bearer " + almost, "X-Api-Key", testAdminToken}},
	}
	logged := h.log.String()
	for _, route := range routes {
		for _, tc := range refused {
			response := h.adminCallWith("GET", route, tc.headers...)
			if response.Code != http.StatusUnauthorized ||
				response.Header().Get("WWW-Authenticate") != `Bearer realm="mnemon-admin"` ||
				response.Header().Get("Content-Type") != "application/json" ||
				strings.TrimSpace(response.Body.String()) != `{"error":"missing or invalid admin token"}` {
				t.Errorf("%s with %s: %d %v %q", route, tc.name, response.Code, response.Header(), response.Body.String())
			}
		}
	}
	failures := float64(len(routes) * len(refused))
	if got := h.authFailures(t); got != failures {
		t.Fatalf("%v failures counted, %v made", got, failures)
	}
	for _, route := range routes {
		for _, scheme := range []string{"Bearer", "bearer", "BEARER", "bEaReR"} {
			if response := h.adminCallWith("GET", route, "Authorization", scheme+" "+testAdminToken); response.Code != http.StatusOK {
				t.Errorf("%s with %s: %d %.80q", route, scheme, response.Code, response.Body.String())
			}
		}
	}
	// Requests that held the token are not failures.
	if got := h.authFailures(t); got != failures {
		t.Fatalf("%v failures counted after the accepted requests, %v made", got, failures)
	}
	// No line is logged for a failure: the counter is the signal. Neither the
	// token, nor what was sent in its place, nor its digest is anywhere.
	if h.log.String() != logged {
		t.Errorf("failures were logged: %s", h.log.String())
	}
	digest := sha256.Sum256([]byte(testAdminToken))
	metrics := h.adminCallWith("GET", "/metrics", adminBearer...).Body.String()
	for name, text := range map[string]string{"the log": h.log.String(), "the metrics": metrics} {
		for _, secret := range []string{testAdminToken, almost, hex.EncodeToString(digest[:]), hex.EncodeToString(digest[:8])} {
			if strings.Contains(text, secret) {
				t.Errorf("%s shows %q", name, secret)
			}
		}
	}
}

// Without a token the admin listener is what it was: nothing is asked of a
// request, a header it happens to carry is ignored, an unknown path is 404
// (not 401), and no failure is ever counted.
func TestAdminWithoutTokenUnchanged(t *testing.T) {
	h := newHarness(t, nil)
	h.roomy()
	for _, route := range []string{"/metrics", "/ready", "/admin/status", "/admin/v1/stores", "/debug/pprof/"} {
		for _, headers := range [][]string{nil, {"Authorization", "Bearer anything"}, {"X-Api-Key", "k"}} {
			response := h.adminCallWith("GET", route, headers...)
			if response.Code != http.StatusOK || response.Header().Get("WWW-Authenticate") != "" {
				t.Errorf("%s with %v: %d %v", route, headers, response.Code, response.Header())
			}
		}
	}
	if response := h.adminCallWith("GET", "/no-such-route"); response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"not found"`) {
		t.Errorf("an unknown path: %d %q", response.Code, response.Body.String())
	}
	// The routes are for reading: the fallback answers anything else.
	if response := h.adminCallWith("POST", "/metrics"); response.Code != http.StatusNotFound {
		t.Errorf("POST /metrics: %d", response.Code)
	}
	h.scrape(t).is(t, 0, "mnemon_aml_admin_auth_failures_total")
}

// A token shorter than 32 bytes is no token: the server is not made, and
// what it says, in bytes (parked T14-c), does not repeat the token. Its
// length is counted in bytes: eleven CJK characters, 33 bytes, are a token.
func TestServerRefusesAShortAdminToken(t *testing.T) {
	if MinAdminTokenLength != 32 {
		t.Fatal("the shortest admin token is", MinAdminTokenLength)
	}
	short := strings.Repeat("s", MinAdminTokenLength-1)
	server, err := NewServer(ServerConfig{
		DataRoot: t.TempDir(), MemoryConfig: simpleConfig(), AuthKey: testKey, AdminToken: short,
		Models: func(memoryagent.Config) (memoryagent.Models, error) {
			return memoryagent.Models{Planner: &fakePlanner{}, Judge: &fakeJudge{}, Consolidator: &fakeConsolidator{}}, nil
		},
	})
	if err == nil {
		server.Close()
		t.Fatal("a server was made with a 31-character admin token")
	}
	if strings.Contains(err.Error(), short) || !strings.Contains(err.Error(), "admin token") || !strings.Contains(err.Error(), "32 bytes") {
		t.Fatal(err)
	}
	server, err = NewServer(ServerConfig{
		DataRoot: t.TempDir(), MemoryConfig: simpleConfig(), AuthKey: testKey, AdminToken: strings.Repeat("日", 11),
		Models: func(memoryagent.Config) (memoryagent.Models, error) {
			return memoryagent.Models{Planner: &fakePlanner{}, Judge: &fakeJudge{}, Consolidator: &fakeConsolidator{}}, nil
		},
	})
	if err != nil {
		t.Fatal("a token of 33 bytes:", err)
	}
	server.Close()
}

// /admin/status says which build runs: the version the server is made with.
func TestAdminStatusHasVersion(t *testing.T) {
	h := newHarness(t, func(config *ServerConfig) { config.Version = "x.y" })
	if body := h.adminCall("GET", "/admin/status").Body.String(); !strings.Contains(body, `"version":"x.y"`) {
		t.Fatal(body)
	}
	if got := h.adminStatus(t).Version; got != "x.y" {
		t.Fatal(got)
	}
}
