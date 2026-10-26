package aml

import (
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
)

// debugBudgetAnswer is /v1/info's debugBudget, and what the debug caps
// routes answer: its caps and remaining amounts are null for an unlimited
// kind.
type debugBudgetAnswer struct {
	Currency  map[string]string   `json:"currency"`
	Caps      map[string]*float64 `json:"caps"`
	Spent     map[string]float64  `json:"spent"`
	Held      map[string]float64  `json:"held"`
	Remaining map[string]*float64 `json:"remaining"`
}

// debugCapsRig is an inspect service whose debug caps (deepseek 5, jev 1 by
// the flag) can change at run time: its settings file is in its own state.
type debugCapsRig struct {
	*inspectHarness
	h        *harness
	settings string
}

func newDebugCapsRig(t *testing.T, edit func(*InspectConfig)) *debugCapsRig {
	t.Helper()
	h := layeredHarness(t)
	h.mustAdd(t, addRequest("r1", "u1"))
	state := t.TempDir()
	r := &debugCapsRig{h: h, settings: filepath.Join(state, "settings.json")}
	r.inspectHarness = newInspector(t, h, func(config *InspectConfig) {
		config.DebugBudgetFile = filepath.Join(state, "debug-budget.db")
		config.SettingsFile = r.settings
		if edit != nil {
			edit(config)
		}
	})
	return r
}

// debugBudget decodes a debug caps answer, or /v1/info's debugBudget.
func (r *debugCapsRig) debugBudget(t *testing.T, method, path string, body any) debugBudgetAnswer {
	t.Helper()
	response := r.call(t, method, path, body)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%s %s: %d %s", method, path, response.Code, response.Body.String())
	}
	var answer debugBudgetAnswer
	if path == "/v1/info" {
		var info struct {
			DebugBudget debugBudgetAnswer `json:"debugBudget"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &info); err != nil {
			t.Fatal(err)
		}
		return info.DebugBudget
	}
	if err := json.Unmarshal(response.Body.Bytes(), &answer); err != nil {
		t.Fatal(err, response.Body.String())
	}
	return answer
}

// capsAre checks the caps and remaining amounts of an answer: nil is null.
func capsAre(t *testing.T, answer debugBudgetAnswer, caps map[string]*float64) {
	t.Helper()
	if !samePointers(answer.Caps, caps) || !samePointers(answer.Remaining, caps) || answer.Currency["deepseek"] != "CNY" || answer.Currency["jev"] != "USD" {
		t.Fatalf("caps %v remaining %v, want %v", answer.Caps, answer.Remaining, caps)
	}
}

// debugSettingsOf reads the inspect settings file: its caps (nil for
// unlimited), and whether it exists.
func debugSettingsOf(t *testing.T, path string) (map[string]*float64, bool) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false
	}
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Version   int                 `json:"version"`
		DebugCaps map[string]*float64 `json:"debugCaps"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil || file.Version != 1 {
		t.Fatalf("the inspect settings file is %s: %v", raw, err)
	}
	return file.DebugCaps, true
}

// TestInspectDebugCapsSetAndRevert: PUT /v1/debug-caps writes the service's
// own settings file and swaps the caps; /v1/info shows them; DELETE puts the
// flag's back; a restart reads the file.
func TestInspectDebugCapsSetAndRevert(t *testing.T) {
	r := newDebugCapsRig(t, nil)
	capsAre(t, r.debugBudget(t, "GET", "/v1/info", nil), map[string]*float64{"deepseek": ptr(5), "jev": ptr(1)})
	capsAre(t, r.debugBudget(t, "PUT", "/v1/debug-caps", `{"deepseek":8}`), map[string]*float64{"deepseek": ptr(8), "jev": ptr(1)})
	capsAre(t, r.debugBudget(t, "GET", "/v1/info", nil), map[string]*float64{"deepseek": ptr(8), "jev": ptr(1)})
	if caps, ok := debugSettingsOf(t, r.settings); !ok || !samePointers(caps, map[string]*float64{"deepseek": ptr(8)}) {
		t.Fatal(caps, ok)
	}
	if info, err := os.Stat(r.settings); err != nil || posixFileModes && info.Mode().Perm() != 0o600 {
		t.Fatal(info, err)
	}
	if caps := r.inspector.budget.Caps(); caps["deepseek"] != 8 || caps["jev"] != 1 {
		t.Fatal("the debug budget enforces", caps)
	}
	if !strings.Contains(r.log.String(), `msg="debug caps set: deepseek 5→8 CNY"`) {
		t.Fatal("the log lacks the change:", r.log.String())
	}
	// A restart reads the file over the flag.
	restarted := newInspector(t, r.h, func(config *InspectConfig) {
		config.DebugBudgetFile = filepath.Join(t.TempDir(), "debug-budget.db")
		config.SettingsFile = r.settings
	})
	var info infoAnswer
	restarted.ok(t, "GET", "/v1/info", nil, &info)
	if info.Debug.Caps["deepseek"] != 8 || info.Debug.Caps["jev"] != 1 {
		t.Fatal(info.Debug)
	}
	capsAre(t, r.debugBudget(t, "DELETE", "/v1/debug-caps?kind=deepseek", nil), map[string]*float64{"deepseek": ptr(5), "jev": ptr(1)})
	if _, ok := debugSettingsOf(t, r.settings); ok {
		t.Fatal("the settings file is left with no cap in it")
	}
	capsAre(t, r.debugBudget(t, "PUT", "/v1/debug-caps", `{"deepseek":2,"jev":0.5}`), map[string]*float64{"deepseek": ptr(2), "jev": ptr(0.5)})
	capsAre(t, r.debugBudget(t, "DELETE", "/v1/debug-caps?kind=all", nil), map[string]*float64{"deepseek": ptr(5), "jev": ptr(1)})
	if !strings.Contains(r.log.String(), `msg="debug caps reverted to the flag's: deepseek 2→5 CNY, jev 0.5→1 USD"`) {
		t.Fatal("the log lacks the revert:", r.log.String())
	}
}

// TestInspectDebugCapsUnlimited: null is unlimited: /v1/info shows a null
// cap and a null remaining amount, full searches count it as positive, and
// the debug budget refuses no reservation for it.
func TestInspectDebugCapsUnlimited(t *testing.T) {
	r := newDebugCapsRig(t, nil)
	capsAre(t, r.debugBudget(t, "PUT", "/v1/debug-caps", `{"jev":null}`), map[string]*float64{"deepseek": ptr(5), "jev": nil})
	var info infoAnswer
	r.ok(t, "GET", "/v1/info", nil, &info)
	if !info.Modes["full"] {
		t.Fatal("an unlimited cap turned full searches off")
	}
	if caps := r.inspector.budget.Caps(); !math.IsInf(caps["jev"], 1) {
		t.Fatal(caps)
	}
	if caps, _ := debugSettingsOf(t, r.settings); !samePointers(caps, map[string]*float64{"jev": nil}) {
		t.Fatal(caps)
	}
	// A full search is not refused for the cap, however low the other kind's
	// is not: the unlimited kind is never exhausted.
	if exhausted, err := r.inspector.debugExhausted(t.Context()); err != nil || exhausted {
		t.Fatal(exhausted, err)
	}
	if !strings.Contains(r.log.String(), `msg="debug caps set: jev 1→unlimited USD"`) {
		t.Fatal(r.log.String())
	}
	// A flag without a JEV cap has no full mode, but an unlimited JEV cap in
	// the settings file gives it one at the start.
	settings := filepath.Join(t.TempDir(), "settings.json")
	writeTestFile(t, settings, `{"version":1,"debugCaps":{"jev":null}}`, 0o600)
	unlimited := newInspector(t, r.h, func(config *InspectConfig) {
		config.DebugCaps = map[string]float64{"deepseek": 5}
		config.DebugBudgetFile = filepath.Join(t.TempDir(), "debug-budget.db")
		config.SettingsFile = settings
	})
	unlimited.ok(t, "GET", "/v1/info", nil, &info)
	if !info.Modes["full"] {
		t.Fatal("an unlimited JEV cap at the start left full searches off")
	}
	answer := unlimited.search(t, map[string]any{"user_id": "u1", "query": "support group", "mode": "full"})
	if answer.Mode != "full" {
		t.Fatal(answer)
	}
}

// TestInspectDebugCapsRules: what the routes refuse, and a service whose
// caps cannot change: without a settings file, or without full searches.
func TestInspectDebugCapsRules(t *testing.T) {
	r := newDebugCapsRig(t, nil)
	for _, tc := range []struct{ body, field string }{
		{`[]`, "body"}, {`{}`, "body"}, {`{"deepseek":1} x`, "body"},
		{`{"openai":1}`, "openai"}, {`{"deepseek":"1"}`, "deepseek"}, {`{"deepseek":0}`, "deepseek"},
		{`{"jev":-1}`, "jev"}, {`{"jev":1000.5}`, "jev"}, {`{"sk-` + strings.Repeat("0", 32) + `":1}`, "body"},
	} {
		r.refused(t, "PUT", "/v1/debug-caps", tc.body, http.StatusBadRequest, "invalid", tc.field)
	}
	for _, query := range []string{"", "?kind=", "?kind=openai", "?kind=jev&kind=deepseek"} {
		r.refused(t, "DELETE", "/v1/debug-caps"+query, nil, http.StatusBadRequest, "invalid", "kind")
	}
	if response := r.call(t, "GET", "/v1/debug-caps", nil); response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != "PUT, DELETE" {
		t.Fatal(response.Code, response.Header())
	}
	capsAre(t, r.debugBudget(t, "PUT", "/v1/debug-caps", `{"jev":1000}`), map[string]*float64{"deepseek": ptr(5), "jev": ptr(1000)})
	if caps, _ := debugSettingsOf(t, r.settings); !samePointers(caps, map[string]*float64{"jev": ptr(1000)}) {
		t.Fatal(caps)
	}
	// Without a settings file, or without full searches, the caps stay.
	none := newDebugCapsRig(t, func(config *InspectConfig) { config.SettingsFile = "" })
	none.refused(t, "PUT", "/v1/debug-caps", `{"jev":2}`, http.StatusConflict, "no_settings_file", "")
	off := newDebugCapsRig(t, func(config *InspectConfig) { config.DebugCaps = nil })
	off.refused(t, "PUT", "/v1/debug-caps", `{"jev":2}`, http.StatusConflict, "full_disabled", "")
	off.refused(t, "DELETE", "/v1/debug-caps?kind=all", nil, http.StatusConflict, "full_disabled", "")
	// The token is asked first.
	withToken := newDebugCapsRig(t, func(config *InspectConfig) { config.Token = strings.Repeat("t", 40) })
	withToken.token = ""
	withToken.refused(t, "PUT", "/v1/debug-caps", `{"jev":2}`, http.StatusUnauthorized, "unauthorized", "")
}

// TestInspectSettingsFileRules: the start refuses an inspect settings file
// that breaks a rule, naming the file; one in the data root is refused, as
// the service never writes there.
func TestInspectSettingsFileRules(t *testing.T) {
	h := layeredHarness(t)
	h.mustAdd(t, addRequest("r1", "u1"))
	start := func(content string, mode os.FileMode, edit func(*InspectConfig)) error {
		t.Helper()
		state := t.TempDir()
		settings := filepath.Join(state, "settings.json")
		if content != "" {
			writeTestFile(t, settings, content, mode)
		}
		config := InspectConfig{DataRoot: h.root, MemoryConfig: h.server.config.MemoryConfig, DebugBudgetFile: filepath.Join(state, "debug-budget.db"),
			DebugCaps: map[string]float64{"deepseek": 5, "jev": 1}, SettingsFile: settings, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			Models: func(memoryagent.Config) (memoryagent.Models, error) {
				return memoryagent.Models{Planner: h.planner, Judge: h.judge}, nil
			}}
		if edit != nil {
			edit(&config)
		}
		inspector, err := NewInspector(config)
		if err == nil {
			inspector.Close()
			return nil
		}
		if !strings.Contains(err.Error(), config.SettingsFile) {
			t.Errorf("the error %q does not name the settings file", err)
		}
		return err
	}
	if err := start(`{"version":1,"debugCaps":{"deepseek":null,"jev":2}}`, 0o600, nil); err != nil {
		t.Fatal(err)
	}
	if posixFileModes {
		if err := start(`{"version":1,"debugCaps":{"jev":2}}`, 0o644, nil); err == nil || !strings.Contains(err.Error(), "chmod 600") {
			t.Error("mode 644:", err)
		}
	}
	for _, content := range []string{`nope`, `{"version":1}`, `{"version":1,"debugCaps":{"jev":0}}`, `{"version":1,"debugCaps":{"jev":1001}}`,
		`{"version":1,"debugCaps":{"openai":1}}`, `{"version":1,"debugCaps":{"jev":1},"updatedAt":"2026-10-25T08:00:00Z"}`, `{"version":2,"debugCaps":{}}`} {
		if err := start(content, 0o600, nil); err == nil {
			t.Errorf("%q was accepted", content)
		}
	}
	if err := start("", 0, func(config *InspectConfig) { config.SettingsFile = filepath.Join(h.root, "settings.json") }); err == nil {
		t.Error("a settings file in the data root was taken")
	}
	if err := start("", 0, func(config *InspectConfig) { config.DebugBudgetFile, config.DebugCaps = "", nil }); err == nil {
		t.Error("a settings file without a debug budget was taken")
	}
}
