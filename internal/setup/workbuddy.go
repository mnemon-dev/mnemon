package setup

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mnemon-dev/mnemon/internal/setup/assets"
)

// WorkBuddyWriteSkill writes the mnemon skill to the WorkBuddy skills directory.
func WorkBuddyWriteSkill(configDir string) (string, error) {
	skillDir := filepath.Join(configDir, "skills", "mnemon")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return "", err
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, assets.WorkBuddySkill, 0644); err != nil {
		return "", err
	}
	return skillPath, nil
}

// WorkBuddyWriteHook writes a hook script to the WorkBuddy hooks directory.
func WorkBuddyWriteHook(configDir, filename string, content []byte) (string, error) {
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

// WorkBuddyRegisterHooks registers Mnemon lifecycle hooks in settings.json.
func WorkBuddyRegisterHooks(configDir string) (string, error) {
	hooksDir := filepath.Join(configDir, "hooks", "mnemon")
	absHooksDir, err := filepath.Abs(hooksDir)
	if err != nil {
		return "", err
	}
	settingsPath := filepath.Join(configDir, "settings.json")
	data, err := ReadJSONFile(settingsPath)
	if err != nil {
		return "", err
	}
	addWorkBuddyHooks(data, absHooksDir)
	if err := WriteJSONFile(settingsPath, data); err != nil {
		return "", err
	}
	return settingsPath, nil
}

// WorkBuddyEject removes mnemon skill and hooks from the given WorkBuddy config dir.
func WorkBuddyEject(configDir string) []error {
	var errs []error

	fmt.Printf("\nRemoving WorkBuddy integration (%s)...\n", configDir)

	hooksDir := filepath.Join(configDir, "hooks", "mnemon")
	if err := os.RemoveAll(hooksDir); err != nil {
		StatusError(1, 3, "Hooks", err)
		errs = append(errs, err)
	} else {
		StatusOK(1, 3, "Hooks", hooksDir+" removed")
	}
	removeIfEmpty(filepath.Join(configDir, "hooks"))

	settingsPath := filepath.Join(configDir, "settings.json")
	data, err := ReadJSONFile(settingsPath)
	if err != nil {
		StatusError(2, 3, "Settings", err)
		errs = append(errs, err)
	} else {
		removeWorkBuddyHooks(data)
		if err := WriteOrRemoveJSONFile(settingsPath, data); err != nil {
			StatusError(2, 3, "Settings", err)
			errs = append(errs, err)
		} else {
			StatusOK(2, 3, "Settings", settingsPath+" cleaned")
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

func addWorkBuddyHooks(data map[string]interface{}, hooksDir string) {
	removeWorkBuddyHooks(data)
	hooks := ensureHooksMap(data)

	addWorkBuddyHook(hooks, "SessionStart", filepath.Join(hooksDir, "prime.sh"))
	addWorkBuddyHook(hooks, "UserPromptSubmit", filepath.Join(hooksDir, "user_prompt.sh"))
	addWorkBuddyHook(hooks, "Stop", filepath.Join(hooksDir, "stop.sh"))
}

func addWorkBuddyHook(hooks map[string]interface{}, event, command string) {
	entry := map[string]interface{}{
		"hooks": []interface{}{
			map[string]interface{}{
				"type":    "command",
				"command": command,
			},
		},
	}
	arr, _ := hooks[event].([]interface{})
	hooks[event] = append(arr, entry)
}

func removeWorkBuddyHooks(data map[string]interface{}) {
	hooks, ok := data["hooks"].(map[string]interface{})
	if !ok {
		return
	}
	for _, key := range []string{"SessionStart", "UserPromptSubmit", "Stop", "PreToolUse", "PostToolUse", "Notification", "PreCompact", "SessionEnd", "SubagentStop"} {
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
	}
}
