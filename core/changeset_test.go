package core

import (
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

func TestCheckConflictsMulti(t *testing.T) {
	// Test detecting conflicts across multiple changesets
	cs1 := &ChangesetPaths{
		Added:    []string{"/file1"},
		Modified: []string{"/file2"},
	}
	cs2 := &ChangesetPaths{
		Added:    []string{"/file3"},
		Modified: []string{"/file4"},
	}
	cs3 := &ChangesetPaths{
		Added:    []string{"/file1"}, // Conflicts with cs1
		Modified: []string{"/file2"}, // Conflicts with cs1
	}

	t.Run("no conflicts between two non-conflicting changesets", func(t *testing.T) {
		all := []*ChangesetPaths{cs1, cs2}
		pathSets := make([]changesetPathSets, len(all))
		for i, cs := range all {
			pathSets[i] = cs.pathSets()
		}
		conflicts := checkConflictsMulti(all, pathSets)
		require.Empty(t, conflicts)
	})

	t.Run("detects conflicts in three changesets", func(t *testing.T) {
		all := []*ChangesetPaths{cs1, cs2, cs3}
		pathSets := make([]changesetPathSets, len(all))
		for i, cs := range all {
			pathSets[i] = cs.pathSets()
		}
		conflicts := checkConflictsMulti(all, pathSets)
		// Should find 2 conflicts: /file1 (added twice) and /file2 (modified twice)
		require.Len(t, conflicts, 2)

		// Verify the paths in conflicts
		conflictPaths := make(map[string]bool)
		for _, c := range conflicts {
			conflictPaths[c.Path] = true
		}
		require.True(t, conflictPaths["/file1"])
		require.True(t, conflictPaths["/file2"])
	})
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
