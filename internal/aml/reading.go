package aml

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
)

// ReadStatus is the role reading /admin/status and the role reading
// endpoints answer: the reading consolidation reads by, where it comes from
// ("config", or "settings" for one the settings file sets), and the
// configuration's, which DELETE /admin/v1/read goes back to.
type ReadStatus struct {
	Policy Reading                          `json:"policy"`
	Source string                           `json:"source"`
	Config Reading                          `json:"config"`
	Units  map[string]memoryagent.ReadUnits `json:"units"`
}

// Reading is a normalized role reading as the admin routes spell it: every
// field present, roles {} when it names none, for the console.
type Reading struct {
	Default string            `json:"default"`
	Excerpt int               `json:"excerpt"`
	Roles   map[string]string `json:"roles"`
}

func readingOf(r memoryagent.RoleReading) Reading {
	roles := r.Roles
	if roles == nil {
		roles = map[string]string{}
	}
	return Reading{Default: r.Default, Excerpt: r.Excerpt, Roles: roles}
}

// readStatus is the server's ReadStatus; nil without timelines or layered
// consolidation, which reads nothing.
func (s *Server) readStatus() *ReadStatus {
	if !s.timelines {
		return nil
	}
	status := &ReadStatus{Policy: readingOf(s.configRead), Source: capFromConfig, Config: readingOf(s.configRead), Units: s.metrics.readingByRole()}
	if read := s.settingsRead.Load(); read != nil {
		status.Policy, status.Source = readingOf(*read), capFromSettings
	}
	return status
}

// setRoleReading is PUT /admin/v1/read {"default":…,"excerpt":…,"roles":{…}}:
// the role reading given, checked (memoryagent.RoleReading.Normalized), goes
// into the settings file, written atomically, and then to the consolidation
// jobs that start from then on: the records they read from then on are read
// by it, what is pending keeps how it was read. Any refusal changes nothing:
// 400 invalid naming the field, 409 without a settings file or without
// consolidation.
func (s *Server) setRoleReading(w http.ResponseWriter, r *http.Request) {
	var fields map[string]json.RawMessage
	if !decodeBodyWithin(w, r, &fields, adminBody) {
		return
	}
	if !s.canSetReading(w) {
		return
	}
	raw, _ := json.Marshal(fields)
	var given memoryagent.RoleReading
	if decodeStrict(raw, &given) != nil {
		invalid(w, "body")
		return
	}
	read, err := given.Normalized()
	if refused := (*memoryagent.RoleReadingError)(nil); errors.As(err, &refused) {
		invalid(w, refused.Field)
		return
	}
	s.changeReading(w, r, "role reading set", &read)
}

// revertRoleReading is DELETE /admin/v1/read: the role reading leaves the
// settings file, which is removed once it holds no caps either, and the
// configuration's is read by again.
func (s *Server) revertRoleReading(w http.ResponseWriter, r *http.Request) {
	if s.canSetReading(w) {
		s.changeReading(w, r, "role reading reverted to the configuration's", nil)
	}
}

// canSetReading reports whether the role reading can change, and answers 409
// when it cannot: no settings file, or no consolidation to read by it.
func (s *Server) canSetReading(w http.ResponseWriter) bool {
	switch {
	case s.config.SettingsFile == "":
		adminError(w, http.StatusConflict, "no_settings_file")
	case !s.timelines:
		adminError(w, http.StatusConflict, "no_consolidation")
	default:
		return true
	}
	return false
}

// changeReading makes the settings file's role reading read (nil: none),
// answers the ReadStatus and logs one INFO line, line and the reading read
// by from then on. A write that fails changes nothing: 500.
func (s *Server) changeReading(w http.ResponseWriter, r *http.Request, line string, read *memoryagent.RoleReading) {
	if err := s.writeSettings(r.Context(), func(_ map[string]float64, current **memoryagent.RoleReading) { *current = read }); err != nil {
		s.logf(r.Context(), slog.LevelError, "role reading not changed: %v", err)
		adminError(w, http.StatusInternalServerError, "internal")
		return
	}
	status := s.readStatus()
	policy, _ := json.Marshal(status.Policy)
	s.logf(r.Context(), slog.LevelInfo, "%s: %s", line, policy)
	inspectJSON(w, http.StatusOK, status)
}
