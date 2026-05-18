package setup

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mnemon-dev/mnemon/internal/setup/assets"
)

// CodexWriteSkill writes the mnemon skill to the Codex skills directory.
func CodexWriteSkill(configDir string) (string, error) {
	skillDir := filepath.Join(configDir, "skills", "mnemon")
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(skillPath, assets.CodexSkill, 0644); err != nil {
		return "", err
	}
	return skillPath, nil
}

// CodexWriteHook writes a hook script to the Codex hooks directory.
func CodexWriteHook(configDir, filename string, content []byte) (string, error) {
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

// CodexRegisterHooks registers Mnemon lifecycle hooks in hooks.json.
func CodexRegisterHooks(configDir string) (string, error) {
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
	addCodexHooks(data, absHooksDir)
	if err := WriteJSONFile(hooksPath, data); err != nil {
		return "", err
	}
	return hooksPath, nil
}

// CodexEject removes mnemon integration from the given Codex config dir.
func CodexEject(configDir string) []error {
	var errs []error

	fmt.Printf("\nRemoving Codex integration (%s)...\n", configDir)

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
		removeCodexHooks(data)
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

func addCodexHooks(data map[string]interface{}, hooksDir string) {
	removeCodexHooks(data)
	hooks := ensureHooksMap(data)

	primeEntry := map[string]interface{}{
		"matcher": "startup|resume|clear",
		"hooks": []interface{}{
			map[string]interface{}{
				"type":          "command",
				"command":       filepath.Join(hooksDir, "prime.sh"),
				"timeout":       30,
				"statusMessage": "Loading Mnemon context",
			},
		},
	}
	sessionArr, _ := hooks["SessionStart"].([]interface{})
	hooks["SessionStart"] = append(sessionArr, primeEntry)

	remindEntry := map[string]interface{}{
		"hooks": []interface{}{
			map[string]interface{}{
				"type":          "command",
				"command":       filepath.Join(hooksDir, "user_prompt.sh"),
				"timeout":       30,
				"statusMessage": "Checking Mnemon recall guidance",
			},
		},
	}
	promptArr, _ := hooks["UserPromptSubmit"].([]interface{})
	hooks["UserPromptSubmit"] = append(promptArr, remindEntry)

	stopEntry := map[string]interface{}{
		"hooks": []interface{}{
			map[string]interface{}{
				"type":          "command",
				"command":       filepath.Join(hooksDir, "stop.sh"),
				"timeout":       30,
				"statusMessage": "Checking Mnemon writeback guidance",
			},
		},
	}
	stopArr, _ := hooks["Stop"].([]interface{})
	hooks["Stop"] = append(stopArr, stopEntry)
}

func removeCodexHooks(data map[string]interface{}) {
	hooks, ok := data["hooks"].(map[string]interface{})
	if !ok {
		return
	}
	for _, key := range []string{"SessionStart", "UserPromptSubmit", "Stop"} {
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
