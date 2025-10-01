package main

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/dagger/dagger/.dagger/internal/dagger"
)

func formatJSONFile(ctx context.Context, f *dagger.File) (*dagger.File, error) {
	name, err := f.Name(ctx)
	if err != nil {
		return nil, err
	}

	contents, err := f.Contents(ctx)
	if err != nil {
		return nil, err
	}

	var out bytes.Buffer
	err = json.Indent(&out, []byte(contents), "", "\t")
	if err != nil {
		return nil, err
	}

	return dag.File(name, out.String()), nil
}

// Merge Changesets together
// FIXME: move this to core dagger: https://github.com/dagger/dagger/issues/11189
func changesetMerge(base *dagger.Directory, changesets ...*dagger.Changeset) *dagger.Changeset {
	dir := base
	for _, changeset := range changesets {
		dir = dir.WithChanges(changeset)
	}
	return dir.Changes(base)
}

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
