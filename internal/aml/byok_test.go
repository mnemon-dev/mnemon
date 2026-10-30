package aml

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
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

// The host keys of the BYOK tests' instance, and accounts' keys.
const (
	hostDeepSeek    = "host-deepseek-key-0001"
	hostJEV         = "host-jev-key-0001"
	accountDeepSeek = "account-deepseek-key-0001"
	accountJEV      = "account-jev-key-0001"
)

// fakeAt is a running fake provider (scripts/aml/fakeprovider): its base URL.
type fakeAt string

// control sends a request to the fake's loopback control endpoint and
// decodes its answer into out (nil to drop it).
func (f fakeAt) control(t *testing.T, method, path, body string, out any) {
	t.Helper()
	request, err := http.NewRequest(method, string(f)+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("%s %s %s: %d %s", method, path, body, response.StatusCode, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatal(err, string(raw))
		}
	}
}

func (f fakeAt) fault(t *testing.T, profile string) { f.control(t, "POST", "/_fault", profile, nil) }
func (f fakeAt) clear(t *testing.T)                 { f.control(t, "DELETE", "/_fault", "", nil) }

// keys are the requests the fake received, by kind, route ("models" or
// "calls") and the fingerprint of their credential.
func (f fakeAt) keys(t *testing.T) map[string]map[string]map[string]int {
	t.Helper()
	var seen map[string]map[string]map[string]int
	f.control(t, "GET", "/_keys", "", &seen)
	return seen
}

// byok is a server whose planner (DeepSeek) and judge (JEV) are the fake
// provider's, with the host's keys in their variables, an admin token, a
// budget file and the instance's state directory, where its key file goes.
type byok struct {
	*harness
	fake     fakeAt
	state    string // the instance's state directory
	keysFile string // <state>/provider-keys.json
	config   ServerConfig
}

// byokConfig is the configuration of a byok server over fake: the models,
// the budget file, the key file and the admin token.
func byokConfig(t *testing.T, fake fakeAt, state string) func(*ServerConfig) {
	t.Helper()
	t.Setenv("BYOK_TEST_DEEPSEEK", hostDeepSeek)
	t.Setenv("BYOK_TEST_JEV", hostJEV)
	return func(config *ServerConfig) {
		config.Models = nil
		config.MemoryConfig.Planner = &memoryagent.ModelConfig{Kind: "deepseek", Model: "deepseek-flash", KeyEnv: "BYOK_TEST_DEEPSEEK", TestEndpoint: string(fake) + "/chat/completions"}
		config.MemoryConfig.Judge = &memoryagent.ModelConfig{Kind: "jev", Model: "jev-1.13.0", KeyEnv: "BYOK_TEST_JEV", TestEndpoint: string(fake) + "/v1/systemone"}
		config.MemoryConfig.BudgetFile = filepath.Join(state, "model-budget.db")
		config.MemoryConfig.Budgets = map[string]float64{"deepseek": 600, "jev": 60}
		config.ProviderKeysFile = filepath.Join(state, "provider-keys.json")
		config.AdminToken = testAdminToken
	}
}

func newBYOK(t *testing.T, edit func(*ServerConfig)) *byok {
	t.Helper()
	b := &byok{fake: fakeAt(startFakeProvider(t)), state: t.TempDir()}
	b.keysFile = filepath.Join(b.state, "provider-keys.json")
	setup := byokConfig(t, b.fake, b.state)
	b.harness = newHarness(t, func(config *ServerConfig) {
		setup(config)
		if edit != nil {
			edit(config)
		}
		b.config = *config
	})
	return b
}

// admin sends method path with body ("" for none) to the admin handler,
// with the admin token.
func (b *byok) admin(method, path, body string) *httptest.ResponseRecorder {
	return adminRequest(b.server, method, path, body, true)
}

// adminStatus is /admin/status, read with the admin token.
func (b *byok) adminStatus(t *testing.T) AdminStatus {
	t.Helper()
	var status AdminStatus
	if response := b.admin("GET", "/admin/status", ""); response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &status) != nil {
		t.Fatal(response.Code, response.Body.String())
	}
	return status
}

func adminRequest(s *Server, method, path, body string, token bool) *httptest.ResponseRecorder {
	var reader io.Reader = http.NoBody
	if body != "" {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, path, reader)
	if token {
		request.Header.Set("Authorization", "Bearer "+testAdminToken)
	}
	recorder := httptest.NewRecorder()
	s.AdminHandler().ServeHTTP(recorder, request)
	return recorder
}

// answered checks an admin answer's status and its body, to the byte but
// the final newline.
func answered(t *testing.T, response *httptest.ResponseRecorder, status int, body string) {
	t.Helper()
	if got := strings.TrimSuffix(response.Body.String(), "\n"); response.Code != status || got != body {
		t.Fatalf("answered %d %s\nwant %d %s", response.Code, got, status, body)
	}
	if response.Header().Get("Content-Type") != "application/json" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("headers %v", response.Header())
	}
}

// keyStatus is the providerKeys map as the admin routes spell it.
func keyStatus(deepseekSource, deepseekKey, jevSource, jevKey string) string {
	entry := func(source, key string) string {
		fingerprint := ""
		if key != "" {
			fingerprint = memoryagent.Fingerprint(key)
		}
		return `{"source":"` + source + `","fingerprint":"` + fingerprint + `"}`
	}
	return `{"deepseek":` + entry(deepseekSource, deepseekKey) + `,"jev":` + entry(jevSource, jevKey) + `}`
}

// keyFileOf reads the key file as JSON: its keys, and whether it exists.
func keyFileOf(t *testing.T, path string) (map[string]string, bool) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false
	}
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Version   int               `json:"version"`
		Keys      map[string]string `json:"keys"`
		UpdatedAt string            `json:"updatedAt"`
	}
	if err := json.Unmarshal(raw, &file); err != nil || file.Version != 1 {
		t.Fatalf("the key file is %s: %v", raw, err)
	}
	if _, err := time.Parse(time.RFC3339, file.UpdatedAt); err != nil {
		t.Fatalf("updatedAt %q: %v", file.UpdatedAt, err)
	}
	return file.Keys, true
}

// fileBytes is the file's content, or "absent".
func fileBytes(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "absent"
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// calls makes one call of each model, which the fake answers.
func (b *byok) calls(t *testing.T) {
	t.Helper()
	if _, _, err := b.server.models.Planner.Complete(context.Background(), "s", "a call"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.server.models.Judge.Decide(context.Background(), map[string]any{}, map[string]memoryagent.Question{"q": {Type: "noul", Instructions: "a"}}); err != nil {
		t.Fatal(err)
	}
}

// sent are the calls of each kind the fake received with key since the
// counts were before.
func sent(after, before map[string]map[string]map[string]int, kind, route, key string) int {
	fingerprint := memoryagent.Fingerprint(key)
	return after[kind][route][fingerprint] - before[kind][route][fingerprint]
}

// TestProviderKeysReplace: the check, then the write and the swap. A new
// call uses the new key, the fake sees it, a call in flight keeps the old
// one, and a restart starts with it.
func TestProviderKeysReplace(t *testing.T) {
	b := newBYOK(t, nil)
	if got := b.adminStatus(t).ProviderKeys; !sameMap(got, map[string]memoryagent.KeyStatus{
		"deepseek": {Source: "host", Fingerprint: memoryagent.Fingerprint(hostDeepSeek)}, "jev": {Source: "host", Fingerprint: memoryagent.Fingerprint(hostJEV)}}) {
		t.Fatal(got)
	}

	// A DeepSeek call in flight: the fake keeps it for a second and a half.
	b.fake.fault(t, `{"kind":"deepseek","latency":"1500ms"}`)
	before := b.fake.keys(t)
	inFlight := make(chan error, 1)
	go func() {
		_, _, err := b.server.models.Planner.Complete(context.Background(), "s", "in flight")
		inFlight <- err
	}()
	eventually(t, func() bool { return sent(b.fake.keys(t), before, "deepseek", "calls", hostDeepSeek) == 1 })

	answered(t, b.admin("PUT", "/admin/v1/provider-keys", `{"deepseek":"`+accountDeepSeek+`"}`), http.StatusOK,
		`{"providerKeys":`+keyStatus("account", accountDeepSeek, "host", hostJEV)+`,"checked":{"deepseek":"models"}}`)
	// The check: one GET of DeepSeek's list, with the new key.
	checked := b.fake.keys(t)
	if sent(checked, before, "deepseek", "models", accountDeepSeek) != 1 || sent(checked, before, "deepseek", "calls", accountDeepSeek) != 0 {
		t.Fatalf("the fake saw %v", checked)
	}
	// The write: the file, 0600, the account's DeepSeek key alone.
	if keys, ok := keyFileOf(t, b.keysFile); !ok || !sameMap(keys, map[string]string{"deepseek": accountDeepSeek}) {
		t.Fatal(keys, ok)
	}
	if info, err := os.Stat(b.keysFile); err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatal(info.Mode(), err)
	}
	// The call in flight finishes with the key it started with.
	if err := <-inFlight; err != nil {
		t.Fatal(err)
	}
	b.fake.clear(t)
	// The next calls: DeepSeek's with the account's key, JEV's with the host's.
	before = b.fake.keys(t)
	b.calls(t)
	after := b.fake.keys(t)
	if sent(after, before, "deepseek", "calls", accountDeepSeek) != 1 || sent(after, before, "deepseek", "calls", hostDeepSeek) != 0 ||
		sent(after, before, "jev", "calls", hostJEV) != 1 {
		t.Fatalf("after the swap the fake saw %v (before %v)", after, before)
	}
	if got := b.adminStatus(t).ProviderKeys["deepseek"]; got != (memoryagent.KeyStatus{Source: "account", Fingerprint: memoryagent.Fingerprint(accountDeepSeek)}) {
		t.Fatal(got)
	}
	// One INFO line, naming the kind given and its fingerprint.
	if lines := linesWith(b.log.String(), "provider keys"); len(lines) != 1 || !strings.Contains(lines[0], "level=INFO") ||
		!strings.Contains(lines[0], `msg="provider keys replaced: deepseek `+memoryagent.Fingerprint(accountDeepSeek)+`"`) {
		t.Fatalf("the log has %q", lines)
	}

	// Both kinds at once, and the other kind's account key is kept.
	answered(t, b.admin("PUT", "/admin/v1/provider-keys", `{"jev":"`+accountJEV+`"}`), http.StatusOK,
		`{"providerKeys":`+keyStatus("account", accountDeepSeek, "account", accountJEV)+`,"checked":{"jev":"models"}}`)
	if keys, _ := keyFileOf(t, b.keysFile); !sameMap(keys, map[string]string{"deepseek": accountDeepSeek, "jev": accountJEV}) {
		t.Fatal(keys)
	}
	const second = "account-deepseek-key-0002"
	answered(t, b.admin("PUT", "/admin/v1/provider-keys", `{"deepseek":"`+second+`","jev":"`+accountJEV+`"}`), http.StatusOK,
		`{"providerKeys":`+keyStatus("account", second, "account", accountJEV)+`,"checked":{"deepseek":"models","jev":"models"}}`)
	if lines := linesWith(b.log.String(), "provider keys replaced"); len(lines) != 3 ||
		!strings.Contains(lines[2], "provider keys replaced: deepseek "+memoryagent.Fingerprint(second)+", jev "+memoryagent.Fingerprint(accountJEV)) {
		t.Fatalf("the log has %q", lines)
	}

	// A restart reads the file: the account's keys from the start.
	b.server.Close()
	restarted, err := NewServer(b.config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if got := restarted.status(context.Background()).ProviderKeys; !sameMap(got, map[string]memoryagent.KeyStatus{
		"deepseek": {Source: "account", Fingerprint: memoryagent.Fingerprint(second)}, "jev": {Source: "account", Fingerprint: memoryagent.Fingerprint(accountJEV)}}) {
		t.Fatal(got)
	}
}

// linesWith are the lines of log that hold text.
func linesWith(log, text string) []string {
	lines := []string{}
	for _, line := range strings.Split(log, "\n") {
		if strings.Contains(line, text) {
			lines = append(lines, line)
		}
	}
	return lines
}

// unchanged checks that a refused replacement changed nothing: the file's
// bytes, the status and the key the next DeepSeek call sends.
func (b *byok) unchanged(t *testing.T, file, status, key string) {
	t.Helper()
	if got := fileBytes(t, b.keysFile); got != file {
		t.Fatalf("the key file is %s, was %s", got, file)
	}
	if got := strings.TrimSuffix(b.admin("GET", "/admin/status", "").Body.String(), "\n"); !strings.Contains(got, `"providerKeys":`+status) {
		t.Fatalf("the status is %s, want providerKeys %s", got, status)
	}
	before := b.fake.keys(t)
	b.fake.clear(t)
	if _, _, err := b.server.models.Planner.Complete(context.Background(), "s", "after a refusal"); err != nil {
		t.Fatal(err)
	}
	if sent(b.fake.keys(t), before, "deepseek", "calls", key) != 1 {
		t.Fatalf("the next call did not send the key it sent before")
	}
}

// TestProviderKeysRejected: a key the provider refuses (401, 402, 403)
// changes nothing, neither the file nor the keys in memory.
func TestProviderKeysRejected(t *testing.T) {
	b := newBYOK(t, nil)
	// Once with no file, once over an account key.
	for _, existing := range []string{"", accountDeepSeek} {
		if existing != "" {
			answered(t, b.admin("PUT", "/admin/v1/provider-keys", `{"deepseek":"`+existing+`"}`), http.StatusOK,
				`{"providerKeys":`+keyStatus("account", existing, "host", hostJEV)+`,"checked":{"deepseek":"models"}}`)
		}
		file := fileBytes(t, b.keysFile)
		status, key := keyStatus("host", hostDeepSeek, "host", hostJEV), hostDeepSeek
		if existing != "" {
			status, key = keyStatus("account", existing, "host", hostJEV), existing
		}
		for _, code := range []int{401, 402, 403} {
			b.fake.fault(t, fmt.Sprintf(`{"kind":"deepseek","modelsStatus":%d}`, code))
			answered(t, b.admin("PUT", "/admin/v1/provider-keys", `{"deepseek":"rejected-key-0001"}`), http.StatusBadRequest,
				fmt.Sprintf(`{"error":"key_rejected","kind":"deepseek","status":%d}`, code))
			b.unchanged(t, file, status, key)
		}
	}
	// A refusal is logged as a warning that names the kind and the status,
	// never the key.
	if lines := linesWith(b.log.String(), "provider keys not replaced"); len(lines) != 6 || !strings.Contains(lines[0], "level=WARN") ||
		!strings.Contains(lines[0], "deepseek") || !strings.Contains(lines[0], "401") {
		t.Fatalf("the log has %q", lines)
	}
	if strings.Contains(b.log.String(), "rejected-key-0001") {
		t.Fatal("the log holds the rejected key")
	}
}

// TestProviderKeysCheckUnavailable: a check the provider does not answer
// about the key (500, 429, a hang past the check's 10 s, a closed
// connection) changes nothing.
func TestProviderKeysCheckUnavailable(t *testing.T) {
	b := newBYOK(t, nil)
	if b.server.keyCheckTimeout != 10*time.Second || memoryagent.KeyCheckTimeout != 10*time.Second {
		t.Fatal("a key check waits", b.server.keyCheckTimeout, memoryagent.KeyCheckTimeout)
	}
	// The hang is past the check's bound, shortened here.
	b.server.keyCheckTimeout = 400 * time.Millisecond
	file, status := fileBytes(t, b.keysFile), keyStatus("host", hostDeepSeek, "host", hostJEV)
	for _, fault := range []string{`"modelsStatus":500`, `"modelsStatus":429`, `"modelsFault":"hang","stallFor":"5s"`, `"modelsFault":"close"`} {
		b.fake.fault(t, `{"kind":"jev",`+fault+`}`)
		start := time.Now()
		answered(t, b.admin("PUT", "/admin/v1/provider-keys", `{"jev":"unchecked-key-0001"}`), http.StatusBadGateway, `{"error":"key_check_unavailable","kind":"jev"}`)
		if took := time.Since(start); took > 3*time.Second {
			t.Fatalf("%s: the check took %v", fault, took)
		}
		b.unchanged(t, file, status, hostDeepSeek)
		before := b.fake.keys(t)
		if _, _, err := b.server.models.Judge.Decide(context.Background(), map[string]any{}, map[string]memoryagent.Question{"q": {Type: "noul", Instructions: "a"}}); err != nil {
			t.Fatal(err)
		}
		if sent(b.fake.keys(t), before, "jev", "calls", hostJEV) != 1 {
			t.Fatalf("%s: JEV's next call did not send the host's key", fault)
		}
	}
}

// TestProviderKeysAllOrNothing: two kinds, one fails, and neither is
// applied; a kind after a failed one is not checked at all.
func TestProviderKeysAllOrNothing(t *testing.T) {
	b := newBYOK(t, nil)
	file, status := fileBytes(t, b.keysFile), keyStatus("host", hostDeepSeek, "host", hostJEV)
	b.fake.fault(t, `{"kind":"jev","modelsStatus":401}`)
	answered(t, b.admin("PUT", "/admin/v1/provider-keys", `{"deepseek":"`+accountDeepSeek+`","jev":"`+accountJEV+`"}`), http.StatusBadRequest,
		`{"error":"key_rejected","kind":"jev","status":401}`)
	b.unchanged(t, file, status, hostDeepSeek)

	b.fake.fault(t, `{"kind":"deepseek","modelsStatus":503}`)
	before := b.fake.keys(t)
	answered(t, b.admin("PUT", "/admin/v1/provider-keys", `{"deepseek":"`+accountDeepSeek+`","jev":"`+accountJEV+`"}`), http.StatusBadGateway,
		`{"error":"key_check_unavailable","kind":"deepseek"}`)
	if sent(b.fake.keys(t), before, "jev", "models", accountJEV) != 0 {
		t.Fatal("JEV's key was checked after DeepSeek's check failed")
	}
	b.unchanged(t, file, status, hostDeepSeek)
}

// TestProviderKeysCompletionFallback: /models answers 404 (or 405), so one
// minimal call checks the key, outside the budget, and checked says so.
func TestProviderKeysCompletionFallback(t *testing.T) {
	b := newBYOK(t, nil)
	b.fake.fault(t, `{"kind":"jev","modelsStatus":404}`)
	before := b.fake.keys(t)
	answered(t, b.admin("PUT", "/admin/v1/provider-keys", `{"jev":"`+accountJEV+`"}`), http.StatusOK,
		`{"providerKeys":`+keyStatus("host", hostDeepSeek, "account", accountJEV)+`,"checked":{"jev":"completion"}}`)
	after := b.fake.keys(t)
	if sent(after, before, "jev", "models", accountJEV) != 1 || sent(after, before, "jev", "calls", accountJEV) != 1 {
		t.Fatalf("the fake saw %v (before %v)", after, before)
	}
	b.fake.fault(t, `{"kind":"deepseek","modelsStatus":405}`)
	answered(t, b.admin("PUT", "/admin/v1/provider-keys", `{"deepseek":"`+accountDeepSeek+`","jev":"`+accountJEV+`"}`), http.StatusOK,
		`{"providerKeys":`+keyStatus("account", accountDeepSeek, "account", accountJEV)+`,"checked":{"deepseek":"completion","jev":"completion"}}`)
	// The checks spent nothing and reserved nothing: no account was used.
	if accounts, err := b.server.Accounts(context.Background()); err != nil || len(accounts) != 0 {
		t.Fatal("the checks used the budget:", accounts, err)
	}
}

// TestProviderKeysRevert: one kind, then all; the file is then gone and the
// keys are the host's.
func TestProviderKeysRevert(t *testing.T) {
	b := newBYOK(t, nil)
	answered(t, b.admin("PUT", "/admin/v1/provider-keys", `{"deepseek":"`+accountDeepSeek+`","jev":"`+accountJEV+`"}`), http.StatusOK,
		`{"providerKeys":`+keyStatus("account", accountDeepSeek, "account", accountJEV)+`,"checked":{"deepseek":"models","jev":"models"}}`)
	answered(t, b.admin("DELETE", "/admin/v1/provider-keys?kind=deepseek", ""), http.StatusOK, `{"providerKeys":`+keyStatus("host", hostDeepSeek, "account", accountJEV)+`}`)
	if keys, ok := keyFileOf(t, b.keysFile); !ok || !sameMap(keys, map[string]string{"jev": accountJEV}) {
		t.Fatal(keys, ok)
	}
	before := b.fake.keys(t)
	b.calls(t)
	if after := b.fake.keys(t); sent(after, before, "deepseek", "calls", hostDeepSeek) != 1 || sent(after, before, "jev", "calls", accountJEV) != 1 {
		t.Fatal("after reverting DeepSeek the fake saw", after)
	}
	answered(t, b.admin("DELETE", "/admin/v1/provider-keys?kind=all", ""), http.StatusOK, `{"providerKeys":`+keyStatus("host", hostDeepSeek, "host", hostJEV)+`}`)
	if _, ok := keyFileOf(t, b.keysFile); ok {
		t.Fatal("the key file is left with no key in it")
	}
	before = b.fake.keys(t)
	b.calls(t)
	if after := b.fake.keys(t); sent(after, before, "deepseek", "calls", hostDeepSeek) != 1 || sent(after, before, "jev", "calls", hostJEV) != 1 {
		t.Fatal("after reverting all the fake saw", after)
	}
	// Reverting what is the host's already changes nothing, and answers so.
	answered(t, b.admin("DELETE", "/admin/v1/provider-keys?kind=jev", ""), http.StatusOK, `{"providerKeys":`+keyStatus("host", hostDeepSeek, "host", hostJEV)+`}`)
	if lines := linesWith(b.log.String(), "reverted to the host's"); len(lines) != 3 ||
		!strings.Contains(lines[0], "provider keys reverted to the host's: deepseek") || !strings.Contains(lines[1], "provider keys reverted to the host's: deepseek, jev") {
		t.Fatalf("the log has %q", lines)
	}
	// A missing, unknown or repeated kind, or a malformed query, is refused.
	for _, query := range []string{"", "?kind=", "?kind=openai", "?kind=deepseek&kind=jev", "?kind=%zz", "?kind=jev;x=1", "?kind=ALL"} {
		answered(t, b.admin("DELETE", "/admin/v1/provider-keys"+query, ""), http.StatusBadRequest, `{"error":"invalid","field":"kind"}`)
	}
}

// TestProviderKeysRequestRules: what the routes refuse before any check,
// and the routes of a server that has no key file or no model of a kind.
func TestProviderKeysRequestRules(t *testing.T) {
	b := newBYOK(t, nil)
	for _, tc := range []struct{ body, answer string }{
		{`[]`, `{"error":"invalid","field":"body"}`},
		{`{}`, `{"error":"invalid","field":"body"}`},
		{`{"deepseek":"` + accountDeepSeek + `"} {}`, `{"error":"invalid","field":"body"}`},
		{`not json`, `{"error":"invalid","field":"body"}`},
		{`{"deepseek":"` + accountDeepSeek + `","openai":"x"}`, `{"error":"invalid","field":"openai"}`},
		// A field whose name is not a name may be a key in the wrong place.
		{`{"sk-` + strings.Repeat("0", 32) + `":"x"}`, `{"error":"invalid","field":"body"}`},
		{`{"deepseek":12345678}`, `{"error":"invalid","field":"deepseek"}`},
		{`{"deepseek":null}`, `{"error":"invalid","field":"deepseek"}`},
		{`{"jev":"short"}`, `{"error":"invalid","field":"jev"}`},
		{`{"jev":"with a space"}`, `{"error":"invalid","field":"jev"}`},
		{`{"jev":"` + strings.Repeat("k", 513) + `"}`, `{"error":"invalid","field":"jev"}`},
	} {
		answered(t, b.admin("PUT", "/admin/v1/provider-keys", tc.body), http.StatusBadRequest, tc.answer)
	}
	answered(t, b.admin("PUT", "/admin/v1/provider-keys", `{"jev":"`+strings.Repeat("k", 4096)+`"}`), http.StatusRequestEntityTooLarge, `{"error":"too_large"}`)
	// Nothing reached the fake, and nothing changed.
	if seen := b.fake.keys(t); len(seen["deepseek"]["models"])+len(seen["jev"]["models"]) != 0 {
		t.Fatal("a refused request was checked:", seen)
	}
	if _, ok := keyFileOf(t, b.keysFile); ok {
		t.Fatal("a refused request wrote the key file")
	}
	// The admin token is asked first, and the write routes answer no GET.
	for _, method := range []string{"PUT", "DELETE"} {
		if response := adminRequest(b.server, method, "/admin/v1/provider-keys?kind=all", `{"deepseek":"`+accountDeepSeek+`"}`, false); response.Code != http.StatusUnauthorized ||
			strings.TrimSpace(response.Body.String()) != `{"error":"missing or invalid admin token"}` {
			t.Fatalf("%s without the token: %d %s", method, response.Code, response.Body.String())
		}
	}
	if response := b.admin("GET", "/admin/v1/provider-keys", ""); response.Code != http.StatusNotFound {
		t.Fatalf("GET: %d %s", response.Code, response.Body.String())
	}

	// A kind the configuration does not use: here JEV, the judge being local.
	local := newBYOK(t, func(config *ServerConfig) {
		config.MemoryConfig.Judge = &memoryagent.ModelConfig{Kind: "local", Model: "local-judge", BaseURL: string(b.fake) + "/v1"}
	})
	answered(t, local.admin("PUT", "/admin/v1/provider-keys", `{"jev":"`+accountJEV+`"}`), http.StatusConflict, `{"error":"unknown_kind","kind":"jev"}`)
	answered(t, local.admin("DELETE", "/admin/v1/provider-keys?kind=jev", ""), http.StatusConflict, `{"error":"unknown_kind","kind":"jev"}`)
	if got := local.adminStatus(t).ProviderKeys; !sameMap(got, map[string]memoryagent.KeyStatus{"deepseek": {Source: "host", Fingerprint: memoryagent.Fingerprint(hostDeepSeek)}}) {
		t.Fatal(got)
	}
	answered(t, local.admin("DELETE", "/admin/v1/provider-keys?kind=all", ""), http.StatusOK, `{"providerKeys":{"deepseek":{"source":"host","fingerprint":"`+memoryagent.Fingerprint(hostDeepSeek)+`"}}}`)

	// Without a key file the keys cannot change at run time.
	none := newBYOK(t, func(config *ServerConfig) { config.ProviderKeysFile = "" })
	answered(t, none.admin("PUT", "/admin/v1/provider-keys", `{"deepseek":"`+accountDeepSeek+`"}`), http.StatusConflict, `{"error":"no_key_file"}`)
	answered(t, none.admin("DELETE", "/admin/v1/provider-keys?kind=all", ""), http.StatusConflict, `{"error":"no_key_file"}`)
	if got := none.adminStatus(t).ProviderKeys["deepseek"]; got.Source != "host" {
		t.Fatal(got)
	}
}

// writeTestFile writes content to path with mode (POSIX), as an operator or
// a test would, not atomically.
func writeTestFile(t *testing.T, path, content string, mode fs.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// keyFileText is a key file holding keys, by kind.
func keyFileText(keys map[string]string) string {
	raw, _ := json.Marshal(map[string]any{"version": 1, "keys": keys, "updatedAt": "2026-10-25T08:00:00Z"})
	return string(raw)
}

// TestProviderKeysFileRules: the file is 0600; a start refuses one that
// others may read, that is malformed, that names a kind the configuration
// does not use, or that is not the process user's, naming the file and never
// its content; and the write is atomic: a crash between the write and the
// rename leaves the old file, whole.
func TestProviderKeysFileRules(t *testing.T) {
	const planted = "sk-planted-in-a-file-0123456789"
	b := newBYOK(t, nil)
	answered(t, b.admin("PUT", "/admin/v1/provider-keys", `{"deepseek":"`+accountDeepSeek+`"}`), http.StatusOK,
		`{"providerKeys":`+keyStatus("account", accountDeepSeek, "host", hostJEV)+`,"checked":{"deepseek":"models"}}`)
	if info, err := os.Stat(b.keysFile); err != nil || posixFileModes && info.Mode().Perm() != 0o600 {
		t.Fatal(info, err)
	}
	if entries, _ := os.ReadDir(b.state); slices.ContainsFunc(entries, func(e os.DirEntry) bool { return strings.Contains(e.Name(), ".tmp") }) {
		t.Fatal("a write left a temporary file:", entries)
	}

	// start makes a server of b's configuration, its key file holding
	// content with mode, and returns its key status or its error.
	start := func(edit func(*ServerConfig), content string, mode fs.FileMode) (map[string]memoryagent.KeyStatus, error) {
		t.Helper()
		state := t.TempDir()
		config := b.config
		config.DataRoot, config.ProviderKeysFile = t.TempDir(), filepath.Join(state, "provider-keys.json")
		config.MemoryConfig.BudgetFile = filepath.Join(state, "model-budget.db")
		config.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
		if edit != nil {
			edit(&config)
		}
		if content != "" {
			writeTestFile(t, config.ProviderKeysFile, content, mode)
		}
		server, err := NewServer(config)
		if err != nil {
			if !strings.Contains(err.Error(), config.ProviderKeysFile) {
				t.Errorf("the error %q does not name the key file", err)
			}
			if strings.Contains(err.Error(), planted) {
				t.Errorf("the error %q holds the key", err)
			}
			return nil, err
		}
		defer server.Close()
		return server.status(context.Background()).ProviderKeys, nil
	}
	if keys, err := start(nil, keyFileText(map[string]string{"jev": planted}), 0o600); err != nil || keys["jev"].Source != "account" || keys["deepseek"].Source != "host" {
		t.Fatal(keys, err)
	}
	if keys, err := start(nil, "", 0); err != nil || keys["jev"].Source != "host" {
		t.Fatal("a missing file:", keys, err)
	}
	if posixFileModes {
		for _, mode := range []fs.FileMode{0o644, 0o640, 0o604, 0o620} {
			if _, err := start(nil, keyFileText(map[string]string{"jev": planted}), mode); err == nil || !strings.Contains(err.Error(), "chmod 600") {
				t.Errorf("mode %o: %v", mode, err)
			}
		}
	}
	for _, content := range []string{
		"not json",
		`{"version":2,"keys":{}}`,
		`{"version":1}`,
		`{"version":1,"keys":{"deepseek":12345678}}`,
		`{"version":1,"keys":{"deepseek":"` + planted + `"},"extra":1}`,
		`{"version":1,"keys":{"deepseek":` + planted + `}}`,
		`{"version":1,"keys":{"deepseek":"short"}}`,
		`{"version":1,"keys":{"deepseek":"` + planted + `"}} trailing`,
		`{"version":1,"keys":{"deepseek":"` + planted + `"},"updatedAt":"yesterday"}`,
		`{"version":1,"keys":{"openai":"` + planted + `"}}`,
		`{"version":1,"keys":{"` + planted + `":"` + planted + `"}}`,
		strings.Repeat(" ", 70<<10) + `{"version":1,"keys":{}}`,
	} {
		if _, err := start(nil, content, 0o600); err == nil {
			t.Errorf("%.60q was accepted", content)
		}
	}
	// A kind the configuration does not use.
	localJudge := func(config *ServerConfig) {
		config.MemoryConfig.Judge = &memoryagent.ModelConfig{Kind: "local", Model: "local-judge", BaseURL: string(b.fake) + "/v1"}
	}
	if _, err := start(localJudge, keyFileText(map[string]string{"jev": planted}), 0o600); err == nil || !strings.Contains(err.Error(), "jev") {
		t.Errorf("a JEV key without a JEV model: %v", err)
	}
	// Not a regular file: a directory, a link.
	if _, err := start(func(config *ServerConfig) {
		if err := os.Mkdir(config.ProviderKeysFile, 0o700); err != nil {
			t.Fatal(err)
		}
	}, "", 0); err == nil {
		t.Error("a directory was taken for the key file")
	}
	linked := false
	if _, err := start(func(config *ServerConfig) {
		linked = os.Symlink(b.keysFile, config.ProviderKeysFile) == nil
	}, "", 0); err == nil && linked {
		t.Error("a symbolic link was taken for the key file")
	}
	// Another user's file.
	if posixFileModes {
		owner := ownerOf
		ownerOf = func(info os.FileInfo) (int, int, bool) { return 4242, 1000, true }
		_, err := start(nil, keyFileText(map[string]string{"jev": planted}), 0o600)
		ownerOf = owner
		if err == nil || !strings.Contains(err.Error(), "uid 4242") {
			t.Errorf("another user's file: %v", err)
		}
	}

	// A crash between the write and the rename: the old file stays whole,
	// the keys in memory are as they were, and the next start reads the old
	// file and removes what the write left.
	before, status := fileBytes(t, b.keysFile), keyStatus("account", accountDeepSeek, "host", hostJEV)
	writeInterrupted = func(string) bool { return true }
	answered(t, b.admin("PUT", "/admin/v1/provider-keys", `{"deepseek":"account-deepseek-key-0002","jev":"`+accountJEV+`"}`), http.StatusInternalServerError, `{"error":"internal"}`)
	writeInterrupted = nil
	b.unchanged(t, before, status, accountDeepSeek)
	entries, _ := os.ReadDir(b.state)
	if !slices.ContainsFunc(entries, func(e os.DirEntry) bool { return strings.HasPrefix(e.Name(), ".provider-keys.json.tmp") }) {
		t.Fatal("the interrupted write left no temporary file to clean up:", entries)
	}
	b.server.Close()
	restarted, err := NewServer(b.config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if got := restarted.status(context.Background()).ProviderKeys["deepseek"]; got.Fingerprint != memoryagent.Fingerprint(accountDeepSeek) {
		t.Fatal(got)
	}
	if entries, _ := os.ReadDir(b.state); slices.ContainsFunc(entries, func(e os.DirEntry) bool { return strings.Contains(e.Name(), ".tmp") }) {
		t.Fatal("the start left the interrupted write's file:", entries)
	}
}

// TestProviderKeysNeverLeak: a planted key never appears in a log, a reply,
// an error, a metric or /admin/status; only its fingerprint does.
func TestProviderKeysNeverLeak(t *testing.T) {
	const planted, rejected = "sk-planted-SECRET-0123456789", "sk-rejected-SECRET-0123456789"
	b := newBYOK(t, nil)
	var replies []string
	reply := func(response *httptest.ResponseRecorder) { replies = append(replies, response.Body.String()) }
	reply(b.admin("PUT", "/admin/v1/provider-keys", `{"deepseek":"`+planted+`","jev":"`+planted+`"}`))
	b.calls(t)
	b.fake.fault(t, `{"kind":"deepseek","modelsStatus":401}`)
	reply(b.admin("PUT", "/admin/v1/provider-keys", `{"deepseek":"`+rejected+`"}`))
	b.fake.fault(t, `{"kind":"deepseek","modelsFault":"close"}`)
	reply(b.admin("PUT", "/admin/v1/provider-keys", `{"deepseek":"`+rejected+`"}`))
	reply(b.admin("PUT", "/admin/v1/provider-keys", `{"`+planted+`":"`+rejected+`"}`))
	reply(b.admin("PUT", "/admin/v1/provider-keys", `{"jev":"`+planted+` "}`))
	reply(b.admin("GET", "/admin/status", ""))
	reply(b.admin("GET", "/metrics", ""))
	reply(b.admin("DELETE", "/admin/v1/provider-keys?kind=jev", ""))
	reply(b.admin("GET", "/admin/status", ""))
	if !strings.Contains(replies[0], memoryagent.Fingerprint(planted)) {
		t.Fatal("the reply does not show the fingerprint:", replies[0])
	}
	if !strings.Contains(b.log.String(), memoryagent.Fingerprint(planted)) {
		t.Fatal("the log does not show the fingerprint")
	}
	// The start's errors either.
	config := b.config
	config.DataRoot, config.ProviderKeysFile = t.TempDir(), filepath.Join(t.TempDir(), "provider-keys.json")
	var errs []string
	for _, content := range []string{`{"version":1,"keys":{"deepseek":` + planted + `}}`, `{"version":1,"keys":{"deepseek":"` + planted + `"},"x":0}`, `{"version":1,"keys":{"` + planted + `":"` + planted + `"}}`} {
		writeTestFile(t, config.ProviderKeysFile, content, 0o600)
		if server, err := NewServer(config); err == nil {
			server.Close()
			t.Fatalf("%s was accepted", content)
		} else {
			errs = append(errs, err.Error())
		}
	}
	for name, text := range map[string]string{"the log": b.log.String(), "a reply": strings.Join(replies, "\n"), "an error": strings.Join(errs, "\n")} {
		for _, key := range []string{planted, rejected} {
			if strings.Contains(text, key) {
				t.Errorf("%s holds the key %s", name, key)
			}
		}
	}
}

// fakeOf is the fake provider an inspect service's configuration calls.
func fakeOf(ih *inspectHarness) fakeAt {
	return fakeAt(strings.TrimSuffix(ih.inspector.config.MemoryConfig.Planner.TestEndpoint, "/chat/completions"))
}

// TestInspectUsesAccountKeys: aml-inspect reads the instance's key file at
// its start and again before each full search, by the same rules; one it
// cannot read keeps the keys it has, logged once. A lexical search needs no
// key.
func TestInspectUsesAccountKeys(t *testing.T) {
	keysFile := filepath.Join(t.TempDir(), "provider-keys.json")
	f := newFakeInstance(t, func(c *InspectConfig) {
		writeTestFile(t, keysFile, keyFileText(map[string]string{"deepseek": accountDeepSeek}), 0o600)
		c.ProviderKeysFile = keysFile
	})
	ih, fake := f.ih, fakeOf(f.ih)
	const host = "test-only-placeholder" // fakeProviderModels' host keys
	full := func() map[string]map[string]map[string]int {
		t.Helper()
		before := fake.keys(t)
		if answer := ih.search(t, map[string]any{"user_id": "u1", "query": "When did Caroline go to the support group?", "mode": "full"}); answer.Mode != "full" {
			t.Fatal(answer)
		}
		after := fake.keys(t)
		delta := map[string]map[string]map[string]int{}
		for kind, routes := range after {
			delta[kind] = map[string]map[string]int{}
			for route, counts := range routes {
				delta[kind][route] = map[string]int{}
				for fingerprint, n := range counts {
					if d := n - before[kind][route][fingerprint]; d > 0 {
						delta[kind][route][fingerprint] = d
					}
				}
			}
		}
		return delta
	}
	only := func(delta map[string]map[string]map[string]int, deepseek, jev string) {
		t.Helper()
		for kind, key := range map[string]string{"deepseek": deepseek, "jev": jev} {
			if calls := delta[kind]["calls"]; len(calls) != 1 || calls[memoryagent.Fingerprint(key)] == 0 {
				t.Fatalf("%s's calls sent %v, want only %s", kind, calls, memoryagent.Fingerprint(key))
			}
		}
	}
	only(full(), accountDeepSeek, host)
	// aml-serve replaces a key: the next full search reads it.
	writeTestFile(t, keysFile, keyFileText(map[string]string{"deepseek": "account-deepseek-key-0002", "jev": accountJEV}), 0o600)
	only(full(), "account-deepseek-key-0002", accountJEV)
	// It reverts them all: the file is gone, the host's keys are back.
	if err := os.Remove(keysFile); err != nil {
		t.Fatal(err)
	}
	only(full(), host, host)
	// A file it cannot read: the keys it has, and one line, however often.
	writeTestFile(t, keysFile, keyFileText(map[string]string{"jev": accountJEV}), 0o600)
	only(full(), host, accountJEV)
	writeTestFile(t, keysFile, `{"version":1,"keys":{"jev":`, 0o600)
	only(full(), host, accountJEV)
	only(full(), host, accountJEV)
	if lines := linesWith(ih.log.String(), keysFile); len(lines) != 1 || !strings.Contains(lines[0], "level=WARN") || strings.Contains(lines[0], accountJEV) {
		t.Fatalf("the log has %q", lines)
	}
	writeTestFile(t, keysFile, keyFileText(map[string]string{"jev": "account-jev-key-0002"}), 0o600)
	only(full(), host, "account-jev-key-0002")

	// A lexical search needs no key: a service with none at all serves it.
	t.Setenv("INSPECT_TEST_DEEPSEEK", "")
	t.Setenv("INSPECT_TEST_JEV", "")
	lexical := newInspector(t, f.h, func(c *InspectConfig) {
		c.MemoryConfig = ih.inspector.config.MemoryConfig
		c.DebugCaps, c.DebugBudgetFile = nil, ""
		realModels(c)
	})
	if answer := lexical.search(t, map[string]any{"user_id": "u1", "query": "support group"}); answer.Mode != "lexical" {
		t.Fatal(answer)
	}
	// With the account's keys alone, full searches start and send them.
	writeTestFile(t, keysFile, keyFileText(map[string]string{"deepseek": accountDeepSeek, "jev": accountJEV}), 0o600)
	accountOnly := newInspector(t, f.h, func(c *InspectConfig) {
		c.MemoryConfig = ih.inspector.config.MemoryConfig
		c.DebugBudgetFile = filepath.Join(t.TempDir(), "debug-budget.db")
		c.ProviderKeysFile = keysFile
		realModels(c)
	})
	ih = accountOnly
	only(full(), accountDeepSeek, accountJEV)
}
