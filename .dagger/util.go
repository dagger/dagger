package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

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
func changesetMerge(changesets ...*dagger.Changeset) *dagger.Changeset {
	before := dag.Directory()
	for _, changeset := range changesets {
		before = before.WithDirectory("", changeset.Before())
	}
	after := before
	for _, changeset := range changesets {
		after = after.WithChanges(changeset)
	}
	return after.Changes(before)
}

func assertNoChanges(ctx context.Context, cs *dagger.Changeset, log io.Writer) error {
	empty, err := cs.IsEmpty(ctx)
	if err != nil {
		return err
	}
	if !empty {
		summary, err := changesetSummary(ctx, cs)
		if err != nil {
			return err
		}
		fmt.Fprint(log, summary)
		return errors.New("generated files are not up-to-date")
	}
	return nil
}

func changesetSummary(ctx context.Context, cs *dagger.Changeset) (string, error) {
	added, err := cs.AddedPaths(ctx)
	if err != nil {
		return "", err
	}
	removed, err := cs.RemovedPaths(ctx)
	if err != nil {
		return "", err
	}
	modified, err := cs.ModifiedPaths(ctx)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`%d MODIFIED:
%s

%d REMOVED:
%s

%d ADDED:
%s
`,
		len(modified), strings.Join(modified, "\n"),
		len(removed), strings.Join(removed, "\n"),
		len(added), strings.Join(added, "\n"),
	), nil
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
