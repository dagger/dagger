package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChangesetConflicts(t *testing.T) {
	origin := &Changeset{
		AddedPaths: []string{
			"/path1/file1",
			"/path1/file2",
		},
		ModifiedPaths: []string{
			"/path1/file3",
			"/path2/filea",
		},
		RemovedPaths: []string{
			"/path3/fileb",
		},
	}
	for _, tc := range []struct {
		name          string
		addition      *Changeset
		expectedError error
	}{
		{
			"no conflicts",
			&Changeset{
				AddedPaths: []string{
					"/path1/file3",
					"/path4/filez",
				},
				ModifiedPaths: []string{
					"/path4/filex",
				},
				RemovedPaths: []string{
					"/path1/file4",
				},
			},
			nil,
		},
		{
			"empty addition",
			&Changeset{},
			nil,
		},
		{
			"added path",
			&Changeset{
				AddedPaths: []string{
					"/path1/file2",
				},
			},
			ErrAddedTwice,
		},
		{
			"modified",
			&Changeset{
				ModifiedPaths: []string{
					"/path1/file3",
				},
			},
			ErrModifiedTwice,
		},
		{
			"modified and deleted",
			&Changeset{
				RemovedPaths: []string{
					"/path2/filea",
				},
			},
			ErrModifiedRemoved,
		},
		{
			"deleted and modified",
			&Changeset{
				ModifiedPaths: []string{
					"/path3/fileb",
				},
			},
			ErrModifiedRemoved,
		},
		{
			"removed twice",
			&Changeset{
				RemovedPaths: []string{
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
