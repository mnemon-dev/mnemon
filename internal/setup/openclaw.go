package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mnemon-dev/mnemon/internal/setup/assets"
)

// OpenClawWriteSkill writes the SKILL.md to the OpenClaw skills directory.
func OpenClawWriteSkill(configDir string) (string, error) {
	skillDir := filepath.Join(configDir, "skills", "mnemon")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return "", err
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, assets.OpenClawSkill, 0644); err != nil {
		return "", err
	}
	return skillPath, nil
}

// OpenClawWriteHook writes the mnemon-prime internal hook to the OpenClaw hooks directory.
func OpenClawWriteHook(configDir string) (string, error) {
	hookDir := filepath.Join(configDir, "hooks", "mnemon-prime")
	if err := os.MkdirAll(hookDir, 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(hookDir, "HOOK.md"), assets.OpenClawHookMD, 0644); err != nil {
		return "", err
	}
	handlerPath := filepath.Join(hookDir, "handler.js")
	if err := os.WriteFile(handlerPath, assets.OpenClawHookHandler, 0644); err != nil {
		return "", err
	}
	return hookDir, nil
}

// OpenClawWritePlugin writes the mnemon plugin to the OpenClaw extensions directory.
// ver is the mnemon version string (e.g. from ldflags); it replaces the
// embedded manifest's static version field so the installed plugin always
// reflects the running binary.
func OpenClawWritePlugin(configDir, ver string) (string, error) {
	pluginDir := filepath.Join(configDir, "extensions", "mnemon")
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		return "", err
	}

	// Patch manifest version when a real version is available.
	manifest := assets.OpenClawPluginManifest
	if ver != "" && ver != "dev" {
		var m map[string]any
		if err := json.Unmarshal(manifest, &m); err == nil {
			m["version"] = ver
			if patched, err := json.MarshalIndent(m, "", "  "); err == nil {
				manifest = append(patched, '\n')
			}
		}
	}

	files := []struct {
		name string
		data []byte
	}{
		{"package.json", assets.OpenClawPluginPackage},
		{"openclaw.plugin.json", manifest},
		{"index.js", assets.OpenClawPluginIndex},
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(pluginDir, f.name), f.data, 0644); err != nil {
			return "", err
		}
	}
	return pluginDir, nil
}

// OpenClawRegisterPlugin adds the mnemon plugin entry to openclaw.json,
// recording which optional hooks the user selected.
func OpenClawRegisterPlugin(configDir string, sel HookSelection) (string, error) {
	cfgPath := filepath.Join(configDir, "openclaw.json")

	var cfg map[string]any
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			cfg = make(map[string]any)
		} else {
			return "", err
		}
	} else {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return "", fmt.Errorf("parse openclaw.json: %w", err)
		}
	}

	plugins, _ := cfg["plugins"].(map[string]any)
	if plugins == nil {
		plugins = make(map[string]any)
	}
	entries, _ := plugins["entries"].(map[string]any)
	if entries == nil {
		entries = make(map[string]any)
	}

	// Always write the entry so config reflects current selection.
	entries["mnemon"] = map[string]any{
		"enabled": true,
		"config": map[string]any{
			"remind":  sel.Remind,
			"nudge":   sel.Nudge,
			"compact": sel.Compact,
		},
	}
	plugins["entries"] = entries
	cfg["plugins"] = plugins

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(cfgPath, append(out, '\n'), 0600); err != nil {
		return "", err
	}

	return cfgPath, nil
}

// OpenClawEject removes mnemon skill, hook, and plugin from the given OpenClaw config dir.
func OpenClawEject(configDir string) []error {
	var errs []error

	fmt.Printf("\nRemoving OpenClaw integration (%s)...\n", configDir)

	targets := []struct {
		label string
		path  string
	}{
		{"Skill", filepath.Join(configDir, "skills", "mnemon")},
		{"Hook", filepath.Join(configDir, "hooks", "mnemon-prime")},
		{"Plugin", filepath.Join(configDir, "extensions", "mnemon")},
	}

	total := len(targets)
	for i, t := range targets {
		if err := os.RemoveAll(t.path); err != nil {
			StatusError(i+1, total, t.label, err)
			errs = append(errs, err)
		} else {
			StatusOK(i+1, total, t.label, t.path+" removed")
		}
	}

	// Clean up empty parent dirs
	removeIfEmpty(filepath.Join(configDir, "skills"))
	removeIfEmpty(filepath.Join(configDir, "hooks"))
	removeIfEmpty(filepath.Join(configDir, "extensions"))

	if err := removeOpenClawPluginEntry(filepath.Join(configDir, "openclaw.json")); err != nil {
		StatusError(total+1, total+1, "Config", err)
		errs = append(errs, err)
	}

	removeIfEmpty(configDir)
	return errs
}

func removeOpenClawPluginEntry(cfgPath string) error {
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse openclaw.json: %w", err)
	}

	plugins, ok := cfg["plugins"].(map[string]any)
	if !ok {
		return nil
	}
	entries, ok := plugins["entries"].(map[string]any)
	if !ok {
		return nil
	}

	if _, ok := entries["mnemon"]; !ok {
		return nil
	}
	delete(entries, "mnemon")
	if len(entries) == 0 {
		delete(plugins, "entries")
	} else {
		plugins["entries"] = entries
	}
	if len(plugins) == 0 {
		delete(cfg, "plugins")
	} else {
		cfg["plugins"] = plugins
	}

	if len(cfg) == 0 {
		if err := os.Remove(cfgPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cfgPath, append(out, '\n'), 0600)
}
