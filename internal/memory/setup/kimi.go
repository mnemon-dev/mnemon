package setup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mnemon-dev/mnemon/internal/memory/setup/assets"
)

// KimiWriteSkill writes the mnemon skill to the Kimi Code skills directory.
func KimiWriteSkill(configDir string) (string, error) {
	skillDir := filepath.Join(configDir, "skills", "mnemon")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return "", err
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, assets.KimiSkill, 0644); err != nil {
		return "", err
	}
	return skillPath, nil
}

// KimiWriteHook writes a hook script to the Kimi Code hooks directory.
func KimiWriteHook(configDir, filename string, content []byte) (string, error) {
	hooksDir := filepath.Join(configDir, "hooks", "mnemon")
	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		return "", err
	}
	hookPath := filepath.Join(hooksDir, filename)
	if err := writeExecutableFile(hookPath, content); err != nil {
		return "", err
	}
	return hookPath, nil
}

// KimiRegisterHooks registers Mnemon lifecycle hooks in config.toml.
func KimiRegisterHooks(configDir string) (string, error) {
	hooksDir := filepath.Join(configDir, "hooks", "mnemon")
	absHooksDir, err := filepath.Abs(hooksDir)
	if err != nil {
		return "", err
	}
	configPath := filepath.Join(configDir, "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}

	updated := addKimiHooks(string(data), absHooksDir, runtime.GOOS, kimiWindowsBash)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return "", err
	}
	tmp := configPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(updated), 0644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, configPath); err != nil {
		return "", err
	}
	return configPath, nil
}

// KimiEject removes mnemon skill and hooks from the given Kimi Code config dir.
func KimiEject(configDir string) []error {
	var errs []error

	fmt.Printf("\nRemoving Kimi Code integration (%s)...\n", configDir)

	hooksDir := filepath.Join(configDir, "hooks", "mnemon")
	if err := os.RemoveAll(hooksDir); err != nil {
		StatusError(1, 3, "Hooks", err)
		errs = append(errs, err)
	} else {
		StatusOK(1, 3, "Hooks", hooksDir+" removed")
	}
	removeIfEmpty(filepath.Join(configDir, "hooks"))

	configPath := filepath.Join(configDir, "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if !os.IsNotExist(err) {
			StatusError(2, 3, "Config", err)
			errs = append(errs, err)
		}
	} else {
		cleaned := removeKimiHooks(string(data))
		if strings.TrimSpace(cleaned) == "" {
			if err := os.Remove(configPath); err != nil && !os.IsNotExist(err) {
				StatusError(2, 3, "Config", err)
				errs = append(errs, err)
			} else {
				StatusOK(2, 3, "Config", configPath+" removed")
			}
		} else if err := os.WriteFile(configPath, []byte(cleaned), 0644); err != nil {
			StatusError(2, 3, "Config", err)
			errs = append(errs, err)
		} else {
			StatusOK(2, 3, "Config", configPath+" cleaned")
		}
	}

	skillDir := filepath.Join(configDir, "skills", "mnemon")
	if err := os.RemoveAll(skillDir); err != nil {
		StatusError(3, 3, "Skill", err)
		errs = append(errs, err)
	} else {
		StatusOK(3, 3, "Skill", skillDir+" removed")
	}
	removeIfEmpty(filepath.Join(configDir, "skills"))
	removeIfEmpty(configDir)

	return errs
}

func addKimiHooks(config, hooksDir, goos string, findBash func() string) string {
	cleaned := strings.TrimRight(removeKimiHooks(config), "\n")
	bash := ""
	if goos == "windows" {
		bash = findBash()
	}
	blocks := []string{
		kimiHookBlock("SessionStart", "", kimiHookCommand(hooksDir, "prime.sh", goos, bash)),
		kimiHookBlock("UserPromptSubmit", "", kimiHookCommand(hooksDir, "user_prompt.sh", goos, bash)),
		kimiHookBlock("Stop", "", kimiHookCommand(hooksDir, "stop.sh", goos, bash)),
	}
	if cleaned == "" {
		return strings.Join(blocks, "\n\n") + "\n"
	}
	return cleaned + "\n\n" + strings.Join(blocks, "\n\n") + "\n"
}

// kimiHookCommand returns the config.toml command for a hook script. Kimi Code
// on Windows runs the command natively, so a bare .sh path either fails under
// cmd or opens the .sh file association (a Git Bash window). Run it through an
// explicit bash.exe instead.
func kimiHookCommand(hooksDir, filename, goos, bash string) string {
	if goos != "windows" {
		return filepath.Join(hooksDir, filename)
	}
	script := strings.TrimRight(hooksDir, `\/`) + `\` + filename
	return `"` + bash + `" "` + script + `"`
}

// kimiWindowsBash locates the Git for Windows bash.exe. It deliberately skips
// the System32 bash.exe, which is the WSL launcher and cannot run these scripts.
func kimiWindowsBash() string {
	var candidates []string
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "ProgramW6432"} {
		if root := os.Getenv(env); root != "" {
			candidates = append(candidates, filepath.Join(root, "Git", "bin", "bash.exe"))
		}
	}
	if local := os.Getenv("LocalAppData"); local != "" {
		candidates = append(candidates, filepath.Join(local, "Programs", "Git", "bin", "bash.exe"))
	}
	if git, err := exec.LookPath("git"); err == nil {
		// <root>/cmd/git.exe or <root>/bin/git.exe -> <root>/bin/bash.exe
		candidates = append(candidates, filepath.Join(filepath.Dir(filepath.Dir(git)), "bin", "bash.exe"))
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c
		}
	}
	return "bash.exe"
}

func kimiHookBlock(event, matcher, command string) string {
	lines := []string{
		"[[hooks]]",
		fmt.Sprintf("event = %q", event),
		fmt.Sprintf("command = %q", command),
		"timeout = 10",
	}
	if matcher != "" {
		lines = append(lines[:2], append([]string{fmt.Sprintf("matcher = %q", matcher)}, lines[2:]...)...)
	}
	return strings.Join(lines, "\n")
}

func removeKimiHooks(config string) string {
	if strings.TrimSpace(config) == "" {
		return ""
	}
	lines := strings.SplitAfter(config, "\n")
	var out strings.Builder
	for i := 0; i < len(lines); {
		trimmed := strings.TrimSpace(strings.TrimRight(lines[i], "\n"))
		if trimmed != "[[hooks]]" {
			out.WriteString(lines[i])
			i++
			continue
		}

		start := i
		i++
		for i < len(lines) {
			next := strings.TrimSpace(strings.TrimRight(lines[i], "\n"))
			if strings.HasPrefix(next, "[") {
				break
			}
			i++
		}
		block := strings.Join(lines[start:i], "")
		if containsMnemon(block) {
			continue
		}
		out.WriteString(block)
	}
	return collapseExcessBlankLines(out.String())
}

func collapseExcessBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	blank := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			blank++
			if blank > 2 {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, line)
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"
}
