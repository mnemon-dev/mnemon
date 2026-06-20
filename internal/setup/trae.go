package setup

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mnemon-dev/mnemon/internal/setup/assets"
)

// TraeWriteSkill writes the mnemon skill to the Trae skills directory.
func TraeWriteSkill(configDir string) (string, error) {
	skillDir := filepath.Join(configDir, "skills", "mnemon")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return "", err
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, assets.TraeSkill, 0644); err != nil {
		return "", err
	}
	return skillPath, nil
}

// TraeWriteHook writes a hook script to the Trae hooks directory.
func TraeWriteHook(configDir, filename string, content []byte) (string, error) {
	hooksDir := filepath.Join(configDir, "hooks", "mnemon")
	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		return "", err
	}
	hookPath := filepath.Join(hooksDir, filename)
	if err := os.WriteFile(hookPath, content, 0755); err != nil {
		return "", err
	}
	return hookPath, nil
}

// TraeRegisterHooks registers Mnemon lifecycle hooks in Trae hooks.json.
func TraeRegisterHooks(configDir string) (string, error) {
	hooksDir := filepath.Join(configDir, "hooks", "mnemon")
	absHooksDir, err := filepath.Abs(hooksDir)
	if err != nil {
		return "", err
	}
	hooksPath := filepath.Join(configDir, "hooks.json")
	data, err := ReadJSONFile(hooksPath)
	if err != nil {
		return "", err
	}
	addTraeHooks(data, absHooksDir)
	if err := WriteJSONFile(hooksPath, data); err != nil {
		return "", err
	}
	return hooksPath, nil
}

// TraeEject removes mnemon skill and hooks from the given Trae config dir.
func TraeEject(configDir string) []error {
	var errs []error

	fmt.Printf("\nRemoving Trae integration (%s)...\n", configDir)

	hooksDir := filepath.Join(configDir, "hooks", "mnemon")
	if err := os.RemoveAll(hooksDir); err != nil {
		StatusError(1, 3, "Hooks", err)
		errs = append(errs, err)
	} else {
		StatusOK(1, 3, "Hooks", hooksDir+" removed")
	}
	removeIfEmpty(filepath.Join(configDir, "hooks"))

	hooksPath := filepath.Join(configDir, "hooks.json")
	data, err := ReadJSONFile(hooksPath)
	if err != nil {
		StatusError(2, 3, "Hooks config", err)
		errs = append(errs, err)
	} else {
		removeTraeHooks(data)
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
	removeIfEmpty(filepath.Join(configDir, "skills"))
	removeIfEmpty(configDir)

	return errs
}

func addTraeHooks(data map[string]interface{}, hooksDir string) {
	removeTraeHooks(data)
	if _, ok := data["version"]; !ok {
		data["version"] = 1
	}
	hooks := ensureHooksMap(data)

	addTraeHook(hooks, "SessionStart", "", 0, filepath.Join(hooksDir, "prime.sh"))
	addTraeHook(hooks, "UserPromptSubmit", "", 0, filepath.Join(hooksDir, "user_prompt.sh"))
	addTraeHook(hooks, "Stop", "", 1, filepath.Join(hooksDir, "stop.sh"))
}

func addTraeHook(hooks map[string]interface{}, event, matcher string, loopLimit int, command string) {
	entry := map[string]interface{}{
		"hooks": []interface{}{
			map[string]interface{}{
				"type":    "command",
				"command": command,
				"timeout": 30,
			},
		},
	}
	if matcher != "" {
		entry["matcher"] = matcher
	}
	if loopLimit > 0 {
		entry["loop_limit"] = loopLimit
	}
	arr, _ := hooks[event].([]interface{})
	hooks[event] = append(arr, entry)
}

func removeTraeHooks(data map[string]interface{}) {
	hooks, ok := data["hooks"].(map[string]interface{})
	if !ok {
		return
	}
	for _, key := range []string{"SessionStart", "UserPromptSubmit", "Stop", "PreToolUse", "PostToolUse", "Notification"} {
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
	if len(hooks) == 0 {
		delete(data, "hooks")
		if _, ok := data["version"]; ok && len(data) == 1 {
			delete(data, "version")
		}
	}
}
