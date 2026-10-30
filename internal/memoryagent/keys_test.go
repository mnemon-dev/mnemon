package memoryagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// keysConfig is a configuration of the three roles, DeepSeek planning and
// folding and JEV judging, each naming its host key's variable.
func keysConfig(deepseekEnv, jevEnv string) Config {
	return Config{
		Planner:      &ModelConfig{Kind: "deepseek", Model: "deepseek-flash", KeyEnv: deepseekEnv},
		Judge:        &ModelConfig{Kind: "jev", Model: "jev-1.13.0", KeyEnv: jevEnv},
		Consolidator: &ModelConfig{Kind: "deepseek", Model: "deepseek-flash", KeyEnv: deepseekEnv},
	}
}

func sha12(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:12]
}

// The key source: for each kind the configuration uses, the account's key
// over the host's; a status that names only fingerprints; and account keys
// that are checked before they are taken.
func TestProviderKeysSourceAndStatus(t *testing.T) {
	const hostDS, hostJEV, accountDS = "host-deepseek-0001", "host-jev-0001", "account-deepseek-0001"
	t.Setenv("UNIT_KEYS_DS", hostDS)
	t.Setenv("UNIT_KEYS_JEV", hostJEV)
	keys := NewProviderKeys(keysConfig("UNIT_KEYS_DS", "UNIT_KEYS_JEV"))
	if got := keys.Kinds(); !slices.Equal(got, []string{"deepseek", "jev"}) {
		t.Fatal(got)
	}
	if !keys.Uses("deepseek") || !keys.Uses("jev") || keys.Uses("openai") || keys.Uses("local") {
		t.Fatal("Uses")
	}
	want := map[string]KeyStatus{"deepseek": {KeySourceHost, sha12(hostDS)}, "jev": {KeySourceHost, sha12(hostJEV)}}
	if got := keys.Status(); !mapsEqual(got, want) {
		t.Fatalf("status %v, want %v", got, want)
	}
	if got := keys.credential("deepseek", "UNIT_KEYS_DS"); got != hostDS {
		t.Fatal(got)
	}

	// An account key wins over the host's, for its kind only.
	if err := keys.SetAccount(map[string]string{"deepseek": accountDS}); err != nil {
		t.Fatal(err)
	}
	want["deepseek"] = KeyStatus{KeySourceAccount, sha12(accountDS)}
	if got := keys.Status(); !mapsEqual(got, want) {
		t.Fatalf("status %v, want %v", got, want)
	}
	if keys.credential("deepseek", "UNIT_KEYS_DS") != accountDS || keys.credential("jev", "UNIT_KEYS_JEV") != hostJEV {
		t.Fatal("the account key does not win for its kind alone")
	}
	account := keys.Account()
	account["jev"] = "mutated-0000"
	if _, ok := keys.Account()["jev"]; ok {
		t.Fatal("Account is not a copy")
	}

	// A kind the configuration does not use, or a key that is no key, is
	// refused, and nothing changes; the error never holds the key.
	for _, bad := range []map[string]string{
		{"openai": "sk-0123456789"},
		{"jev": "short"},
		{"jev": "has a space in it"},
		{"jev": strings.Repeat("k", 513)},
		{"jev": "non-ascii-ключ-0001"},
		{"deepseek": "account-deepseek-0002", "local": "local-key-000"},
	} {
		err := keys.SetAccount(bad)
		if err == nil {
			t.Fatalf("SetAccount(%v) was accepted", bad)
		}
		for _, key := range bad {
			if strings.Contains(err.Error(), key) {
				t.Fatalf("the error %q holds the key", err)
			}
		}
		if got := keys.Status(); !mapsEqual(got, want) {
			t.Fatalf("a refused SetAccount changed the status to %v", got)
		}
	}

	// A kind with no key at all has none.
	t.Setenv("UNIT_KEYS_JEV", "")
	if got := keys.Status()["jev"]; got != (KeyStatus{KeySourceNone, ""}) {
		t.Fatal(got)
	}
	if err := keys.SetAccount(nil); err != nil {
		t.Fatal(err)
	}
	if got := keys.Status()["deepseek"]; got != (KeyStatus{KeySourceHost, sha12(hostDS)}) {
		t.Fatal("clearing the account keys left", got)
	}

	// The fingerprint, the key rules and the kinds.
	if Fingerprint("abc") != "ba7816bf8f01" {
		t.Fatal(Fingerprint("abc"))
	}
	for key, valid := range map[string]bool{"12345678": true, strings.Repeat("~", 512): true, "1234567": false, strings.Repeat("a", 513): false,
		"tab\tinside": false, "sk-" + strings.Repeat("0", 32): true, "": false, "new\nline00": false} {
		if ValidKey(key) != valid {
			t.Errorf("ValidKey(%q) = %v", key, !valid)
		}
	}
	if !slices.Equal(KeyKinds(), []string{"deepseek", "jev"}) {
		t.Fatal(KeyKinds())
	}
	only := NewProviderKeys(Config{Planner: &ModelConfig{Kind: "openai", Model: "gpt-4.1-mini", KeyEnv: "X"}, Judge: &ModelConfig{Kind: "local", Model: "m"}})
	if len(only.Kinds()) != 0 || len(only.Status()) != 0 || only.SetAccount(map[string]string{"deepseek": "account-deepseek-0001"}) == nil {
		t.Fatal("a configuration without DeepSeek or JEV has key kinds", only.Kinds())
	}
}

func mapsEqual[V comparable](a, b map[string]V) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// keyedEndpoint records the credential of every request; the first one waits
// until release is closed and is answered 500.
type keyedEndpoint struct {
	*httptest.Server
	mu      sync.Mutex
	seen    []string
	arrived chan struct{}
	release chan struct{}
	first   atomic.Bool
}

func newKeyedEndpoint(t *testing.T) *keyedEndpoint {
	t.Helper()
	e := &keyedEndpoint{arrived: make(chan struct{}, 1), release: make(chan struct{})}
	e.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		e.mu.Lock()
		e.seen = append(e.seen, r.Header.Get("Authorization"))
		e.mu.Unlock()
		if e.first.CompareAndSwap(false, true) {
			e.arrived <- struct{}{}
			select {
			case <-e.release:
			case <-r.Context().Done():
			}
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`))
	}))
	t.Cleanup(e.Close)
	return e
}

func (e *keyedEndpoint) credentials() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.seen)
}

// A model call keeps the key it started with through all its attempts: a
// retry after the swap still sends the old one, and the next call the new.
func TestProviderKeysInFlightCallKeepsItsKey(t *testing.T) {
	const host, account = "host-deepseek-0001", "account-deepseek-0001"
	t.Setenv("UNIT_INFLIGHT_DS", host)
	endpoint := newKeyedEndpoint(t)
	budget, err := OpenBudget(filepath.Join(t.TempDir(), "budget.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { budget.Close() })
	config := Config{BudgetFile: "", Planner: &ModelConfig{Kind: "deepseek", Model: "deepseek-flash", KeyEnv: "UNIT_INFLIGHT_DS", TestEndpoint: endpoint.URL + "/chat/completions"}}
	keys := NewProviderKeys(config)
	provider, err := newProvider(*config.Planner, budget, keys)
	if err != nil {
		t.Fatal(err)
	}
	provider.random = func() float64 { return 0 } // retries at once
	t.Cleanup(func() { provider.Close() })

	called := make(chan error, 1)
	go func() {
		_, _, err := provider.Complete(context.Background(), "s", "p")
		called <- err
	}()
	<-endpoint.arrived
	if err := keys.SetAccount(map[string]string{"deepseek": account}); err != nil {
		t.Fatal(err)
	}
	close(endpoint.release)
	if err := <-called; err != nil {
		t.Fatal("the call in flight failed:", err)
	}
	if _, _, err := provider.Complete(context.Background(), "s", "q"); err != nil {
		t.Fatal(err)
	}
	if err := keys.SetAccount(nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := provider.Complete(context.Background(), "s", "r"); err != nil {
		t.Fatal(err)
	}
	want := []string{"Bearer " + host, "Bearer " + host, "Bearer " + account, "Bearer " + host}
	if got := endpoint.credentials(); !slices.Equal(got, want) {
		t.Fatalf("the endpoint saw %q, want %q", got, want)
	}
}

// With an account key, a model whose host variable is empty can be built;
// without either it cannot, as before.
func TestOpenModelsWithAnAccountKeyAndNoHostKey(t *testing.T) {
	t.Setenv("UNIT_NOHOST_DS", "")
	t.Setenv("UNIT_NOHOST_JEV", "host-jev-0001")
	config := keysConfig("UNIT_NOHOST_DS", "UNIT_NOHOST_JEV")
	config.BudgetFile = filepath.Join(t.TempDir(), "budget.db")
	keys := NewProviderKeys(config)
	if _, budget, _, err := OpenModelsWith(config, keys); err == nil || !strings.Contains(err.Error(), "credential environment variable") {
		if budget != nil {
			budget.Close()
		}
		t.Fatal("a model without any key was built:", err)
	}
	if err := keys.SetAccount(map[string]string{"deepseek": "account-deepseek-0001"}); err != nil {
		t.Fatal(err)
	}
	models, budget, cache, err := OpenModelsWith(config, keys)
	if err != nil {
		t.Fatal(err)
	}
	defer closeModelFiles(budget, cache)
	defer models.Close()
	for _, p := range models.providers() {
		want := map[string]string{"deepseek": "account-deepseek-0001", "jev": "host-jev-0001"}[p.config.Kind]
		if got := p.credential(); got != want {
			t.Errorf("%s sends %q, want %q", p.config.Kind, got, want)
		}
	}
	// OpenModels is OpenModelsWith the host's keys alone.
	t.Setenv("UNIT_NOHOST_DS", "host-deepseek-0001")
	models, budget2, cache2, err := OpenModels(config)
	if err != nil {
		t.Fatal(err)
	}
	defer closeModelFiles(budget2, cache2)
	defer models.Close()
	if got := models.providers()[0].credential(); got != "host-deepseek-0001" {
		t.Fatal(got)
	}
}

// checkEndpoint answers a key check as the test sets it: the status of
// GET .../models and of the fallback's POST, a hang, or a closed connection.
type checkEndpoint struct {
	*httptest.Server
	mu         sync.Mutex
	models     int      // the status of GET /models and /v1/models; 0 hangs, -1 closes
	completion int      // the status of POST /chat/completions and /v1/systemone
	requests   []string // method, path, protocol and credential of each request
	bodies     []string
}

func newCheckEndpoint(t *testing.T) *checkEndpoint {
	t.Helper()
	e := &checkEndpoint{models: http.StatusOK, completion: http.StatusOK}
	e.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		e.mu.Lock()
		e.requests = append(e.requests, r.Method+" "+r.URL.Path+" "+r.Proto+" "+r.Header.Get("Authorization"))
		e.bodies = append(e.bodies, string(raw))
		models, completion := e.models, e.completion
		e.mu.Unlock()
		status := completion
		if r.Method == http.MethodGet {
			status = models
		}
		switch status {
		case 0:
			<-r.Context().Done()
		case -1:
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
		default:
			w.WriteHeader(status)
			w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(e.Close)
	return e
}

func (e *checkEndpoint) set(models, completion int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.models, e.completion, e.requests, e.bodies = models, completion, nil, nil
}

func (e *checkEndpoint) seen() ([]string, []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.requests), slices.Clone(e.bodies)
}

// recordingObserver counts the attempts it is shown.
type recordingObserver struct{ n atomic.Int64 }

func (o *recordingObserver) ModelAttempt(AttemptEvent) { o.n.Add(1) }

// A key is checked with one GET of the provider's list of models, through
// the provider's own transport (HTTP/1.1), outside the resilience layer and
// the budget; a provider without that list is asked one minimal call.
func TestCheckKey(t *testing.T) {
	if KeyCheckTimeout != 10*time.Second {
		t.Fatal("a key check waits", KeyCheckTimeout)
	}
	t.Setenv("UNIT_CHECK_DS", "host-deepseek-0001")
	t.Setenv("UNIT_CHECK_JEV", "host-jev-0001")
	endpoint := newCheckEndpoint(t)
	config := keysConfig("UNIT_CHECK_DS", "UNIT_CHECK_JEV")
	config.Planner.TestEndpoint = endpoint.URL + "/chat/completions"
	config.Consolidator = nil
	config.Judge.TestEndpoint = endpoint.URL + "/v1/systemone"
	config.BudgetFile = filepath.Join(t.TempDir(), "budget.db")
	models, budget, cache, err := OpenModelsWith(config, NewProviderKeys(config))
	if err != nil {
		t.Fatal(err)
	}
	defer closeModelFiles(budget, cache)
	defer models.Close()
	observer := &recordingObserver{}
	models.Observe(observer)
	const candidate = "candidate-key-0001"

	cases := []struct {
		name               string
		kind               string
		models, completion int
		deadline           time.Duration
		via                string
		status             int
		err                error
		requests           []string
	}{
		{"models answers", "deepseek", 200, 200, 0, "models", 200, nil, []string{"GET /models HTTP/1.1 Bearer " + candidate}},
		{"JEV's models", "jev", 204, 200, 0, "models", 204, nil, []string{"GET /v1/models HTTP/1.1 Bearer " + candidate}},
		{"wrong key", "deepseek", 401, 200, 0, "models", 401, ErrKeyRejected, nil},
		{"empty balance", "deepseek", 402, 200, 0, "models", 402, ErrKeyRejected, nil},
		{"forbidden", "jev", 403, 200, 0, "models", 403, ErrKeyRejected, nil},
		{"no models route", "deepseek", 404, 200, 0, "completion", 200, nil, []string{"GET /models HTTP/1.1 Bearer " + candidate, "POST /chat/completions HTTP/1.1 Bearer " + candidate}},
		{"no models method", "jev", 405, 200, 0, "completion", 200, nil, []string{"GET /v1/models HTTP/1.1 Bearer " + candidate, "POST /v1/systemone HTTP/1.1 Bearer " + candidate}},
		{"completion rejects", "deepseek", 404, 402, 0, "completion", 402, ErrKeyRejected, nil},
		{"completion fails", "jev", 404, 500, 0, "completion", 500, ErrKeyCheckUnavailable, nil},
		{"server error", "deepseek", 500, 200, 0, "models", 500, ErrKeyCheckUnavailable, nil},
		{"throttled", "deepseek", 429, 200, 0, "models", 429, ErrKeyCheckUnavailable, nil},
		{"redirect", "jev", 302, 200, 0, "models", 302, ErrKeyCheckUnavailable, nil},
		{"hang", "deepseek", 0, 200, 300 * time.Millisecond, "models", 0, ErrKeyCheckUnavailable, nil},
		{"closed connection", "jev", -1, 200, 0, "models", 0, ErrKeyCheckUnavailable, nil},
	}
	for _, tc := range cases {
		endpoint.set(tc.models, tc.completion)
		ctx := context.Background()
		if tc.deadline > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, tc.deadline)
			defer cancel()
		}
		start := time.Now()
		check, err := models.CheckKey(ctx, tc.kind, candidate)
		if check.Via != tc.via || check.Status != tc.status || !errors.Is(err, tc.err) || tc.err == nil && err != nil {
			t.Errorf("%s: %+v %v, want %s %d %v", tc.name, check, err, tc.via, tc.status, tc.err)
		}
		if err != nil && strings.Contains(err.Error(), candidate) {
			t.Errorf("%s: the error %q holds the key", tc.name, err)
		}
		if took := time.Since(start); tc.deadline > 0 && took > tc.deadline+2*time.Second {
			t.Errorf("%s: the check took %v past its deadline", tc.name, took)
		}
		requests, bodies := endpoint.seen()
		if tc.requests != nil && !slices.Equal(requests, tc.requests) {
			t.Errorf("%s: the endpoint saw %q, want %q", tc.name, requests, tc.requests)
		}
		if tc.via == "completion" {
			var body map[string]any
			if err := json.Unmarshal([]byte(bodies[1]), &body); err != nil {
				t.Fatal(err)
			}
			switch tc.kind {
			case "deepseek":
				if body["model"] != "deepseek-flash" || body["max_tokens"] != 1.0 {
					t.Errorf("%s: the completion sent %s", tc.name, bodies[1])
				}
			case "jev":
				if body["model"] != "jev-1.13.0" || len(body["questions"].(map[string]any)) != 1 {
					t.Errorf("%s: the decision sent %s", tc.name, bodies[1])
				}
			}
		}
	}
	// A kind no model has cannot be checked.
	if _, err := models.CheckKey(context.Background(), "openai", candidate); !errors.Is(err, ErrKeyCheckUnavailable) {
		t.Fatal(err)
	}
	// Outside the resilience layer and every budget: no attempt was shown,
	// counted as a rejection or reserved, and the breakers are closed.
	if n := observer.n.Load(); n != 0 {
		t.Errorf("the observer was shown %d attempts", n)
	}
	for kind, n := range models.Rejections() {
		if n != 0 {
			t.Errorf("%d rejections of %s counted", n, kind)
		}
	}
	for _, b := range models.Breakers() {
		if b.State != BreakerClosed {
			t.Errorf("the %s breaker is %s", b.Kind, b.State)
		}
	}
	if attempts := budgetAttempts(t, budget); len(attempts) != 0 {
		t.Errorf("the checks reserved %d attempts", len(attempts))
	}
}
