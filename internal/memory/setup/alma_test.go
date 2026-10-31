package setup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/memory/setup/assets"
)

func TestAlmaConfigDirGlobalOnly(t *testing.T) {
	if _, err := AlmaConfigDir(false); err == nil || !strings.Contains(err.Error(), "--global") {
		t.Fatalf("expected user-global error, got %v", err)
	}
	dir, err := AlmaConfigDir(true)
	if err != nil {
		t.Fatalf("global config dir: %v", err)
	}
	if dir != filepath.Join(HomeDir(), ".config", "alma") {
		t.Fatalf("global config dir = %q", dir)
	}
}

func TestDetectAlma(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	binDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "alma"), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("write alma: %v", err)
	}
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))
	if _, err := AlmaWriteSkill(filepath.Join(home, ".config", "alma")); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	env := detectAlma()
	if env.Name != "alma" || env.Display != "Alma" {
		t.Fatalf("unexpected environment: %#v", env)
	}
	if !env.Detected || !env.Installed {
		t.Fatalf("expected detected and installed: %#v", env)
	}
	if env.ConfigDir != filepath.Join(home, ".config", "alma") {
		t.Fatalf("config dir = %q", env.ConfigDir)
	}
}

func TestAlmaWriteSkillAndHook(t *testing.T) {
	dir := t.TempDir()

	skillPath, err := AlmaWriteSkill(dir)
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
	if !strings.Contains(string(data), "name: mnemon") || !strings.Contains(string(data), "mnemon:begin") {
		t.Fatalf("unexpected skill content: %q", string(data))
	}
	info, err := os.Stat(skillPath)
	if err != nil {
		t.Fatalf("stat skill: %v", err)
	}
	if info.Mode().Perm() != 0644 {
		t.Fatalf("skill permissions = %v, want 0644", info.Mode().Perm())
	}

	hookPath, err := AlmaWriteHook(dir, "prime.sh", assets.AlmaPrimeHook)
	if err != nil {
		t.Fatalf("write hook: %v", err)
	}
	if hookPath != filepath.Join(dir, "mnemon-bridge", "prime.sh") {
		t.Fatalf("hook path = %q", hookPath)
	}
	info, err = os.Stat(hookPath)
	if err != nil {
		t.Fatalf("stat hook: %v", err)
	}
	if info.Mode().Perm() != 0755 {
		t.Fatalf("hook permissions = %v, want 0755", info.Mode().Perm())
	}
}

func TestAlmaRegisterHooksCreatesValidConfig(t *testing.T) {
	dir := t.TempDir()
	hooksPath, err := AlmaRegisterHooks(dir, HookSelection{Remind: true, Nudge: true, Compact: true})
	if err != nil {
		t.Fatalf("register hooks: %v", err)
	}
	if hooksPath != filepath.Join(dir, "hooks.json") {
		t.Fatalf("hooks path = %q", hooksPath)
	}

	data, err := ReadJSONFile(hooksPath)
	if err != nil {
		t.Fatalf("read hooks config: %v", err)
	}
	hooks := data["hooks"].(map[string]any)
	groups := hooks["app.ready"].([]any)
	if len(groups) != 1 {
		t.Fatalf("expected one app.ready group, got %#v", groups)
	}
	group := groups[0].(map[string]any)
	if group["matcher"] != ".*" {
		t.Fatalf("matcher = %#v", group["matcher"])
	}
	commands := group["hooks"].([]any)
	if len(commands) != 1 {
		t.Fatalf("expected one command, got %#v", commands)
	}
	command := commands[0].(map[string]any)
	if !strings.HasSuffix(command["command"].(string), filepath.Join("mnemon-bridge", "prime.sh")) {
		t.Fatalf("command = %#v", command["command"])
	}
	if !filepath.IsAbs(command["command"].(string)) {
		t.Fatalf("command must be absolute: %#v", command["command"])
	}
	if command["timeout"].(float64) != 5000 {
		t.Fatalf("timeout = %#v", command["timeout"])
	}
	if command["enabled"] != true {
		t.Fatalf("enabled = %#v", command["enabled"])
	}
}

func TestAlmaRegisterHooksIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 2; i++ {
		if _, err := AlmaRegisterHooks(dir, HookSelection{}); err != nil {
			t.Fatalf("register hooks %d: %v", i, err)
		}
	}
	data, err := ReadJSONFile(filepath.Join(dir, "hooks.json"))
	if err != nil {
		t.Fatalf("read hooks config: %v", err)
	}
	groups := data["hooks"].(map[string]any)["app.ready"].([]any)
	if len(groups) != 1 {
		t.Fatalf("expected one app.ready group after reinstall, got %#v", groups)
	}
	if commands := groups[0].(map[string]any)["hooks"].([]any); len(commands) != 1 {
		t.Fatalf("expected one command after reinstall, got %#v", commands)
	}
}

func TestAlmaRegisterHooksPreservesUnrelatedHooks(t *testing.T) {
	dir := t.TempDir()
	hooksPath := filepath.Join(dir, "hooks.json")
	if err := os.WriteFile(hooksPath, []byte(`{
  "version": 2,
  "hooks": {
    "app.ready": [
      {"matcher": ".*", "hooks": [{"command": "/keep/warmup.sh", "timeout": 1000}]},
      {"matcher": ".*", "hooks": [{"command": "/old/mnemon-bridge/prime.sh", "timeout": 3000}]}
    ],
    "tool.willExecute": [
      {"matcher": "^bash$", "hooks": [{"command": "/keep/audit.sh", "timeout": 2000}]}
    ]
  }
}`), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if _, err := AlmaRegisterHooks(dir, HookSelection{}); err != nil {
		t.Fatalf("register hooks: %v", err)
	}
	data, err := ReadJSONFile(hooksPath)
	if err != nil {
		t.Fatalf("read hooks config: %v", err)
	}
	if data["version"].(float64) != 2 {
		t.Fatalf("version should be preserved: %#v", data)
	}
	hooks := data["hooks"].(map[string]any)
	groups := hooks["app.ready"].([]any)
	if len(groups) != 2 {
		t.Fatalf("expected foreign hook plus new mnemon hook, got %#v", groups)
	}
	if !strings.Contains(groups[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)["command"].(string), "/keep/warmup.sh") {
		t.Fatalf("foreign hook should be preserved: %#v", groups)
	}
	for _, group := range groups {
		for _, command := range group.(map[string]any)["hooks"].([]any) {
			if strings.Contains(command.(map[string]any)["command"].(string), "/old/mnemon-bridge/prime.sh") {
				t.Fatalf("old mnemon hook should be removed: %#v", groups)
			}
		}
	}
	if _, ok := hooks["tool.willExecute"]; !ok {
		t.Fatalf("unrelated event should be preserved: %#v", hooks)
	}
}

func TestAlmaEjectRemovesOnlyMnemonFilesAndHooks(t *testing.T) {
	dir := t.TempDir()
	if _, err := AlmaWriteSkill(dir); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	if _, err := AlmaWriteHook(dir, "prime.sh", assets.AlmaPrimeHook); err != nil {
		t.Fatalf("write hook: %v", err)
	}
	if _, err := AlmaRegisterHooks(dir, HookSelection{}); err != nil {
		t.Fatalf("register hooks: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mnemon-bridge", "alma-hook.log"), []byte("log\n"), 0644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	hooksPath := filepath.Join(dir, "hooks.json")
	data, err := ReadJSONFile(hooksPath)
	if err != nil {
		t.Fatalf("read hooks: %v", err)
	}
	hooks := data["hooks"].(map[string]any)
	hooks["app.ready"] = append(hooks["app.ready"].([]any), map[string]any{
		"matcher": ".*",
		"hooks":   []any{map[string]any{"command": "/keep/custom.sh", "timeout": 1000}},
	})
	hooks["thread.created"] = []any{map[string]any{
		"matcher": ".*",
		"hooks":   []any{map[string]any{"command": "/keep/thread.sh", "timeout": 1000}},
	}}
	if err := WriteJSONFile(hooksPath, data); err != nil {
		t.Fatalf("write hooks: %v", err)
	}

	errs := AlmaEject(dir)
	if len(errs) > 0 {
		t.Fatalf("eject errors: %v", errs)
	}
	if _, err := os.Stat(filepath.Join(dir, "skills", "mnemon")); !os.IsNotExist(err) {
		t.Fatalf("mnemon skill should be removed, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "mnemon-bridge")); !os.IsNotExist(err) {
		t.Fatalf("mnemon bridge dir should be removed, err=%v", err)
	}

	data, err = ReadJSONFile(hooksPath)
	if err != nil {
		t.Fatalf("read hooks after eject: %v", err)
	}
	hooks = data["hooks"].(map[string]any)
	groups := hooks["app.ready"].([]any)
	if len(groups) != 1 || containsMnemon(groups[0]) {
		t.Fatalf("custom hook should be preserved and mnemon removed: %#v", groups)
	}
	if _, ok := hooks["thread.created"]; !ok {
		t.Fatalf("unrelated event should be preserved: %#v", hooks)
	}
}

func TestAlmaEjectSafeWhenNothingInstalled(t *testing.T) {
	dir := t.TempDir()
	if errs := AlmaEject(dir); len(errs) > 0 {
		t.Fatalf("eject errors: %v", errs)
	}
	if _, err := os.Stat(filepath.Join(dir, "hooks.json")); !os.IsNotExist(err) {
		t.Fatalf("hooks.json should not be created, err=%v", err)
	}
}

func TestAlmaEjectRemovesVersionOnlyConfig(t *testing.T) {
	dir := t.TempDir()
	hooksPath := filepath.Join(dir, "hooks.json")
	if err := os.WriteFile(hooksPath, []byte(`{"version": 2}`), 0644); err != nil {
		t.Fatalf("write hooks config: %v", err)
	}
	if errs := AlmaEject(dir); len(errs) > 0 {
		t.Fatalf("eject errors: %v", errs)
	}
	if _, err := os.Stat(hooksPath); !os.IsNotExist(err) {
		t.Fatalf("version-only hooks.json should be removed, err=%v", err)
	}
}

func TestAlmaHooksNeverUseUnsupportedEvents(t *testing.T) {
	dir := t.TempDir()
	if _, err := AlmaRegisterHooks(dir, HookSelection{Remind: true, Nudge: true, Compact: true}); err != nil {
		t.Fatalf("register hooks: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "hooks.json"))
	if err != nil {
		t.Fatalf("read hooks: %v", err)
	}
	text := string(data)
	for _, forbidden := range []string{"chat.system.transform", "chat.params.transform", "chat.message.willSend", "user_prompt", "stop.sh", "nudge"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("hooks.json must not contain %q: %s", forbidden, text)
		}
	}
	parsed, err := ReadJSONFile(filepath.Join(dir, "hooks.json"))
	if err != nil {
		t.Fatalf("parse hooks: %v", err)
	}
	hooks := parsed["hooks"].(map[string]any)
	if len(hooks) != 1 {
		t.Fatalf("expected only app.ready, got %#v", hooks)
	}
	if _, ok := hooks["app.ready"]; !ok {
		t.Fatalf("missing app.ready: %#v", hooks)
	}
}
func TestAlmaPrimeRefreshesMarkerBlock(t *testing.T) {
	configDir, hookPath, env := almaPrimeFixture(t)
	memoryPath := filepath.Join(configDir, "MEMORY.md")
	original := "# MEMORY.md — user header\n\nkeep before\n<!-- mnemon:begin -->\nstale content\n<!-- mnemon:end -->\nkeep after\n"
	if err := os.WriteFile(memoryPath, []byte(original), 0600); err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}

	runAlmaPrime(t, hookPath, env)

	got, err := os.ReadFile(memoryPath)
	if err != nil {
		t.Fatalf("read MEMORY.md: %v", err)
	}
	text := string(got)
	if !strings.HasPrefix(text, "# MEMORY.md — user header\n\nkeep before\n<!-- mnemon:begin -->") {
		t.Fatalf("prefix changed: %q", text)
	}
	if !strings.HasSuffix(text, "<!-- mnemon:end -->\nkeep after\n") {
		t.Fatalf("suffix changed: %q", text)
	}
	for _, want := range []string{"first durable memory", "second durable memory", "## mnemon"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q from MEMORY.md:\n%s", want, text)
		}
	}
	if strings.Contains(text, "stale content") {
		t.Fatalf("stale block content survived:\n%s", text)
	}
}

func TestAlmaPrimeAppendsBlockWithoutTruncating(t *testing.T) {
	configDir, hookPath, env := almaPrimeFixture(t)
	memoryPath := filepath.Join(configDir, "MEMORY.md")
	original := "# MEMORY.md\n\nuser-owned content\n"
	if err := os.WriteFile(memoryPath, []byte(original), 0600); err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}

	runAlmaPrime(t, hookPath, env)

	got, err := os.ReadFile(memoryPath)
	if err != nil {
		t.Fatalf("read MEMORY.md: %v", err)
	}
	text := string(got)
	if !strings.HasPrefix(text, "# MEMORY.md\n\nuser-owned content\n") {
		t.Fatalf("existing content was not preserved: %q", text)
	}
	if !strings.Contains(text, "first durable memory") {
		t.Fatalf("block was not appended:\n%s", text)
	}
	if strings.Count(text, "<!-- mnemon:begin -->") != 1 {
		t.Fatalf("expected exactly one block:\n%s", text)
	}
}

// almaPrimeFixture creates a temp HOME with a fake mnemon binary, a sqlite
// fixture holding two durable memories, and the real prime.sh + mem shim in
// place. Returns the config dir; the caller writes MEMORY.md.
func almaPrimeFixture(t *testing.T) (configDir, hookPath string, env []string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("prime.sh and the shim are POSIX shell")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("shim requires bash")
	}
	sqlite3, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 not available")
	}

	home := t.TempDir()
	configDir = filepath.Join(home, ".config", "alma")
	bridgeDir := filepath.Join(configDir, "mnemon-bridge")
	if err := os.MkdirAll(bridgeDir, 0700); err != nil {
		t.Fatalf("mkdir bridge: %v", err)
	}
	hookPath = filepath.Join(bridgeDir, "prime.sh")
	if err := os.WriteFile(hookPath, assets.AlmaPrimeHook, 0o755); err != nil {
		t.Fatalf("write hook: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bridgeDir, "mem"), assets.AlmaMemShim, 0o755); err != nil {
		t.Fatalf("write shim: %v", err)
	}

	dbPath := filepath.Join(home, "mnemon.db")
	sql := `
CREATE TABLE insights (
  id TEXT PRIMARY KEY,
  content TEXT NOT NULL,
  category TEXT NOT NULL,
  importance INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  deleted_at TEXT
);
INSERT INTO insights VALUES ('id-1', 'first durable memory', 'decision', 5, '2026-01-01T00:00:00Z', NULL);
INSERT INTO insights VALUES ('id-2', 'second durable memory', 'preference', 4, '2026-01-02T00:00:00Z', NULL);
`
	if out, err := exec.Command(sqlite3, dbPath, sql).CombinedOutput(); err != nil {
		t.Fatalf("create sqlite fixture: %v: %s", err, out)
	}

	binDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	fakeMnemon := filepath.Join(binDir, "mnemon")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' '{\"db_path\":\"%s\"}'\n", dbPath)
	if err := os.WriteFile(fakeMnemon, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake mnemon: %v", err)
	}

	env = append(os.Environ(),
		"HOME="+home,
		"PATH="+binDir+":"+os.Getenv("PATH"),
		"ALMA_HOOK=true",
		"ALMA_CONFIG_DIR="+configDir,
		"MNEMON_BIN=",
	)
	return configDir, hookPath, env
}

func runAlmaPrime(t *testing.T, hookPath string, env []string) {
	t.Helper()
	cmd := exec.Command("/bin/sh", hookPath)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run prime.sh: %v: %s", err, out)
	}
	if len(out) != 0 {
		t.Fatalf("prime.sh wrote stdout: %q", out)
	}
}

func TestAlmaPrimeMigratesLegacySyncOutput(t *testing.T) {
	configDir, hookPath, env := almaPrimeFixture(t)
	memoryPath := filepath.Join(configDir, "MEMORY.md")
	legacy := "# MEMORY.md — hot index\n\n> Derived from mnemon store `default`. **Do not hand-edit.**\n\n## Core (importance>=4)\n- fact one\n- fact two\n"
	if err := os.WriteFile(memoryPath, []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy MEMORY.md: %v", err)
	}

	runAlmaPrime(t, hookPath, env)

	data, err := os.ReadFile(memoryPath)
	if err != nil {
		t.Fatalf("read MEMORY.md: %v", err)
	}
	body := string(data)
	if strings.Contains(body, "## Core (importance>=4)") {
		t.Fatalf("legacy sync dump was not replaced:\n%s", body)
	}
	if strings.Count(body, "<!-- mnemon:begin -->") != 1 {
		t.Fatalf("expected exactly one mnemon block, got:\n%s", body)
	}
	if strings.Count(body, "<!-- mnemon:end -->") != 1 {
		t.Fatalf("expected exactly one mnemon end marker, got:\n%s", body)
	}
	matches, _ := filepath.Glob(filepath.Join(configDir, "mnemon-bridge", "MEMORY.md.pre-migration.*"))
	if len(matches) != 1 {
		t.Fatalf("expected one pre-migration backup, found %d", len(matches))
	}
}

func TestAlmaPrimePreservesForeignContent(t *testing.T) {
	configDir, hookPath, env := almaPrimeFixture(t)
	memoryPath := filepath.Join(configDir, "MEMORY.md")
	original := "# MEMORY.md\n\n## Notes\n- hand written line\n"
	if err := os.WriteFile(memoryPath, []byte(original), 0o600); err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}

	runAlmaPrime(t, hookPath, env)

	data, err := os.ReadFile(memoryPath)
	if err != nil {
		t.Fatalf("read MEMORY.md: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, "- hand written line") {
		t.Fatalf("foreign content was lost:\n%s", body)
	}
	if !strings.HasPrefix(body, original) {
		t.Fatalf("original prefix changed:\n%s", body)
	}
	if strings.Count(body, "<!-- mnemon:begin -->") != 1 {
		t.Fatalf("expected one mnemon block:\n%s", body)
	}
}

func TestAlmaPrimeIsIdempotentOnMtime(t *testing.T) {
	configDir, hookPath, env := almaPrimeFixture(t)
	memoryPath := filepath.Join(configDir, "MEMORY.md")

	runAlmaPrime(t, hookPath, env)
	first, err := os.Stat(memoryPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	runAlmaPrime(t, hookPath, env)
	second, err := os.Stat(memoryPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	if !first.ModTime().Equal(second.ModTime()) {
		t.Fatalf("MEMORY.md was rewritten although content did not change (%v -> %v)",
			first.ModTime(), second.ModTime())
	}
}

func TestAlmaPrimeRejectsHalfMarkers(t *testing.T) {
	configDir, hookPath, env := almaPrimeFixture(t)
	memoryPath := filepath.Join(configDir, "MEMORY.md")
	broken := "# MEMORY.md\n<!-- mnemon:begin -->\n- stale\n"
	if err := os.WriteFile(memoryPath, []byte(broken), 0o600); err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}

	runAlmaPrime(t, hookPath, env)

	data, err := os.ReadFile(memoryPath)
	if err != nil {
		t.Fatalf("read MEMORY.md: %v", err)
	}
	if string(data) != broken {
		t.Fatalf("half-marker file was modified:\n%s", string(data))
	}

	logPath := filepath.Join(configDir, "mnemon-bridge", "alma-hook.log")
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("expected hook log entry, got: %v", err)
	}
	if !strings.Contains(string(logData), "malformed markers") {
		t.Fatalf("expected malformed-marker log line, got:\n%s", string(logData))
	}
}

// A fresh install must be self-sufficient: the skill tells the model to call the
// shim, so the shim has to exist, be executable, and match the embedded asset.
func TestAlmaWriteShimInstallsExecutableShim(t *testing.T) {
	dir := t.TempDir()

	shimPath, err := AlmaWriteShim(dir)
	if err != nil {
		t.Fatalf("AlmaWriteShim: %v", err)
	}
	if want := filepath.Join(dir, "mnemon-bridge", "mem"); shimPath != want {
		t.Fatalf("shim path = %q, want %q", shimPath, want)
	}

	info, err := os.Stat(shimPath)
	if err != nil {
		t.Fatalf("stat shim: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("shim is not executable: %v", info.Mode())
	}

	got, err := os.ReadFile(shimPath)
	if err != nil {
		t.Fatalf("read shim: %v", err)
	}
	if string(got) != string(assets.AlmaMemShim) {
		t.Fatal("installed shim does not match the embedded asset")
	}

	dirInfo, err := os.Stat(filepath.Join(dir, "mnemon-bridge"))
	if err != nil {
		t.Fatalf("stat bridge dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("bridge dir mode = %o, want 700", perm)
	}
}

// prime.sh must not carry its own SQL: it has to delegate to the shim, or the two
// writers of the MEMORY.md block drift apart.
func TestAlmaPrimeHookDelegatesToShim(t *testing.T) {
	hook := string(assets.AlmaPrimeHook)
	if strings.Contains(hook, "from insights") {
		t.Fatal("prime.sh embeds its own SQL; it must call `mem sync` instead")
	}
	if !strings.Contains(hook, `"${MEM}" sync`) {
		t.Fatalf("prime.sh does not invoke the shim's sync:\n%s", hook)
	}
	if !strings.Contains(hook, "ALMA_HOOK") {
		t.Fatal("prime.sh lost its ALMA_HOOK guard")
	}
}

// The skill must describe the shim at the path setup actually writes.
func TestAlmaSkillReferencesInstalledShim(t *testing.T) {
	skill := string(assets.AlmaSkill)
	if !strings.Contains(skill, "~/.config/alma/mnemon-bridge/mem") {
		t.Fatal("SKILL.md does not point at the installed shim path")
	}
	if strings.Contains(skill, "mnemon-bridge/SKILL.md") {
		t.Fatal("SKILL.md still references the retired hand-written skill")
	}
}

// Eject removes the shim but keeps user data in the bridge dir.
func TestAlmaEjectRemovesShimKeepsUserData(t *testing.T) {
	dir := t.TempDir()
	if _, err := AlmaWriteSkill(dir); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	if _, err := AlmaWriteShim(dir); err != nil {
		t.Fatalf("write shim: %v", err)
	}
	if _, err := AlmaWriteHook(dir, "prime.sh", assets.AlmaPrimeHook); err != nil {
		t.Fatalf("write hook: %v", err)
	}
	if _, err := AlmaRegisterHooks(dir, HookSelection{}); err != nil {
		t.Fatalf("register hooks: %v", err)
	}

	keep := filepath.Join(dir, "mnemon-bridge", "MEMORY.md.pre-migration.123")
	if err := os.WriteFile(keep, []byte("user data"), 0o600); err != nil {
		t.Fatalf("write user data: %v", err)
	}

	if errs := AlmaEject(dir); len(errs) != 0 {
		t.Fatalf("eject errors: %v", errs)
	}

	for _, gone := range []string{
		filepath.Join(dir, "mnemon-bridge", "mem"),
		filepath.Join(dir, "mnemon-bridge", "prime.sh"),
		filepath.Join(dir, "skills", "mnemon"),
	} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("%s should have been removed", gone)
		}
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("eject deleted user data at %s: %v", keep, err)
	}
}

// Ejecting must not leave a stale hot index behind: nothing would refresh it,
// but Alma would keep injecting it into the system prompt forever.
func TestAlmaEjectStripsMemoryBlock(t *testing.T) {
	dir := t.TempDir()
	memoryPath := filepath.Join(dir, "MEMORY.md")
	original := "# MEMORY.md\n\n- my own note\n"
	if err := os.WriteFile(memoryPath, []byte(original), 0o600); err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}
	if _, err := AlmaWriteShim(dir); err != nil {
		t.Fatalf("write shim: %v", err)
	}
	block := "<!-- mnemon:begin -->\n## Mnemon hot index\n\n- [decision i5] something\n<!-- mnemon:end -->\n"
	if err := os.WriteFile(memoryPath, []byte(original+"\n"+block), 0o600); err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}

	if errs := AlmaEject(dir); len(errs) != 0 {
		t.Fatalf("eject errors: %v", errs)
	}

	got, err := os.ReadFile(memoryPath)
	if err != nil {
		t.Fatalf("read MEMORY.md: %v", err)
	}
	if string(got) != original {
		t.Fatalf("MEMORY.md after eject = %q, want %q", string(got), original)
	}
}

// A MEMORY.md that contained nothing but the generated block should be removed
// rather than left as an empty file.
func TestAlmaEjectRemovesMemoryFileWhenOnlyBlock(t *testing.T) {
	dir := t.TempDir()
	memoryPath := filepath.Join(dir, "MEMORY.md")
	only := "<!-- mnemon:begin -->\n## Mnemon hot index\n\n- (no high-importance memories yet)\n<!-- mnemon:end -->\n"
	if err := os.WriteFile(memoryPath, []byte(only), 0o600); err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}

	if errs := AlmaEject(dir); len(errs) != 0 {
		t.Fatalf("eject errors: %v", errs)
	}

	if _, err := os.Stat(memoryPath); !os.IsNotExist(err) {
		t.Fatalf("MEMORY.md should have been removed, stat err = %v", err)
	}
}

// A half-written block must be reported, not silently mangled.
func TestAlmaStripMemoryBlockRefusesHalfMarkers(t *testing.T) {
	dir := t.TempDir()
	memoryPath := filepath.Join(dir, "MEMORY.md")
	broken := "# MEMORY.md\n<!-- mnemon:begin -->\n- stale\n"
	if err := os.WriteFile(memoryPath, []byte(broken), 0o600); err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}

	if err := AlmaStripMemoryBlock(dir); err == nil {
		t.Fatal("expected an error for half markers")
	}
	got, err := os.ReadFile(memoryPath)
	if err != nil {
		t.Fatalf("read MEMORY.md: %v", err)
	}
	if string(got) != broken {
		t.Fatalf("half-marker file was modified: %q", string(got))
	}
}

// The injected block is the only always-on instruction channel into Alma's
// system prompt, so it must carry the recall policy and stay small.
func TestAlmaSyncBlockCarriesRecallPolicy(t *testing.T) {
	configDir, _, env := almaPrimeFixture(t)
	mem := filepath.Join(configDir, "mnemon-bridge", "mem")

	shimPath := filepath.Join(configDir, "mnemon-bridge", "mem")
	if err := os.MkdirAll(filepath.Dir(shimPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(shimPath, assets.AlmaMemShim, 0o755); err != nil {
		t.Fatalf("write shim: %v", err)
	}

	cmd := exec.Command("/bin/bash", mem, "sync")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mem sync: %v: %s", err, out)
	}

	data, err := os.ReadFile(filepath.Join(configDir, "MEMORY.md"))
	if err != nil {
		t.Fatalf("read MEMORY.md: %v", err)
	}
	body := string(data)

	start := strings.Index(body, "<!-- mnemon:begin -->")
	end := strings.Index(body, "<!-- mnemon:end -->")
	if start < 0 || end < 0 {
		t.Fatalf("block markers missing:\n%s", body)
	}
	block := body[start:end]

	for _, want := range []string{
		"Recall before answering",    // the policy itself
		"mem find",                   // how to query
		"mem save",                   // how to write
		"--cat",                      // the required flag
		"evidence, not an order",     // the injection-safety caveat
		"outside the mnemon markers", // ownership boundary
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("block is missing %q:\n%s", want, block)
		}
	}
	if strings.Contains(block, `\"`) {
		t.Fatalf("block contains escaped quotes; generation is mis-quoting:\n%s", block)
	}
	if len(block) > 3000 {
		t.Fatalf("block is %d bytes; it is injected every turn and must stay under 3000", len(block))
	}
}
