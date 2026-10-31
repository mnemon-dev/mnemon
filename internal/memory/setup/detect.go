package setup

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Environment describes a detected LLM CLI environment.
type Environment struct {
	Name      string // "claude-code", "codex", "cursor", "zcode", "minimax-code", "trae", "qoder", "qoderwork", "codebuddy", "workbuddy", "kimi", "opencode", "openclaw", "nanobot", "pi", "hermes", "alma"
	Display   string // "Claude Code", "Codex", "Cursor", "ZCode", "MiniMax Code", "Trae", "Qoder", "QoderWork", "CodeBuddy", "WorkBuddy", "Kimi Code", "OpenCode", "OpenClaw", "Nanobot", "Pi", "Hermes Agent", "Alma"
	Detected  bool   // CLI binary or global config dir found
	BinPath   string // exec.LookPath result
	Installed bool   // mnemon integration present at ConfigDir
	Version   string // CLI version string
	ConfigDir string // target config dir (local or global)
}

// HomeDir returns the user's home directory.
func HomeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

// DetectEnvironments probes for all supported LLM CLI environments.
// When global is false, ConfigDir points to project-local config;
// when true, it points to the user-global config for each environment.
func DetectEnvironments(global bool) []Environment {
	return []Environment{
		detectClaude(global),
		detectCodex(global),
		detectCursor(global),
		detectZCode(global),
		detectMiniMaxCode(global),
		detectTrae(global),
		detectQoder(global),
		detectQoderWork(),
		detectCodeBuddy(global),
		detectWorkBuddy(global),
		detectKimi(),
		detectOpenCode(global),
		detectOpenClaw(global),
		detectNanobot(global),
		detectPi(global),
		detectHermes(),
		detectAlma(),
	}
}

func detectZCode(global bool) Environment {
	home := HomeDir()
	globalDir := filepath.Join(home, ".zcode")
	localDir := ".zcode"

	configDir := localDir
	if global {
		configDir = globalDir
	}

	env := Environment{
		Name:      "zcode",
		Display:   "ZCode",
		ConfigDir: configDir,
	}

	if binPath, err := exec.LookPath("zcode"); err == nil {
		env.Detected = true
		env.BinPath = binPath
	}
	if _, err := os.Stat(globalDir); err == nil {
		env.Detected = true
	}

	skillPath := filepath.Join(configDir, "skills", "mnemon", "SKILL.md")
	hooksPath := filepath.Join(configDir, "cli", "config.json")
	if _, err := os.Stat(skillPath); err == nil {
		env.Installed = true
	} else if data, err := ReadJSONFile(hooksPath); err == nil && containsMnemon(data) {
		env.Installed = true
	}

	if env.BinPath != "" {
		if out, err := exec.Command(env.BinPath, "--version").Output(); err == nil {
			env.Version = cleanVersion(strings.TrimSpace(string(out)))
		}
	}

	return env
}

func detectClaude(global bool) Environment {
	home := HomeDir()
	globalDir := filepath.Join(home, ".claude")
	localDir := ".claude"

	configDir := localDir
	if global {
		configDir = globalDir
	}

	env := Environment{
		Name:      "claude-code",
		Display:   "Claude Code",
		ConfigDir: configDir,
	}

	// CLI detection is always global
	if binPath, err := exec.LookPath("claude"); err == nil {
		env.Detected = true
		env.BinPath = binPath
	}
	if _, err := os.Stat(globalDir); err == nil {
		env.Detected = true
	}

	// Check if mnemon integration is already installed at the target location
	skillPath := filepath.Join(configDir, "skills", "mnemon", "SKILL.md")
	if _, err := os.Stat(skillPath); err == nil {
		env.Installed = true
	}

	// Get version
	if env.BinPath != "" {
		if out, err := exec.Command(env.BinPath, "--version").Output(); err == nil {
			env.Version = cleanVersion(strings.TrimSpace(string(out)))
		}
	}

	return env
}

func detectCodex(global bool) Environment {
	home := HomeDir()
	globalDir := filepath.Join(home, ".codex")
	localDir := ".codex"

	configDir := localDir
	if global {
		configDir = globalDir
	}

	env := Environment{
		Name:      "codex",
		Display:   "Codex",
		ConfigDir: configDir,
	}

	// CLI detection is always global.
	if binPath, err := exec.LookPath("codex"); err == nil {
		env.Detected = true
		env.BinPath = binPath
	}
	if _, err := os.Stat(globalDir); err == nil {
		env.Detected = true
	}

	skillPath := filepath.Join(configDir, "skills", "mnemon", "SKILL.md")
	if _, err := os.Stat(skillPath); err == nil {
		env.Installed = true
	}

	if env.BinPath != "" {
		if out, err := exec.Command(env.BinPath, "--version").Output(); err == nil {
			env.Version = cleanVersion(strings.TrimSpace(string(out)))
		}
	}

	return env
}

func detectCursor(global bool) Environment {
	home := HomeDir()
	globalDir := filepath.Join(home, ".cursor")
	localDir := ".cursor"

	configDir := localDir
	if global {
		configDir = globalDir
	}

	env := Environment{
		Name:      "cursor",
		Display:   "Cursor",
		ConfigDir: configDir,
	}

	if binPath, err := exec.LookPath("cursor"); err == nil {
		env.Detected = true
		env.BinPath = binPath
	}
	if _, err := os.Stat(globalDir); err == nil {
		env.Detected = true
	}

	skillPath := filepath.Join(configDir, "skills", "mnemon", "SKILL.md")
	if _, err := os.Stat(skillPath); err == nil {
		env.Installed = true
	}

	if env.BinPath != "" {
		if out, err := exec.Command(env.BinPath, "--version").Output(); err == nil {
			env.Version = cleanVersion(strings.TrimSpace(string(out)))
		}
	}

	return env
}

func detectMiniMaxCode(global bool) Environment {
	home := HomeDir()
	globalDir := filepath.Join(home, ".minimax")
	legacyDir := filepath.Join(home, ".mavis")
	localDir := ".minimax"

	configDir := localDir
	if global {
		configDir = globalDir
	}

	env := Environment{
		Name:      "minimax-code",
		Display:   "MiniMax Code",
		ConfigDir: configDir,
	}

	// MiniMax Code is primarily a desktop app. New releases use ~/.minimax;
	// ~/.mavis is the legacy data directory migrated by the app on startup.
	for _, dir := range []string{globalDir, legacyDir} {
		if _, err := os.Stat(dir); err == nil {
			env.Detected = true
			break
		}
	}
	for _, name := range []string{"minimax-code", "minimax", "mavis"} {
		if binPath, err := exec.LookPath(name); err == nil {
			env.Detected = true
			env.BinPath = binPath
			break
		}
	}

	skillPath := filepath.Join(configDir, "skills", "mnemon", "SKILL.md")
	if _, err := os.Stat(skillPath); err == nil {
		env.Installed = true
	}

	return env
}

func detectTrae(global bool) Environment {
	home := HomeDir()
	globalDir := filepath.Join(home, ".trae")
	localDir := ".trae"

	configDir := localDir
	if global {
		configDir = globalDir
	}

	env := Environment{
		Name:      "trae",
		Display:   "Trae",
		ConfigDir: configDir,
	}

	if binPath, err := exec.LookPath("trae"); err == nil {
		env.Detected = true
		env.BinPath = binPath
	}
	if _, err := os.Stat(globalDir); err == nil {
		env.Detected = true
	}

	skillPath := filepath.Join(configDir, "skills", "mnemon", "SKILL.md")
	hooksPath := filepath.Join(configDir, "hooks.json")
	if _, err := os.Stat(skillPath); err == nil {
		env.Installed = true
	} else if data, err := ReadJSONFile(hooksPath); err == nil && containsMnemon(data) {
		env.Installed = true
	}

	if env.BinPath != "" {
		if out, err := exec.Command(env.BinPath, "--version").Output(); err == nil {
			env.Version = cleanVersion(strings.TrimSpace(string(out)))
		}
	}

	return env
}

func detectQoder(global bool) Environment {
	home := HomeDir()
	globalDir := filepath.Join(home, ".qoder")
	localDir := ".qoder"

	configDir := localDir
	if global {
		configDir = globalDir
	}

	env := Environment{
		Name:      "qoder",
		Display:   "Qoder",
		ConfigDir: configDir,
	}

	if binPath, err := exec.LookPath("qoder"); err == nil {
		env.Detected = true
		env.BinPath = binPath
	}
	if _, err := os.Stat(globalDir); err == nil {
		env.Detected = true
	}

	skillPath := filepath.Join(configDir, "skills", "mnemon", "SKILL.md")
	settingsPath := filepath.Join(configDir, "settings.json")
	if _, err := os.Stat(skillPath); err == nil {
		env.Installed = true
	} else if data, err := ReadJSONFile(settingsPath); err == nil && containsMnemon(data) {
		env.Installed = true
	}

	if env.BinPath != "" {
		if out, err := exec.Command(env.BinPath, "--version").Output(); err == nil {
			env.Version = cleanVersion(strings.TrimSpace(string(out)))
		}
	}

	return env
}

func detectQoderWork() Environment {
	home := HomeDir()
	configDir := filepath.Join(home, ".qoderwork")

	env := Environment{
		Name:      "qoderwork",
		Display:   "QoderWork",
		ConfigDir: configDir,
	}

	if binPath, err := exec.LookPath("qoderwork"); err == nil {
		env.Detected = true
		env.BinPath = binPath
	}
	if _, err := os.Stat(configDir); err == nil {
		env.Detected = true
	}

	skillPath := filepath.Join(configDir, "skills", "mnemon", "SKILL.md")
	settingsPath := filepath.Join(configDir, "settings.json")
	if _, err := os.Stat(skillPath); err == nil {
		env.Installed = true
	} else if data, err := ReadJSONFile(settingsPath); err == nil && containsMnemon(data) {
		env.Installed = true
	}

	if env.BinPath != "" {
		if out, err := exec.Command(env.BinPath, "--version").Output(); err == nil {
			env.Version = cleanVersion(strings.TrimSpace(string(out)))
		}
	}

	return env
}

func detectCodeBuddy(global bool) Environment {
	home := HomeDir()
	globalDir := filepath.Join(home, ".codebuddy")
	localDir := ".codebuddy"

	configDir := localDir
	if global {
		configDir = globalDir
	}

	env := Environment{
		Name:      "codebuddy",
		Display:   "CodeBuddy",
		ConfigDir: configDir,
	}

	if binPath, err := exec.LookPath("codebuddy"); err == nil {
		env.Detected = true
		env.BinPath = binPath
	}
	if _, err := os.Stat(globalDir); err == nil {
		env.Detected = true
	}

	skillPath := filepath.Join(configDir, "skills", "mnemon", "SKILL.md")
	settingsPath := filepath.Join(configDir, "settings.json")
	if _, err := os.Stat(skillPath); err == nil {
		env.Installed = true
	} else if data, err := ReadJSONFile(settingsPath); err == nil && containsMnemon(data) {
		env.Installed = true
	}

	if env.BinPath != "" {
		if out, err := exec.Command(env.BinPath, "--version").Output(); err == nil {
			env.Version = cleanVersion(strings.TrimSpace(string(out)))
		}
	}

	return env
}

func detectWorkBuddy(global bool) Environment {
	home := HomeDir()
	globalDir := filepath.Join(home, ".workbuddy")
	localDir := ".workbuddy"

	configDir := localDir
	if global {
		configDir = globalDir
	}

	env := Environment{
		Name:      "workbuddy",
		Display:   "WorkBuddy",
		ConfigDir: configDir,
	}

	if binPath, err := exec.LookPath("workbuddy"); err == nil {
		env.Detected = true
		env.BinPath = binPath
	}
	if _, err := os.Stat(globalDir); err == nil {
		env.Detected = true
	}

	skillPath := filepath.Join(configDir, "skills", "mnemon", "SKILL.md")
	settingsPath := filepath.Join(configDir, "settings.json")
	if _, err := os.Stat(skillPath); err == nil {
		env.Installed = true
	} else if data, err := ReadJSONFile(settingsPath); err == nil && containsMnemon(data) {
		env.Installed = true
	}

	if env.BinPath != "" {
		if out, err := exec.Command(env.BinPath, "--version").Output(); err == nil {
			env.Version = cleanVersion(strings.TrimSpace(string(out)))
		}
	}

	return env
}

func detectKimi() Environment {
	home := HomeDir()
	configDir := filepath.Join(home, ".kimi-code")
	if envHome := strings.TrimSpace(os.Getenv("KIMI_CODE_HOME")); envHome != "" {
		configDir = envHome
	}

	env := Environment{
		Name:      "kimi",
		Display:   "Kimi Code",
		ConfigDir: configDir,
	}

	if binPath, err := exec.LookPath("kimi"); err == nil {
		env.Detected = true
		env.BinPath = binPath
	}
	if _, err := os.Stat(configDir); err == nil {
		env.Detected = true
	}

	skillPath := filepath.Join(configDir, "skills", "mnemon", "SKILL.md")
	configPath := filepath.Join(configDir, "config.toml")
	if _, err := os.Stat(skillPath); err == nil {
		env.Installed = true
	} else if data, err := os.ReadFile(configPath); err == nil && strings.Contains(string(data), "mnemon") {
		env.Installed = true
	}

	if env.BinPath != "" {
		if out, err := exec.Command(env.BinPath, "--version").Output(); err == nil {
			env.Version = cleanVersion(strings.TrimSpace(string(out)))
		}
	}

	return env
}

func detectOpenCode(global bool) Environment {
	home := HomeDir()
	globalDir := filepath.Join(home, ".config", "opencode")
	localDir := ".opencode"

	configDir := localDir
	if global {
		configDir = globalDir
	}
	if envDir := strings.TrimSpace(os.Getenv("OPENCODE_CONFIG_DIR")); envDir != "" && global {
		configDir = envDir
	}

	env := Environment{
		Name:      "opencode",
		Display:   "OpenCode",
		ConfigDir: configDir,
	}

	if binPath, err := exec.LookPath("opencode"); err == nil {
		env.Detected = true
		env.BinPath = binPath
	}
	if _, err := os.Stat(globalDir); err == nil {
		env.Detected = true
	}

	skillPath := filepath.Join(configDir, "skills", "mnemon", "SKILL.md")
	pluginPath := filepath.Join(configDir, "plugins", "mnemon.js")
	configPath := OpenCodeConfigPath(configDir)
	if _, err := os.Stat(skillPath); err == nil {
		env.Installed = true
	} else if _, err := os.Stat(pluginPath); err == nil {
		env.Installed = true
	} else if data, err := ReadJSONFile(configPath); err == nil && containsMnemon(data) {
		env.Installed = true
	}

	if env.BinPath != "" {
		if out, err := exec.Command(env.BinPath, "--version").Output(); err == nil {
			env.Version = cleanVersion(strings.TrimSpace(string(out)))
		}
	}

	return env
}

func detectOpenClaw(global bool) Environment {
	home := HomeDir()
	globalDir := filepath.Join(home, ".openclaw")
	localDir := ".openclaw"

	configDir := localDir
	if global {
		configDir = globalDir
	}

	env := Environment{
		Name:      "openclaw",
		Display:   "OpenClaw",
		ConfigDir: configDir,
	}

	// CLI detection is always global
	if binPath, err := exec.LookPath("openclaw"); err == nil {
		env.Detected = true
		env.BinPath = binPath
	}
	if _, err := os.Stat(globalDir); err == nil {
		env.Detected = true
	}

	// Check if mnemon integration is already installed at the target location
	skillPath := filepath.Join(configDir, "skills", "mnemon", "SKILL.md")
	if _, err := os.Stat(skillPath); err == nil {
		env.Installed = true
	}

	// Get version
	if env.BinPath != "" {
		if out, err := exec.Command(env.BinPath, "--version").Output(); err == nil {
			env.Version = cleanVersion(strings.TrimSpace(string(out)))
		}
	}

	return env
}

// cleanVersion strips parenthesized suffixes like "(Claude Code)" from version strings.
func cleanVersion(v string) string {
	if idx := strings.Index(v, " ("); idx > 0 {
		return v[:idx]
	}
	return v
}

func detectNanobot(global bool) Environment {
	home := HomeDir()
	globalDir := filepath.Join(home, ".nanobot", "workspace")
	localDir := ".nanobot"

	configDir := localDir
	if global {
		configDir = globalDir
	}

	env := Environment{
		Name:      "nanobot",
		Display:   "Nanobot",
		ConfigDir: configDir,
	}

	// CLI detection is always global
	if binPath, err := exec.LookPath("nanobot"); err == nil {
		env.Detected = true
		env.BinPath = binPath
	}
	if _, err := os.Stat(globalDir); err == nil {
		env.Detected = true
	}

	// Check if mnemon integration is already installed at the target location
	skillPath := filepath.Join(configDir, "skills", "mnemon", "SKILL.md")
	if _, err := os.Stat(skillPath); err == nil {
		env.Installed = true
	}

	// Get version
	if env.BinPath != "" {
		if out, err := exec.Command(env.BinPath, "--version").Output(); err == nil {
			env.Version = cleanVersion(strings.TrimSpace(string(out)))
		}
	}

	return env
}

func detectPi(global bool) Environment {
	globalDir := PiGlobalDir()
	localDir := ".pi"

	configDir := localDir
	if global {
		configDir = globalDir
	}

	env := Environment{
		Name:      "pi",
		Display:   "Pi",
		ConfigDir: configDir,
	}

	if binPath, err := exec.LookPath("pi"); err == nil {
		env.Detected = true
		env.BinPath = binPath
	}
	if _, err := os.Stat(globalDir); err == nil {
		env.Detected = true
	}

	skillPath := filepath.Join(configDir, "skills", "mnemon", "SKILL.md")
	if _, err := os.Stat(skillPath); err == nil {
		env.Installed = true
	}

	if env.BinPath != "" {
		if out, err := exec.Command(env.BinPath, "--version").Output(); err == nil {
			env.Version = cleanVersion(strings.TrimSpace(string(out)))
		}
	}

	return env
}

func detectAlma() Environment {
	configDir, _ := AlmaConfigDir(true)

	env := Environment{
		Name:      "alma",
		Display:   "Alma",
		ConfigDir: configDir,
	}

	if _, err := os.Stat("/Applications/Alma.app"); err == nil {
		env.Detected = true
	}
	if binPath, err := exec.LookPath("alma"); err == nil {
		env.Detected = true
		env.BinPath = binPath
	}

	skillPath := filepath.Join(configDir, "skills", "mnemon", "SKILL.md")
	hooksPath := filepath.Join(configDir, "hooks.json")
	if _, err := os.Stat(skillPath); err == nil {
		env.Installed = true
	} else if data, err := ReadJSONFile(hooksPath); err == nil && containsMnemon(data) {
		env.Installed = true
	}

	return env
}

func detectHermes() Environment {
	home := HomeDir()
	configDir := filepath.Join(home, ".hermes")

	env := Environment{
		Name:      "hermes",
		Display:   "Hermes Agent",
		ConfigDir: configDir,
	}

	if binPath, err := exec.LookPath("hermes"); err == nil {
		env.Detected = true
		env.BinPath = binPath
	}
	if _, err := os.Stat(configDir); err == nil {
		env.Detected = true
	}

	skillPath := filepath.Join(configDir, "skills", "mnemon", "SKILL.md")
	if _, err := os.Stat(skillPath); err == nil {
		env.Installed = true
	}

	if env.BinPath != "" {
		if out, err := exec.Command(env.BinPath, "--version").Output(); err == nil {
			env.Version = cleanVersion(strings.TrimSpace(string(out)))
		}
	}

	return env
}
