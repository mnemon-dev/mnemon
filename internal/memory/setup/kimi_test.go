package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKimiRegisterHooksPreservesUnrelatedConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`model = "kimi"

[[hooks]]
event = "SessionStart"
command = "/old/mnemon/prime.sh"
timeout = 1

[[hooks]]
event = "Notification"
matcher = "task\\.completed"
command = "/keep/custom.sh"
timeout = 3
`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := KimiRegisterHooks(dir); err != nil {
		t.Fatalf("register hooks: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `model = "kimi"`) {
		t.Fatalf("unrelated setting should be preserved: %s", text)
	}
	if !strings.Contains(text, `command = "/keep/custom.sh"`) {
		t.Fatalf("custom hook should be preserved: %s", text)
	}
	if strings.Contains(text, "/old/mnemon/prime.sh") {
		t.Fatalf("old mnemon hook should be removed: %s", text)
	}
	for _, event := range []string{`event = "SessionStart"`, `event = "UserPromptSubmit"`, `event = "Stop"`} {
		if !strings.Contains(text, event) {
			t.Fatalf("missing %s hook: %s", event, text)
		}
	}
	if strings.Contains(text, "loop_limit") {
		t.Fatalf("kimi hook schema should not include loop_limit: %s", text)
	}
}

func TestKimiEjectRemovesOnlyMnemonFilesAndHooks(t *testing.T) {
	dir := t.TempDir()
	if _, err := KimiWriteSkill(dir); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	if _, err := KimiWriteHook(dir, "prime.sh", []byte("#!/bin/bash\n")); err != nil {
		t.Fatalf("write hook: %v", err)
	}
	if _, err := KimiRegisterHooks(dir); err != nil {
		t.Fatalf("register hooks: %v", err)
	}
	customSkillDir := filepath.Join(dir, "skills", "custom")
	if err := os.MkdirAll(customSkillDir, 0755); err != nil {
		t.Fatalf("create custom skill: %v", err)
	}
	configPath := filepath.Join(dir, "config.toml")
	existing, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	existing = append(existing, []byte(`
[[hooks]]
event = "Notification"
command = "/keep/custom.sh"
timeout = 3
`)...)
	if err := os.WriteFile(configPath, existing, 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	errs := KimiEject(dir)
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
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config after eject: %v", err)
	}
	text := string(data)
	if strings.Contains(text, "mnemon") {
		t.Fatalf("mnemon hooks should be removed: %s", text)
	}
	if !strings.Contains(text, `command = "/keep/custom.sh"`) {
		t.Fatalf("custom hook should be preserved: %s", text)
	}
}

func TestAddKimiHooksWindowsInvokesScriptsThroughBash(t *testing.T) {
	const bash = `C:\Program Files\Git\bin\bash.exe`
	hooksDir := `C:\Users\u\.kimi-code\hooks\mnemon`

	text := addKimiHooks("", hooksDir, "windows", func() string { return bash })

	for _, script := range []string{"prime.sh", "user_prompt.sh", "stop.sh"} {
		want := fmt.Sprintf("command = %q", `"`+bash+`" "`+hooksDir+`\`+script+`"`)
		if !strings.Contains(text, want) {
			t.Fatalf("%s should be run through bash.exe, want %s in:\n%s", script, want, text)
		}
	}
}

func TestAddKimiHooksNonWindowsRegistersBareScripts(t *testing.T) {
	text := addKimiHooks("", "/home/u/.kimi-code/hooks/mnemon", "linux", func() string {
		t.Fatal("bash lookup should not run off Windows")
		return ""
	})
	if !strings.Contains(text, fmt.Sprintf("command = %q", filepath.Join("/home/u/.kimi-code/hooks/mnemon", "prime.sh"))) {
		t.Fatalf("non-Windows hooks should stay bare scripts: %s", text)
	}
}

func TestRemoveKimiHooksRemovesWindowsBashCommands(t *testing.T) {
	text := addKimiHooks("model = \"kimi\"\n", `C:\Users\u\.kimi-code\hooks\mnemon`, "windows",
		func() string { return `C:\Program Files\Git\bin\bash.exe` })
	if got := removeKimiHooks(text); strings.Contains(got, "mnemon") || !strings.Contains(got, `model = "kimi"`) {
		t.Fatalf("windows bash hooks should be removed cleanly: %s", got)
	}
}
