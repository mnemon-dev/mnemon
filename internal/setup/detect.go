package setup

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Environment describes a detected LLM CLI environment.
type Environment struct {
	Name      string // "claude-code", "openclaw"
	Display   string // "Claude Code", "OpenClaw"
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
// When global is false, ConfigDir points to project-local config (.claude/);
// when true, it points to the user-global config (~/.claude/).
func DetectEnvironments(global bool) []Environment {
	return []Environment{
		detectClaude(global),
		detectOpenClaw(global),
		detectNanobot(global),
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
