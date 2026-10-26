package memoryagent

import (
	"maps"
	"slices"
	"strings"
	"unicode/utf8"
)

// RoleReading says how much of each role's messages consolidation reads: the
// whole message ("full"), the first Excerpt UTF-16 units of its text
// ("excerpt"), or nothing ("ignore"). Roles maps a role, compared without
// case, to its mode; every other role takes Default. A policy only changes
// what the consolidating model reads: every message is stored and searched
// whole.
type RoleReading struct {
	Default string            `json:"default"`
	Excerpt int               `json:"excerpt,omitempty"`
	Roles   map[string]string `json:"roles,omitempty"`
}

// The modes of a RoleReading.
const (
	ReadFull    = "full"
	ReadExcerpt = "excerpt"
	ReadIgnore  = "ignore"
)

// An excerpt is 240 units unless the policy says otherwise (the reference
// system's assistant cut), and from 40 to 2000.
const (
	defaultExcerpt = 240
	minExcerpt     = 40
	maxExcerpt     = 2000
)

// defaultCutShort is what the reference prompts say of the records' text,
// which is what the default policy cuts.
const defaultCutShort = " (assistant messages are cut short)"

// DefaultRoleReading is the policy of a configuration that sets none: user
// messages whole, the first 240 units of each assistant message, every other
// role whole.
func DefaultRoleReading() RoleReading {
	return RoleReading{Default: ReadFull, Excerpt: defaultExcerpt, Roles: map[string]string{"user": ReadFull, "assistant": ReadExcerpt}}
}

// RoleReadingError names the field of a policy that is not valid: default,
// excerpt, roles (a role name that is not one), or roles.<role>.
type RoleReadingError struct{ Field string }

func (e *RoleReadingError) Error() string { return "invalid role reading: " + e.Field }

// Normalized checks p and returns it complete: Excerpt set, Roles keyed by
// the trimmed, lower-case role and never nil. A role name is 1–64 bytes of
// single-line UTF-8; two names that differ only in case are refused.
func (p RoleReading) Normalized() (RoleReading, error) {
	if !readMode(p.Default) {
		return RoleReading{}, &RoleReadingError{Field: "default"}
	}
	if p.Excerpt == 0 {
		p.Excerpt = defaultExcerpt
	}
	if p.Excerpt < minExcerpt || p.Excerpt > maxExcerpt {
		return RoleReading{}, &RoleReadingError{Field: "excerpt"}
	}
	roles := make(map[string]string, len(p.Roles))
	for _, name := range slices.Sorted(maps.Keys(p.Roles)) {
		trimmed := strings.TrimFunc(name, isJSSpace)
		if trimmed == "" || len(trimmed) > maxRoleBytes || !utf8.ValidString(trimmed) || strings.ContainsAny(trimmed, "\r\n") {
			return RoleReading{}, &RoleReadingError{Field: "roles"}
		}
		key := strings.ToLower(trimmed)
		if _, twice := roles[key]; twice || !readMode(p.Roles[name]) {
			return RoleReading{}, &RoleReadingError{Field: "roles." + key}
		}
		roles[key] = p.Roles[name]
	}
	p.Roles = roles
	return p, nil
}

func readMode(mode string) bool {
	return mode == ReadFull || mode == ReadExcerpt || mode == ReadIgnore
}

// Bucket is the name a role's messages are counted under: the role in lower
// case when it is user, assistant, system or tool or the reading names it,
// else "other", so that the names stay few whatever roles arrive.
func (p RoleReading) Bucket(role string) string {
	key := strings.ToLower(strings.TrimFunc(role, isJSSpace))
	switch key {
	case "user", "assistant", "system", "tool":
		return key
	}
	if _, named := p.Roles[key]; named {
		return key
	}
	return "other"
}

// modeOf is how the policy reads a message of role.
func (p RoleReading) modeOf(role string) string {
	if mode, ok := p.Roles[strings.ToLower(strings.TrimFunc(role, isJSSpace))]; ok {
		return mode
	}
	return p.Default
}

// cutShort is what the consolidation prompt says, after "the text", of the
// messages the policy cuts: defaultCutShort for the default policy, the
// excerpted roles by name, all but the whole ones when the default excerpts,
// and nothing when it cuts none.
func (p RoleReading) cutShort() string {
	var cut, whole []string
	for _, role := range slices.Sorted(maps.Keys(p.Roles)) {
		switch p.Roles[role] {
		case ReadExcerpt:
			cut = append(cut, role)
		case ReadFull:
			whole = append(whole, role)
		}
	}
	switch {
	case p.Default == ReadExcerpt && len(whole) == 0:
		return " (messages are cut short)"
	case p.Default == ReadExcerpt:
		return " (messages are cut short, except those of " + strings.Join(whole, ", ") + ")"
	case len(cut) > 0:
		return " (" + strings.Join(cut, ", ") + " messages are cut short)"
	}
	return ""
}

// readFold is spec with its prompt saying what policy cuts (cutShort).
func readFold(spec foldSpec, policy RoleReading) foldSpec {
	spec.system = edited(spec.system, defaultCutShort, policy.cutShort())
	return spec
}

// roleReading is the policy the agent's consolidation reads by: the run-time
// one (Config.ReadOverride) when there is one, else the configuration's.
func (a *Agent) roleReading() RoleReading {
	if a.config.ReadOverride != nil {
		if policy := a.config.ReadOverride(); policy != nil {
			return *policy
		}
	}
	return a.config.Consolidation.RoleReading()
}
