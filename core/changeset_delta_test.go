package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/snapshots/fsdiff"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func writeDeltaTestFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	p := filepath.Join(root, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(contents), 0o644))
}

// requireSamePaths asserts the delta implementation and the git full-tree
// implementation agree on the given before/after trees.
func requireSamePaths(t *testing.T, beforeDir, afterDir string) *ChangesetPaths {
	t.Helper()
	ctx := context.Background()

	gitPaths, err := computeChangesetPaths(ctx, beforeDir, afterDir)
	require.NoError(t, err)
	deltaPaths, _, err := computeChangesetPathsDelta(ctx, beforeDir, afterDir, nil, true)
	require.NoError(t, err)

	require.ElementsMatch(t, gitPaths.Added, deltaPaths.Added, "Added")
	require.ElementsMatch(t, gitPaths.Modified, deltaPaths.Modified, "Modified")
	require.ElementsMatch(t, gitPaths.Removed, deltaPaths.Removed, "Removed")
	require.ElementsMatch(t, gitPaths.AllRemoved, deltaPaths.AllRemoved, "AllRemoved")
	require.Equal(t, gitPaths.Renamed, deltaPaths.Renamed, "Renamed")

	// The stats-less variant stages fewer files but must report the same paths.
	deltaPathsNoStats, _, err := computeChangesetPathsDelta(ctx, beforeDir, afterDir, nil, false)
	require.NoError(t, err)
	require.Equal(t, deltaPaths, deltaPathsNoStats, "withStats=false paths")

	// The IsEmpty fast path must agree with whether git sees any file-level
	// change (renames included; directory-only changes don't count).
	isFile := func(p string) bool { return !strings.HasSuffix(p, "/") }
	gitEmpty := len(gitPaths.Modified) == 0 &&
		!slices.ContainsFunc(gitPaths.Added, isFile) &&
		!slices.ContainsFunc(gitPaths.AllRemoved, isFile)
	deltaEmpty, err := changesetDeltaIsEmpty(ctx, beforeDir, afterDir, nil)
	require.NoError(t, err)
	require.Equal(t, gitEmpty, deltaEmpty, "IsEmpty")

	return deltaPaths
}

func requireSameNumStat(t *testing.T, beforeDir, afterDir string) {
	t.Helper()
	ctx := context.Background()

	gitStats, err := compareDirectoriesNumStat(ctx, beforeDir, afterDir)
	require.NoError(t, err)
	_, deltaStats, err := computeChangesetPathsDelta(ctx, beforeDir, afterDir, nil, true)
	require.NoError(t, err)
	// git omits nothing; delta may omit zero-value entries. Compare as maps
	// treating missing == zero.
	for path, gs := range gitStats {
		require.Equal(t, gs, deltaStats[path], "numstat for %s", path)
	}
	for path, ds := range deltaStats {
		if _, ok := gitStats[path]; !ok {
			require.Equal(t, lineChanges{}, ds, "extra numstat for %s", path)
		}
	}
}

func TestChangesetDeltaMatchesGit(t *testing.T) {
	t.Run("add modify remove", func(t *testing.T) {
		before := t.TempDir()
		after := t.TempDir()
		writeDeltaTestFile(t, before, "keep.txt", "same\n")
		writeDeltaTestFile(t, after, "keep.txt", "same\n")
		writeDeltaTestFile(t, before, "mod.txt", "old\n")
		writeDeltaTestFile(t, after, "mod.txt", "new\nnew2\n")
		writeDeltaTestFile(t, before, "remove.txt", "bye\nbye\n")
		writeDeltaTestFile(t, after, "add.txt", "hi\n")

		requireSamePaths(t, before, after)
		requireSameNumStat(t, before, after)
	})

	t.Run("pure rename", func(t *testing.T) {
		before := t.TempDir()
		after := t.TempDir()
		writeDeltaTestFile(t, before, "old.txt", "content of a decently sized file\nwith more than one line\n")
		writeDeltaTestFile(t, after, "new.txt", "content of a decently sized file\nwith more than one line\n")

		paths := requireSamePaths(t, before, after)
		require.Equal(t, map[string]string{"new.txt": "old.txt"}, paths.Renamed)
		requireSameNumStat(t, before, after)
	})

	t.Run("nested removed dir", func(t *testing.T) {
		before := t.TempDir()
		after := t.TempDir()
		writeDeltaTestFile(t, before, "keep.txt", "same\n")
		writeDeltaTestFile(t, after, "keep.txt", "same\n")
		writeDeltaTestFile(t, before, "gone/a.txt", "a\n")
		writeDeltaTestFile(t, before, "gone/sub/b.txt", "b\n")

		requireSamePaths(t, before, after)
		requireSameNumStat(t, before, after)
	})

	t.Run("added dir tree", func(t *testing.T) {
		before := t.TempDir()
		after := t.TempDir()
		writeDeltaTestFile(t, before, "keep.txt", "same\n")
		writeDeltaTestFile(t, after, "keep.txt", "same\n")
		writeDeltaTestFile(t, after, "fresh/a.txt", "a\n")
		writeDeltaTestFile(t, after, "fresh/sub/b.txt", "b\n")

		requireSamePaths(t, before, after)
		requireSameNumStat(t, before, after)
	})

	t.Run("empty before", func(t *testing.T) {
		before := t.TempDir()
		after := t.TempDir()
		writeDeltaTestFile(t, after, "a.txt", "a\n")
		writeDeltaTestFile(t, after, "sub/b.txt", "b1\nb2\nb3")

		requireSamePaths(t, before, after)
		requireSameNumStat(t, before, after)
	})

	t.Run("mtime-only change is not modified", func(t *testing.T) {
		before := t.TempDir()
		after := t.TempDir()
		writeDeltaTestFile(t, before, "f.txt", "same\n")
		writeDeltaTestFile(t, after, "f.txt", "same\n")
		past := time.Now().Add(-time.Hour)
		require.NoError(t, os.Chtimes(filepath.Join(after, "f.txt"), past, past))

		requireSamePaths(t, before, after)
	})

	t.Run("same size same mtime different content", func(t *testing.T) {
		// The pathological stat collision: same size, same mtime (with
		// non-zero nanoseconds, as fast successive writes can produce),
		// different bytes. Must be detected via content comparison.
		before := t.TempDir()
		after := t.TempDir()
		writeDeltaTestFile(t, before, "f.txt", "aaaa\n")
		writeDeltaTestFile(t, after, "f.txt", "bbbb\n")
		ts := time.Unix(1700000000, 123456789)
		require.NoError(t, os.Chtimes(filepath.Join(before, "f.txt"), ts, ts))
		require.NoError(t, os.Chtimes(filepath.Join(after, "f.txt"), ts, ts))

		requireSamePaths(t, before, after)
		requireSameNumStat(t, before, after)
	})

	t.Run("exec bit change", func(t *testing.T) {
		before := t.TempDir()
		after := t.TempDir()
		writeDeltaTestFile(t, before, "f.sh", "#!/bin/sh\n")
		writeDeltaTestFile(t, after, "f.sh", "#!/bin/sh\n")
		require.NoError(t, os.Chmod(filepath.Join(after, "f.sh"), 0o755))

		requireSamePaths(t, before, after)
	})

	t.Run("symlink target change", func(t *testing.T) {
		before := t.TempDir()
		after := t.TempDir()
		writeDeltaTestFile(t, before, "t1", "x\n")
		writeDeltaTestFile(t, after, "t1", "x\n")
		require.NoError(t, os.Symlink("t1", filepath.Join(before, "link")))
		require.NoError(t, os.Symlink("t2", filepath.Join(after, "link")))

		requireSamePaths(t, before, after)
	})

	t.Run("file replaced by dir", func(t *testing.T) {
		before := t.TempDir()
		after := t.TempDir()
		writeDeltaTestFile(t, before, "thing", "file\n")
		writeDeltaTestFile(t, after, "thing/nested.txt", "dir now\n")

		requireSamePaths(t, before, after)
	})

	t.Run("dir replaced by file", func(t *testing.T) {
		before := t.TempDir()
		after := t.TempDir()
		writeDeltaTestFile(t, before, "thing/nested.txt", "dir\n")
		writeDeltaTestFile(t, after, "thing", "file now\n")

		requireSamePaths(t, before, after)
	})

	t.Run("binary files", func(t *testing.T) {
		before := t.TempDir()
		after := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(before, "rm.bin"), []byte{0, 1, 2, 3}, 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(after, "add.bin"), []byte{7, 0, 9}, 0o644))

		requireSamePaths(t, before, after)
		requireSameNumStat(t, before, after)
	})

	t.Run("identical trees", func(t *testing.T) {
		before := t.TempDir()
		after := t.TempDir()
		writeDeltaTestFile(t, before, "a.txt", "same\n")
		writeDeltaTestFile(t, after, "a.txt", "same\n")
		writeDeltaTestFile(t, before, "sub/b.txt", "b\n")
		writeDeltaTestFile(t, after, "sub/b.txt", "b\n")

		requireSamePaths(t, before, after)
	})

	t.Run("added empty dir only", func(t *testing.T) {
		before := t.TempDir()
		after := t.TempDir()
		writeDeltaTestFile(t, before, "a.txt", "same\n")
		writeDeltaTestFile(t, after, "a.txt", "same\n")
		require.NoError(t, os.MkdirAll(filepath.Join(after, "empty"), 0o755))

		requireSamePaths(t, before, after)
	})

	t.Run("rename with modification stays paired like git", func(t *testing.T) {
		before := t.TempDir()
		after := t.TempDir()
		base := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\n"
		writeDeltaTestFile(t, before, "old.txt", base)
		writeDeltaTestFile(t, after, "renamed.txt", base+"line9\n")

		requireSamePaths(t, before, after)
		requireSameNumStat(t, before, after)
	})
}

func TestChangesetDeltaUpperdir(t *testing.T) {
	ctx := context.Background()

	// An overlay layer (upper) on top of before, alongside the merged view
	// (after) it mounts as. Walking the layer must find what walking both
	// trees does.
	type layer struct{ before, upper, after string }
	newLayer := func(t *testing.T) layer {
		return layer{t.TempDir(), t.TempDir(), t.TempDir()}
	}
	write := func(t *testing.T, rel, contents string, roots ...string) {
		t.Helper()
		for _, root := range roots {
			writeDeltaTestFile(t, root, rel, contents)
		}
	}
	requireSameAsTrees := func(t *testing.T, l layer) {
		t.Helper()
		want := requireSamePaths(t, l.before, l.after)
		got, gotStats, err := computeChangesetPathsDelta(ctx, l.before, l.after, &fsdiff.LayerDelta{Upper: []string{l.upper}}, true)
		require.NoError(t, err)
		require.Equal(t, want, got)
		_, wantStats, err := computeChangesetPathsDelta(ctx, l.before, l.after, nil, true)
		require.NoError(t, err)
		require.Equal(t, wantStats, gotStats)

		empty, err := changesetDeltaIsEmpty(ctx, l.before, l.after, &fsdiff.LayerDelta{Upper: []string{l.upper}})
		require.NoError(t, err)
		require.False(t, empty)
		full := changesetPathCount(got)
		for _, limit := range []int{0, full - 1, 1000} {
			wantExceeds, err := changesetDeltaExceeds(ctx, l.before, l.after, nil, limit)
			require.NoError(t, err)
			gotExceeds, err := changesetDeltaExceeds(ctx, l.before, l.after, &fsdiff.LayerDelta{Upper: []string{l.upper}}, limit)
			require.NoError(t, err)
			require.Equal(t, wantExceeds, gotExceeds, "limit %d", limit)
		}
	}

	t.Run("additions and modifications", func(t *testing.T) {
		l := newLayer(t)
		write(t, "keep.txt", "same\n", l.before, l.after)
		write(t, "dir/keep.txt", "same\n", l.before, l.after)
		// Modified, and copied up with its content unchanged.
		write(t, "dir/mod.txt", "old\n", l.before)
		write(t, "dir/mod.txt", "new\n", l.upper, l.after)
		write(t, "touched.txt", "same\n", l.before, l.upper, l.after)
		// Added, and replaced by the other kind.
		write(t, "fresh/sub/a.txt", "a\n", l.upper, l.after)
		write(t, "thing", "file\n", l.before)
		write(t, "thing/nested.txt", "dir now\n", l.upper, l.after)
		write(t, "was/nested.txt", "dir\n", l.before)
		write(t, "was", "file now\n", l.upper, l.after)
		// A retargeted symlink and an exec bit change.
		write(t, "t1", "x\n", l.before, l.after)
		require.NoError(t, os.Symlink("t1", filepath.Join(l.before, "link")))
		require.NoError(t, os.Symlink("t2", filepath.Join(l.upper, "link")))
		require.NoError(t, os.Symlink("t2", filepath.Join(l.after, "link")))
		write(t, "run.sh", "#!/bin/sh\n", l.before, l.upper, l.after)
		require.NoError(t, os.Chmod(filepath.Join(l.upper, "run.sh"), 0o755))
		require.NoError(t, os.Chmod(filepath.Join(l.after, "run.sh"), 0o755))

		requireSameAsTrees(t, l)
	})

	t.Run("removals", func(t *testing.T) {
		l := newLayer(t)
		whiteout := func(rel string) {
			t.Helper()
			err := unix.Mknod(filepath.Join(l.upper, rel), unix.S_IFCHR, 0)
			if errors.Is(err, unix.EPERM) {
				t.Skip("creating overlay whiteouts needs CAP_MKNOD")
			}
			require.NoError(t, err)
		}
		opaque := func(rel string) {
			t.Helper()
			dir := filepath.Join(l.upper, rel)
			require.NoError(t, os.MkdirAll(dir, 0o755))
			err := unix.Lsetxattr(dir, "trusted.overlay.opaque", []byte("y"), 0)
			if err != nil {
				err = unix.Lsetxattr(dir, "user.overlay.opaque", []byte("y"), 0)
			}
			if err != nil {
				t.Skipf("setting overlay opaque xattrs: %v", err)
			}
		}

		write(t, "keep.txt", "same\n", l.before, l.after)
		// A file, and a directory with everything in it.
		write(t, "gone.txt", "bye\n", l.before)
		whiteout("gone.txt")
		write(t, "olddir/a.txt", "a\n", l.before)
		write(t, "olddir/sub/b.txt", "b\n", l.before)
		whiteout("olddir")
		// A directory replaced wholesale.
		write(t, "opq/x.txt", "x\n", l.before)
		write(t, "opq/y.txt", "y\n", l.before)
		opaque("opq")
		write(t, "opq/y.txt", "new y\n", l.upper, l.after)
		write(t, "opq/z.txt", "z\n", l.upper, l.after)

		requireSameAsTrees(t, l)
	})

	t.Run("only the layer is walked", func(t *testing.T) {
		// The trees differ, but the layer says nothing changed: what the
		// layer records is all that is consulted.
		l := newLayer(t)
		write(t, "f.txt", "old\n", l.before)
		write(t, "f.txt", "new\n", l.after)
		paths, _, err := computeChangesetPathsDelta(ctx, l.before, l.after, &fsdiff.LayerDelta{Upper: []string{l.upper}}, false)
		require.NoError(t, err)
		require.True(t, changesetPathsEmpty(paths))
	})

	// Several layers on top of before, as a workspace's successive edits
	// stack them: each path any layer holds is classified against the merged
	// view, so later layers can revert, refill or hide earlier ones.
	t.Run("several layers", func(t *testing.T) {
		before, after := t.TempDir(), t.TempDir()
		upper := []string{t.TempDir(), t.TempDir(), t.TempDir()}
		layers := upper
		whiteout := func(layer, rel string) {
			t.Helper()
			require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(layer, rel)), 0o755))
			err := unix.Mknod(filepath.Join(layer, rel), unix.S_IFCHR, 0)
			if errors.Is(err, unix.EPERM) {
				// Several layers are classified by the merged view, not
				// by what a layer entry is: any entry marks the path.
				err = os.WriteFile(filepath.Join(layer, rel), nil, 0o644)
			}
			require.NoError(t, err)
		}
		opaque := func(layer, rel string) {
			t.Helper()
			dir := filepath.Join(layer, rel)
			require.NoError(t, os.MkdirAll(dir, 0o755))
			err := unix.Lsetxattr(dir, "trusted.overlay.opaque", []byte("y"), 0)
			if err != nil {
				err = unix.Lsetxattr(dir, "user.overlay.opaque", []byte("y"), 0)
			}
			if err != nil {
				t.Skipf("setting overlay opaque xattrs: %v", err)
			}
		}
		write(t, "keep.txt", "same\n", before, after)
		write(t, "deep/keep.txt", "same\n", before, after)
		// Modified by one layer, modified again by another.
		write(t, "twice.txt", "old\n", before)
		write(t, "twice.txt", "first\n", layers[0])
		write(t, "twice.txt", "second\n", layers[2], after)
		// Modified by one layer, reverted by another: a copy-up with the
		// original content is no change.
		write(t, "reverted.txt", "orig\n", before, after)
		write(t, "reverted.txt", "changed\n", layers[0])
		write(t, "reverted.txt", "orig\n", layers[1])
		// Added, then removed again.
		write(t, "transient/x.txt", "x\n", layers[0])
		whiteout(layers[1], "transient")
		// Removed, then added back with new content.
		write(t, "refilled.txt", "old\n", before)
		whiteout(layers[0], "refilled.txt")
		write(t, "refilled.txt", "new\n", layers[2], after)
		// A directory with a file added inside, then removed wholesale.
		write(t, "olddir/a.txt", "a\n", before)
		write(t, "olddir/sub/b.txt", "b\n", before)
		write(t, "olddir/added.txt", "added\n", layers[0])
		whiteout(layers[1], "olddir")
		// A directory replaced wholesale by a lower layer, and added to by
		// a higher one.
		write(t, "opq/x.txt", "x\n", before)
		write(t, "opq/y.txt", "y\n", before)
		opaque(layers[0], "opq")
		write(t, "opq/y.txt", "new y\n", layers[0], after)
		write(t, "opq/z.txt", "z\n", layers[2], after)
		// Type changes across layers.
		write(t, "thing", "file\n", before)
		whiteout(layers[0], "thing")
		write(t, "thing/nested.txt", "dir now\n", layers[1], after)
		write(t, "was/nested.txt", "dir\n", before)
		whiteout(layers[1], "was")
		write(t, "was", "file now\n", layers[2], after)
		// Added across layers into a fresh directory.
		write(t, "fresh/a.txt", "a\n", layers[0], after)
		write(t, "fresh/sub/b.txt", "b\n", layers[2], after)
		// Exec bit and symlink changes in a middle layer.
		write(t, "run.sh", "#!/bin/sh\n", before, layers[1], after)
		require.NoError(t, os.Chmod(filepath.Join(layers[1], "run.sh"), 0o755))
		require.NoError(t, os.Chmod(filepath.Join(after, "run.sh"), 0o755))
		require.NoError(t, os.Symlink("keep.txt", filepath.Join(before, "link")))
		require.NoError(t, os.Symlink("twice.txt", filepath.Join(layers[1], "link")))
		require.NoError(t, os.Symlink("twice.txt", filepath.Join(after, "link")))

		requireLayersSameAsTrees(t, before, after, &fsdiff.LayerDelta{Upper: upper})
	})

	// Two copy-on-write children of one snapshot (two checkouts of one
	// commit, a merge result and the next commit's tree): each side's own
	// layers hold its differences from the shared ones, and both sides'
	// layers together hold every difference between them.
	t.Run("sibling layers", func(t *testing.T) {
		before, after := t.TempDir(), t.TempDir()
		lower := []string{t.TempDir(), t.TempDir()}
		upper := []string{t.TempDir(), t.TempDir()}
		mark := func(layer, rel string) {
			t.Helper()
			require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(layer, rel)), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(layer, rel), nil, 0o644))
		}
		// Shared and untouched.
		write(t, "keep.txt", "same\n", before, after)
		// Changed on one side or the other only: the other side keeps the
		// shared content.
		write(t, "lower-edit.txt", "shared\n", after)
		write(t, "lower-edit.txt", "lower\n", lower[0], before)
		write(t, "upper-edit.txt", "shared\n", before)
		write(t, "upper-edit.txt", "upper\n", upper[1], after)
		// Changed identically on both sides: no difference.
		write(t, "both-same.txt", "both\n", lower[1], upper[0], before, after)
		// Changed differently on both sides.
		write(t, "both-differ.txt", "lower\n", lower[0], before)
		write(t, "both-differ.txt", "upper\n", upper[0], after)
		// Removed on the lower side: upper still has the shared file.
		write(t, "lower-removed.txt", "shared\n", after)
		mark(lower[1], "lower-removed.txt")
		// A shared directory removed on the lower side: upper still has
		// all of it, though no layer lists its contents.
		write(t, "shareddir/a.txt", "a\n", after)
		write(t, "shareddir/sub/b.txt", "b\n", after)
		mark(lower[0], "shareddir")
		// A shared directory removed on the upper side.
		write(t, "gonedir/a.txt", "a\n", before)
		mark(upper[0], "gonedir")
		// Added on the lower side only: a removal from before to after.
		write(t, "lower-added/x.txt", "x\n", lower[1], before)
		// Added on the upper side.
		write(t, "upper-added/y.txt", "y\n", upper[1], after)
		// A shared file the lower side replaced by a directory.
		write(t, "kind", "shared file\n", after)
		write(t, "kind/inner.txt", "lower dir\n", lower[0], before)

		requireLayersSameAsTrees(t, before, after, &fsdiff.LayerDelta{Lower: lower, Upper: upper})
	})
}

// requireLayersSameAsTrees requires walking layers to find exactly what
// walking both trees finds, and nothing a layer does not record.
func requireLayersSameAsTrees(t *testing.T, before, after string, layers *fsdiff.LayerDelta) {
	t.Helper()
	ctx := t.Context()
	want := requireSamePaths(t, before, after)
	got, gotStats, err := computeChangesetPathsDelta(ctx, before, after, layers, true)
	require.NoError(t, err)
	require.Equal(t, want, got)
	_, wantStats, err := computeChangesetPathsDelta(ctx, before, after, nil, true)
	require.NoError(t, err)
	require.Equal(t, wantStats, gotStats)
	empty, err := changesetDeltaIsEmpty(ctx, before, after, layers)
	require.NoError(t, err)
	require.False(t, empty)

	// Only the layers are consulted: a difference no layer records is not
	// found, so the walk costs the size of the layers.
	writeDeltaTestFile(t, after, "unlisted/keep.txt", "behind the layers' back\n")
	got, _, err = computeChangesetPathsDelta(ctx, before, after, layers, false)
	require.NoError(t, err)
	require.NotContains(t, got.Added, "unlisted/keep.txt")
}

func TestChangesetDeltaExceeds(t *testing.T) {
	ctx := context.Background()

	t.Run("agrees with the full count when nothing is fuzzy", func(t *testing.T) {
		before := t.TempDir()
		after := t.TempDir()
		writeDeltaTestFile(t, before, "keep.txt", "same\n")
		require.NoError(t, os.Link(filepath.Join(before, "keep.txt"), filepath.Join(after, "keep.txt")))
		writeDeltaTestFile(t, before, "mod.txt", "old\n")
		writeDeltaTestFile(t, after, "mod.txt", "new\n")
		writeDeltaTestFile(t, before, "remove.txt", "bye\n")
		writeDeltaTestFile(t, before, "gone/a.txt", "a\n")
		writeDeltaTestFile(t, before, "gone/sub/b.txt", "b\n")
		writeDeltaTestFile(t, after, "add.txt", "hi\n")
		writeDeltaTestFile(t, after, "fresh/c.txt", "c\n")

		// No renames and no metadata-only changes, so the bound is exact:
		// 3 added (add.txt, fresh/, fresh/c.txt), 1 modified, 5 removed
		// (remove.txt, gone/, gone/a.txt, gone/sub/, gone/sub/b.txt).
		paths, _, err := computeChangesetPathsDelta(ctx, before, after, nil, false)
		require.NoError(t, err)
		full := changesetPathCount(paths)
		require.Equal(t, 9, full)

		for _, limit := range []int{0, 1, full - 1} {
			exceeds, err := changesetDeltaExceeds(ctx, before, after, nil, limit)
			require.NoError(t, err)
			require.True(t, exceeds, "limit %d", limit)
		}
		for _, limit := range []int{full, full + 1, 1000} {
			exceeds, err := changesetDeltaExceeds(ctx, before, after, nil, limit)
			require.NoError(t, err)
			require.False(t, exceeds, "limit %d", limit)
		}
	})

	t.Run("shared backing files never exceed", func(t *testing.T) {
		before := t.TempDir()
		after := t.TempDir()
		writeDeltaTestFile(t, before, "a.txt", "same\n")
		require.NoError(t, os.Link(filepath.Join(before, "a.txt"), filepath.Join(after, "a.txt")))

		exceeds, err := changesetDeltaExceeds(ctx, before, after, nil, 0)
		require.NoError(t, err)
		require.False(t, exceeds)
	})

	t.Run("counts metadata-only changes as an upper bound", func(t *testing.T) {
		// An mtime-only change is not a modification to ComputePaths, but
		// the bounded walk skips content verification and counts it.
		before := t.TempDir()
		after := t.TempDir()
		writeDeltaTestFile(t, before, "f.txt", "same\n")
		writeDeltaTestFile(t, after, "f.txt", "same\n")
		past := time.Now().Add(-time.Hour)
		require.NoError(t, os.Chtimes(filepath.Join(after, "f.txt"), past, past))

		paths, _, err := computeChangesetPathsDelta(ctx, before, after, nil, false)
		require.NoError(t, err)
		require.Equal(t, 0, changesetPathCount(paths))

		exceeds, err := changesetDeltaExceeds(ctx, before, after, nil, 0)
		require.NoError(t, err)
		require.True(t, exceeds, "metadata-differing paths count toward the bound")
		exceeds, err = changesetDeltaExceeds(ctx, before, after, nil, 1)
		require.NoError(t, err)
		require.False(t, exceeds)
	})

	t.Run("large additions and removals skip rename detection", func(t *testing.T) {
		before := t.TempDir()
		after := t.TempDir()
		for i := range patchSummaryMaxPaths {
			writeDeltaTestFile(t, before, fmt.Sprintf("old-%03d", i), "same content\n")
			writeDeltaTestFile(t, after, fmt.Sprintf("new-%03d", i), "same content\n")
		}
		// Full path computation would stage both sides and invoke git to
		// pair renames. The bounded walk must succeed without git at all.
		t.Setenv("PATH", t.TempDir())
		delta, exceeded, err := collectChangesetDeltaBounded(ctx, before, after, nil, patchSummaryMaxPaths)
		require.NoError(t, err)
		require.True(t, exceeded)
		require.Nil(t, delta)
	})

	t.Run("aborts inside a removed tree", func(t *testing.T) {
		// The walker reports a removed directory once; its children are
		// expanded by a nested walk that must honor the budget too.
		before := t.TempDir()
		after := t.TempDir()
		for i := range 50 {
			writeDeltaTestFile(t, before, filepath.Join("gone", "sub", fmt.Sprintf("%d.txt", i)), "x\n")
		}

		delta, exceeded, err := collectChangesetDeltaBounded(ctx, before, after, nil, 5)
		require.NoError(t, err)
		require.True(t, exceeded)
		require.Nil(t, delta, "no partial delta is handed back")

		delta, exceeded, err = collectChangesetDeltaBounded(ctx, before, after, nil, -1)
		require.NoError(t, err)
		require.False(t, exceeded)
		require.Equal(t, 52, delta.count())
	})

	t.Run("unbounded collection is unchanged", func(t *testing.T) {
		before := t.TempDir()
		after := t.TempDir()
		writeDeltaTestFile(t, before, "mod.txt", "old\n")
		writeDeltaTestFile(t, after, "mod.txt", "new\n")
		writeDeltaTestFile(t, after, "add.txt", "hi\n")

		delta, err := collectChangesetDelta(ctx, before, after, nil)
		require.NoError(t, err)
		require.Equal(t, []string{"add.txt"}, delta.addedFiles)
		require.Equal(t, []string{"mod.txt"}, delta.modifiedCandidates)
		require.Equal(t, 2, delta.count())
	})
}
