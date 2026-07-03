package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/setup/assets"
)

func TestHermesWriteSkillAndHooks(t *testing.T) {
	dir := t.TempDir()

	skillPath, err := HermesWriteSkill(dir)
	if err != nil {
		t.Fatalf("write skill: %v", err)
	}
	if skillPath != filepath.Join(dir, "skills", "mnemon", "SKILL.md") {
		t.Fatalf("skill path = %q", skillPath)
	}
	if data, err := os.ReadFile(skillPath); err != nil {
		t.Fatalf("read skill: %v", err)
	} else if !strings.Contains(string(data), "name: mnemon") {
		t.Fatalf("unexpected skill content: %q", string(data))
	}

	hookPath, err := HermesWriteHook(dir, "remind.sh", assets.HermesRemindHook)
	if err != nil {
		t.Fatalf("write hook: %v", err)
	}
	if hookPath != filepath.Join(dir, "agent-hooks", "mnemon", "remind.sh") {
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

func TestHermesPythonHooksUseExplicitUTF8(t *testing.T) {
	tests := []struct {
		name string
		hook string
		want []string
	}{
		{
			name: "remind",
			hook: string(assets.HermesRemindHook),
			want: []string{
				`payload_path.read_text(encoding="utf-8")`,
				`path.read_text(encoding="utf-8").strip()`,
				`encoding="utf-8",`,
				`errors="replace",`,
			},
		},
		{
			name: "nudge",
			hook: string(assets.HermesNudgeHook),
			want: []string{
				`Path(sys.argv[1]).read_text(encoding="utf-8")`,
				`encoding="utf-8",`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, want := range tt.want {
				if !strings.Contains(tt.hook, want) {
					t.Fatalf("Hermes %s hook is missing %q", tt.name, want)
				}
			}
		})
	}
}

func TestHermesRegisterHooksPreservesUnrelatedConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	input := []byte(`model:
  provider: openrouter
hooks:
  pre_llm_call:
    - command: /custom/inject.sh
      timeout: 3
  post_llm_call:
    - command: /old/mnemon/nudge.sh
      timeout: 2
`)
	if err := os.WriteFile(configPath, input, 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if _, err := HermesRegisterHooks(dir, HookSelection{Remind: true, Nudge: true, Compact: true}); err != nil {
		t.Fatalf("register hooks: %v", err)
	}

	data, err := readYAMLFile(configPath)
	if err != nil {
		t.Fatalf("read yaml: %v", err)
	}
	model := data["model"].(map[string]any)
	if model["provider"] != "openrouter" {
		t.Fatalf("lost unrelated model config: %#v", data)
	}

	hooks := data["hooks"].(map[string]any)
	pre := hooks["pre_llm_call"].([]any)
	if len(pre) != 2 {
		t.Fatalf("expected custom pre_llm hook plus mnemon hook: %#v", pre)
	}
	if !containsMnemon(pre[1]) {
		t.Fatalf("missing mnemon pre_llm hook: %#v", pre)
	}
	post := hooks["post_llm_call"].([]any)
	if len(post) != 1 || !containsMnemon(post[0]) {
		t.Fatalf("expected old mnemon hook replaced with new one: %#v", post)
	}
	if _, ok := hooks["on_session_finalize"]; !ok {
		t.Fatalf("compact hook should be registered: %#v", hooks)
	}
}

func TestHermesEjectRemovesOnlyMnemonFilesAndHooks(t *testing.T) {
	dir := t.TempDir()
	if _, err := HermesWriteSkill(dir); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	if _, err := HermesWriteHook(dir, "remind.sh", assets.HermesRemindHook); err != nil {
		t.Fatalf("write hook: %v", err)
	}
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(`hooks:
  pre_llm_call:
    - command: /custom/inject.sh
    - command: /old/mnemon/remind.sh
`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	errs := HermesEject(dir)
	if len(errs) > 0 {
		t.Fatalf("eject errors: %v", errs)
	}
	if _, err := os.Stat(filepath.Join(dir, "skills", "mnemon")); !os.IsNotExist(err) {
		t.Fatalf("mnemon skill should be removed, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "agent-hooks", "mnemon")); !os.IsNotExist(err) {
		t.Fatalf("mnemon hooks should be removed, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "mnemon")); !os.IsNotExist(err) {
		t.Fatalf("mnemon state should be removed, err=%v", err)
	}

	data, err := readYAMLFile(configPath)
	if err != nil {
		t.Fatalf("read yaml: %v", err)
	}
	hooks := data["hooks"].(map[string]any)
	pre := hooks["pre_llm_call"].([]any)
	if len(pre) != 1 || containsMnemon(pre[0]) {
		t.Fatalf("custom hook should be preserved and mnemon removed: %#v", pre)
	}
}
