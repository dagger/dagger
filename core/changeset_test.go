package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

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
