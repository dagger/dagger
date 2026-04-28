package idtui

import (
	"context"
	"fmt"

	"dagger.io/dagger"
	"github.com/dagger/dagger/util/patchpreview"
)

const previewPatchQuery = `
query PreviewPatch($changeset: ID!) {
	changeset: node(id: $changeset) {
		... on Changeset {
			diffStats {
				path
				oldPath
				kind
				addedLines
				removedLines
			}
		}
	}
}
`

func PreviewPatch(ctx context.Context, dag *dagger.Client, changeset *dagger.Changeset) ([]patchpreview.Entry, error) {
	changesetID, err := changeset.ID(ctx)
	if err != nil {
		return nil, fmt.Errorf("query diff stat: get changeset id: %w", err)
	}

	var res struct {
		Changeset struct {
			DiffStats []struct {
				Path         string
				OldPath      *string
				Kind         string
				AddedLines   int
				RemovedLines int
			}
		}
	}

	err = dag.Do(ctx, &dagger.Request{
		Query: previewPatchQuery,
		Variables: map[string]any{
			"changeset": changesetID,
		},
	}, &dagger.Response{
		Data: &res,
	})
	if err != nil {
		return nil, fmt.Errorf("query diff stat: %w", err)
	}

	diffStat := res.Changeset.DiffStats
	entries := make([]patchpreview.Entry, len(diffStat))
	for i, s := range diffStat {
		entries[i] = patchpreview.Entry{Path: s.Path, Kind: s.Kind, Added: s.AddedLines, Removed: s.RemovedLines}
		if s.OldPath != nil {
			entries[i].OldPath = *s.OldPath
		}
	}
	return entries, nil
}
