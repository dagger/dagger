//go:build linux

package layercopy

import (
	"os"
	"path/filepath"
	"testing"

	continuityfs "github.com/containerd/continuity/fs"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// An overlay directory is opaque only when its opaque attribute is the one
// byte "y": a longer value, or another byte, isn't the marker.
func TestOverlayEntryStateOpaqueMarker(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		value  string
		opaque bool
	}{
		{"y", true},
		{"n", false},
		{"yy", false},
		{"yes", false},
	} {
		dir := t.TempDir()
		require.NoError(t, unix.Lsetxattr(dir, "user.overlay.opaque", []byte(test.value), 0))
		isDir, hidesLower, err := overlayEntryState(dir)
		require.NoError(t, err, test.value)
		require.True(t, isDir, test.value)
		require.Equal(t, test.opaque, hidesLower, "opaque value %q", test.value)
	}
	isDir, hidesLower, err := overlayEntryState(t.TempDir())
	require.NoError(t, err)
	require.True(t, isDir)
	require.False(t, hidesLower, "no opaque value")
}

// TestRootPathResolvesAsContinuity checks rootPath against continuity's walk,
// for paths with symlinks and without, and under a root reached through one.
func TestRootPathResolvesAsContinuity(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	root := filepath.Join(tmp, "root")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "a", "b", "dir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a", "b", "file"), []byte("x"), 0o644))
	for link, target := range map[string]string{
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

	// want is rootPath as it was, through continuity's walk alone.
	want := func(root, p string, followFinalSymlink bool) (string, error) {
		p = filepath.Join("/", p)
		if p == "/" {
			return root, nil
		}
		if followFinalSymlink {
			return continuityfs.RootPath(root, p)
		}
		dir, base := filepath.Split(p)
		resolvedDir, err := continuityfs.RootPath(root, dir)
		if err != nil {
			return "", err
		}
		return filepath.Join(resolvedDir, base), nil
	}

	for _, r := range []string{root, linkedRoot} {
		for _, p := range []string{
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
		} {
			for _, follow := range []bool{true, false} {
				wantPath, wantErr := want(r, p, follow)
				gotPath, gotErr := rootPath(r, p, follow)
				if wantErr != nil {
					require.Error(t, gotErr, "%s %s follow=%v", r, p, follow)
					continue
				}
				require.NoError(t, gotErr, "%s %s follow=%v", r, p, follow)
				require.Equal(t, wantPath, gotPath, "%s %s follow=%v", r, p, follow)
			}
		}
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
			got, err := rootPath(root, p, true)
			if err != nil || got != want {
				t.Fatalf("rootPath(%q) = %q, %v", p, got, err)
			}
		})
		require.LessOrEqual(t, allocs, 3.0, p)
	}
}
