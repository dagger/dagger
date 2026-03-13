package idtui

import (
	"context"
	"fmt"

	"dagger.io/dagger"
	"github.com/dagger/dagger/util/patchpreview"
)

func PreviewPatch(ctx context.Context, dag *dagger.Client, changeset *dagger.Changeset) ([]patchpreview.Entry, error) {
	q := dag.QueryBuilder().
		Select("loadChangesetFromID").
		Arg("id", changeset).
		Select("diffStat")

	var diffStat []struct {
		Path         string `json:"path"`
		Kind         string `json:"kind"`
		AddedLines   int    `json:"addedLines"`
		RemovedLines int    `json:"removedLines"`
	}
	if err := q.Bind(&diffStat).Execute(ctx); err != nil {
		return nil, fmt.Errorf("query diff stat: %w", err)
	}

	entries := make([]patchpreview.Entry, len(diffStat))
	for i, s := range diffStat {
		entries[i] = patchpreview.Entry{Path: s.Path, Kind: s.Kind, Added: s.AddedLines, Removed: s.RemovedLines}
	}
	return entries, nil
}
