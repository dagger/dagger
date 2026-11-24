package daggercmd

import (
	"testing"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/stretchr/testify/require"
)

func TestRecommendDaggerSkill(t *testing.T) {
	ctx := t.Context()
	dag, err := dagger.Connect(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, dag.Close()) })
	root := core.NewQuery(dag).Directory()
	for _, dir := range []string{".agents/skills", ".claude/skills", ".cursor/skills", ".gemini/skills", ".github/skills", ".opencode/skills"} {
		t.Run(dir, func(t *testing.T) {
			ws := root.WithNewDirectory(dir).AsWorkspace()
			matches, err := recommendDaggerSkill(ctx, ws)
			require.NoError(t, err)
			require.Equal(t, []string{dir}, matches)
		})
	}
	for name, dir := range map[string]*core.Directory{
		"missing":       root,
		"file":          root.WithNewFile(".agents/skills", ""),
		"nested":        root.WithNewDirectory("app/.agents/skills"),
		"linked parent": root.WithNewDirectory("other/skills").WithSymlink("other", ".agents"),
		"linked skills": root.WithNewDirectory("other").WithSymlink("../other", ".agents/skills"),
	} {
		t.Run(name, func(t *testing.T) {
			matches, err := recommendDaggerSkill(ctx, dir.AsWorkspace())
			require.NoError(t, err)
			require.Empty(t, matches)
		})
	}
}
