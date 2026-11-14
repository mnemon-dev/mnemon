package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// ReadJSONFile reads a JSON file into a map. Returns empty map if file doesn't exist.
// Tolerates JSON5-style // line comments and trailing commas.
func ReadJSONFile(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]interface{}), nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return make(map[string]interface{}), nil
	}
	cleaned := stripJSON5(string(data))
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(cleaned), &m); err != nil {
		return nil, err
	}
	return m, nil
}

// stripJSON5 removes // line comments and trailing commas from JSON5 input.
// Only strips comments outside of quoted strings.
func stripJSON5(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inString := false
	escaped := false
	i := 0
	for i < len(s) {
		ch := s[i]
		if escaped {
			b.WriteByte(ch)
			escaped = false
			i++
			continue
		}
		if inString {
			if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inString = false
			}
			b.WriteByte(ch)
			i++
			continue
		}
		// Outside string
		if ch == '"' {
			inString = true
			b.WriteByte(ch)
			i++
			continue
		}
		if ch == '/' && i+1 < len(s) && s[i+1] == '/' {
			// Skip to end of line
			for i < len(s) && s[i] != '\n' {
				i++
			}
			continue
		}
		// Trailing comma: ,] or ,}
		if ch == ',' {
			// Look ahead past whitespace for ] or }
			j := i + 1
			for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r') {
				j++
			}
			if j < len(s) && (s[j] == ']' || s[j] == '}') {
				i++ // skip trailing comma
				continue
			}
		}
		b.WriteByte(ch)
		i++
	}
	return b.String()
}

// WriteJSONFile writes a map to a JSON file atomically via .tmp + rename.
func WriteJSONFile(path string, data map[string]interface{}) error {
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// containsMnemon recursively checks if any string value contains "mnemon".
func containsMnemon(v interface{}) bool {
	switch val := v.(type) {
	case string:
		return strings.Contains(val, "mnemon")
	case map[string]interface{}:
		for _, v := range val {
			if containsMnemon(v) {
				return true
			}
		}
	case []interface{}:
		for _, v := range val {
			if containsMnemon(v) {
				return true
			}
		}
	}
	return false
}

// ensureHooksMap ensures data["hooks"] is a map and returns it.
func ensureHooksMap(data map[string]interface{}) map[string]interface{} {
	hooks, ok := data["hooks"].(map[string]interface{})
	if !ok {
		hooks = make(map[string]interface{})
		data["hooks"] = hooks
	}
	return hooks
}

// filterHookArray removes entries that reference mnemon from a hook event array.
func filterHookArray(arr []interface{}) []interface{} {
	filtered := make([]interface{}, 0)
	for _, entry := range arr {
		if !containsMnemon(entry) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

// RemoveClaudeHooks removes all mnemon-related entries from Claude Code hooks.
// Cleans up empty hook arrays and the hooks map itself when nothing remains.
func RemoveClaudeHooks(data map[string]interface{}) {
	hooks, ok := data["hooks"].(map[string]interface{})
	if !ok {
		return
	}
	for _, key := range []string{"UserPromptSubmit", "Stop", "SessionStart", "PreCompact"} {
		arr, ok := hooks[key].([]interface{})
		if !ok {
			continue
		}
		filtered := filterHookArray(arr)
		if len(filtered) == 0 {
			delete(hooks, key)
		} else {
			hooks[key] = filtered
		}
	}
	// Remove empty hooks map
	if len(hooks) == 0 {
		delete(data, "hooks")
	}
}

// WriteOrRemoveJSONFile writes the settings, or removes the file if the map is empty.
func WriteOrRemoveJSONFile(path string, data map[string]interface{}) error {
	if len(data) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return WriteJSONFile(path, data)
}

// removeIfEmpty removes a directory only if it exists and contains no entries.
func removeIfEmpty(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	if len(entries) == 0 {
		os.Remove(dir)
	}
}

// claudeHookCommand returns the settings.json command for a hook script. Claude
// Code runs the command through Git Bash on Windows, where bash treats the
// backslashes of a native path as escapes (C:\Users\x\prime.sh becomes
// C:Usersxprime.sh). On Windows emit forward slashes and quote the path, which
// also survives spaces in the profile directory. Other platforms are unchanged.
func claudeHookCommand(hooksDir, filename, goos string) string {
	if goos != "windows" {
		return filepath.Join(hooksDir, filename)
	}
	dir := strings.TrimRight(strings.ReplaceAll(hooksDir, `\`, "/"), "/")
	return `"` + dir + "/" + filename + `"`
}

// addClaudeHooksSelective idempotently sets mnemon hooks in Claude Code settings.
// Prime (SessionStart) is always registered; Remind and Nudge are conditional.
func addClaudeHooksSelective(data map[string]interface{}, hooksDir string, sel HookSelection, goos string) {
	RemoveClaudeHooks(data)
	hooks := ensureHooksMap(data)

	// SessionStart (prime) — always
	primeEntry := map[string]interface{}{
		"hooks": []interface{}{
			map[string]interface{}{
				"type":    "command",
				"command": claudeHookCommand(hooksDir, "prime.sh", goos),
			},
		},
	}
	sessionArr, _ := hooks["SessionStart"].([]interface{})
	hooks["SessionStart"] = append(sessionArr, primeEntry)

	// UserPromptSubmit (remind) — optional
	if sel.Remind {
		remindEntry := map[string]interface{}{
			"hooks": []interface{}{
				map[string]interface{}{
					"type":    "command",
					"command": claudeHookCommand(hooksDir, "user_prompt.sh", goos),
				},
			},
		}
		arr, _ := hooks["UserPromptSubmit"].([]interface{})
		hooks["UserPromptSubmit"] = append(arr, remindEntry)
	}

	// Stop (nudge) — optional
	if sel.Nudge {
		nudgeEntry := map[string]interface{}{
			"hooks": []interface{}{
				map[string]interface{}{
					"type":    "command",
					"command": claudeHookCommand(hooksDir, "stop.sh", goos),
				},
			},
		}
		stopArr, _ := hooks["Stop"].([]interface{})
		hooks["Stop"] = append(stopArr, nudgeEntry)
	}

	// PreCompact (compact) — optional
	if sel.Compact {
		compactEntry := map[string]interface{}{
			"hooks": []interface{}{
				map[string]interface{}{
					"type":    "command",
					"command": claudeHookCommand(hooksDir, "compact.sh", goos),
				},
			},
		}
		compactArr, _ := hooks["PreCompact"].([]interface{})
		hooks["PreCompact"] = append(compactArr, compactEntry)
	}
}
