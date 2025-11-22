package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/dagger/dagger/.dagger/internal/dagger"
)

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
	// If there are changes, return an error
	empty, err := cs.IsEmpty(ctx)
	if err != nil {
		return err
	}
	if !empty {
		// Prepare a report with details on the diff
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
