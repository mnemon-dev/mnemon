package setup

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/memory/setup/assets"
)

func TestPiPromptsPreserveOtherHostsInEitherSetupOrder(t *testing.T) {
	for _, tc := range []struct {
		name    string
		piFirst bool
	}{{"Pi then Claude", true}, {"Claude then Pi", false}} {
		t.Run(tc.name, func(t *testing.T) {
			data := t.TempDir()
			t.Setenv("MNEMON_DATA_DIR", data)
			writers := []func() (string, error){WritePromptFiles, PiWritePromptFiles}
			if tc.piFirst {
				writers[0], writers[1] = writers[1], writers[0]
			}
			for _, write := range writers {
				if _, err := write(); err != nil {
					t.Fatal(err)
				}
			}
			for name, want := range map[string][]byte{
				"guide.md": assets.ClaudeGuide, "skill.md": assets.ClaudeSkill,
				"pi/guide.md": assets.PiGuide, "pi/skill.md": assets.PiSkill,
			} {
				got, err := os.ReadFile(filepath.Join(data, "prompt", filepath.FromSlash(name)))
				if err != nil || !bytes.Equal(got, want) {
					t.Fatalf("%s changed across host setup: %v", name, err)
				}
			}
		})
	}
}

func TestPiGuideUsesAvailableMemoryWorkflow(t *testing.T) {
	guide := string(assets.PiGuide)
	for _, want := range []string{"mnemon recall", "mnemon remember", "mnemon link", "directly in the main conversation", "Do NOT store secrets", "private keys", "short-lived operational noise"} {
		if !strings.Contains(guide, want) {
			t.Fatalf("Pi guide missing %q", want)
		}
	}
	for _, forbidden := range []string{"Task", "sonnet", "subagent_type", "Do NOT run memory writes in the main conversation"} {
		if strings.Contains(guide, forbidden) {
			t.Fatalf("Pi guide contains unavailable workflow %q", forbidden)
		}
	}
}

func TestPiGlobalDirAndDetection(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PATH", t.TempDir()) // Detection must not launch a real host.
	for _, tc := range []struct{ override, want string }{
		{"", filepath.Join(home, ".pi", "agent")},
		{filepath.Join(home, "custom 中文 agent"), filepath.Join(home, "custom 中文 agent")},
		{"~/custom 中文 agent", filepath.Join(home, "custom 中文 agent")},
		{"~", home},
	} {
		t.Run(tc.override, func(t *testing.T) {
			t.Setenv("PI_CODING_AGENT_DIR", tc.override)
			if got := PiGlobalDir(); got != tc.want {
				t.Fatalf("PiGlobalDir = %q, want %q", got, tc.want)
			}
			if _, err := PiWriteSkill(tc.want); err != nil {
				t.Fatal(err)
			}
			global := detectPi(true)
			if global.ConfigDir != tc.want || !global.Installed || !global.Detected {
				t.Fatalf("global detection = %+v", global)
			}
			if local := detectPi(false); local.ConfigDir != ".pi" {
				t.Fatalf("global override changed local directory: %q", local.ConfigDir)
			}
		})
	}
	if runtime.GOOS == "windows" {
		t.Setenv("PI_CODING_AGENT_DIR", `~\custom 中文 agent`)
		if got := PiGlobalDir(); got != filepath.Join(home, "custom 中文 agent") {
			t.Fatalf("Windows tilde directory = %q", got)
		}
	}
}

func TestPiPromptsDefaultToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("MNEMON_DATA_DIR", "")
	got, err := PiWritePromptFiles()
	if err != nil || got != filepath.Join(home, ".mnemon", "prompt", "pi") {
		t.Fatalf("Pi prompt directory = %q, error = %v", got, err)
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
