package aml

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"net/http"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
)

// The inspect service's debug caps at run time (rulings R99 and R100;
// docs/inspect-api.md): PUT and DELETE /v1/debug-caps change the caps its
// full searches spend within, kept in a settings file of its own state, never
// the instance's:
//
//	{"version":1,"debugCaps":{"deepseek":<cap>,"jev":<cap>}}
//
// A cap there replaces the flag's (--debug-caps) for its kind, and null is
// unlimited (memoryagent.Unlimited). The service writes only that file and
// its debug budget, both in its own state.

const debugSettingsWhat = "the inspect settings file"

// debugCapsFile is the format of the inspect service's settings file; a nil
// cap is unlimited.
type debugCapsFile struct {
	Version   int                 `json:"version"`
	DebugCaps map[string]*float64 `json:"debugCaps"`
}

// anyKind admits every key kind: the debug caps of a kind the read does not
// use are allowed, as the flag's are.
func anyKind(string) bool { return true }

// readDebugCapsFile reads the inspect service's settings file at path: its
// caps by kind, none when it does not exist; what readPrivateFile refuses,
// and a file that is not the format (another version or field, no debugCaps,
// a kind other than deepseek and jev, a cap neither above 0 and at most 1000
// nor null), is an error naming the file.
func readDebugCapsFile(path string) (map[string]float64, error) {
	raw, exists, err := readPrivateFile(debugSettingsWhat, path)
	if err != nil || !exists {
		return map[string]float64{}, err
	}
	var file debugCapsFile
	if decodeStrict(raw, &file) != nil || file.Version != 1 || file.DebugCaps == nil {
		return nil, fmt.Errorf(`%s %s is malformed: it must be {"version":1,"debugCaps":{"deepseek":<cap or null>,"jev":<cap or null>}}`, debugSettingsWhat, path)
	}
	return fileCaps(debugSettingsWhat, path, file.DebugCaps, anyKind, maxDebugCap)
}

// debugCaps are the debug budget's caps now, by paid kind (0 for none,
// memoryagent.Unlimited for unlimited).
func (i *Inspector) debugCaps() map[string]float64 { return *i.capsNow.Load() }

// fullCaps reports whether caps let full searches run: at least one is
// positive, and so is that of every paid kind the read calls (kinds). An
// unlimited cap is positive.
func fullCaps(caps map[string]float64, kinds []string) bool {
	positive := false
	for _, cap := range caps {
		positive = positive || cap > 0
	}
	for _, kind := range kinds {
		positive = positive && caps[kind] > 0
	}
	return positive
}

// fullOn reports whether full searches may run now: their models were built
// at the start (full) and the caps of the moment let them.
func (i *Inspector) fullOn() bool { return i.full && fullCaps(i.debugCaps(), i.readKinds()) }

// finiteCaps is caps with every unlimited cap 0: what a budget file is
// opened with, the unlimited ones set after (Budget.SetCaps).
func finiteCaps(caps map[string]float64) map[string]float64 {
	out := maps.Clone(caps)
	for kind, cap := range out {
		if math.IsInf(cap, 1) {
			out[kind] = 0
		}
	}
	return out
}

// capOrNull is a cap or an amount left of it as JSON: null when unlimited.
func capOrNull(v float64) *float64 {
	if math.IsInf(v, 1) {
		return nil
	}
	return &v
}

// capsByKind is a cap, or what is left of one, for each kind the console
// shows: null when unlimited.
type capsByKind struct {
	DeepSeek *float64 `json:"deepseek"`
	JEV      *float64 `json:"jev"`
}

// debugBudget is /v1/info's debugBudget, and what the debug caps routes
// answer.
type debugBudget struct {
	Currency  map[string]string `json:"currency"`
	Caps      capsByKind        `json:"caps"`
	Spent     usageByKind       `json:"spent"`
	Held      usageByKind       `json:"held"`
	Remaining capsByKind        `json:"remaining"`
}

// debugBudgetOf is the debug budget, of the amounts spent and held read just
// before, with the caps of the moment.
func (i *Inspector) debugBudgetOf(spent, held map[string]float64) debugBudget {
	caps := i.debugCaps()
	return debugBudget{
		Currency:  map[string]string{"deepseek": paidKinds["deepseek"], "jev": paidKinds["jev"]},
		Caps:      capsByKind{capOrNull(caps["deepseek"]), capOrNull(caps["jev"])},
		Spent:     usageByKind{spent["deepseek"], spent["jev"]},
		Held:      usageByKind{held["deepseek"], held["jev"]},
		Remaining: capsByKind{capOrNull(i.remaining("deepseek", spent, held)), capOrNull(i.remaining("jev", spent, held))},
	}
}

// serveDebugCaps is /v1/debug-caps: PUT sets caps, DELETE reverts them;
// another method answers 405.
func (i *Inspector) serveDebugCaps(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPut:
		i.setDebugCaps(w, r)
	case http.MethodDelete:
		i.revertDebugCaps(w, r)
	default:
		w.Header().Set("Allow", "PUT, DELETE")
		inspectError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

// capsFixed is why the debug caps cannot change at run time, "" when they
// can: no settings file to keep them (no_settings_file), or no full searches
// at this start (full_disabled), whose models are then not built.
func (i *Inspector) capsFixed() string {
	switch {
	case i.config.SettingsFile == "":
		return "no_settings_file"
	case !i.full:
		return "full_disabled"
	}
	return ""
}

// setDebugCaps is PUT /v1/debug-caps {"deepseek":<cap>,"jev":<cap>}: each a
// number above 0 and at most 1000, or null for unlimited.
func (i *Inspector) setDebugCaps(w http.ResponseWriter, r *http.Request) {
	var fields map[string]json.RawMessage
	if !decodeBody(w, r, &fields) {
		return
	}
	if code := i.capsFixed(); code != "" {
		inspectError(w, http.StatusConflict, code)
		return
	}
	given, ok := parseCaps(w, fields, maxDebugCap)
	if !ok {
		return
	}
	kinds := []string{}
	for _, kind := range memoryagent.KeyKinds() {
		if _, ok := given[kind]; ok {
			kinds = append(kinds, kind)
		}
	}
	i.changeDebugCaps(w, r, kinds, "debug caps set", func(file map[string]float64) { maps.Copy(file, given) })
}

// revertDebugCaps is DELETE /v1/debug-caps?kind=deepseek|jev|all: back to
// the flag's caps.
func (i *Inspector) revertDebugCaps(w http.ResponseWriter, r *http.Request) {
	kinds, ok := kindQuery(w, r, memoryagent.KeyKinds())
	if !ok {
		return
	}
	if code := i.capsFixed(); code != "" {
		inspectError(w, http.StatusConflict, code)
		return
	}
	i.changeDebugCaps(w, r, kinds, "debug caps reverted to the flag's", func(file map[string]float64) {
		for _, kind := range kinds {
			delete(file, kind)
		}
	})
}

// changeDebugCaps makes one change of the settings file's caps
// (writeDebugCaps), logs one INFO line, line and how each of kinds changed,
// and answers the debug budget, with what it spent and held just before. A
// debug budget that does not answer, or a write that fails, changes nothing:
// 500.
func (i *Inspector) changeDebugCaps(w http.ResponseWriter, r *http.Request, kinds []string, line string, change func(file map[string]float64)) {
	spent, held, err := i.debugAccounts(r.Context())
	if err != nil {
		i.log.Warn(fmt.Sprintf("debug caps not changed: the debug budget does not answer: %v", err))
		inspectError(w, http.StatusInternalServerError, "internal")
		return
	}
	before := i.debugCaps()
	if err := i.writeDebugCaps(r.Context(), change); err != nil {
		i.log.Error(fmt.Sprintf("debug caps not changed: %v", err))
		inspectError(w, http.StatusInternalServerError, "internal")
		return
	}
	i.log.Info(fmt.Sprintf("%s: %s", line, capChanges(kinds, before, i.debugCaps())))
	inspectJSON(w, http.StatusOK, i.debugBudgetOf(spent, held))
}

// writeDebugCaps is one change of the settings file's caps: change is made
// to a copy of them, written atomically (the file is removed once it holds
// none), then set into the debug budget over the flag's caps. One change
// runs at a time (capsWriter), until ctx ends; a failed write changes
// nothing.
func (i *Inspector) writeDebugCaps(ctx context.Context, change func(file map[string]float64)) error {
	select {
	case i.capsWriter <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-i.capsWriter }()
	file := maps.Clone(*i.fileCaps.Load())
	change(file)
	var err error
	if len(file) == 0 {
		err = removePrivateFile(i.config.SettingsFile)
	} else {
		raw, _ := json.Marshal(debugCapsFile{Version: 1, DebugCaps: capsIn(file)})
		err = writePrivateFile(i.config.SettingsFile, raw)
	}
	if err != nil {
		return fmt.Errorf("writing %s %s: %w", debugSettingsWhat, i.config.SettingsFile, err)
	}
	caps := maps.Clone(i.flagCaps)
	maps.Copy(caps, file)
	if err := i.budget.SetCaps(caps); err != nil {
		return err
	}
	i.capsNow.Store(&caps)
	i.fileCaps.Store(&file)
	return nil
}
