package setup

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/memory/setup/assets"
)

func TestPiPromptFilesRemainSeparateFromClaude(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("MNEMON_DATA_DIR", dataDir)
	if _, err := WritePromptFiles(); err != nil {
		t.Fatal(err)
	}
	dir, err := PiWritePromptFiles()
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(dataDir, "prompt", "pi") {
		t.Fatalf("Pi prompt directory = %q", dir)
	}
	// A later Claude setup must not change Pi's installed guide or skill.
	if _, err := WritePromptFiles(); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string][]byte{
		filepath.Join(dir, "guide.md"):               assets.PiGuide,
		filepath.Join(dir, "skill.md"):               assets.PiSkill,
		filepath.Join(dataDir, "prompt", "guide.md"): assets.ClaudeGuide,
		filepath.Join(dataDir, "prompt", "skill.md"): assets.ClaudeSkill,
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("wrong host's prompt at %s", path)
		}
	}
}

func TestPiWriteSkillAndExtension(t *testing.T) {
	dir := t.TempDir()

	skillPath, err := PiWriteSkill(dir)
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

	extPath, err := PiWriteExtension(dir)
	if err != nil {
		t.Fatalf("write extension: %v", err)
	}
	if extPath != filepath.Join(dir, "extensions", "mnemon.ts") {
		t.Fatalf("extension path = %q", extPath)
	}
	if data, err := os.ReadFile(extPath); err != nil {
		t.Fatalf("read extension: %v", err)
	} else if !strings.Contains(string(data), `pi.on("before_agent_start"`) {
		t.Fatalf("unexpected extension content: %q", string(data))
	}
}

func TestPiExtensionMapsLifecycleEvents(t *testing.T) {
	extension := string(assets.PiExtension)
	for _, want := range []string{
		`pi.on("resources_discover"`,
		`pi.on("session_start"`,
		`pi.on("before_agent_start"`,
		`pi.on("agent_end"`,
		`pi.on("session_before_compact"`,
		"process.env.MNEMON_DATA_DIR",
		"process.env.PI_CODING_AGENT_DIR",
	} {
		if !strings.Contains(extension, want) {
			t.Fatalf("Pi extension missing %q", want)
		}
	}
}

func TestPiEjectRemovesOnlyMnemonFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := PiWriteSkill(dir); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	if _, err := PiWriteExtension(dir); err != nil {
		t.Fatalf("write extension: %v", err)
	}
	other := filepath.Join(dir, "extensions", "custom.ts")
	if err := os.WriteFile(other, []byte("export default function () {}\n"), 0644); err != nil {
		t.Fatalf("write custom extension: %v", err)
	}

	errs := PiEject(dir)
	if len(errs) > 0 {
		t.Fatalf("eject errors: %v", errs)
	}
	if _, err := os.Stat(filepath.Join(dir, "skills", "mnemon")); !os.IsNotExist(err) {
		t.Fatalf("mnemon skill should be removed, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "extensions", "mnemon.ts")); !os.IsNotExist(err) {
		t.Fatalf("mnemon extension should be removed, err=%v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("custom extension should be preserved: %v", err)
	}
}
