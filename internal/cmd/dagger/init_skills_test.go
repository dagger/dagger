package daggercmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInstallWorkspaceSkills(t *testing.T) {
	t.Run("no skill directories", func(t *testing.T) {
		root := t.TempDir()
		installed, err := installWorkspaceSkills(root)
		require.NoError(t, err)
		require.Empty(t, installed)
		entries, err := os.ReadDir(root)
		require.NoError(t, err)
		require.Empty(t, entries)
	})

	t.Run("all supported directories and repeat run", func(t *testing.T) {
		root := t.TempDir()
		var expected []string
		for _, dir := range workspaceSkillDirs {
			require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0o755))
			expected = append(expected, filepath.Join(root, dir, "dagger", "SKILL.md"))
		}
		// Detection must use the selected workspace, not the process cwd.
		t.Chdir(t.TempDir())
		installed, err := installWorkspaceSkills(root)
		require.NoError(t, err)
		require.Equal(t, expected, installed)
		for _, filename := range installed {
			data, err := os.ReadFile(filename)
			require.NoError(t, err)
			require.Equal(t, workspaceSkill, string(data))
			require.NotEmpty(t, data)
			require.NoError(t, os.WriteFile(filename, []byte("user edits\n"), 0o644))
		}
		installed, err = installWorkspaceSkills(root)
		require.NoError(t, err)
		require.Empty(t, installed)
		for _, filename := range expected {
			data, err := os.ReadFile(filename)
			require.NoError(t, err)
			require.Equal(t, "user edits\n", string(data))
		}
	})

	t.Run("do not create missing or nested skill directories", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(root, ".agents"), 0o755))
		nested := filepath.Join(root, "child", ".claude", "skills")
		require.NoError(t, os.MkdirAll(nested, 0o755))
		installed, err := installWorkspaceSkills(root)
		require.NoError(t, err)
		require.Empty(t, installed)
		require.NoDirExists(t, filepath.Join(root, ".agents", "skills"))
		require.NoDirExists(t, filepath.Join(nested, "dagger"))
	})

	for _, collision := range []string{".agents", ".agents/skills", ".agents/skills/dagger"} {
		t.Run("preserve file at "+collision, func(t *testing.T) {
			root := t.TempDir()
			filename := filepath.Join(root, collision)
			require.NoError(t, os.MkdirAll(filepath.Dir(filename), 0o755))
			require.NoError(t, os.WriteFile(filename, []byte("keep"), 0o644))
			installed, err := installWorkspaceSkills(root)
			require.NoError(t, err)
			require.Empty(t, installed)
			data, err := os.ReadFile(filename)
			require.NoError(t, err)
			require.Equal(t, "keep", string(data))
		})
	}

	for _, link := range []string{".agents", ".agents/skills", ".agents/skills/dagger"} {
		t.Run("skip symlink at "+link, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			filename := filepath.Join(root, link)
			require.NoError(t, os.MkdirAll(filepath.Dir(filename), 0o755))
			require.NoError(t, os.Symlink(outside, filename))
			installed, err := installWorkspaceSkills(root)
			require.NoError(t, err)
			require.Empty(t, installed)
			entries, err := os.ReadDir(outside)
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}

	t.Run("preserve incomplete custom skill", func(t *testing.T) {
		root := t.TempDir()
		skill := filepath.Join(root, ".agents", "skills", "dagger")
		require.NoError(t, os.MkdirAll(skill, 0o755))
		installed, err := installWorkspaceSkills(root)
		require.NoError(t, err)
		require.Empty(t, installed)
		require.NoFileExists(t, filepath.Join(skill, "SKILL.md"))
	})
}

func TestWorkspaceInitializedMessageWithSkills(t *testing.T) {
	message := workspaceInitializedMessage(&initState{
		ConfigPath: "/workspace/dagger.toml",
		Created:    true,
		SkillPaths: []string{"/workspace/.agents/skills/dagger/SKILL.md"},
	})
	require.Contains(t, message, "Workspace configuration initialized at ")
	require.Contains(t, message, "\nInstalled Dagger skill at /workspace/.agents/skills/dagger/SKILL.md")
	ui, view := newTestInitUI()
	ui.workspaceDone(message)
	for _, final := range []bool{false, true} {
		view.SetFinal(final)
		require.Contains(t, renderInitView(t, view), "Installed Dagger skill at /workspace/.agents/skills/dagger/SKILL.md")
	}
}
