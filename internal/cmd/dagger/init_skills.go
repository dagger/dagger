package daggercmd

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed skills/dagger/SKILL.md
var workspaceSkill string

// Only use existing project skill directories. Do not create agent settings
// or search user-level directories outside the workspace.
var workspaceSkillDirs = []string{
	".agents/skills",
	".claude/skills",
	".cursor/skills",
	".gemini/skills",
	".github/skills",
	".opencode/skills",
}

// installWorkspaceSkills runs after workspace export has validated the local
// Git workspace. Root confines all file operations to that workspace. Skip
// symbolic links and existing dagger entries, including user-authored skills.
func installWorkspaceSkills(rootPath string) ([]string, error) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	var installed []string
skillDirs:
	for _, dir := range workspaceSkillDirs {
		for _, component := range []string{filepath.Dir(dir), dir} {
			info, err := root.Lstat(component)
			if errors.Is(err, os.ErrNotExist) || (err == nil && !info.IsDir()) {
				continue skillDirs
			}
			if err != nil {
				return installed, fmt.Errorf("inspect skill directory %s: %w", component, err)
			}
		}
		skillDir := filepath.Join(dir, "dagger")
		// Mkdir claims a new skill directory without replacing any existing
		// file, directory, or symlink at this path.
		if err := root.Mkdir(skillDir, 0o755); errors.Is(err, os.ErrExist) {
			continue
		} else if err != nil {
			return installed, fmt.Errorf("create skill directory %s: %w", skillDir, err)
		}
		filename := filepath.Join(skillDir, "SKILL.md")
		f, err := root.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return installed, fmt.Errorf("create skill %s: %w", filename, err)
		}
		_, writeErr := f.WriteString(workspaceSkill)
		if err := errors.Join(writeErr, f.Close()); err != nil {
			return installed, fmt.Errorf("write skill %s: %w", filename, err)
		}
		installed = append(installed, filepath.Join(rootPath, filename))
	}
	return installed, nil
}
