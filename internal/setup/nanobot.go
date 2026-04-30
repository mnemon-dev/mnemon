package setup

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mnemon-dev/mnemon/internal/setup/assets"
)

// NanobotWriteSkill writes the SKILL.md to the nanobot skills directory.
func NanobotWriteSkill(configDir string) (string, error) {
	skillDir := filepath.Join(configDir, "skills", "mnemon")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return "", err
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, assets.NanobotSkill, 0644); err != nil {
		return "", err
	}
	return skillPath, nil
}

// NanobotEject removes mnemon skill from the given nanobot config dir.
func NanobotEject(configDir string) []error {
	var errs []error

	fmt.Printf("\nRemoving nanobot integration (%s)...\n", configDir)

	// Remove skill directory
	skillDir := filepath.Join(configDir, "skills", "mnemon")
	if err := os.RemoveAll(skillDir); err != nil {
		StatusError(1, 1, "Skill", err)
		errs = append(errs, err)
	} else {
		StatusOK(1, 1, "Skill", skillDir+" removed")
	}
	removeIfEmpty(filepath.Join(configDir, "skills"))

	// Clean up configDir itself if empty
	removeIfEmpty(configDir)

	return errs
}
