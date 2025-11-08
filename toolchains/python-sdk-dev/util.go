package main

import "dagger/python-sdk-dev/internal/dagger"

// Return the changes between two directory, excluding the specified path patterns from the comparison
func changes(before, after *dagger.Directory, exclude []string) *dagger.Changeset {
	if exclude == nil {
		return after.Changes(before)
	}
	return after.
		// 1. Remove matching files from after
		Filter(dagger.DirectoryFilterOpts{Exclude: exclude}).
		// 2. Copy matching files from before
		WithDirectory("", before.Filter(dagger.DirectoryFilterOpts{Include: exclude})).
		Changes(before)
}
