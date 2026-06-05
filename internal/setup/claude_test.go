package setup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPromptDirHonorsMnemonDataDir(t *testing.T) {
	custom := t.TempDir()
	t.Setenv("MNEMON_DATA_DIR", custom)

	got, err := promptDir()
	if err != nil {
		t.Fatalf("promptDir: %v", err)
	}
	want := filepath.Join(custom, "prompt")
	if got != want {
		t.Fatalf("promptDir = %q, want %q", got, want)
	}
}

func TestPromptDirFallsBackToHomeWhenEnvUnset(t *testing.T) {
	t.Setenv("MNEMON_DATA_DIR", "")
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	got, err := promptDir()
	if err != nil {
		t.Fatalf("promptDir: %v", err)
	}
	want := filepath.Join(fakeHome, ".mnemon", "prompt")
	if got != want {
		t.Fatalf("promptDir = %q, want %q", got, want)
	}
}

func TestWritePromptFilesWritesUnderMnemonDataDir(t *testing.T) {
	custom := t.TempDir()
	t.Setenv("MNEMON_DATA_DIR", custom)

	path, err := WritePromptFiles()
	if err != nil {
		t.Fatalf("WritePromptFiles: %v", err)
	}

	wantDir := filepath.Join(custom, "prompt")
	if path != wantDir {
		t.Fatalf("returned path = %q, want %q", path, wantDir)
	}

	for _, name := range []string{"guide.md", "skill.md"} {
		full := filepath.Join(wantDir, name)
		info, err := os.Stat(full)
		if err != nil {
			t.Fatalf("stat %s: %v", full, err)
		}
		if info.Size() == 0 {
			t.Fatalf("%s is empty", full)
		}
	}
}

func TestCollidesWithUserConfigHomeInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Chdir(home)

	if !collidesWithUserConfig(".claude") {
		t.Fatal("project-local .claude with cwd == $HOME must collide with ~/.claude")
	}

	proj := t.TempDir()
	t.Chdir(proj)
	if collidesWithUserConfig(".claude") {
		t.Fatal("genuine project-local install must not collide")
	}
}

func TestCollidesWithUserConfigResolvesSymlinks(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "realhome")
	link := filepath.Join(base, "linkhome")
	if err := os.MkdirAll(filepath.Join(real, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", link) // $HOME reached via symlink
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Chdir(real) // cwd is the physical path

	if !collidesWithUserConfig(".claude") {
		t.Fatal("symlinked $HOME vs physical cwd must still be detected as a collision")
	}
}

func TestCollidesWithUserConfigHonorsClaudeConfigDir(t *testing.T) {
	home := t.TempDir()
	relocated := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", relocated)
	t.Chdir(home)

	// With the user config relocated, ~/.claude is NOT the global file.
	if collidesWithUserConfig(".claude") {
		t.Fatal("cwd == $HOME must not collide when CLAUDE_CONFIG_DIR points elsewhere")
	}

	// But installing into the relocated dir itself is a collision.
	t.Chdir(filepath.Dir(relocated))
	if !collidesWithUserConfig(filepath.Base(relocated)) {
		t.Fatal("install targeting the CLAUDE_CONFIG_DIR dir must collide")
	}
}

func TestClaudeRegisterHooksCollisionWritesAbsoluteCommands(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Chdir(home)

	if _, err := ClaudeRegisterHooks(".claude", HookSelection{Remind: true, Nudge: true}); err != nil {
		t.Fatalf("register: %v", err)
	}
	data, err := ReadJSONFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	hooks := data["hooks"].(map[string]any)
	for _, ev := range []string{"SessionStart", "UserPromptSubmit", "Stop"} {
		entry := hooks[ev].([]any)[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
		cmd := entry["command"].(string)
		if !filepath.IsAbs(cmd) {
			t.Fatalf("%s command must be absolute in the user-global file, got %q", ev, cmd)
		}
	}
}

func TestClaudeRegisterHooksProjectLocalStaysRelative(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Chdir(proj)

	if _, err := ClaudeRegisterHooks(".claude", HookSelection{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	data, err := ReadJSONFile(filepath.Join(proj, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	hooks := data["hooks"].(map[string]any)
	entry := hooks["SessionStart"].([]any)[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
	if cmd := entry["command"].(string); filepath.IsAbs(cmd) {
		t.Fatalf("genuine project-local install must keep existing relative form, got %q", cmd)
	}
}
