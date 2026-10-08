//go:build linux
// +build linux

package fsdiff

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	continuityfs "github.com/containerd/continuity/fs"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestGetLayerDelta(t *testing.T) {
	overlay := func(lower []string, upper string) mount.Mount {
		reversed := make([]string, len(lower))
		for i, l := range lower {
			reversed[len(lower)-1-i] = l
		}
		opts := []string{"lowerdir=" + strings.Join(reversed, ":")}
		if upper != "" {
			opts = append(opts, "upperdir="+upper, "workdir=/work")
		}
		return mount.Mount{Type: "overlay", Source: "overlay", Options: opts}
	}
	lower := []mount.Mount{overlay([]string{"/l/0", "/l/1"}, "/l/2")}

	// Several layers above lower.
	delta, err := GetLayerDelta(lower, []mount.Mount{overlay([]string{"/l/0", "/l/1", "/l/2", "/l/3"}, "/l/4")})
	require.NoError(t, err)
	require.Equal(t, LayerDelta{Upper: []string{"/l/3", "/l/4"}}, delta)

	// One layer is GetUpperdir's answer.
	delta, err = GetLayerDelta(lower, []mount.Mount{overlay([]string{"/l/0", "/l/1", "/l/2"}, "/l/3")})
	require.NoError(t, err)
	require.Equal(t, LayerDelta{Upper: []string{"/l/3"}}, delta)

	// A bind-mounted single layer below.
	delta, err = GetLayerDelta([]mount.Mount{{Type: "bind", Source: "/l/0"}}, []mount.Mount{overlay([]string{"/l/0", "/l/1"}, "/l/2")})
	require.NoError(t, err)
	require.Equal(t, LayerDelta{Upper: []string{"/l/1", "/l/2"}}, delta)

	// Siblings on a common ancestor.
	delta, err = GetLayerDelta(lower, []mount.Mount{overlay([]string{"/l/0", "/l/1", "/x/2"}, "/x/3")})
	require.NoError(t, err)
	require.Equal(t, LayerDelta{Lower: []string{"/l/2"}, Upper: []string{"/x/2", "/x/3"}}, delta)

	// An ancestor of lower.
	delta, err = GetLayerDelta(lower, []mount.Mount{overlay([]string{"/l/0"}, "/l/1")})
	require.NoError(t, err)
	require.Equal(t, LayerDelta{Lower: []string{"/l/2"}}, delta)

	// Nothing shared, or the very same layers.
	_, err = GetLayerDelta(lower, []mount.Mount{overlay([]string{"/x/0", "/l/1"}, "/l/2")})
	require.Error(t, err)
	delta, err = GetLayerDelta(lower, lower)
	require.NoError(t, err)
	require.Equal(t, LayerDelta{Same: true}, delta)
	require.False(t, delta.Empty())
	require.NoError(t, WalkLayerDeltaChanges(context.Background(), func(continuityfs.ChangeKind, string, os.FileInfo, error) error {
		t.Fatal("the same layers have no changes")
		return nil
	}, delta, t.TempDir(), t.TempDir(), CompareInodeThenContent))
}

// layerDeltaFixture builds the merged views of two snapshots and the layers
// separating them. Files unchanged between the views are the same backing
// file in both, as with overlay snapshots of one lineage.
type layerDeltaFixture struct {
	t            *testing.T
	lower, upper string
}

func (f layerDeltaFixture) write(rel, contents string, roots ...string) {
	f.t.Helper()
	for _, root := range roots {
		writeFile(f.t, root, rel, contents)
	}
}

func (f layerDeltaFixture) share(rel, contents string) {
	f.t.Helper()
	writeFile(f.t, f.lower, rel, contents)
	require.NoError(f.t, os.MkdirAll(filepath.Dir(filepath.Join(f.upper, rel)), 0o755))
	require.NoError(f.t, os.Link(filepath.Join(f.lower, rel), filepath.Join(f.upper, rel)))
}

func (f layerDeltaFixture) mark(layer, rel string) {
	f.t.Helper()
	full := filepath.Join(layer, rel)
	require.NoError(f.t, os.MkdirAll(filepath.Dir(full), 0o755))
	// A whiteout where possible; the walk classifies paths by the merged
	// views, so any entry marks the path.
	if err := unix.Mknod(full, unix.S_IFCHR, 0); err != nil {
		require.NoError(f.t, os.WriteFile(full, nil, 0o644))
	}
}

// requireSameAsWalkChanges requires the layers' walk to report exactly what a
// double walk of the merged views reports, in the same order, for every
// comparison mode.
func (f layerDeltaFixture) requireSameAsWalkChanges(delta LayerDelta) {
	t := f.t
	for _, comparison := range []Comparison{CompareContentOnMetadataMatch, CompareInodeThenContent, CompareInodeOnly} {
		t.Run(fmt.Sprint(comparison), func(t *testing.T) {
			collect := func(walk func(continuityfs.ChangeFunc) error) []string {
				var changes []string
				require.NoError(t, walk(func(kind continuityfs.ChangeKind, path string, fi os.FileInfo, err error) error {
					if err != nil {
						return err
					}
					if kind == continuityfs.ChangeKindUnmodified {
						return nil
					}
					entry := fmt.Sprintf("%s %s", kind, path)
					if fi != nil {
						entry += fmt.Sprintf(" dir=%t", fi.IsDir())
					}
					changes = append(changes, entry)
					return nil
				}))
				return changes
			}
			want := collect(func(fn continuityfs.ChangeFunc) error {
				return WalkChanges(context.Background(), f.lower, f.upper, comparison, fn)
			})
			got := collect(func(fn continuityfs.ChangeFunc) error {
				return WalkLayerDeltaChanges(context.Background(), fn, delta, f.upper, f.lower, comparison)
			})
			require.NotEmpty(t, want)
			require.Equal(t, want, got)
		})
	}
}

func TestWalkLayerDeltaChangesAbove(t *testing.T) {
	f := layerDeltaFixture{t: t, lower: t.TempDir(), upper: t.TempDir()}
	layers := []string{t.TempDir(), t.TempDir(), t.TempDir()}
	f.share("keep.txt", "same\n")
	f.share("deep/er/keep.txt", "same\n")
	f.write("twice.txt", "old\n", f.lower)
	f.write("twice.txt", "first\n", layers[0])
	f.write("twice.txt", "second\n", layers[2], f.upper)
	f.share("reverted.txt", "orig\n")
	f.write("reverted.txt", "changed\n", layers[0])
	f.write("reverted.txt", "orig\n", layers[1])
	f.write("transient/x.txt", "x\n", layers[0])
	f.mark(layers[1], "transient")
	f.write("olddir/a.txt", "a\n", f.lower)
	f.write("olddir/sub/b.txt", "b\n", f.lower)
	f.write("olddir/added.txt", "added\n", layers[0])
	f.mark(layers[1], "olddir")
	f.write("thing", "file\n", f.lower)
	f.mark(layers[0], "thing")
	f.write("thing/nested.txt", "dir now\n", layers[1], f.upper)
	f.write("was/nested.txt", "dir\n", f.lower)
	f.mark(layers[1], "was")
	f.write("was", "file now\n", layers[2], f.upper)
	f.write("fresh/a.txt", "a\n", layers[0], f.upper)
	f.write("fresh/sub/b.txt", "b\n", layers[2], f.upper)
	f.write("a-b", "sorts between a and a/b\n", layers[1], f.upper)
	f.share("a/b", "nested\n")
	f.write("a/c", "changed\n", f.lower)
	f.write("a/c", "changed!\n", layers[2], f.upper)
	require.NoError(t, os.Symlink("keep.txt", filepath.Join(f.lower, "link")))
	require.NoError(t, os.Symlink("twice.txt", filepath.Join(layers[1], "link")))
	require.NoError(t, os.Symlink("twice.txt", filepath.Join(f.upper, "link")))
	f.requireSameAsWalkChanges(LayerDelta{Upper: layers})
}

func TestWalkLayerDeltaChangesSiblings(t *testing.T) {
	f := layerDeltaFixture{t: t, lower: t.TempDir(), upper: t.TempDir()}
	lower := []string{t.TempDir(), t.TempDir()}
	upper := []string{t.TempDir(), t.TempDir()}
	f.share("keep.txt", "same\n")
	f.share("deep/keep.txt", "same\n")
	// Changed on one side only: the other keeps the shared file.
	f.share("lower-edit.txt", "shared\n")
	require.NoError(t, os.Remove(filepath.Join(f.lower, "lower-edit.txt")))
	f.write("lower-edit.txt", "lower\n", lower[0], f.lower)
	f.share("upper-edit.txt", "shared\n")
	require.NoError(t, os.Remove(filepath.Join(f.upper, "upper-edit.txt")))
	f.write("upper-edit.txt", "upper\n", upper[1], f.upper)
	// Changed on both sides, identically and differently.
	f.share("both-same.txt", "both\n")
	f.write("both-same.txt", "both\n", lower[1], upper[0])
	f.write("both-differ.txt", "lower\n", lower[0], f.lower)
	f.write("both-differ.txt", "upper\n", upper[0], f.upper)
	// Removed on the lower side: upper has the shared file.
	f.write("lower-removed.txt", "shared\n", f.upper)
	f.mark(lower[1], "lower-removed.txt")
	// A shared directory removed on the lower side: upper has all of it,
	// though no layer lists its contents.
	f.write("shareddir/a.txt", "a\n", f.upper)
	f.write("shareddir/sub/b.txt", "b\n", f.upper)
	f.mark(lower[0], "shareddir")
	// A shared directory removed on the upper side.
	f.write("gonedir/a.txt", "a\n", f.lower)
	f.mark(upper[0], "gonedir")
	// Added on one side only.
	f.write("lower-added/x.txt", "x\n", lower[1], f.lower)
	f.write("upper-added/y.txt", "y\n", upper[1], f.upper)
	// A shared file the lower side replaced by a directory.
	f.write("kind", "shared file\n", f.upper)
	f.write("kind/inner.txt", "lower dir\n", lower[0], f.lower)
	f.requireSameAsWalkChanges(LayerDelta{Lower: lower, Upper: upper})
}
