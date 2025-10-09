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

func (dev *DaggerDev) CurrentGitBranch(ctx context.Context) (string, error) {
	branches, err := dev.Git.Branches(ctx, dagger.VersionGitBranchesOpts{
		Commit: "HEAD",
	})
	if err != nil {
		return "", err
	}
	// Use the first branch name if available, otherwise fallback to "HEAD"
	if len(branches) == 0 {
		return "HEAD", nil
	}
	return branches[0].Branch(ctx)
}
