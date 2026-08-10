package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPathSets(t *testing.T) {
	cs := &ChangesetPaths{
		Added:    []string{"/a/file1", "/b/file2"},
		Modified: []string{"/c/file3"},
		Removed:  []string{"/d/file4"},
	}

	sets := cs.pathSets()

	// Verify added paths
	_, ok := sets.added["/a/file1"]
	require.True(t, ok)
	_, ok = sets.added["/b/file2"]
	require.True(t, ok)
	_, ok = sets.added["/nonexistent"]
	require.False(t, ok)

	// Verify modified paths
	_, ok = sets.modified["/c/file3"]
	require.True(t, ok)
	_, ok = sets.modified["/nonexistent"]
	require.False(t, ok)

	// Verify removed paths
	_, ok = sets.removed["/d/file4"]
	require.True(t, ok)
	_, ok = sets.removed["/nonexistent"]
	require.False(t, ok)
}

// diffStatSummary renders the diff stat entries a changeset between two
// on-disk trees would report, as "KIND path +added -removed" strings.
func diffStatSummary(t *testing.T, beforeDir, afterDir string) []string {
	t.Helper()
	ctx := context.Background()
	paths, stats, err := computeChangesetPathsDelta(ctx, beforeDir, afterDir, true)
	require.NoError(t, err)
	entries := buildDiffStats(paths, stats)
	summary := make([]string, 0, len(entries))
	for _, e := range entries {
		summary = append(summary, fmt.Sprintf("%s %s +%d -%d", e.Kind, e.Path, e.AddedLines, e.RemovedLines))
	}
	return summary
}

// TestDiffStatsDirectoryEntries pins which directory entries diff stats report
// on their own: an added directory is only reported when nothing else records
// it, while a removed directory is suppressed whenever the removed files
// beneath it already imply the removal (git tracks no directories, and a
// unified diff can't express one).
func TestDiffStatsDirectoryEntries(t *testing.T) {
	for _, tc := range []struct {
		name      string
		before    map[string]string
		beforeDir []string
		after     map[string]string
		afterDir  []string
		want      []string
		wantEmpty bool
	}{
		{
			name:   "removing the only file in a directory reports just the file",
			before: map[string]string{"keep.txt": "keep\n", "qa2/alpha.txt": "a\nb\n"},
			after:  map[string]string{"keep.txt": "keep\n"},
			want:   []string{"REMOVED qa2/alpha.txt +0 -2"},
		},
		{
			name:   "removing one of two files reports just the file",
			before: map[string]string{"dir/a.txt": "a\n", "dir/b.txt": "b\n"},
			after:  map[string]string{"dir/a.txt": "a\n"},
			want:   []string{"REMOVED dir/b.txt +0 -1"},
		},
		{
			name: "removing a whole tree reports only its files",
			before: map[string]string{
				"keep.txt":         "stay\n",
				"dir/file1.txt":    "one\ntwo\n",
				"dir/sub/deep.txt": "deep\n",
			},
			after: map[string]string{"keep.txt": "stay\n"},
			want: []string{
				"REMOVED dir/file1.txt +0 -2",
				"REMOVED dir/sub/deep.txt +0 -1",
			},
		},
		{
			name:      "removing an empty directory is not implied, so it is reported",
			before:    map[string]string{"keep.txt": "stay\n"},
			beforeDir: []string{"empty"},
			after:     map[string]string{"keep.txt": "stay\n"},
			// Directory-only change: like `git diff --quiet`, IsEmpty
			// ignores it, mirroring the added empty directory below.
			wantEmpty: true,
			want:      []string{"REMOVED empty/ +0 -0"},
		},
		{
			name:     "an added directory with no files is the only record of itself",
			before:   map[string]string{"keep.txt": "stay\n"},
			after:    map[string]string{"keep.txt": "stay\n", "core/probe.txt": "probe\n"},
			afterDir: []string{"empty"},
			want: []string{
				"ADDED core/probe.txt +1 -0",
				"ADDED empty/ +0 -0",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			beforeDir := t.TempDir()
			afterDir := t.TempDir()
			for rel, contents := range tc.before {
				writeDeltaTestFile(t, beforeDir, rel, contents)
			}
			for _, rel := range tc.beforeDir {
				require.NoError(t, os.MkdirAll(filepath.Join(beforeDir, rel), 0o755))
			}
			for rel, contents := range tc.after {
				writeDeltaTestFile(t, afterDir, rel, contents)
			}
			for _, rel := range tc.afterDir {
				require.NoError(t, os.MkdirAll(filepath.Join(afterDir, rel), 0o755))
			}

			require.Equal(t, tc.want, diffStatSummary(t, beforeDir, afterDir))

			// IsEmpty and DiffStats must agree: any file-level entry means
			// the changeset is non-empty.
			isEmpty, err := changesetDeltaIsEmpty(context.Background(), beforeDir, afterDir)
			require.NoError(t, err)
			require.Equal(t, tc.wantEmpty, isEmpty)
		})
	}
}

// TestChangesetStaleAnchorReportsWholeFileAdd pins the classification failure
// behind the workspace's intermittent whole-file ADDs: a surgical edit to a
// long-tracked, host-present file recorded by a staged commit as "A +N -0".
//
// Nothing here is intermittent and nothing consults git's index. ADDED vs
// MODIFIED comes from computeChangesetPathsDelta + buildDiffStats, i.e. purely
// from whether the path exists in the BEFORE tree. For a host-backed workspace
// that tree is the overlay's SPARSE base — host.directory(include: <touched
// paths>) — so it holds host content for exactly the paths the overlay had
// touched WHEN IT WAS SIZED, and nothing else. The touched set only ever grows,
// so a base sized earlier is strictly narrower than one sized later.
//
// stagedCommitChanges (core/schema/workspace_commit.go:383-386) derives a
// commit's own delta by substituting the PREVIOUS staged commit's recorded
// After tree as the before-anchor. That tree was sized by the earlier, narrower
// touched set, so any path first edited since is absent from it — and reads as
// a whole-file add rather than the edit it is. Index 0 escapes this: it anchors
// on the commit's own Before, which is the current base.
func TestChangesetStaleAnchorReportsWholeFileAdd(t *testing.T) {
	// A file that has existed on the host, tracked, since long before the
	// session. Its whole-file line count is what a bogus ADD reports.
	var tracked string
	for i := range 12 {
		tracked += fmt.Sprintf("line %d\n", i)
	}
	edited := strings.Replace(tracked, "line 7\n", "line 7 (edited)\n", 1)
	require.NotEqual(t, tracked, edited)

	// The state the FIRST staged commit recorded as its After: the sparse base
	// as of touched set {early.txt}, with that commit's content applied.
	// late.txt had not been touched yet, so the base never read it from the
	// host and this tree does not carry it at all.
	firstCommitAfter := t.TempDir()
	writeDeltaTestFile(t, firstCommitAfter, "early.txt", "early edited\n")

	// The base the SECOND staged commit is anchored on (its own recorded
	// Before): the overlay's sparse base after late.txt was touched, so it now
	// holds late.txt at unedited host content.
	secondCommitBefore := t.TempDir()
	writeDeltaTestFile(t, secondCommitBefore, "early.txt", "early\n")
	writeDeltaTestFile(t, secondCommitBefore, "late.txt", tracked)

	// The state the SECOND staged commit recorded as its After: that same base
	// with the first commit's content still applied, plus its own one-line edit
	// to late.txt.
	secondCommitAfter := t.TempDir()
	writeDeltaTestFile(t, secondCommitAfter, "early.txt", "early edited\n")
	writeDeltaTestFile(t, secondCommitAfter, "late.txt", edited)

	// Anchored on its own base — the cumulative record, and what
	// Workspace.changes / git.uncommitted effectively agree with — the edit is
	// the one-line modification it actually is.
	require.Equal(t, []string{
		"MODIFIED early.txt +1 -1",
		"MODIFIED late.txt +1 -1",
	}, diffStatSummary(t, secondCommitBefore, secondCommitAfter))

	// Anchored on the previous commit's After — the substitution
	// stagedCommitChanges performs for every commit after the first — the same
	// content reads as a whole-file add. This is the reported "A +N -0" shape:
	// added lines equal to the file's entire length, zero removed.
	summary := diffStatSummary(t, firstCommitAfter, secondCommitAfter)
	require.Equal(t, []string{
		fmt.Sprintf("ADDED late.txt +%d -0", strings.Count(edited, "\n")),
	}, summary)
	// ... and it really is the whole file, not a large-but-partial diff.
	require.Equal(t, strings.Count(edited, "\n"), 12)

	// The anchor a correct per-commit delta needs: the commit's OWN base — which
	// is sized by the current touched set — carrying the previous commit's
	// content. Re-anchoring both sides on the same, current base is what makes
	// the step between two staged states comparable, and it reports the edit
	// correctly while still crediting the first commit's content to the first
	// commit (early.txt does not appear).
	previousStateOnCurrentBase := t.TempDir()
	writeDeltaTestFile(t, previousStateOnCurrentBase, "early.txt", "early edited\n")
	writeDeltaTestFile(t, previousStateOnCurrentBase, "late.txt", tracked)

	require.Equal(t, []string{"MODIFIED late.txt +1 -1"},
		diffStatSummary(t, previousStateOnCurrentBase, secondCommitAfter))
}

// TestChangesetAbsentFromBeforeShapes catalogues the ways a path can be missing
// from a BEFORE tree that is otherwise present and healthy, since each one
// produces the same whole-file ADD. The double walk (collectChangesetDelta)
// lstats both trees and compares path by path: it never follows a symlink and
// never reconciles a shape mismatch, so "reachable through the before tree" is
// not the same as "present in the before tree".
func TestChangesetAbsentFromBeforeShapes(t *testing.T) {
	t.Run("path simply not included in the before tree", func(t *testing.T) {
		before := t.TempDir()
		after := t.TempDir()
		writeDeltaTestFile(t, before, "in-base.txt", "same\n")
		writeDeltaTestFile(t, after, "in-base.txt", "same\n")
		writeDeltaTestFile(t, after, "sub/tracked.txt", "one\ntwo\n")

		require.Equal(t, []string{"ADDED sub/tracked.txt +2 -0"},
			diffStatSummary(t, before, after))
	})

	t.Run("before has a file where after has a directory", func(t *testing.T) {
		before := t.TempDir()
		after := t.TempDir()
		writeDeltaTestFile(t, before, "sub", "i am a file\n")
		writeDeltaTestFile(t, after, "sub/tracked.txt", "one\ntwo\n")

		require.Equal(t, []string{
			"REMOVED sub +0 -1",
			"ADDED sub/tracked.txt +2 -0",
		}, diffStatSummary(t, before, after))
	})

	t.Run("before reaches the content through a symlinked parent", func(t *testing.T) {
		before := t.TempDir()
		after := t.TempDir()
		// The content IS readable at before/sub/tracked.txt, but only by
		// following the link — which the walk never does.
		writeDeltaTestFile(t, before, "real/tracked.txt", "one\ntwo\n")
		require.NoError(t, os.Symlink("real", filepath.Join(before, "sub")))
		writeDeltaTestFile(t, after, "real/tracked.txt", "one\ntwo\n")
		writeDeltaTestFile(t, after, "sub/tracked.txt", "one\ntwo\n")

		require.Equal(t, []string{
			"REMOVED sub +0 -1",
			"ADDED sub/tracked.txt +2 -0",
		}, diffStatSummary(t, before, after))
	})
}

func TestRemovalImpliedByChildren(t *testing.T) {
	allRemoved := []string{"dir/", "dir/sub/", "dir/sub/deep.txt", "empty/", "empty/nested/"}
	require.True(t, removalImpliedByChildren("dir/", allRemoved))
	require.True(t, removalImpliedByChildren("dir/sub/", allRemoved))
	require.False(t, removalImpliedByChildren("empty/", allRemoved),
		"a directory holding no files is implied by nothing")
	require.False(t, removalImpliedByChildren("empty/nested/", allRemoved))
}

func TestChangesetConflicts(t *testing.T) {
	origin := &ChangesetPaths{
		Added: []string{
			"/path1/file1",
			"/path1/file2",
		},
		Modified: []string{
			"/path1/file3",
			"/path2/filea",
		},
		Removed: []string{
			"/path3/fileb",
		},
	}
	for _, tc := range []struct {
		name          string
		addition      *ChangesetPaths
		expectedError error
	}{
		{
			"no conflicts",
			&ChangesetPaths{
				Added: []string{
					"/path1/file3",
					"/path4/filez",
				},
				Modified: []string{
					"/path4/filex",
				},
				Removed: []string{
					"/path1/file4",
				},
			},
			nil,
		},
		{
			"empty addition",
			&ChangesetPaths{},
			nil,
		},
		{
			"added path",
			&ChangesetPaths{
				Added: []string{
					"/path1/file2",
				},
			},
			ErrAddedTwice,
		},
		{
			"modified",
			&ChangesetPaths{
				Modified: []string{
					"/path1/file3",
				},
			},
			ErrModifiedTwice,
		},
		{
			"modified and deleted",
			&ChangesetPaths{
				Removed: []string{
					"/path2/filea",
				},
			},
			ErrModifiedRemoved,
		},
		{
			"deleted and modified",
			&ChangesetPaths{
				Modified: []string{
					"/path3/fileb",
				},
			},
			ErrModifiedRemoved,
		},
		{
			"removed twice",
			&ChangesetPaths{
				Removed: []string{
					"/path3/fileb",
				},
			},
			nil,
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := origin.CheckConflicts(tc.addition).Error()
			if tc.expectedError != nil {
				require.ErrorIs(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
