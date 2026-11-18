package daggercmd

import (
	"context"
	"path"

	"dagger.io/dagger/core"
)

func recommendDaggerSkill(ctx context.Context, ws *core.Workspace) ([]string, error) {
	dir := recommendationDirectory(ws)
	var matches []string
	for _, skillDir := range []string{
		".agents/skills", ".claude/skills", ".cursor/skills",
		".gemini/skills", ".github/skills", ".opencode/skills",
	} {
		// Check both components without following symbolic links.
		parent, err := dir.Exists(ctx, path.Dir(skillDir), core.DirectoryExistsOpts{
			ExpectedType: core.ExistsTypeDirectoryType, DoNotFollowSymlinks: true,
		})
		if err != nil {
			return nil, err
		}
		if !parent {
			continue
		}
		exists, err := dir.Exists(ctx, skillDir, core.DirectoryExistsOpts{
			ExpectedType: core.ExistsTypeDirectoryType, DoNotFollowSymlinks: true,
		})
		if err != nil {
			return nil, err
		}
		if exists {
			matches = append(matches, skillDir)
		}
	}
	return matches, nil
}
