package aml

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
)

// newBudgets is a byok server with a settings file in its state directory
// (<state>/settings.json): its caps, DeepSeek CNY 600 and JEV USD 60, can
// change at run time.
func newBudgets(t *testing.T, edit func(*ServerConfig)) *byok {
	t.Helper()
	return newBYOK(t, func(config *ServerConfig) {
		config.SettingsFile = filepath.Join(filepath.Dir(config.ProviderKeysFile), "settings.json")
		if edit != nil {
			edit(config)
		}
	})
}

// settingsFile is b's settings file.
func (b *byok) settingsFile() string { return filepath.Join(b.state, "settings.json") }

// capEntry is one kind of the budget routes' reply, spelled as the server
// spells it (encoding/json): cap is a number, or nil for an unlimited one.
func capEntry(cap any, currency string, spent, held float64, source string, over, unlimited bool) string {
	raw, _ := json.Marshal(struct {
		Cap       any     `json:"cap"`
		Currency  string  `json:"currency"`
		Spent     float64 `json:"spent"`
		Held      float64 `json:"held"`
		Source    string  `json:"source"`
		Over      bool    `json:"over"`
		Unlimited bool    `json:"unlimited"`
	}{cap, currency, spent, held, source, over, unlimited})
	return string(raw)
}

// okAnswer checks that an admin answer is a 200 with the admin routes'
// headers, and returns its body.
func okAnswer(t *testing.T, response interface {
	Result() *http.Response
}) string {
	t.Helper()
	result := response.Result()
	raw, _ := io.ReadAll(result.Body)
	if result.StatusCode != http.StatusOK || result.Header.Get("Content-Type") != "application/json" || result.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("answered %d %v %s", result.StatusCode, result.Header, raw)
	}
	return string(raw)
}

// spentNow is what each kind's account has spent and holds, by
// /admin/status.
func (b *byok) spentNow(t *testing.T) (spent, held map[string]float64) {
	t.Helper()
	spent, held = map[string]float64{}, map[string]float64{}
	for _, account := range b.adminStatus(t).Budgets {
		spent[account.Kind], held[account.Kind] = account.Spent, account.Held
	}
	return spent, held
}

// accountOf is the kind's entry of /admin/status's budgets, as JSON fields.
func (b *byok) accountOf(t *testing.T, kind string) map[string]any {
	t.Helper()
	var status struct{ Budgets []map[string]any }
	if err := json.Unmarshal(b.admin("GET", "/admin/status", "").Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	for _, account := range status.Budgets {
		if account["kind"] == kind {
			return account
		}
	}
	t.Fatalf("no %s account in %v", kind, status.Budgets)
	return nil
}

// settingsOf reads the settings file as JSON: its caps (nil for unlimited),
// and whether it exists.
func settingsOf(t *testing.T, path string) (map[string]*float64, bool) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false
	}
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Version   int                 `json:"version"`
		Budgets   map[string]*float64 `json:"budgets"`
		UpdatedAt string              `json:"updatedAt"`
	}
	if err := json.Unmarshal(raw, &file); err != nil || file.Version != 1 {
		t.Fatalf("the settings file is %s: %v", raw, err)
	}
	if _, err := time.Parse(time.RFC3339, file.UpdatedAt); err != nil {
		t.Fatalf("updatedAt %q: %v", file.UpdatedAt, err)
	}
	return file.Budgets, true
}

func ptr(v float64) *float64 { return &v }

func samePointers(a, b map[string]*float64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		w, ok := b[k]
		if !ok || (v == nil) != (w == nil) || v != nil && *v != *w {
			return false
		}
	}
	return true
}

// TestBudgetCapsSetAndRevert: a cap set through the admin listener is
// written to the settings file and applies at once, shown with its source;
// null is unlimited; a revert puts the configuration's cap back, and the
// file goes once it holds none.
func TestBudgetCapsSetAndRevert(t *testing.T) {
	b := newBudgets(t, nil)
	b.calls(t)
	spent, _ := b.spentNow(t)
	if spent["deepseek"] <= 0 || spent["jev"] <= 0 {
		t.Fatal("the calls spent nothing:", spent)
	}
	for kind, cap := range map[string]float64{"deepseek": 600, "jev": 60} {
		if account := b.accountOf(t, kind); account["cap"] != cap || account["source"] != "config" || account["unlimited"] != false {
			t.Fatalf("%s: %v", kind, account)
		}
	}

	answered(t, b.admin("PUT", "/admin/v1/budgets", `{"deepseek":800}`), http.StatusOK,
		`{"budgets":{"deepseek":`+capEntry(800, "CNY", spent["deepseek"], 0, "settings", false, false)+`,"jev":`+capEntry(60, "USD", spent["jev"], 0, "config", false, false)+`}}`)
	if caps, ok := settingsOf(t, b.settingsFile()); !ok || !samePointers(caps, map[string]*float64{"deepseek": ptr(800)}) {
		t.Fatal(caps, ok)
	}
	if info, err := os.Stat(b.settingsFile()); err != nil || posixFileModes && info.Mode().Perm() != 0o600 {
		t.Fatal(info, err)
	}
	if account := b.accountOf(t, "deepseek"); account["cap"] != 800.0 || account["source"] != "settings" {
		t.Fatal(account)
	}
	// Unlimited: null, in the file and in the answers.
	answered(t, b.admin("PUT", "/admin/v1/budgets", `{"jev":null}`), http.StatusOK,
		`{"budgets":{"deepseek":`+capEntry(800, "CNY", spent["deepseek"], 0, "settings", false, false)+`,"jev":`+capEntry(nil, "USD", spent["jev"], 0, "settings", false, true)+`}}`)
	if caps, _ := settingsOf(t, b.settingsFile()); !samePointers(caps, map[string]*float64{"deepseek": ptr(800), "jev": nil}) {
		t.Fatal(caps)
	}
	if account := b.accountOf(t, "jev"); account["cap"] != nil || account["unlimited"] != true || account["source"] != "settings" {
		t.Fatal(account)
	}
	if lines := linesWith(b.log.String(), "budget caps"); len(lines) != 2 || !strings.Contains(lines[0], "level=INFO") ||
		!strings.Contains(lines[0], `msg="budget caps set: deepseek 600→800 CNY"`) || !strings.Contains(lines[1], `msg="budget caps set: jev 60→unlimited USD"`) {
		t.Fatalf("the log has %q", lines)
	}

	// A restart reads the file.
	b.server.Close()
	restarted, err := NewServer(b.config)
	if err != nil {
		t.Fatal(err)
	}
	b.server = restarted
	t.Cleanup(func() { restarted.Close() })
	if caps := restarted.budget.Caps(); caps["deepseek"] != 800 || !math.IsInf(caps["jev"], 1) || caps["openai"] != 5 {
		t.Fatal(caps)
	}

	// Revert one kind, then all: the configuration's caps, and no file.
	spent, _ = b.spentNow(t)
	answered(t, b.admin("DELETE", "/admin/v1/budgets?kind=deepseek", ""), http.StatusOK,
		`{"budgets":{"deepseek":`+capEntry(600, "CNY", spent["deepseek"], 0, "config", false, false)+`,"jev":`+capEntry(nil, "USD", spent["jev"], 0, "settings", false, true)+`}}`)
	if caps, _ := settingsOf(t, b.settingsFile()); !samePointers(caps, map[string]*float64{"jev": nil}) {
		t.Fatal(caps)
	}
	answered(t, b.admin("DELETE", "/admin/v1/budgets?kind=all", ""), http.StatusOK,
		`{"budgets":{"deepseek":`+capEntry(600, "CNY", spent["deepseek"], 0, "config", false, false)+`,"jev":`+capEntry(60, "USD", spent["jev"], 0, "config", false, false)+`}}`)
	if _, ok := settingsOf(t, b.settingsFile()); ok {
		t.Fatal("the settings file is left with no cap in it")
	}
	if caps := restarted.budget.Caps(); caps["deepseek"] != 600 || caps["jev"] != 60 {
		t.Fatal(caps)
	}
	if lines := linesWith(b.log.String(), "budget caps reverted"); len(lines) != 2 ||
		!strings.Contains(lines[0], "budget caps reverted to the configuration's: deepseek 800→600 CNY") ||
		!strings.Contains(lines[1], "budget caps reverted to the configuration's: deepseek 600→600 CNY, jev unlimited→60 USD") {
		t.Fatalf("the log has %q", lines)
	}
	for _, query := range []string{"", "?kind=", "?kind=openai", "?kind=deepseek&kind=jev", "?kind=%zz", "?kind=jev;x=1"} {
		answered(t, b.admin("DELETE", "/admin/v1/budgets"+query, ""), http.StatusBadRequest, `{"error":"invalid","field":"kind"}`)
	}
}

// TestBudgetCapBelowSpentStopsPaidCalls: a cap below what is spent is
// allowed and said so ("over"); the kind's paid calls then stop before any
// request, as at a cap reached, while the other kind's go on, until a cap
// above it again.
func TestBudgetCapBelowSpentStopsPaidCalls(t *testing.T) {
	b := newBudgets(t, nil)
	b.roomy()
	b.calls(t)
	spent, _ := b.spentNow(t)
	const below = 0.000001
	if spent["deepseek"] <= below {
		t.Fatal(spent)
	}
	answered(t, b.admin("PUT", "/admin/v1/budgets", `{"deepseek":0.000001}`), http.StatusOK,
		`{"budgets":{"deepseek":`+capEntry(below, "CNY", spent["deepseek"], 0, "settings", true, false)+`,"jev":`+capEntry(60, "USD", spent["jev"], 0, "config", false, false)+`}}`)
	before := b.fake.keys(t)
	if _, _, err := b.server.models.Planner.Complete(context.Background(), "s", "over the cap"); !errors.Is(err, memoryagent.ErrBudgetCap) {
		t.Fatal("a DeepSeek call over its cap:", err)
	}
	if _, _, err := b.server.models.Judge.Decide(context.Background(), map[string]any{}, map[string]memoryagent.Question{"q": {Type: "noul", Instructions: "a"}}); err != nil {
		t.Fatal("JEV stopped with DeepSeek:", err)
	}
	after := b.fake.keys(t)
	if sent(after, before, "deepseek", "calls", hostDeepSeek) != 0 || sent(after, before, "jev", "calls", hostJEV) != 1 {
		t.Fatalf("the fake saw %v (before %v)", after, before)
	}
	if response := b.admin("GET", "/ready", ""); response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "budget deepseek is at") {
		t.Fatal(response.Code, response.Body.String())
	}
	answered(t, b.admin("PUT", "/admin/v1/budgets", `{"deepseek":600}`), http.StatusOK,
		`{"budgets":{"deepseek":`+capEntry(600, "CNY", spent["deepseek"], 0, "settings", false, false)+`,"jev":`+capEntry(60, "USD", b.accountOf(t, "jev")["spent"].(float64), 0, "config", false, false)+`}}`)
	if _, _, err := b.server.models.Planner.Complete(context.Background(), "s", "under the cap again"); err != nil {
		t.Fatal(err)
	}
}

// TestBudgetCapsKeepAHeldReservation: a reservation already held when the
// cap changes keeps its own amount, and settles as it would have; the next
// reservation meets the new cap.
func TestBudgetCapsKeepAHeldReservation(t *testing.T) {
	b := newBudgets(t, nil)
	b.calls(t)
	b.fake.fault(t, `{"kind":"deepseek","latency":"1500ms"}`)
	before := b.fake.keys(t)
	inFlight := make(chan error, 1)
	go func() {
		_, _, err := b.server.models.Planner.Complete(context.Background(), "s", "held across the change")
		inFlight <- err
	}()
	eventually(t, func() bool { return sent(b.fake.keys(t), before, "deepseek", "calls", hostDeepSeek) == 1 })
	spent, held := b.spentNow(t)
	if held["deepseek"] != 0.25 {
		t.Fatal("the call in flight holds", held)
	}
	answered(t, b.admin("PUT", "/admin/v1/budgets", `{"deepseek":0.000001}`), http.StatusOK,
		`{"budgets":{"deepseek":`+capEntry(0.000001, "CNY", spent["deepseek"], 0.25, "settings", true, false)+`,"jev":`+capEntry(60, "USD", spent["jev"], 0, "config", false, false)+`}}`)
	if err := <-inFlight; err != nil {
		t.Fatal("the held call failed:", err)
	}
	after, held := b.spentNow(t)
	if held["deepseek"] != 0 || after["deepseek"] <= spent["deepseek"] {
		t.Fatal("the held reservation did not settle:", after, held)
	}
	b.fake.clear(t)
	if _, _, err := b.server.models.Planner.Complete(context.Background(), "s", "after the change"); !errors.Is(err, memoryagent.ErrBudgetCap) {
		t.Fatal(err)
	}
}

// TestBudgetCapsUnlimited: an unlimited cap refuses no reservation for a
// cap, however much is spent, and its spend is still metered; it shows as
// null and unlimited, never over, and as +Inf in the cap gauge.
func TestBudgetCapsUnlimited(t *testing.T) {
	b := newBudgets(t, nil)
	b.calls(t)
	okAnswer(t, b.admin("PUT", "/admin/v1/budgets", `{"deepseek":0.000001}`))
	if _, _, err := b.server.models.Planner.Complete(context.Background(), "s", "refused"); !errors.Is(err, memoryagent.ErrBudgetCap) {
		t.Fatal(err)
	}
	spent, _ := b.spentNow(t)
	answered(t, b.admin("PUT", "/admin/v1/budgets", `{"deepseek":null}`), http.StatusOK,
		`{"budgets":{"deepseek":`+capEntry(nil, "CNY", spent["deepseek"], 0, "settings", false, true)+`,"jev":`+capEntry(60, "USD", spent["jev"], 0, "config", false, false)+`}}`)
	for i := range 20 {
		if _, _, err := b.server.models.Planner.Complete(context.Background(), "s", fmt.Sprintf("unlimited %d", i)); err != nil {
			t.Fatalf("call %d while unlimited: %v", i, err)
		}
	}
	account := b.accountOf(t, "deepseek")
	if account["cap"] != nil || account["unlimited"] != true || account["spent"].(float64) <= spent["deepseek"] {
		t.Fatal("while unlimited:", account)
	}
	if !strings.Contains(b.admin("GET", "/metrics", "").Body.String(), `mnemon_aml_budget_cap{account="deepseek"} +Inf`) {
		t.Error("the cap gauge of an unlimited account is not +Inf")
	}
	if response := b.admin("GET", "/ready", ""); strings.Contains(response.Body.String(), "budget deepseek") {
		t.Fatal("an unlimited account is not ready:", response.Body.String())
	}
	if !strings.Contains(b.log.String(), `msg="budget caps set: deepseek 0.000001→unlimited CNY"`) {
		t.Fatal("the log lacks the change to unlimited")
	}
	if line := b.server.statusLine(context.Background()); !strings.Contains(line, "deepseek spent") || !strings.Contains(line, "cap unlimited") {
		t.Fatal(line)
	}
	okAnswer(t, b.admin("DELETE", "/admin/v1/budgets?kind=deepseek", ""))
	if account := b.accountOf(t, "deepseek"); account["cap"] != 600.0 || account["unlimited"] != false {
		t.Fatal(account)
	}
}

// TestBudgetCapsRequestRules: what the routes refuse before they change
// anything, and those of a server without a settings file or a model of a
// kind.
func TestBudgetCapsRequestRules(t *testing.T) {
	b := newBudgets(t, nil)
	for _, tc := range []struct{ body, answer string }{
		{`[]`, `{"error":"invalid","field":"body"}`},
		{`{}`, `{"error":"invalid","field":"body"}`},
		{`{"deepseek":800} {}`, `{"error":"invalid","field":"body"}`},
		{`{"deepseek":800,"openai":5}`, `{"error":"invalid","field":"openai"}`},
		{`{"sk-` + strings.Repeat("0", 32) + `":5}`, `{"error":"invalid","field":"body"}`},
		{`{"deepseek":"800"}`, `{"error":"invalid","field":"deepseek"}`},
		{`{"deepseek":true}`, `{"error":"invalid","field":"deepseek"}`},
		{`{"deepseek":0}`, `{"error":"invalid","field":"deepseek"}`},
		{`{"deepseek":-1}`, `{"error":"invalid","field":"deepseek"}`},
		{`{"jev":100000.5}`, `{"error":"invalid","field":"jev"}`},
		{`{"jev":1e400}`, `{"error":"invalid","field":"jev"}`},
	} {
		answered(t, b.admin("PUT", "/admin/v1/budgets", tc.body), http.StatusBadRequest, tc.answer)
	}
	answered(t, b.admin("PUT", "/admin/v1/budgets", `{"jev":`+strings.Repeat(" ", 4096)+`1}`), http.StatusRequestEntityTooLarge, `{"error":"too_large"}`)
	if _, ok := settingsOf(t, b.settingsFile()); ok {
		t.Fatal("a refused request wrote the settings file")
	}
	// The bounds themselves pass.
	for _, body := range []string{`{"jev":100000}`, `{"deepseek":0.0001}`} {
		if response := b.admin("PUT", "/admin/v1/budgets", body); response.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", body, response.Code, response.Body.String())
		}
	}
	for _, method := range []string{"PUT", "DELETE"} {
		if response := adminRequest(b.server, method, "/admin/v1/budgets?kind=all", `{"deepseek":800}`, false); response.Code != http.StatusUnauthorized {
			t.Fatalf("%s without the token: %d", method, response.Code)
		}
	}
	if response := b.admin("GET", "/admin/v1/budgets", ""); response.Code != http.StatusNotFound {
		t.Fatalf("GET: %d", response.Code)
	}
	local := newBudgets(t, func(config *ServerConfig) {
		config.MemoryConfig.Judge = &memoryagent.ModelConfig{Kind: "local", Model: "local-judge", BaseURL: string(b.fake) + "/v1"}
	})
	answered(t, local.admin("PUT", "/admin/v1/budgets", `{"jev":5}`), http.StatusConflict, `{"error":"unknown_kind","kind":"jev"}`)
	answered(t, local.admin("DELETE", "/admin/v1/budgets?kind=jev", ""), http.StatusConflict, `{"error":"unknown_kind","kind":"jev"}`)
	none := newBudgets(t, func(config *ServerConfig) { config.SettingsFile = "" })
	answered(t, none.admin("PUT", "/admin/v1/budgets", `{"deepseek":800}`), http.StatusConflict, `{"error":"no_settings_file"}`)
	answered(t, none.admin("DELETE", "/admin/v1/budgets?kind=all", ""), http.StatusConflict, `{"error":"no_settings_file"}`)
}

// settingsText is a settings file holding caps (nil for unlimited).
func settingsText(caps map[string]*float64) string {
	raw, _ := json.Marshal(map[string]any{"version": 1, "budgets": caps, "updatedAt": "2026-10-25T08:00:00Z"})
	return string(raw)
}

// TestSettingsFileRules: the settings file is 0600, written atomically; a
// start refuses one that others may read, another user's, a malformed one or
// one with a kind the configuration has no model of, naming the file.
func TestSettingsFileRules(t *testing.T) {
	b := newBudgets(t, nil)
	okAnswer(t, b.admin("PUT", "/admin/v1/budgets", `{"deepseek":800}`))
	if info, err := os.Stat(b.settingsFile()); err != nil || posixFileModes && info.Mode().Perm() != 0o600 {
		t.Fatal(info, err)
	}
	start := func(edit func(*ServerConfig), content string, mode fs.FileMode) (map[string]float64, error) {
		t.Helper()
		state := t.TempDir()
		config := b.config
		config.DataRoot, config.SettingsFile = t.TempDir(), filepath.Join(state, "settings.json")
		config.ProviderKeysFile = filepath.Join(state, "provider-keys.json")
		config.MemoryConfig.BudgetFile = filepath.Join(state, "model-budget.db")
		config.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
		if edit != nil {
			edit(&config)
		}
		if content != "" {
			writeTestFile(t, config.SettingsFile, content, mode)
		}
		server, err := NewServer(config)
		if err != nil {
			if !strings.Contains(err.Error(), config.SettingsFile) {
				t.Errorf("the error %q does not name the settings file", err)
			}
			return nil, err
		}
		defer server.Close()
		return server.budget.Caps(), nil
	}
	if caps, err := start(nil, settingsText(map[string]*float64{"deepseek": nil, "jev": ptr(70)}), 0o600); err != nil || !math.IsInf(caps["deepseek"], 1) || caps["jev"] != 70 {
		t.Fatal(caps, err)
	}
	if caps, err := start(nil, "", 0); err != nil || caps["deepseek"] != 600 || caps["jev"] != 60 {
		t.Fatal("a missing file:", caps, err)
	}
	if posixFileModes {
		if _, err := start(nil, settingsText(map[string]*float64{"jev": ptr(70)}), 0o644); err == nil || !strings.Contains(err.Error(), "chmod 600") {
			t.Error("mode 644:", err)
		}
		owner := ownerOf
		ownerOf = func(os.FileInfo) (int, int, bool) { return 4242, 1000, true }
		_, err := start(nil, settingsText(map[string]*float64{"jev": ptr(70)}), 0o600)
		ownerOf = owner
		if err == nil || !strings.Contains(err.Error(), "uid 4242") {
			t.Error("another user's file:", err)
		}
	}
	for _, content := range []string{
		"not json",
		`{"version":2,"budgets":{}}`,
		`{"version":1}`,
		`{"version":1,"budgets":{"deepseek":"600"}}`,
		`{"version":1,"budgets":{"deepseek":0}}`,
		`{"version":1,"budgets":{"deepseek":-1}}`,
		`{"version":1,"budgets":{"deepseek":100001}}`,
		`{"version":1,"budgets":{"deepseek":600},"extra":1}`,
		`{"version":1,"budgets":{"deepseek":600}} trailing`,
		`{"version":1,"budgets":{"deepseek":600},"updatedAt":"yesterday"}`,
		`{"version":1,"budgets":{"openai":5}}`,
	} {
		if _, err := start(nil, content, 0o600); err == nil {
			t.Errorf("%.60q was accepted", content)
		}
	}
	localJudge := func(config *ServerConfig) {
		config.MemoryConfig.Judge = &memoryagent.ModelConfig{Kind: "local", Model: "local-judge", BaseURL: string(b.fake) + "/v1"}
	}
	if _, err := start(localJudge, settingsText(map[string]*float64{"jev": ptr(5)}), 0o600); err == nil || !strings.Contains(err.Error(), "jev") {
		t.Error("a JEV cap without a JEV model:", err)
	}
	// Outside the data root, as the key file.
	config := b.config
	config.SettingsFile = filepath.Join(config.DataRoot, "settings.json")
	if server, err := NewServer(config); err == nil {
		server.Close()
		t.Fatal("a settings file in the data root was taken")
	}

	// A crash between the write and the rename leaves the old file whole.
	before := fileBytes(t, b.settingsFile())
	writeInterrupted = func(string) bool { return true }
	answered(t, b.admin("PUT", "/admin/v1/budgets", `{"deepseek":900,"jev":null}`), http.StatusInternalServerError, `{"error":"internal"}`)
	writeInterrupted = nil
	if got := fileBytes(t, b.settingsFile()); got != before {
		t.Fatalf("the settings file is %s, was %s", got, before)
	}
	if caps := b.server.budget.Caps(); caps["deepseek"] != 800 || caps["jev"] != 60 {
		t.Fatal("an interrupted write changed the caps:", caps)
	}
	entries, _ := os.ReadDir(b.state)
	if !slices.ContainsFunc(entries, func(e os.DirEntry) bool { return strings.HasPrefix(e.Name(), ".settings.json.tmp") }) {
		t.Fatal("the interrupted write left no temporary file:", entries)
	}
	b.server.Close()
	restarted, err := NewServer(b.config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if caps := restarted.budget.Caps(); caps["deepseek"] != 800 {
		t.Fatal(caps)
	}
	if entries, _ := os.ReadDir(b.state); slices.ContainsFunc(entries, func(e os.DirEntry) bool { return strings.Contains(e.Name(), ".tmp") }) {
		t.Fatal("the start left the interrupted write's file:", entries)
	}
}
