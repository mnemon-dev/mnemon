package aml

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
)

// An account's own provider keys (BYOK, ruling R98; docs/admin-api.md): the
// console sets them through aml-serve's admin listener, which checks each
// with its provider, writes them to the instance's key file and swaps them
// in, for the model calls that start from then on (memoryagent's
// ProviderKeys). The file is the account's keys over the host's, by kind:
//
//	{"version":1,"keys":{"deepseek":"<key>","jev":"<key>"},"updatedAt":"<RFC 3339>"}
//
// It lives beside the data root, never in it, so a snapshot of the data root
// never holds a key; aml-inspect reads it too. A key never reaches a log, an
// error, a metric or an answer: only its fingerprint
// (memoryagent.Fingerprint).

const (
	// keyFileWhat is what an error calls the key file.
	keyFileWhat = "the provider key file"
	// adminBody bounds an admin request's body: 4 KiB.
	adminBody = 4 << 10
)

// keyFile is the key file's format.
type keyFile struct {
	Version   int               `json:"version"`
	Keys      map[string]string `json:"keys"`
	UpdatedAt string            `json:"updatedAt,omitempty"`
}

// readKeyFile reads the key file at path for the kinds keys' configuration
// uses: the account's keys by kind, none when the file does not exist. It
// refuses what readPrivateFile refuses, and a file that is not the format:
// another version or field, no keys, a kind that is not deepseek or jev or
// that the configuration has no model of, a key that is not one
// (memoryagent.ValidKey), or an updatedAt that is not an RFC 3339 time. Its
// errors name the file and the rule, never what it holds.
func readKeyFile(path string, keys *memoryagent.ProviderKeys) (map[string]string, error) {
	raw, exists, err := readPrivateFile(keyFileWhat, path)
	if err != nil || !exists {
		return nil, err
	}
	var file keyFile
	if decodeStrict(raw, &file) != nil || file.Version != 1 || file.Keys == nil {
		return nil, fmt.Errorf(`%s %s is malformed: it must be {"version":1,"keys":{"deepseek":"<key>","jev":"<key>"},"updatedAt":"<RFC 3339>"}`, keyFileWhat, path)
	}
	if file.UpdatedAt != "" {
		if _, err := time.Parse(time.RFC3339, file.UpdatedAt); err != nil {
			return nil, fmt.Errorf("%s %s is malformed: its updatedAt is not an RFC 3339 time", keyFileWhat, path)
		}
	}
	for _, kind := range slices.Sorted(maps.Keys(file.Keys)) {
		switch {
		case !slices.Contains(memoryagent.KeyKinds(), kind):
			return nil, fmt.Errorf("%s %s names %s: its kinds are deepseek and jev", keyFileWhat, path, memoryagent.KindName(kind))
		case !keys.Uses(kind):
			return nil, fmt.Errorf("%s %s holds a %s key, but the configuration has no %s model", keyFileWhat, path, kind, kind)
		case !memoryagent.ValidKey(file.Keys[kind]):
			return nil, fmt.Errorf("%s %s holds a %s key that is not 8 to 512 printable ASCII characters without spaces", keyFileWhat, path, kind)
		}
	}
	return file.Keys, nil
}

// loadKeyFile is the account's keys from the key file at path at a start,
// set into keys (readKeyFile). Without a path there are none.
func loadKeyFile(path string, keys *memoryagent.ProviderKeys) error {
	if path == "" {
		return nil
	}
	account, err := readKeyFile(path, keys)
	if err != nil {
		return err
	}
	return keys.SetAccount(account)
}

// keysAnswer is what the key routes answer: every configured kind's key
// status, and for a replacement how each key given was checked.
type keysAnswer struct {
	ProviderKeys map[string]memoryagent.KeyStatus `json:"providerKeys"`
	Checked      map[string]string                `json:"checked,omitempty"`
}

// adminError writes {"error":code} with the admin routes' headers.
func adminError(w http.ResponseWriter, status int, code string) { inspectError(w, status, code) }

// kindError writes {"error":code,"kind":kind}.
func kindError(w http.ResponseWriter, status int, code, kind string) {
	inspectJSON(w, status, map[string]string{"error": code, "kind": kind})
}

// fieldNameShape is what a field's name may look like to be named in an
// error: anything else may be a key in the wrong place, and is the body's.
var fieldNameShape = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,31}$`)

// fieldName is how a 400 names a field of a request's body.
func fieldName(name string) string {
	if fieldNameShape.MatchString(name) {
		return name
	}
	return "body"
}

// kindQuery reads a revert's ?kind=: deepseek, jev, or all (every kind of
// all). A missing, empty, unknown or repeated kind, or a query string that
// does not parse, answers 400 invalid kind.
func kindQuery(w http.ResponseWriter, r *http.Request, all []string) ([]string, bool) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if values := query["kind"]; err == nil && len(values) == 1 {
		switch kind := values[0]; kind {
		case "all":
			return all, true
		case "deepseek", "jev":
			return []string{kind}, true
		}
	}
	invalid(w, "kind")
	return nil, false
}

// replaceProviderKeys is PUT /admin/v1/provider-keys {"deepseek":…,"jev":…}:
// each key given is checked with its provider (memoryagent.Models.CheckKey),
// within keyCheckTimeout; only when every one passed are they merged into
// the key file, over the other kind's account key if it has one, the file
// written atomically, and the keys swapped in. Any refusal changes nothing.
func (s *Server) replaceProviderKeys(w http.ResponseWriter, r *http.Request) {
	var fields map[string]json.RawMessage
	if !decodeBodyWithin(w, r, &fields, adminBody) {
		return
	}
	if s.config.ProviderKeysFile == "" {
		adminError(w, http.StatusConflict, "no_key_file")
		return
	}
	given := map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(fields)) {
		if !slices.Contains(memoryagent.KeyKinds(), name) {
			invalid(w, fieldName(name))
			return
		}
		var key string
		if json.Unmarshal(fields[name], &key) != nil || !memoryagent.ValidKey(key) {
			invalid(w, name)
			return
		}
		given[name] = key
	}
	if len(given) == 0 {
		invalid(w, "body")
		return
	}
	kinds := []string{}
	for _, kind := range memoryagent.KeyKinds() {
		if _, ok := given[kind]; !ok {
			continue
		}
		if !s.keys.Uses(kind) {
			kindError(w, http.StatusConflict, "unknown_kind", kind)
			return
		}
		kinds = append(kinds, kind)
	}
	checked := map[string]string{}
	for _, kind := range kinds {
		ctx, cancel := context.WithTimeout(r.Context(), s.keyCheckTimeout)
		check, err := s.models.CheckKey(ctx, kind, given[kind])
		cancel()
		switch {
		case errors.Is(err, memoryagent.ErrKeyRejected):
			s.logf(r.Context(), slog.LevelWarn, "provider keys not replaced: the %s key was rejected (HTTP %d)", kind, check.Status)
			inspectJSON(w, http.StatusBadRequest, map[string]any{"error": "key_rejected", "kind": kind, "status": check.Status})
			return
		case err != nil:
			s.logf(r.Context(), slog.LevelWarn, "provider keys not replaced: the %s key: %v", kind, err)
			kindError(w, http.StatusBadGateway, "key_check_unavailable", kind)
			return
		}
		checked[kind] = check.Via
	}
	if err := s.writeKeys(r.Context(), func(account map[string]string) { maps.Copy(account, given) }); err != nil {
		s.logf(r.Context(), slog.LevelError, "provider keys not replaced: %v", err)
		adminError(w, http.StatusInternalServerError, "internal")
		return
	}
	named := []string{}
	for _, kind := range kinds {
		named = append(named, kind+" "+memoryagent.Fingerprint(given[kind]))
	}
	s.logf(r.Context(), slog.LevelInfo, "provider keys replaced: %s", strings.Join(named, ", "))
	inspectJSON(w, http.StatusOK, keysAnswer{ProviderKeys: s.keys.Status(), Checked: checked})
}

// revertProviderKeys is DELETE /admin/v1/provider-keys?kind=deepseek|jev|all:
// the account's keys of those kinds leave the key file, which is removed
// once it holds none, and the kinds' calls send the host's keys at once.
func (s *Server) revertProviderKeys(w http.ResponseWriter, r *http.Request) {
	kinds, ok := kindQuery(w, r, s.keys.Kinds())
	if !ok {
		return
	}
	if s.config.ProviderKeysFile == "" {
		adminError(w, http.StatusConflict, "no_key_file")
		return
	}
	for _, kind := range kinds {
		if !s.keys.Uses(kind) {
			kindError(w, http.StatusConflict, "unknown_kind", kind)
			return
		}
	}
	err := s.writeKeys(r.Context(), func(account map[string]string) {
		for _, kind := range kinds {
			delete(account, kind)
		}
	})
	if err != nil {
		s.logf(r.Context(), slog.LevelError, "provider keys not reverted: %v", err)
		adminError(w, http.StatusInternalServerError, "internal")
		return
	}
	s.logf(r.Context(), slog.LevelInfo, "provider keys reverted to the host's: %s", strings.Join(kinds, ", "))
	inspectJSON(w, http.StatusOK, keysAnswer{ProviderKeys: s.keys.Status()})
}

// writeKeys is one change of the account's keys: change is made to a copy of
// them, which is written to the key file atomically (the file is removed
// when no key is left) and then swapped in. One change runs at a time, for
// as long as it writes and swaps (keysWriter, which a call never waits for):
// a change waits for the one before it, until ctx ends. A failed write
// changes nothing; the keys given were checked, so the swap cannot fail.
func (s *Server) writeKeys(ctx context.Context, change func(account map[string]string)) error {
	select {
	case s.keysWriter <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-s.keysWriter }()
	account := s.keys.Account()
	change(account)
	var err error
	if len(account) == 0 {
		err = removePrivateFile(s.config.ProviderKeysFile)
	} else {
		raw, _ := json.Marshal(keyFile{Version: 1, Keys: account, UpdatedAt: time.Now().UTC().Format(time.RFC3339)})
		err = writePrivateFile(s.config.ProviderKeysFile, raw)
	}
	if err != nil {
		return fmt.Errorf("writing %s %s: %w", keyFileWhat, s.config.ProviderKeysFile, err)
	}
	return s.keys.SetAccount(account)
}

// reloadKeys reads the instance's key file again before a full search (the
// file is aml-serve's; the inspect service never writes it), with the rules
// of the start. One it cannot read keeps the keys read before, and is logged
// once until a read succeeds again.
func (i *Inspector) reloadKeys() {
	if i.config.ProviderKeysFile == "" {
		return
	}
	if err := loadKeyFile(i.config.ProviderKeysFile, i.keys); err != nil {
		if !i.keysFailing.Swap(true) {
			i.log.Warn(fmt.Sprintf("%v; full searches keep the provider keys read before until it can be read", err))
		}
		return
	}
	i.keysFailing.Store(false)
}
