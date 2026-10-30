package fsxutil

import (
	"os"
	"path/filepath"
	"testing"

	continuityfs "github.com/containerd/continuity/fs"
	"github.com/stretchr/testify/require"
)

// TestRootPathResolvesAsContinuity checks RootPath against continuity's walk,
// for paths with symlinks and without, and under a root reached through one.
func TestRootPathResolvesAsContinuity(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	root := filepath.Join(tmp, "root")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "a", "b", "dir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a", "b", "file"), []byte("x"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "other", "deep"), 0o755))
	for link, target := range map[string]string{
		"a/deep":       "/other/deep",
		"a/rel":        "b",
		"a/abs":        "/a/b",
		"a/up":         "../../../a",
		"a/b/flink":    "file",
		"a/b/dangling": "missing",
	} {
		require.NoError(t, os.Symlink(target, filepath.Join(root, link)))
	}
	linkedRoot := filepath.Join(tmp, "linked")
	require.NoError(t, os.Symlink(root, linkedRoot))

	cwd, err := os.Getwd()
	require.NoError(t, err)
	relRoot, err := filepath.Rel(cwd, root)
	require.NoError(t, err)

	for _, r := range []string{root, root + "/", linkedRoot, relRoot} {
		for _, p := range []string{
			"",
			"/",
			"a",
			"/a/b/file",
			"a//b/./dir/",
			"../a/b/file",
			"a/b/missing",
			"a/missing/deeper/file",
			"a/b/file/under",
			"a/rel/file",
			"a/abs/file",
			"a/up/b/file",
			"a/b/flink",
			"a/b/dangling",
			"a/rel",
			"a/deep/../file",
			"a/deep/..",
			"a/b/file/x/../../dir",
			"a/b/../b/file",
		} {
			want, wantErr := continuityfs.RootPath(r, p)
			got, gotErr := RootPath(r, p)
			if wantErr != nil {
				require.EqualError(t, gotErr, wantErr.Error(), "%s %s", r, p)
				continue
			}
			require.NoError(t, gotErr, "%s %s", r, p)
			require.Equal(t, want, got, "%s %s", r, p)
		}
	}
}

// TestRootPathUnderEmptyRoot checks RootPath against continuity's walk for an
// empty root, which resolves from the filesystem's root.
func TestRootPathUnderEmptyRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "a"), 0o755))
	for _, p := range []string{root + "/a", root + "/missing", root + "/a/../a"} {
		want, wantErr := continuityfs.RootPath("", p)
		require.NoError(t, wantErr, p)
		got, err := RootPath("", p)
		require.NoError(t, err, p)
		require.Equal(t, want, got, p)
	}
}

// TestRootPathWithoutSymlinksTakesOneLookup checks that a path without
// symlinks isn't walked a component at a time: the walk allocates several
// objects per component.
func TestRootPathWithoutSymlinksTakesOneLookup(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "a", "b", "c", "d"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a", "b", "c", "d", "file"), []byte("x"), 0o644))

	for _, p := range []string{"/a/b/c/d/file", "/a/b/c/d/missing"} {
		want := filepath.Join(root, p)
		allocs := testing.AllocsPerRun(100, func() {
			got, err := RootPath(root, p)
			if err != nil || got != want {
				t.Fatalf("RootPath(%q) = %q, %v", p, got, err)
			}
		})
		require.LessOrEqual(t, allocs, 3.0, p)
	}
}
