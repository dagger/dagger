package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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
	deltaPaths, _, err := computeChangesetPathsDelta(ctx, beforeDir, afterDir, true)
	require.NoError(t, err)

	require.ElementsMatch(t, gitPaths.Added, deltaPaths.Added, "Added")
	require.ElementsMatch(t, gitPaths.Modified, deltaPaths.Modified, "Modified")
	require.ElementsMatch(t, gitPaths.Removed, deltaPaths.Removed, "Removed")
	require.ElementsMatch(t, gitPaths.AllRemoved, deltaPaths.AllRemoved, "AllRemoved")
	require.Equal(t, gitPaths.Renamed, deltaPaths.Renamed, "Renamed")

	// The stats-less variant stages fewer files but must report the same paths.
	deltaPathsNoStats, _, err := computeChangesetPathsDelta(ctx, beforeDir, afterDir, false)
	require.NoError(t, err)
	require.Equal(t, deltaPaths, deltaPathsNoStats, "withStats=false paths")

	// The IsEmpty fast path must agree with whether git sees any file-level
	// change (renames included; directory-only changes don't count).
	isFile := func(p string) bool { return !strings.HasSuffix(p, "/") }
	gitEmpty := len(gitPaths.Modified) == 0 &&
		!slices.ContainsFunc(gitPaths.Added, isFile) &&
		!slices.ContainsFunc(gitPaths.AllRemoved, isFile)
	deltaEmpty, err := changesetDeltaIsEmpty(ctx, beforeDir, afterDir)
	require.NoError(t, err)
	require.Equal(t, gitEmpty, deltaEmpty, "IsEmpty")

	return deltaPaths
}

func requireSameNumStat(t *testing.T, beforeDir, afterDir string) {
	t.Helper()
	ctx := context.Background()

	gitStats, err := compareDirectoriesNumStat(ctx, beforeDir, afterDir)
	require.NoError(t, err)
	_, deltaStats, err := computeChangesetPathsDelta(ctx, beforeDir, afterDir, true)
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
		paths, _, err := computeChangesetPathsDelta(ctx, before, after, false)
		require.NoError(t, err)
		full := changesetPathCount(paths)
		require.Equal(t, 9, full)

		for _, limit := range []int{0, 1, full - 1} {
			exceeds, err := changesetDeltaExceeds(ctx, before, after, limit)
			require.NoError(t, err)
			require.True(t, exceeds, "limit %d", limit)
		}
		for _, limit := range []int{full, full + 1, 1000} {
			exceeds, err := changesetDeltaExceeds(ctx, before, after, limit)
			require.NoError(t, err)
			require.False(t, exceeds, "limit %d", limit)
		}
	})

	t.Run("shared backing files never exceed", func(t *testing.T) {
		before := t.TempDir()
		after := t.TempDir()
		writeDeltaTestFile(t, before, "a.txt", "same\n")
		require.NoError(t, os.Link(filepath.Join(before, "a.txt"), filepath.Join(after, "a.txt")))

		exceeds, err := changesetDeltaExceeds(ctx, before, after, 0)
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

		paths, _, err := computeChangesetPathsDelta(ctx, before, after, false)
		require.NoError(t, err)
		require.Equal(t, 0, changesetPathCount(paths))

		exceeds, err := changesetDeltaExceeds(ctx, before, after, 0)
		require.NoError(t, err)
		require.True(t, exceeds, "metadata-differing paths count toward the bound")
		exceeds, err = changesetDeltaExceeds(ctx, before, after, 1)
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
		delta, exceeded, err := collectChangesetDeltaBounded(ctx, before, after, patchSummaryMaxPaths)
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

		delta, exceeded, err := collectChangesetDeltaBounded(ctx, before, after, 5)
		require.NoError(t, err)
		require.True(t, exceeded)
		require.Nil(t, delta, "no partial delta is handed back")

		delta, exceeded, err = collectChangesetDeltaBounded(ctx, before, after, -1)
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

		delta, err := collectChangesetDelta(ctx, before, after)
		require.NoError(t, err)
		require.Equal(t, []string{"add.txt"}, delta.addedFiles)
		require.Equal(t, []string{"mod.txt"}, delta.modifiedCandidates)
		require.Equal(t, 2, delta.count())
	})
}
