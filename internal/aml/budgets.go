package aml

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
)

// Budget caps at run time (rulings R99 and R100; docs/admin-api.md): the
// console changes an instance's caps through aml-serve's admin listener,
// which keeps them in a settings file of its state, beside the key file:
//
//	{"version":1,"budgets":{"deepseek":<cap>,"jev":<cap>},"updatedAt":"<RFC 3339>"}
//
// A cap there replaces the configuration's for its kind, and null is
// unlimited (memoryagent.Unlimited): the kind's reservations are never
// refused for a cap, while its spend is still metered. A missing file means
// the configuration's caps. A change applies to the reservations made from
// then on; one already held keeps its amount (memoryagent.Budget.SetCaps).
// The inspect service keeps its debug caps in a settings file of its own
// (inspect_caps.go). Caps are no secret: they are answered and logged.

const (
	// settingsWhat is what an error calls aml-serve's settings file.
	settingsWhat = "the settings file"
	// maxBudgetCap and maxDebugCap bound a cap set at run time, in its
	// kind's currency.
	maxBudgetCap = 100000
	maxDebugCap  = 1000
	// Where a budget's cap comes from (BudgetAccount.Source).
	capFromConfig   = "config"
	capFromSettings = "settings"
)

// budgetsFile is the format of aml-serve's settings file: budget caps, a nil
// cap unlimited, and (version 2) the role reading consolidation reads by. A
// file without a role reading stays version 1, which binaries that predate
// it read.
type budgetsFile struct {
	Version   int                      `json:"version"`
	Budgets   map[string]*float64      `json:"budgets"`
	Read      *memoryagent.RoleReading `json:"read,omitempty"`
	UpdatedAt string                   `json:"updatedAt,omitempty"`
}

// capsIn is caps as a settings file spells them: an unlimited cap is null.
func capsIn(caps map[string]float64) map[string]*float64 {
	out := map[string]*float64{}
	for kind, cap := range caps {
		if math.IsInf(cap, 1) {
			out[kind] = nil
			continue
		}
		out[kind] = &cap
	}
	return out
}

// fileCaps checks the caps of the settings file at path (what says which)
// and returns them, null as memoryagent.Unlimited: kinds deepseek and jev
// that uses admits, each cap above 0 and at most most, or null. Its errors
// name the file and the kind, and nothing else the file holds.
func fileCaps(what, path string, caps map[string]*float64, uses func(string) bool, most float64) (map[string]float64, error) {
	out := map[string]float64{}
	for _, kind := range slices.Sorted(maps.Keys(caps)) {
		switch cap := caps[kind]; {
		case !slices.Contains(memoryagent.KeyKinds(), kind):
			return nil, fmt.Errorf("%s %s names %s: its kinds are deepseek and jev", what, path, memoryagent.KindName(kind))
		case !uses(kind):
			return nil, fmt.Errorf("%s %s has a %s cap, but the configuration has no %s model", what, path, kind, kind)
		case cap == nil:
			out[kind] = memoryagent.Unlimited
		case !(*cap > 0) || *cap > most:
			return nil, fmt.Errorf("%s %s has a %s cap that is neither above 0 and at most %g nor null (unlimited)", what, path, kind, most)
		default:
			out[kind] = *cap
		}
	}
	return out, nil
}

// readSettingsFile reads aml-serve's settings file at path: its caps by kind
// and its role reading (normalized), none of either when it does not exist.
// It refuses what readPrivateFile refuses, and a file that is not the format:
// another version or field, version 1 without budgets or with a role reading,
// version 2 without one, an updatedAt that is not an RFC 3339 time, a cap
// fileCaps refuses (uses says which kinds the configuration has models of),
// or a role reading that is not one.
func readSettingsFile(path string, uses func(string) bool) (map[string]float64, *memoryagent.RoleReading, error) {
	raw, exists, err := readPrivateFile(settingsWhat, path)
	if err != nil || !exists {
		return map[string]float64{}, nil, err
	}
	var file budgetsFile
	if decodeStrict(raw, &file) != nil || file.Version == 1 && (file.Budgets == nil || file.Read != nil) || file.Version == 2 && file.Read == nil || file.Version != 1 && file.Version != 2 {
		return nil, nil, fmt.Errorf(`%s %s is malformed: it must be {"version":1,"budgets":{"deepseek":<cap or null>,"jev":<cap or null>},"updatedAt":"<RFC 3339>"}, or version 2 with a role reading in "read"`, settingsWhat, path)
	}
	if file.UpdatedAt != "" {
		if _, err := time.Parse(time.RFC3339, file.UpdatedAt); err != nil {
			return nil, nil, fmt.Errorf("%s %s is malformed: its updatedAt is not an RFC 3339 time", settingsWhat, path)
		}
	}
	if file.Read != nil {
		read, err := file.Read.Normalized()
		if err != nil {
			return nil, nil, fmt.Errorf("%s %s is malformed: its read: %v", settingsWhat, path, err)
		}
		file.Read = &read
	}
	caps := map[string]float64{}
	if file.Budgets != nil {
		if caps, err = fileCaps(settingsWhat, path, file.Budgets, uses, maxBudgetCap); err != nil {
			return nil, nil, err
		}
	}
	return caps, file.Read, nil
}

// capText is a cap as a log line says it: its amount, or unlimited.
func capText(cap float64) string {
	if math.IsInf(cap, 1) {
		return "unlimited"
	}
	return strconv.FormatFloat(cap, 'f', -1, 64)
}

// capChanges is a log line's list of how each kind's cap changed, from
// before to after, in its currency: "deepseek 600→800 CNY, jev 60→unlimited
// USD".
func capChanges(kinds []string, before, after map[string]float64) string {
	parts := []string{}
	for _, kind := range kinds {
		parts = append(parts, fmt.Sprintf("%s %s→%s %s", kind, capText(before[kind]), capText(after[kind]), paidKinds[kind]))
	}
	return strings.Join(parts, ", ")
}

// parseCaps reads a request's caps by kind: deepseek and jev, each a JSON
// number above 0 and at most most, or null for unlimited. On failure it has
// answered 400 invalid, naming the field.
func parseCaps(w http.ResponseWriter, fields map[string]json.RawMessage, most float64) (map[string]float64, bool) {
	caps := map[string]float64{}
	for _, name := range slices.Sorted(maps.Keys(fields)) {
		if !slices.Contains(memoryagent.KeyKinds(), name) {
			invalid(w, fieldName(name))
			return nil, false
		}
		if strings.TrimSpace(string(fields[name])) == "null" {
			caps[name] = memoryagent.Unlimited
			continue
		}
		var cap float64
		if json.Unmarshal(fields[name], &cap) != nil || !(cap > 0) || cap > most {
			invalid(w, name)
			return nil, false
		}
		caps[name] = cap
	}
	if len(caps) == 0 {
		invalid(w, "body")
		return nil, false
	}
	return caps, true
}

// capped reports whether the instance's caps of kind can change: the
// configuration has a model of the kind, and so a budget.
func (s *Server) capped(kind string) bool { return s.budget != nil && s.keys.Uses(kind) }

// cappedKinds are the kinds whose caps can change, in order.
func (s *Server) cappedKinds() []string {
	kinds := []string{}
	for _, kind := range memoryagent.KeyKinds() {
		if s.capped(kind) {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

// capSource is where kind's cap comes from: the settings file or the
// configuration.
func (s *Server) capSource(kind string) string {
	if _, ok := (*s.settingsCaps.Load())[kind]; ok {
		return capFromSettings
	}
	return capFromConfig
}

// budgetEntry is one kind of the budget routes' answer: its cap (null when
// unlimited), its currency, what it spent and holds, where its cap comes
// from, whether the cap is below what is spent, and whether it is unlimited.
type budgetEntry struct {
	Cap       *float64 `json:"cap"`
	Currency  string   `json:"currency"`
	Spent     float64  `json:"spent"`
	Held      float64  `json:"held"`
	Source    string   `json:"source"`
	Over      bool     `json:"over"`
	Unlimited bool     `json:"unlimited"`
}

// budgetsAnswer is what the budget routes answer: every kind whose cap can
// change.
type budgetsAnswer struct {
	Budgets map[string]budgetEntry `json:"budgets"`
}

// budgets is the budget routes' answer, of accounts read just before.
func (s *Server) budgets(accounts []memoryagent.BudgetAccount) budgetsAnswer {
	answer := budgetsAnswer{Budgets: map[string]budgetEntry{}}
	if s.budget == nil {
		return answer
	}
	caps, used := s.budget.Caps(), map[string]memoryagent.BudgetAccount{}
	for _, account := range accounts {
		used[account.Kind] = account
	}
	for _, kind := range s.cappedKinds() {
		cap, account := caps[kind], used[kind]
		entry := budgetEntry{Currency: paidKinds[kind], Spent: account.Spent, Held: account.Held, Source: s.capSource(kind), Unlimited: math.IsInf(cap, 1)}
		if !entry.Unlimited {
			entry.Cap, entry.Over = &cap, account.Spent > cap
		}
		answer.Budgets[kind] = entry
	}
	return answer
}

// setBudgetCaps is PUT /admin/v1/budgets {"deepseek":<cap>,"jev":<cap>}: the
// caps given, null for unlimited, go into the settings file, written
// atomically, and then into the budget, for the reservations made from then
// on. A cap below what is spent is taken, and answered "over". Any refusal
// changes nothing.
func (s *Server) setBudgetCaps(w http.ResponseWriter, r *http.Request) {
	var fields map[string]json.RawMessage
	if !decodeBodyWithin(w, r, &fields, adminBody) {
		return
	}
	if s.config.SettingsFile == "" {
		adminError(w, http.StatusConflict, "no_settings_file")
		return
	}
	given, ok := parseCaps(w, fields, maxBudgetCap)
	if !ok {
		return
	}
	kinds := []string{}
	for _, kind := range memoryagent.KeyKinds() {
		if _, ok := given[kind]; !ok {
			continue
		}
		if !s.capped(kind) {
			kindError(w, http.StatusConflict, "unknown_kind", kind)
			return
		}
		kinds = append(kinds, kind)
	}
	s.changeCaps(w, r, kinds, "budget caps set", func(file map[string]float64) { maps.Copy(file, given) })
}

// revertBudgetCaps is DELETE /admin/v1/budgets?kind=deepseek|jev|all: those
// kinds leave the settings file, which is removed once it holds none, and go
// back to the configuration's caps.
func (s *Server) revertBudgetCaps(w http.ResponseWriter, r *http.Request) {
	kinds, ok := kindQuery(w, r, s.cappedKinds())
	if !ok {
		return
	}
	if s.config.SettingsFile == "" {
		adminError(w, http.StatusConflict, "no_settings_file")
		return
	}
	for _, kind := range kinds {
		if !s.capped(kind) {
			kindError(w, http.StatusConflict, "unknown_kind", kind)
			return
		}
	}
	s.changeCaps(w, r, kinds, "budget caps reverted to the configuration's", func(file map[string]float64) {
		for _, kind := range kinds {
			delete(file, kind)
		}
	})
}

// changeCaps makes one change of the settings file's caps (writeCaps) and
// answers the budgets, with what the accounts spent and held just before,
// and logs one INFO line, line and how each of kinds changed. A budget file
// that does not answer, or a write that fails, changes nothing: 500.
func (s *Server) changeCaps(w http.ResponseWriter, r *http.Request, kinds []string, line string, change func(file map[string]float64)) {
	if s.budget == nil {
		inspectJSON(w, http.StatusOK, s.budgets(nil))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), adminReadWait)
	accounts, err := s.Accounts(ctx)
	cancel()
	if err != nil {
		s.logf(r.Context(), slog.LevelWarn, "budget caps not changed: the budget file does not answer: %v", err)
		adminError(w, http.StatusInternalServerError, "internal")
		return
	}
	before := s.budget.Caps()
	if err := s.writeCaps(r.Context(), change); err != nil {
		s.logf(r.Context(), slog.LevelError, "budget caps not changed: %v", err)
		adminError(w, http.StatusInternalServerError, "internal")
		return
	}
	s.logf(r.Context(), slog.LevelInfo, "%s: %s", line, capChanges(kinds, before, s.budget.Caps()))
	inspectJSON(w, http.StatusOK, s.budgets(accounts))
}

// writeCaps is one change of the settings file's caps: change is made to a
// copy of them, which is written atomically (the file is removed once it
// holds none), then set into the budget, the configuration's caps for the
// kinds it no longer holds. One change runs at a time (capsWriter, which no
// reservation waits for), waiting for the one before it until ctx ends. A
// failed write changes nothing; the caps were checked, so the swap cannot
// fail.
func (s *Server) writeCaps(ctx context.Context, change func(file map[string]float64)) error {
	return s.writeSettings(ctx, func(caps map[string]float64, _ **memoryagent.RoleReading) { change(caps) })
}

// writeSettings is one change of the settings file: change is made to a copy
// of its caps and to its role reading, which are written atomically together
// (the file is removed once it holds neither, and is version 2 only while it
// holds a role reading), then set: the caps into the budget, the
// configuration's for the kinds it no longer holds, and the role reading for
// the consolidation jobs that start from then on. One change runs at a time
// (capsWriter), waiting for the one before it until ctx ends. A failed write
// changes nothing; what is written was checked, so the swap cannot fail.
func (s *Server) writeSettings(ctx context.Context, change func(caps map[string]float64, read **memoryagent.RoleReading)) error {
	select {
	case s.capsWriter <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-s.capsWriter }()
	file, read := maps.Clone(*s.settingsCaps.Load()), s.settingsRead.Load()
	change(file, &read)
	var err error
	if len(file) == 0 && read == nil {
		err = removePrivateFile(s.config.SettingsFile)
	} else {
		version := 1
		if read != nil {
			version = 2
		}
		raw, _ := json.Marshal(budgetsFile{Version: version, Budgets: capsIn(file), Read: read, UpdatedAt: time.Now().UTC().Format(time.RFC3339)})
		err = writePrivateFile(s.config.SettingsFile, raw)
	}
	if err != nil {
		return fmt.Errorf("writing %s %s: %w", settingsWhat, s.config.SettingsFile, err)
	}
	if s.budget != nil {
		caps := map[string]float64{}
		for _, kind := range s.cappedKinds() {
			caps[kind] = s.configCaps[kind]
			if cap, ok := file[kind]; ok {
				caps[kind] = cap
			}
		}
		if err := s.budget.SetCaps(caps); err != nil {
			return err
		}
	}
	s.settingsCaps.Store(&file)
	s.settingsRead.Store(read)
	return nil
}
