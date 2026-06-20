package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestQoderWriteSkill(t *testing.T) {
	dir := t.TempDir()

	skillPath, err := QoderWriteSkill(dir)
	if err != nil {
		t.Fatalf("write skill: %v", err)
	}
	if skillPath != filepath.Join(dir, "skills", "mnemon", "SKILL.md") {
		t.Fatalf("skill path = %q", skillPath)
	}
	if _, err := os.Stat(skillPath); err != nil {
		t.Fatalf("stat skill: %v", err)
	}
}

func TestQoderWorkWriteSkill(t *testing.T) {
	dir := t.TempDir()

	skillPath, err := QoderWorkWriteSkill(dir)
	if err != nil {
		t.Fatalf("write skill: %v", err)
	}
	if skillPath != filepath.Join(dir, "skills", "mnemon", "SKILL.md") {
		t.Fatalf("skill path = %q", skillPath)
	}
	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read skill: %v", err)
	}
	if !strings.Contains(string(data), "QoderWork") {
		t.Fatalf("qoderwork skill should mention QoderWork: %s", string(data))
	}
}

func TestQoderWriteHook(t *testing.T) {
	dir := t.TempDir()

	hookPath, err := QoderWriteHook(dir, "prime.sh", []byte("#!/bin/bash\n"))
	if err != nil {
		t.Fatalf("write hook: %v", err)
	}
	if hookPath != filepath.Join(dir, "hooks", "mnemon", "prime.sh") {
		t.Fatalf("hook path = %q", hookPath)
	}
	info, err := os.Stat(hookPath)
	if err != nil {
		t.Fatalf("stat hook: %v", err)
	}
	if info.Mode().Perm() != 0755 {
		t.Fatalf("hook permissions = %v, want 0755", info.Mode().Perm())
	}
}

func TestQoderRegisterHooksPreservesUnrelatedConfig(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{
  "hooks": {
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "/old/mnemon/prime.sh"}]},
      {"hooks": [{"type": "command", "command": "/keep/custom.sh"}]}
    ],
    "Stop": [
      {"hooks": [{"type": "command", "command": "/old/mnemon/stop.sh"}]}
    ]
  },
  "other": true
}`), 0644); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	if _, err := QoderRegisterHooks(dir); err != nil {
		t.Fatalf("register hooks: %v", err)
	}

	data, err := ReadJSONFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if data["other"] != true {
		t.Fatalf("unrelated setting should be preserved: %#v", data)
	}
	hooks := data["hooks"].(map[string]any)
	sessionStart := hooks["SessionStart"].([]any)
	if len(sessionStart) != 2 {
		t.Fatalf("expected custom hook plus new prime hook: %#v", sessionStart)
	}
	if !strings.Contains(sessionStart[1].(map[string]any)["hooks"].([]any)[0].(map[string]any)["command"].(string), "hooks/mnemon/prime.sh") {
		t.Fatalf("expected new prime hook, got %#v", sessionStart[1])
	}
	if _, ok := hooks["UserPromptSubmit"]; !ok {
		t.Fatalf("user prompt hook should be registered: %#v", hooks)
	}
	stop := hooks["Stop"].([]any)
	if len(stop) != 1 {
		t.Fatalf("expected one stop hook: %#v", stop)
	}
	if _, ok := stop[0].(map[string]any)["loop_limit"]; ok {
		t.Fatalf("qoder hook schema should not include loop_limit: %#v", stop[0])
	}
}

func TestQoderWorkRegisterHooksUsesSettingsJSON(t *testing.T) {
	dir := t.TempDir()

	settingsPath, err := QoderWorkRegisterHooks(dir)
	if err != nil {
		t.Fatalf("register hooks: %v", err)
	}
	if settingsPath != filepath.Join(dir, "settings.json") {
		t.Fatalf("settings path = %q", settingsPath)
	}
	data, err := ReadJSONFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	hooks := data["hooks"].(map[string]any)
	if _, ok := hooks["SessionStart"]; !ok {
		t.Fatalf("session hook should be registered: %#v", hooks)
	}
	if _, ok := hooks["UserPromptSubmit"]; !ok {
		t.Fatalf("user prompt hook should be registered: %#v", hooks)
	}
	if _, ok := hooks["Stop"]; !ok {
		t.Fatalf("stop hook should be registered: %#v", hooks)
	}
}

func TestQoderEjectRemovesOnlyMnemonFilesAndHooks(t *testing.T) {
	dir := t.TempDir()
	if _, err := QoderWriteSkill(dir); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	if _, err := QoderWriteHook(dir, "prime.sh", []byte("#!/bin/bash\n")); err != nil {
		t.Fatalf("write hook: %v", err)
	}
	if _, err := QoderRegisterHooks(dir); err != nil {
		t.Fatalf("register hooks: %v", err)
	}
	customSkillDir := filepath.Join(dir, "skills", "custom")
	if err := os.MkdirAll(customSkillDir, 0755); err != nil {
		t.Fatalf("create custom skill: %v", err)
	}
	settingsPath := filepath.Join(dir, "settings.json")
	data, err := ReadJSONFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	hooks := data["hooks"].(map[string]any)
	hooks["SessionStart"] = append(hooks["SessionStart"].([]any), map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": "/keep/custom.sh"}},
	})
	if err := WriteJSONFile(settingsPath, data); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	errs := QoderEject(dir)
	if len(errs) > 0 {
		t.Fatalf("eject errors: %v", errs)
	}
	if _, err := os.Stat(filepath.Join(dir, "skills", "mnemon")); !os.IsNotExist(err) {
		t.Fatalf("mnemon skill should be removed, err=%v", err)
	}
	if _, err := os.Stat(customSkillDir); err != nil {
		t.Fatalf("custom skill should be preserved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "hooks", "mnemon")); !os.IsNotExist(err) {
		t.Fatalf("mnemon hooks should be removed, err=%v", err)
	}
	data, err = ReadJSONFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings after eject: %v", err)
	}
	hooks = data["hooks"].(map[string]any)
	sessionStart := hooks["SessionStart"].([]any)
	if len(sessionStart) != 1 || containsMnemon(sessionStart[0]) {
		t.Fatalf("custom hook should be preserved and mnemon removed: %#v", sessionStart)
	}
	if _, ok := hooks["UserPromptSubmit"]; ok {
		t.Fatalf("user prompt hooks should be removed: %#v", hooks)
	}
	if _, ok := hooks["Stop"]; ok {
		t.Fatalf("stop hooks should be removed: %#v", hooks)
	}
}
