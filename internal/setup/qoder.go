package setup

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mnemon-dev/mnemon/internal/setup/assets"
)

// QoderWriteSkill writes the mnemon skill to the Qoder skills directory.
func QoderWriteSkill(configDir string) (string, error) {
	return writeQoderSkill(configDir, assets.QoderSkill)
}

// QoderWorkWriteSkill writes the mnemon skill to the QoderWork skills directory.
func QoderWorkWriteSkill(configDir string) (string, error) {
	return writeQoderSkill(configDir, assets.QoderWorkSkill)
}

func writeQoderSkill(configDir string, content []byte) (string, error) {
	skillDir := filepath.Join(configDir, "skills", "mnemon")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return "", err
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, content, 0644); err != nil {
		return "", err
	}
	return skillPath, nil
}

// QoderWriteHook writes a hook script to the Qoder hooks directory.
func QoderWriteHook(configDir, filename string, content []byte) (string, error) {
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

// QoderRegisterHooks registers Mnemon lifecycle hooks in Qoder settings.json.
func QoderRegisterHooks(configDir string) (string, error) {
	return registerQoderHooks(configDir)
}

// QoderWorkRegisterHooks registers Mnemon lifecycle hooks in QoderWork settings.json.
func QoderWorkRegisterHooks(configDir string) (string, error) {
	return registerQoderHooks(configDir)
}

func registerQoderHooks(configDir string) (string, error) {
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
	addQoderHooks(data, absHooksDir)
	if err := WriteJSONFile(settingsPath, data); err != nil {
		return "", err
	}
	return settingsPath, nil
}

// QoderEject removes mnemon skill and hooks from the given Qoder config dir.
func QoderEject(configDir string) []error {
	return ejectQoder("Qoder", configDir)
}

// QoderWorkEject removes mnemon skill and hooks from the given QoderWork config dir.
func QoderWorkEject(configDir string) []error {
	return ejectQoder("QoderWork", configDir)
}

func ejectQoder(display, configDir string) []error {
	var errs []error

	fmt.Printf("\nRemoving %s integration (%s)...\n", display, configDir)

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
		removeQoderHooks(data)
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

func addQoderHooks(data map[string]interface{}, hooksDir string) {
	removeQoderHooks(data)
	hooks := ensureHooksMap(data)

	addQoderHook(hooks, "SessionStart", filepath.Join(hooksDir, "prime.sh"))
	addQoderHook(hooks, "UserPromptSubmit", filepath.Join(hooksDir, "user_prompt.sh"))
	addQoderHook(hooks, "Stop", filepath.Join(hooksDir, "stop.sh"))
}

func addQoderHook(hooks map[string]interface{}, event, command string) {
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

func removeQoderHooks(data map[string]interface{}) {
	hooks, ok := data["hooks"].(map[string]interface{})
	if !ok {
		return
	}
	for _, key := range []string{"SessionStart", "UserPromptSubmit", "Stop", "PreToolUse", "PostToolUse", "PostToolUseFailure", "Notification", "PermissionRequest", "PreCompact", "SessionEnd", "SubagentStart", "SubagentStop"} {
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
