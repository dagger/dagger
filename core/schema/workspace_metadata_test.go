package schema

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/internal/fsutil"
	fstypes "github.com/dagger/dagger/internal/fsutil/types"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceMetadataPathResolvesLinksBeforeParent(t *testing.T) {
	for _, target := range []string{"rules/link/../ignore", "/rules/link/../ignore"} {
		t.Run(target, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(root, "config/subdir"), 0755))
			require.NoError(t, os.Mkdir(filepath.Join(root, "rules"), 0755))
			require.NoError(t, os.WriteFile(filepath.Join(root, "config/ignore"), []byte("correct\n"), 0644))
			require.NoError(t, os.WriteFile(filepath.Join(root, "rules/ignore"), []byte("wrong\n"), 0644))
			require.NoError(t, os.Symlink("../config/subdir", filepath.Join(root, "rules/link")))
			require.NoError(t, os.Symlink(target, filepath.Join(root, ".gitignore")))
			resolved, err := resolveWorkspaceMetadataPath(".gitignore", func(p string) (*fstypes.Stat, error) {
				return fsutil.Stat(filepath.Join(root, p))
			})
			require.NoError(t, err)
			require.Equal(t, "config/ignore", resolved.file)
			require.Contains(t, resolved.dirs, "config/subdir")
			require.Equal(t, "config/ignore", resolved.links[0].target)
		})
	}
}

func TestWorkspaceMetadataPathRejectsInvalidTraversal(t *testing.T) {
	for _, target := range []string{"../outside", "file/../file", "cycle"} {
		t.Run(target, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, "file"), nil, 0644))
			require.NoError(t, os.Symlink("cycle", filepath.Join(root, "cycle")))
			require.NoError(t, os.Symlink(target, filepath.Join(root, ".gitignore")))
			_, err := resolveWorkspaceMetadataPath(".gitignore", func(p string) (*fstypes.Stat, error) {
				return fsutil.Stat(filepath.Join(root, p))
			})
			require.Error(t, err)
		})
	}
}
