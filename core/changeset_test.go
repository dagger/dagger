package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
