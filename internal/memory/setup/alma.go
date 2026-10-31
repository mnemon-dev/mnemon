package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mnemon-dev/mnemon/internal/memory/setup/assets"
)

// AlmaConfigDir returns Alma's config directory. Alma only reads hook
// configuration from the user-global directory.
func AlmaConfigDir(global bool) (string, error) {
	if !global {
		return "", fmt.Errorf("Alma uses user-global configuration only; rerun with --global")
	}
	return filepath.Join(HomeDir(), ".config", "alma"), nil
}

// AlmaWriteSkill writes the mnemon skill to Alma's skills directory.
func AlmaWriteSkill(configDir string) (string, error) {
	skillDir := filepath.Join(configDir, "skills", "mnemon")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return "", err
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, assets.AlmaSkill, 0644); err != nil {
		return "", err
	}
	return skillPath, nil
}

// AlmaWriteHook writes the mnemon app.ready hook script.
func AlmaWriteHook(configDir, filename string, content []byte) (string, error) {
	hooksDir := filepath.Join(configDir, "mnemon-bridge")
	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		return "", err
	}
	hookPath := filepath.Join(hooksDir, filename)
	if err := writeExecutableFile(hookPath, content); err != nil {
		return "", err
	}
	return hookPath, nil
}

// AlmaWriteShim installs the `mem` shim that the skill and the hook both call.
// Without it the generated SKILL.md would document a binary that does not exist
// on a fresh machine.
func AlmaWriteShim(configDir string) (string, error) {
	bridgeDir := filepath.Join(configDir, "mnemon-bridge")
	if err := os.MkdirAll(bridgeDir, 0700); err != nil {
		return "", err
	}
	shimPath := filepath.Join(bridgeDir, "mem")
	if err := writeExecutableFile(shimPath, assets.AlmaMemShim); err != nil {
		return "", err
	}
	return shimPath, nil
}

// AlmaRegisterHooks registers the mnemon app.ready hook in hooks.json.
// Alma has no other event where mnemon can do useful work: hook stdout is only
// parsed for decision/reason/updatedInput, so reminder-style hooks would be
// silent no-ops.
func AlmaRegisterHooks(configDir string, sel HookSelection) (string, error) {
	hooksDir := filepath.Join(configDir, "mnemon-bridge")
	absHooksDir, err := filepath.Abs(hooksDir)
	if err != nil {
		return "", err
	}
	hooksPath := filepath.Join(configDir, "hooks.json")
	data, err := ReadJSONFile(hooksPath)
	if err != nil {
		return "", err
	}
	addAlmaHooks(data, absHooksDir)
	if err := WriteJSONFile(hooksPath, data); err != nil {
		return "", err
	}
	return hooksPath, nil
}

// AlmaEject removes the mnemon skill, hook script, and hooks.json entries.
func AlmaEject(configDir string) []error {
	var errs []error

	fmt.Printf("\nRemoving Alma integration (%s)...\n", configDir)

	hooksDir := filepath.Join(configDir, "mnemon-bridge")
	for _, name := range []string{"prime.sh", "mem", "alma-hook.log"} {
		path := filepath.Join(hooksDir, name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			StatusError(1, 3, "Hooks", err)
			errs = append(errs, err)
			break
		}
	}
	if len(errs) == 0 {
		StatusOK(1, 3, "Hooks", hooksDir+" removed")
	}
	os.RemoveAll(filepath.Join(hooksDir, ".prime.lock"))
	// Keep backups and drafts: they are user data, not installation artifacts.
	removeIfEmpty(hooksDir)

	hooksPath := filepath.Join(configDir, "hooks.json")
	data, err := ReadJSONFile(hooksPath)
	if err != nil {
		StatusError(2, 3, "Hooks config", err)
		errs = append(errs, err)
	} else {
		removeAlmaHooks(data)
		if _, hasHooks := data["hooks"]; !hasHooks {
			if _, hasVersion := data["version"]; hasVersion && len(data) == 1 {
				delete(data, "version")
			}
		}
		if err := WriteOrRemoveJSONFile(hooksPath, data); err != nil {
			StatusError(2, 3, "Hooks config", err)
			errs = append(errs, err)
		} else {
			StatusOK(2, 3, "Hooks config", hooksPath+" cleaned")
		}
	}

	skillDir := filepath.Join(configDir, "skills", "mnemon")
	if err := os.RemoveAll(skillDir); err != nil {
		StatusError(3, 3, "Skill", err)
		errs = append(errs, err)
	} else {
		StatusOK(3, 3, "Skill", skillDir+" removed")
	}
	// Strip the mnemon block from MEMORY.md. Leaving a stale hot index behind
	// after eject would keep injecting mnemon content into Alma forever, with
	// nothing left running to refresh it.
	if err := AlmaStripMemoryBlock(configDir); err != nil {
		StatusError(4, 4, "Hot index", err)
		errs = append(errs, err)
	} else {
		StatusOK(4, 4, "Hot index", "MEMORY.md mnemon block removed")
	}

	removeIfEmpty(filepath.Join(configDir, "skills"))
	removeIfEmpty(configDir)

	return errs
}

const (
	almaBeginMark = "<!-- mnemon:begin -->"
	almaEndMark   = "<!-- mnemon:end -->"
)

// AlmaStripMemoryBlock removes the marker-delimited mnemon block from
// MEMORY.md and preserves every other byte. A file containing only the block
// (the private store was empty at install time) is deleted outright.
func AlmaStripMemoryBlock(configDir string) error {
	memoryPath := filepath.Join(configDir, "MEMORY.md")
	raw, err := os.ReadFile(memoryPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	text := string(raw)
	start := strings.Index(text, almaBeginMark)
	if start < 0 {
		return nil // nothing of ours in there
	}
	end := strings.Index(text[start:], almaEndMark)
	if end < 0 {
		return fmt.Errorf("malformed markers in %s; left untouched", memoryPath)
	}
	end += start + len(almaEndMark)

	// Drop the block plus the single blank line we inserted before it.
	prefix := text[:start]
	prefix = strings.TrimSuffix(prefix, "\n")
	prefix = strings.TrimSuffix(prefix, "\n")
	suffix := text[end:]
	suffix = strings.TrimPrefix(suffix, "\n")

	out := prefix
	if out != "" && suffix != "" {
		out += "\n\n"
	}
	out += suffix
	out = strings.TrimRight(out, "\n")
	if out != "" {
		out += "\n"
	}

	if strings.TrimSpace(out) == "" {
		if err := os.Remove(memoryPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	info, statErr := os.Stat(memoryPath)
	mode := os.FileMode(0o600)
	if statErr == nil {
		mode = info.Mode().Perm()
	}
	return os.WriteFile(memoryPath, []byte(out), mode)
}

func addAlmaHooks(data map[string]interface{}, hooksDir string) {
	removeAlmaHooks(data)
	hooks := ensureHooksMap(data)

	entry := map[string]interface{}{
		"matcher": ".*",
		"hooks": []interface{}{
			map[string]interface{}{
				"command": filepath.Join(hooksDir, "prime.sh"),
				"timeout": 5000,
				"enabled": true,
			},
		},
	}
	arr, _ := hooks["app.ready"].([]interface{})
	hooks["app.ready"] = append(arr, entry)
}

func removeAlmaHooks(data map[string]interface{}) {
	hooks, ok := data["hooks"].(map[string]interface{})
	if !ok {
		return
	}
	for event, raw := range hooks {
		groups, ok := raw.([]interface{})
		if !ok {
			continue
		}
		keptGroups := make([]interface{}, 0, len(groups))
		for _, group := range groups {
			groupMap, ok := group.(map[string]interface{})
			if !ok {
				if !containsMnemon(group) {
					keptGroups = append(keptGroups, group)
				}
				continue
			}
			inner, hasHooks := groupMap["hooks"].([]interface{})
			if !hasHooks {
				if !containsMnemon(groupMap) {
					keptGroups = append(keptGroups, groupMap)
				}
				continue
			}
			keptInner := make([]interface{}, 0, len(inner))
			for _, command := range inner {
				if !containsMnemon(command) {
					keptInner = append(keptInner, command)
				}
			}
			if len(keptInner) == 0 {
				continue
			}
			groupMap["hooks"] = keptInner
			keptGroups = append(keptGroups, groupMap)
		}
		if len(keptGroups) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = keptGroups
		}
	}
	if len(hooks) == 0 {
		delete(data, "hooks")
		if _, ok := data["version"]; ok && len(data) == 1 {
			delete(data, "version")
		}
	}
}
