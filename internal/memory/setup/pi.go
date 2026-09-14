package setup

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mnemon-dev/mnemon/internal/memory/setup/assets"
)

// PiWritePromptFiles keeps Pi's tool and lifecycle guidance separate from the
// shared Claude prompts when both integrations use the same memory directory.
func PiWritePromptFiles() (string, error) {
	dir, err := promptDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "pi")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "guide.md"), assets.PiGuide, 0644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "skill.md"), assets.PiSkill, 0644); err != nil {
		return "", err
	}
	return dir, nil
}

// PiWriteSkill writes the mnemon skill to the Pi skills directory.
func PiWriteSkill(configDir string) (string, error) {
	skillDir := filepath.Join(configDir, "skills", "mnemon")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return "", err
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, assets.PiSkill, 0644); err != nil {
		return "", err
	}
	return skillPath, nil
}

// PiWriteExtension writes the Mnemon lifecycle extension to Pi.
func PiWriteExtension(configDir string) (string, error) {
	extDir := filepath.Join(configDir, "extensions")
	if err := os.MkdirAll(extDir, 0755); err != nil {
		return "", err
	}
	extPath := filepath.Join(extDir, "mnemon.ts")
	if err := os.WriteFile(extPath, assets.PiExtension, 0644); err != nil {
		return "", err
	}
	return extPath, nil
}

// PiEject removes mnemon skill and extension from the given Pi config dir.
func PiEject(configDir string) []error {
	var errs []error

	fmt.Printf("\nRemoving Pi integration (%s)...\n", configDir)

	targets := []struct {
		label string
		path  string
	}{
		{"Extension", filepath.Join(configDir, "extensions", "mnemon.ts")},
		{"Skill", filepath.Join(configDir, "skills", "mnemon")},
	}

	for i, target := range targets {
		if err := os.RemoveAll(target.path); err != nil {
			StatusError(i+1, len(targets), target.label, err)
			errs = append(errs, err)
		} else {
			StatusOK(i+1, len(targets), target.label, target.path+" removed")
		}
	}

	removeIfEmpty(filepath.Join(configDir, "extensions"))
	removeIfEmpty(filepath.Join(configDir, "skills"))
	removeIfEmpty(configDir)

	return errs
}
