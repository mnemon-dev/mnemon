package setup

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Environment describes a detected LLM CLI environment.
type Environment struct {
	Name      string // "claude-code", "codex", "cursor", "trae", "qoder", "qoderwork", "codebuddy", "workbuddy", "openclaw", "nanobot", "pi", "hermes"
	Display   string // "Claude Code", "Codex", "Cursor", "Trae", "Qoder", "QoderWork", "CodeBuddy", "WorkBuddy", "OpenClaw", "Nanobot", "Pi", "Hermes Agent"
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
		detectTrae(global),
		detectQoder(global),
		detectQoderWork(),
		detectCodeBuddy(global),
		detectWorkBuddy(global),
		detectOpenClaw(global),
		detectNanobot(global),
		detectPi(global),
		detectHermes(),
	}
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
	home := HomeDir()
	globalDir := filepath.Join(home, ".pi", "agent")
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
